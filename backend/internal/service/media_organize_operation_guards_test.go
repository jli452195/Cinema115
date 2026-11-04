package service

import (
	"context"
	"errors"
	"path"
	"testing"

	"strm115/backend/internal/client/p115"
)

func mediaDeleteOperationHistory(t *testing.T, s *MediaOrganizer, runID, fileID string) {
	t.Helper()
	count, err := s.DeleteHistory(context.Background(), MediaOrganizeDelete{Items: []MediaOrganizeDeleteItem{{RunID: runID, FileID: fileID}}})
	if err != nil || count != 1 {
		t.Fatal("无文件操作的任务应能删除记录", count, err)
	}
}

func mediaAssertOperationPermission(t *testing.T, s *MediaOrganizer, runID, fileID string, canDelete bool) {
	t.Helper()
	task, err := s.Task(runID, fileID)
	if err != nil || task.CanDelete != canDelete {
		t.Fatal("删除权限应只随当前文件操作变化", fileID, task.CanDelete, canDelete, err)
	}
}

func TestMedia115BatchOperationOnlyProtectsParticipatingVideos(t *testing.T) {
	s, drive, files, run := newMedia115BatchTest(t, 2)
	t.Cleanup(files.close)
	other := MediaOrganizeEntry{File: OrganizeFile{ShareFile: p115.ShareFile{ID: "unrelated-done", Name: "done.mkv"}, MediaType: "video"}, Status: "done"}
	run.Entries = append(run.Entries, other)
	if err := s.checkpoint(run); err != nil {
		t.Fatal(err)
	}
	drive.beforeMove = func() {
		for _, entry := range run.Entries[:2] {
			mediaAssertOperationPermission(t, s, run.ID, entry.File.ID, false)
		}
		mediaAssertOperationPermission(t, s, run.ID, other.File.ID, true)
		mediaDeleteOperationHistory(t, s, run.ID, other.File.ID)
	}
	if _, err := s.execute115Batch(context.Background(), files, run, drive, []int{0, 1}); err != nil {
		t.Fatal(err)
	}
	for _, entry := range run.Entries[:2] {
		if entry.Status != "done" {
			t.Fatal("参与批次的视频应完成", entry)
		}
		mediaAssertOperationPermission(t, s, run.ID, entry.File.ID, true)
	}
	current, err := s.Snapshot(run.ID)
	if err != nil || len(current.Entries) != 2 {
		t.Fatal("批次检查点不能复活已删除的无关视频", current, err)
	}
}

type mediaCD2GuardFilesystem struct {
	*mediaCD2BatchFake
	beforeSingleMove func()
}

func (f *mediaCD2GuardFilesystem) Move(ctx context.Context, sources []string, destination string) ([]string, error) {
	if f.beforeSingleMove != nil {
		f.beforeSingleMove()
	}
	return f.mediaCD2BatchFake.Move(ctx, sources, destination)
}

func TestMediaCD2BatchReleasesCompletedOwnersBeforeFallback(t *testing.T) {
	s, fs, _, rule := newMediaCD2BatchOrganizer(t)
	first := mediaCD2BatchEntry(fs, rule, "first.mkv", "first.mkv")
	first.CD2Current = &MediaCD2Location{Path: first.File.ID, FileID: first.CD2FileID, Modified: first.Modified}
	second := mediaCD2BatchEntry(fs, rule, "second.mkv", "second.mkv")
	run := mediaCD2BatchRun(t, s, rule, first, second)
	files, err := s.files(context.Background(), rule)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(files.close)
	guarded := &mediaCD2GuardFilesystem{mediaCD2BatchFake: fs}
	guarded.beforeSingleMove = func() {
		mediaAssertOperationPermission(t, s, run.ID, first.File.ID, false)
		mediaAssertOperationPermission(t, s, run.ID, second.File.ID, true)
		mediaDeleteOperationHistory(t, s, run.ID, second.File.ID)
	}
	files.cd2 = guarded
	if err := s.executeCD2Batch(context.Background(), files, &run, fs, []int{0, 1}); err != nil {
		t.Fatal(err)
	}
	current, err := s.Snapshot(run.ID)
	if err != nil || len(current.Entries) != 1 || current.Entries[0].File.ID != first.File.ID || current.Entries[0].Status != "done" || len(fs.moves) != 1 || len(fs.singleMoves) != 1 {
		t.Fatal("回退期间应只保护回退项，已删除记录不能复活", current, err, fs.moves, fs.singleMoves)
	}
	mediaAssertOperationPermission(t, s, run.ID, first.File.ID, true)
}

