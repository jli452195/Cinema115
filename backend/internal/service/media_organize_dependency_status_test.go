package service

import (
	"context"
	"errors"
	"path"
	"slices"
	"testing"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/naming"
)

func mediaAssertMainFileWaitingAfterFinish(t *testing.T, run *MediaOrganizeRun, index int) {
	t.Helper()
	mediaFinishRun(run)
	entry := run.Entries[index]
	if entry.ResultCode != "WAITING_MAIN_FILE" || entry.Result == nil || entry.Result.Target == "" || !mediaEntryWaiting(entry) {
		t.Fatal("完成汇总补写结果目标后，仍应保留等待主文件的状态", entry)
	}
}

func TestMediaDependencySingleWaitSurvivesFinishAndClearsOnRetry(t *testing.T) {
	s, drive, files, run := newMedia115BatchTest(t, 2)
	run.Entries[1].File.MediaType = "subtitle"
	run.Entries[1].VideoID = run.Entries[0].File.ID
	if err := s.executeMediaEntry(context.Background(), files, run, 1); err != nil {
		t.Fatal(err)
	}
	mediaAssertMainFileWaitingAfterFinish(t, run, 1)
	if drive.renames != 0 || drive.moves != 0 {
		t.Fatal("主文件未完成时不能处理附件", drive.renameBatches, drive.moveBatches)
	}
	run.Entries[0].Status = "done"
	s.reserve = func() (func(), error) { return nil, errors.New("模拟115目录繁忙") }
	if err := s.executeMediaEntry(context.Background(), files, run, 1); err != nil {
		t.Fatal(err)
	}
	if run.Entries[1].ResultCode == "WAITING_MAIN_FILE" || mediaEntryWaiting(run.Entries[1]) {
		t.Fatal("主文件已完成后的独立错误不能继续显示等待主文件", run.Entries[1])
	}
	s.reserve = nil
	if err := s.executeMediaEntry(context.Background(), files, run, 1); err != nil {
		t.Fatal(err)
	}
	if run.Entries[1].Status != "done" || run.Entries[1].ResultCode == "WAITING_MAIN_FILE" {
		t.Fatal("真正开始处理后不得沿用等待主文件的状态", run.Entries[1])
	}
}

func TestMediaDependency115PendingIntentWaitSurvivesFinishAndClearsOnRetry(t *testing.T) {
	s, drive, files, run := newMedia115BatchTest(t, 2)
	run.Entries[1].File.MediaType, run.Entries[1].VideoID = "subtitle", run.Entries[0].File.ID
	drive.renameErr, drive.renameApplyIDs = errors.New("模拟主视频改名失败"), map[string]bool{}
	runMedia115BatchTest(t, s, drive, files, run)
	mediaAssertMainFileWaitingAfterFinish(t, run, 1)
	run.Entries[1].P115Move = media115BatchIntent(run.Rule, run.Entries[1], "20")
	run.processing = map[string]bool{run.Entries[1].File.ID: true}
	drive.renameErr, drive.renameApplyIDs = nil, nil
	runMedia115BatchTest(t, s, drive, files, run)
	mediaAssertMainFileWaitingAfterFinish(t, run, 1)
	if drive.moves != 0 || drive.renames != 1 || run.Entries[1].P115Move == nil {
		t.Fatal("待核实的附件移动也必须等待主视频", run.Entries[1], drive.moveBatches)
	}
	run.Entries[0].Status = "done"
	runMedia115BatchTest(t, s, drive, files, run)
	if run.Entries[1].Status != "done" || run.Entries[1].ResultCode == "WAITING_MAIN_FILE" {
		t.Fatal("主文件成功后恢复附件应清除等待码", run.Entries[1])
	}
}

