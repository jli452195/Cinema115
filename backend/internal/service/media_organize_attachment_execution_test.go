package service

import (
	"context"
	"encoding/json"
	"path"
	"testing"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/storage"
)

type mediaAttachmentSaveCounter struct {
	mediaOrganizeStorage
	saved []MediaOrganizeRun
}

func (s *mediaAttachmentSaveCounter) SaveMediaOrganize(ctx context.Context, settings string, updates []storage.MediaOrganizeRunRow, removed []string) error {
	for _, row := range updates {
		var run MediaOrganizeRun
		if err := json.Unmarshal([]byte(row.Record), &run); err != nil {
			return err
		}
		s.saved = append(s.saved, run)
	}
	return s.mediaOrganizeStorage.SaveMediaOrganize(ctx, settings, updates, removed)
}

func TestMediaAttachmentSelectedRunsOnceAndSharesRunningCheckpoint(t *testing.T) {
	s, files, fs, run, source, target := mediaAttachmentCD2Fixture(t)
	run.Entries[1].AttachmentKeep = nil
	fs.deleteErr = context.DeadlineExceeded
	counter := &mediaAttachmentSaveCounter{mediaOrganizeStorage: s.db}
	s.db = counter
	fs.beforeDelete = func() {
		current, err := s.Snapshot(run.ID)
		if err != nil || current.Status != "running" {
			t.Fatal("外部操作之前必须发布实际运行态", current, err)
		}
		task, err := s.Task(run.ID, run.Entries[0].File.ID)
		if err != nil || task.CanDelete {
			t.Fatal("附件正在操作时应保护所属视频，避免删除与文件操作竞争", task, err)
		}
	}
	if err := s.execute(context.Background(), files, run); err != nil {
		t.Fatal(err)
	}
	entry := run.Entries[1]
	if entry.Status != "error" || entry.AttachmentKeep != nil || fs.deleteCalls != 1 || fs.files[source].ID != "" {
		t.Fatal("回收回执失败结束本次尝试并清空选择，不能同轮重试", entry, fs.deleteCalls)
	}
	if len(counter.saved) != 3 {
		t.Fatal("运行态、删除前和结果共用必要的检查点", counter.saved)
	}
	for _, saved := range counter.saved {
		for _, entry := range saved.Entries {
			if entry.AttachmentKeep != nil {
				t.Fatal("附件选择不能进入持久化记录", entry)
			}
		}
	}
	if task, err := s.Task(run.ID, run.Entries[0].File.ID); err != nil || !task.CanDelete {
		t.Fatal("本次失败结束后应立即解除所属视频的执行保护", task, err)
	}
	if err := s.execute(context.Background(), files, run); err != nil {
		t.Fatal(err)
	}
	if run.Entries[0].Status != "done" || run.Entries[1].Status != "done" || run.Entries[1].AttachmentKeep != nil || fs.deleteCalls != 1 || fs.files[target].ID == "" {
		t.Fatal("下一轮只核实失败附件，已成功主视频与已生效删除均不重做", run.Entries, fs.deleteCalls)
	}
}

func TestMediaAttachmentChoiceDiscoveredDuringExecutionDoesNotRunTwice(t *testing.T) {
	s, files, fs, run, source, targetPath := mediaAttachmentCD2Fixture(t)
	target := fs.files[targetPath]
	delete(fs.files, targetPath)
	run.Entries[1].AttachmentKeep = nil
	fs.deleteErr = context.DeadlineExceeded
	targetReads := 0
	fs.onList = func(directory string) error {
		if directory == path.Dir(targetPath) {
			targetReads++
			if targetReads == 2 {
				fs.files[targetPath] = target
			}
		}
		return nil
	}
	if err := s.execute(context.Background(), files, run); err != nil {
		t.Fatal(err)
	}
	if run.Entries[1].Status != "error" || run.Entries[1].AttachmentKeep != nil || fs.files[source].ID != "" || fs.deleteCalls != 1 {
		t.Fatal("普通执行中新发现的选择失败即清空，不能被末尾再次执行", run.Entries[1], fs.deleteCalls)
	}
}