func TestMedia115BatchSkipsAbandonedOwnerBeforeWriting(t *testing.T) {
	s, drive, files, run := newMedia115BatchTest(t, 2)
	t.Cleanup(files.close)
	if err := s.checkpoint(run); err != nil {
		t.Fatal(err)
	}
	abandoned := run.Entries[0]
	mediaDeleteOperationHistory(t, s, run.ID, abandoned.File.ID)
	if _, err := s.execute115Batch(context.Background(), files, run, drive, []int{0, 1}); err != nil {
		t.Fatal(err)
	}
	if len(drive.dirs["10"]) != 1 || drive.dirs["10"][0].ID != abandoned.File.ID || drive.dirs["10"][0].Name != abandoned.File.Name || len(drive.dirs["20"]) != 1 || drive.dirs["20"][0].ID != run.Entries[1].File.ID {
		t.Fatal("操作前已删除记录的文件不能改名或移动", drive.dirs)
	}
}

func TestMediaQuarantineDoesNotReplayDeletedMovingOwner(t *testing.T) {
	s, drive, files, run := newMedia115BatchTest(t, 1)
	t.Cleanup(files.close)
	run.Rule.Failure, run.Rule.FailureID = "/failed", "40"
	files.rule = run.Rule
	run.Entries[0].Status = "invalid"
	run.Entries[0].Failure = &MediaOrganizeFailure{State: "moving", Reason: "旧隔离结果未确认"}
	if err := s.checkpoint(run); err != nil {
		t.Fatal(err)
	}
	mediaDeleteOperationHistory(t, s, run.ID, run.Entries[0].File.ID)
	if err := s.quarantine(context.Background(), files, run, true); err != nil {
		t.Fatal(err)
	}
	if len(drive.dirs["10"]) != 1 || len(drive.dirs["40"]) != 0 || drive.moves != 0 || drive.renames != 0 {
		t.Fatal("删除旧 moving 记录后不能再次发起隔离", drive.dirs, drive.moves, drive.renames)
	}
}

func TestMedia115CanceledBatchReturnsCancellationAndReleasesOwner(t *testing.T) {
	s, drive, files, run := newMedia115BatchTest(t, 1)
	t.Cleanup(files.close)
	if err := s.checkpoint(run); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	drive.beforeSnapshot = func(string) error {
		cancel()
		return ctx.Err()
	}
	_, err := s.execute115Batch(ctx, files, run, drive, []int{0})
	if !errors.Is(err, context.Canceled) || drive.moves != 0 || drive.renames != 0 {
		t.Fatal("读取期间取消应向上传递，不得继续批量写入", err, drive.moves, drive.renames)
	}
	mediaAssertOperationPermission(t, s, run.ID, run.Entries[0].File.ID, true)
}

func TestMediaCanceledVideoEntryReturnsCancellationAndReleasesOwner(t *testing.T) {
	s, fs, _, rule := newMediaCD2BatchOrganizer(t)
	entry := mediaCD2BatchEntry(fs, rule, "source.mkv", "target.mkv")
	run := mediaCD2BatchRun(t, s, rule, entry)
	files, err := s.files(context.Background(), rule)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(files.close)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	files.cd2 = &mediaAttachmentDeleteFake{mediaCD2Fake: fs.mediaCD2Fake, beforeStat: func(full string) {
		if full == path.Join(rule.Source, entry.File.SourcePath) {
			cancel()
		}
	}}
	err = s.executeMediaEntry(ctx, files, &run, 0)
	if !errors.Is(err, context.Canceled) || run.Entries[0].Status == "done" || fs.files[entry.File.ID].ID == "" || len(fs.singleMoves) != 0 {
		t.Fatal("普通视频执行也应传递取消并保留来源", err, run.Entries[0], fs.singleMoves)
	}
	mediaAssertOperationPermission(t, s, run.ID, entry.File.ID, true)
}
