package service

import (
	"context"
	"errors"
	"path"
	"testing"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/storage"
)

func mediaAttachment115AttemptFixture(t *testing.T) (*MediaOrganizer, *media115BatchTestDrive, *mediaOrganizeFiles, *MediaOrganizeRun) {
	t.Helper()
	s, drive, files, run := newMedia115BatchTest(t, 0)
	t.Cleanup(files.close)
	source := p115.ShareFile{ID: "501", Name: "Work.S01E01(1).nfo", Size: 13}
	target := p115.ShareFile{ID: "601", Name: "Work.S01E01.nfo", Size: 27}
	drive.dirs["10"], drive.dirs["20"] = []p115.ShareFile{source}, []p115.ShareFile{target}
	run.Groups = []MediaOrganizeGroup{{ID: "1", Kind: "tv", TMDBID: 42}}
	run.Entries = []MediaOrganizeEntry{
		{File: OrganizeFile{ShareFile: p115.ShareFile{ID: "video", Name: "Work.S01E01.mkv"}, MediaType: "video"}, Group: "1", Relative: "Work.S01E01.mkv", Status: "done"},
		{File: OrganizeFile{ShareFile: source, ParentID: "10", SourcePath: source.Name, MediaType: "attachment"}, Group: "1", VideoID: "video", Relative: target.Name, Target: path.Join(run.Rule.Target, target.Name), Status: "ready"},
	}
	return s, drive, files, run
}

func TestMediaAttachment115UsesOriginalIDWhenNamesCollide(t *testing.T) {
	s, drive, files, run := mediaAttachment115AttemptFixture(t)
	original := drive.dirs["10"][0]
	replacement := original
	replacement.ID = "同名的其他文件"
	drive.dirs["10"] = []p115.ShareFile{replacement, original}
	if err := s.executeMediaEntry(context.Background(), files, run, 1); err != nil {
		t.Fatal(err)
	}
	if run.Entries[1].Status != "done" || run.Entries[1].AttachmentKeep != nil || len(drive.deleted) != 1 || drive.deleted[0] != original.ID || len(drive.dirs["10"]) != 1 || drive.dirs["10"][0].ID != replacement.ID {
		t.Fatal("发现、删除前核验和删除后确认均应按原 ID，保留同名其他文件", run.Entries[1], drive.deleted, drive.dirs["10"])
	}
}

func TestMediaAttachment115AppliedDeleteRetryPreservesSameNameReplacement(t *testing.T) {
	s, drive, files, run := mediaAttachment115AttemptFixture(t)
	original := drive.dirs["10"][0]
	drive.deleteErr, drive.applyOnError = errors.New("删除生效但回执失败"), true
	if err := s.executeMediaEntry(context.Background(), files, run, 1); err != nil {
		t.Fatal(err)
	}
	if run.Entries[1].Status != "error" || run.Entries[1].AttachmentKeep != nil || len(drive.dirs["10"]) != 0 {
		t.Fatal("回执失败只结束本次尝试，不保留恢复计划", run.Entries[1], drive.dirs["10"])
	}
	replacement := original
	replacement.ID = "后来出现的同名文件"
	drive.dirs["10"] = []p115.ShareFile{replacement}
	drive.deleteErr, drive.applyOnError = nil, false
	if err := s.executeMediaEntry(context.Background(), files, run, 1); err != nil {
		t.Fatal(err)
	}
	if run.Entries[1].Status != "done" || len(drive.deleted) != 1 || len(drive.dirs["10"]) != 1 || drive.dirs["10"][0].ID != replacement.ID || len(drive.dirs["20"]) != 1 {
		t.Fatal("重试确认原 ID 消失且保留项仍在，不删除同名替身", run.Entries[1], drive.deleted, drive.dirs)
	}
}

func TestMediaAttachmentCD2AppliedDeleteRetryPreservesSameNameReplacement(t *testing.T) {
	s, files, fs, run, source, _ := mediaAttachmentCD2Fixture(t)
	fs.deleteErr = errors.New("删除生效但回执失败")
	if err := s.executeMediaEntry(context.Background(), files, run, 1); err != nil {
		t.Fatal(err)
	}
	replacement := fs.add(source, false, 13)
	fs.deleteErr = nil
	if err := s.executeMediaEntry(context.Background(), files, run, 1); err != nil {
		t.Fatal(err)
	}
	if run.Entries[1].Status != "error" || run.Entries[1].AttachmentKeep != nil || fs.deleteCalls != 1 || fs.files[source].ID != replacement.ID {
		t.Fatal("CD2 原路径出现不同 ID 时应保留文件并结束尝试", run.Entries[1], fs.deleted, fs.files[source])
	}
}

type mediaAttachmentTransientSaveFailure struct {
	mediaOrganizeStorage
	calls int
	err   error
}

func (s *mediaAttachmentTransientSaveFailure) SaveMediaOrganize(ctx context.Context, settings string, updates []storage.MediaOrganizeRunRow, removed []string) error {
	s.calls++
	if s.calls == 1 {
		return s.err
	}
	return s.mediaOrganizeStorage.SaveMediaOrganize(ctx, settings, updates, removed)
}