func TestMediaAttachmentPrepareOnlyPlansWithoutSaving(t *testing.T) {
	s, files, _, run, _, _ := mediaAttachmentCD2Fixture(t)
	run.Entries[1].AttachmentKeep = nil
	counter := &mediaAttachmentSaveCounter{mediaOrganizeStorage: s.db}
	s.db = counter
	if err := s.prepareAttachmentChoices(context.Background(), files, run); err != nil {
		t.Fatal(err)
	}
	if len(counter.saved) != 0 || run.Entries[1].AttachmentKeep == nil || run.Entries[1].AttachmentKeep.Phase != "selected" {
		t.Fatal("只读选择阶段不单独保存，由执行入口统一保存运行态", counter.saved, run.Entries[1])
	}
}

func TestMediaAttachment115ReusesDiscoveryButRefreshesAcrossDeletion(t *testing.T) {
	for _, changed := range []bool{false, true} {
		name := "stable"
		if changed {
			name = "changed-before-delete"
		}
		t.Run(name, func(t *testing.T) {
			s, drive, files, run := newMedia115BatchTest(t, 0)
			source := p115.ShareFile{ID: "501", Name: "Work.S01E01(1).nfo", Size: 13}
			target := p115.ShareFile{ID: "601", Name: "Work.S01E01.nfo", Size: 27}
			drive.dirs["10"], drive.dirs["20"] = []p115.ShareFile{source}, []p115.ShareFile{target}
			run.Groups = []MediaOrganizeGroup{{ID: "1", Kind: "tv", TMDBID: 42}}
			run.Entries = []MediaOrganizeEntry{
				{File: OrganizeFile{ShareFile: p115.ShareFile{ID: "video", Name: "Work.S01E01.mkv"}, MediaType: "video"}, Group: "1", Relative: "Work.S01E01.mkv", Status: "done"},
				{File: OrganizeFile{ShareFile: source, ParentID: "10", SourcePath: source.Name, MediaType: "attachment"}, Group: "1", VideoID: "video", Relative: target.Name, Target: path.Join(run.Rule.Target, target.Name), Status: "ready"},
			}
			drive.beforeSnapshot = func(id string) error {
				if changed && id == "20" && drive.reads[id] == 2 {
					drive.dirs[id][0].ID = "保留项已被替换"
				}
				return nil
			}
			drive.beforeDelete = func() {
				if drive.reads["10"] != 2 || drive.reads["20"] != 2 {
					t.Fatal("删除前必须重新核对双方", drive.reads)
				}
				selection := run.Entries[1].AttachmentKeep
				if selection.Phase == "recycling" || selection.Source.ID != source.ID {
					t.Fatal("外部删除仅使用本次核对的双方身份", selection)
				}
			}
			if err := s.executeMediaEntry(context.Background(), files, run, 1); err != nil {
				t.Fatal(err)
			}
			if changed {
				if run.Entries[1].Status != "error" || len(drive.deleted) != 0 || len(drive.dirs["10"]) != 1 {
					t.Fatal("只复用发现阶段快照，删除前保留项变化必须阻止回收", run.Entries[1], drive.deleted)
				}
			} else if run.Entries[1].Status != "done" || drive.reads["20"] != 3 || drive.reads["10"] != 3 {
				t.Fatal("发现阶段同一次目录结果可复用，删除前后核验各保留一次", run.Entries[1], drive.reads)
			}
		})
	}
}

type mediaAttachmentUnappliedDelete struct{ *mediaAttachmentDeleteFake }

func (f *mediaAttachmentUnappliedDelete) Delete(context.Context, []string) error {
	f.deleteCalls++
	return nil
}

func TestMediaAttachmentCD2RequiresDeletionConfirmation(t *testing.T) {
	s, files, fs, run, source, _ := mediaAttachmentCD2Fixture(t)
	files.cd2 = &mediaAttachmentUnappliedDelete{mediaAttachmentDeleteFake: fs}
	if err := s.executePendingAttachmentChoices(context.Background(), files, run, nil); err != nil {
		t.Fatal(err)
	}
	if run.Entries[1].Status != "error" || run.Entries[1].AttachmentKeep != nil || fs.files[source].ID == "" || fs.deleteCalls != 1 {
		t.Fatal("删除响应成功也必须核实来源消失，不得复用删除前结果冒充完成", run.Entries[1], fs.deleteCalls)
	}
}