func TestMediaDependencyCD2BatchWaitSurvivesFinishAndClearsOnRetry(t *testing.T) {
	s, fs, store, rule := newMediaCD2BatchOrganizer(t)
	video := mediaCD2BatchEntry(fs, rule, "original.mkv", "Work/Season 01/S01E01.mkv")
	subtitle := mediaCD2BatchEntry(fs, rule, "original.ass", "Work/Season 01/S01E01.ass")
	other := mediaCD2BatchEntry(fs, rule, "other.mkv", "Work/Season 01/S01E02.mkv")
	subtitle.File.MediaType, subtitle.VideoID = "subtitle", video.File.ID
	fs.renameFailures[video.File.ID] = errors.New("模拟主视频改名失败")
	run := mediaCD2BatchRun(t, s, rule, video, subtitle, other)
	if err := s.process(context.Background(), &run, "execute"); err != nil {
		t.Fatal(err)
	}
	saved := mediaCD2BatchPersistedRun(t, store, run.ID)
	subtitleIndex := slices.IndexFunc(saved.Entries, func(entry MediaOrganizeEntry) bool { return entry.File.ID == subtitle.File.ID })
	if subtitleIndex < 0 {
		t.Fatal("失败主文件的字幕上下文必须保留", saved.Entries)
	}
	mediaAssertMainFileWaitingAfterFinish(t, &saved, subtitleIndex)
	otherIndex := slices.IndexFunc(saved.Entries, func(entry MediaOrganizeEntry) bool { return entry.File.ID == other.File.ID })
	if otherIndex < 0 || !saved.Entries[otherIndex].Reference || saved.Entries[otherIndex].Result == nil || saved.Entries[otherIndex].Result.Status != "done" || saved.Entries[otherIndex].Result.At.IsZero() || saved.Entries[otherIndex].Status != "done" || saved.Entries[otherIndex].Target != other.Target {
		t.Fatal("无关成功视频应保留精简完成结果及目标引用", saved.Entries)
	}
	delete(fs.renameFailures, video.File.ID)
	if err := s.process(context.Background(), &saved, "execute"); err != nil {
		t.Fatal(err)
	}
	subtitleIndex = slices.IndexFunc(saved.Entries, func(entry MediaOrganizeEntry) bool { return entry.File.ID == subtitle.File.ID })
	if subtitleIndex < 0 || saved.Entries[subtitleIndex].Status != "done" || saved.Entries[subtitleIndex].ResultCode == "WAITING_MAIN_FILE" {
		t.Fatal("主视频恢复成功后CD2字幕应清除等待码", saved.Entries)
	}
	for _, entry := range []MediaOrganizeEntry{video, subtitle, other} {
		if _, exists := fs.files[entry.Target]; !exists {
			t.Fatal("重试后视频、字幕或其他已完成视频目标缺失", entry.Target)
		}
	}
}

func TestMediaDependencyAttachmentWaitRequiresUniqueOwner(t *testing.T) {
	video := OrganizeFile{ShareFile: p115.ShareFile{ID: "video", Name: "Work.S01E01.mkv"}, ParentID: "folder", SourcePath: "folder/Work.S01E01.mkv", MediaType: "video"}
	companion := OrganizeFile{ShareFile: p115.ShareFile{ID: "nfo", Name: "Work.S01E01.nfo"}, ParentID: "folder", SourcePath: "folder/Work.S01E01.nfo", MediaType: "attachment"}
	orphan := OrganizeFile{ShareFile: p115.ShareFile{ID: "orphan", Name: "Other.nfo"}, ParentID: "folder", SourcePath: "folder/Other.nfo", MediaType: "attachment"}
	run := MediaOrganizeRun{Rule: MediaOrganizeRule{Target: "/target"}, Entries: []MediaOrganizeEntry{
		{File: video, Group: "1", Status: "needs_match"}, {File: companion}, {File: orphan, ResultCode: "WAITING_MAIN_FILE"},
	}}
	mediaOrganizeAttachments(&run, nil)
	if run.Entries[1].ResultCode != "WAITING_MAIN_FILE" || run.Entries[1].VideoID != video.ID || run.Entries[2].ResultCode == "WAITING_MAIN_FILE" || run.Entries[2].VideoID != "" {
		t.Fatal("唯一关联未就绪属于等待，真正无主文件的附件保留原状态", run.Entries)
	}
	stem, directory := "Work.S01E01", "Work/Season 01"
	mediaOrganizeAttachments(&run, map[string]naming.Result{video.ID: {Directory: directory, Name: stem + ".mkv", Stem: stem, Extension: ".mkv", RelativePath: path.Join(directory, stem+".mkv")}})
	if run.Entries[1].Status != "ready" || run.Entries[1].ResultCode == "WAITING_MAIN_FILE" || run.Entries[2].ResultCode == "WAITING_MAIN_FILE" {
		t.Fatal("主文件已命名后附件进入处理，清除旧等待码", run.Entries)
	}
}