func TestMediaAttachmentCheckpointErrorStopsAttempt(t *testing.T) {
	s, files, fs, run, source, _ := mediaAttachmentCD2Fixture(t)
	saveFailure := errors.New("单次数据库保存失败")
	store := &mediaAttachmentTransientSaveFailure{mediaOrganizeStorage: s.db, err: saveFailure}
	s.db = store
	err := s.executeMediaEntry(context.Background(), files, run, 1)
	if !errors.Is(err, saveFailure) || store.calls != 1 || fs.deleteCalls != 0 || fs.files[source].ID == "" || run.Entries[1].Status == "done" || run.Entries[1].AttachmentKeep != nil {
		t.Fatal("数据库失败必须返回，不能再次保存吞掉错误或继续删除", err, store.calls, fs.deleteCalls, run.Entries[1])
	}
}

func TestMediaAttachmentCanceledAttemptClearsSelection(t *testing.T) {
	for _, beforeRead := range []bool{true, false} {
		name := "取消发生在开始前"
		if !beforeRead {
			name = "取消发生在核验中"
		}
		t.Run(name, func(t *testing.T) {
			s, files, fs, run, source, target := mediaAttachmentCD2Fixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if beforeRead {
				cancel()
			} else {
				fs.beforeStat = func(full string) {
					if full == target {
						cancel()
					}
				}
			}
			err := s.executeMediaEntry(ctx, files, run, 1)
			if !errors.Is(err, context.Canceled) || fs.deleteCalls != 0 || fs.files[source].ID == "" || run.Entries[1].Status == "done" || run.Entries[1].AttachmentKeep != nil {
				t.Fatal("调用方取消必须返回并清除本次选择，保留来源", err, fs.deleteCalls, run.Entries[1])
			}
		})
	}
}

func TestMediaAttachmentRetryDiscardsOldSelection(t *testing.T) {
	s, files, fs, run, source, target := mediaAttachmentCD2Fixture(t)
	run.Entries[1].AttachmentKeep.Source = &MediaAttachmentFileSnapshot{ID: "过时的删除计划", Path: "/旧目录/附件.nfo"}
	run.Entries[1].AttachmentKeep.Phase = "recycling"
	currentTarget := fs.files[target]
	currentTarget.ID = "当前保留项"
	fs.files[target] = currentTarget
	if err := s.prepareAttachmentChoices(context.Background(), files, run); err != nil {
		t.Fatal(err)
	}
	if err := s.executePendingAttachmentChoices(context.Background(), files, run, nil); err != nil {
		t.Fatal(err)
	}
	if run.Entries[1].Status != "done" || fs.files[source].ID != "" || fs.files[target].ID != currentTarget.ID || fs.deleteCalls != 1 {
		t.Fatal("重试必须按当前来源及目标执行，忽略旧的选择快照", run.Entries[1], fs.deleted)
	}
}

func TestMediaAttachmentFailedPreparationClearsSelection(t *testing.T) {
	s, files, fs, run, source, target := mediaAttachmentCD2Fixture(t)
	readFailure := errors.New("读取目标失败")
	fs.onList = func(directory string) error {
		if directory == path.Dir(target) {
			return readFailure
		}
		return nil
	}
	err := s.prepareAttachmentChoices(context.Background(), files, run)
	if !errors.Is(err, readFailure) || run.Entries[1].AttachmentKeep != nil || fs.deleteCalls != 0 || fs.files[source].ID == "" {
		t.Fatal("选择阶段读取失败也应清空本次缓存并保留来源", err, run.Entries[1], fs.deleteCalls)
	}
}

func TestMediaAttachmentCD2ChangedSourceIsNotMissing(t *testing.T) {
	for _, scenario := range []string{"directory", "readonly", "different-path"} {
		t.Run(scenario, func(t *testing.T) {
			s, files, fs, run, source, _ := mediaAttachmentCD2Fixture(t)
			current := fs.files[source]
			switch scenario {
			case "directory":
				current.Directory = true
			case "readonly":
				current.ReadOnly = true
			case "different-path":
				current.Path = path.Join(path.Dir(source), "来源已改名.nfo")
			}
			fs.files[source] = current
			if err := s.executeMediaEntry(context.Background(), files, run, 1); err != nil {
				t.Fatal(err)
			}
			if run.Entries[1].Status != "error" || run.Entries[1].AttachmentKeep != nil || fs.deleteCalls != 0 || fs.files[source].ID == "" {
				t.Fatal("来源属性变化不能当作来源消失并误报成功", run.Entries[1], fs.deleted)
			}
		})
	}
}

func TestMediaAttachment115RenamedOriginalIsNotMissing(t *testing.T) {
	s, drive, files, run := mediaAttachment115AttemptFixture(t)
	drive.dirs["10"][0].Name = "原附件已改名.nfo"
	if err := s.executeMediaEntry(context.Background(), files, run, 1); err != nil {
		t.Fatal(err)
	}
	if run.Entries[1].Status != "error" || run.Entries[1].AttachmentKeep != nil || len(drive.deleted) != 0 || len(drive.dirs["10"]) != 1 {
		t.Fatal("原 ID 改名仍在目录时必须保留，不能当作删除完成", run.Entries[1], drive.deleted)
	}
}
