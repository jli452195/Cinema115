package service

import (
	"context"
	"errors"
	"maps"
	"path"
	"strings"
	"testing"

	"strm115/backend/internal/client/clouddrive2"
	"strm115/backend/internal/client/p115"
)

// 只包装内存假文件系统，模拟接口成功但状态未变以及核验期间身份变化。
type mediaFailureDuplicateCD2CheckFS struct {
	*mediaCD2Fake
	keepSource  bool
	deleteCalls int
	beforeStat  func(string)
	afterDelete func()
}

func (f *mediaFailureDuplicateCD2CheckFS) Stat(ctx context.Context, full string, force bool) (clouddrive2.File, error) {
	if f.beforeStat != nil {
		f.beforeStat(full)
	}
	return f.mediaCD2Fake.Stat(ctx, full, force)
}

func (f *mediaFailureDuplicateCD2CheckFS) Delete(ctx context.Context, paths []string) error {
	f.deleteCalls++
	if f.keepSource {
		return ctx.Err()
	}
	if err := f.mediaCD2Fake.Delete(ctx, paths); err != nil {
		return err
	}
	if f.afterDelete != nil {
		f.afterDelete()
	}
	return nil
}

func newMediaFailureDuplicateCD2Check(t *testing.T) (*MediaOrganizer, *mediaFailureDuplicateCD2CheckFS, *mediaOrganizeFiles, *MediaOrganizeRun, clouddrive2.File, clouddrive2.File) {
	t.Helper()
	s, _, _, connection, rule := newMediaCD2Organizer(t)
	rule.Failure, rule.DeleteDuplicates = "/其他网盘/failed", true
	rule, err := s.SaveRule(rule)
	if err != nil {
		t.Fatal(err)
	}
	base := connection.filesystem
	source := base.add(path.Join(rule.Source, "Synthetic.S01E01.mkv"), false, 10)
	source.Hashes = map[uint32]string{2: strings.Repeat("A", 40)}
	base.files[source.Path] = source
	keeper := base.add(path.Join(rule.Failure, source.Name), false, 20)
	keeper.Hashes = maps.Clone(source.Hashes)
	base.files[keeper.Path] = keeper
	fs := &mediaFailureDuplicateCD2CheckFS{mediaCD2Fake: base}
	files, err := s.files(context.Background(), rule)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(files.close)
	files.cd2 = fs
	run, err := s.Start(rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	run.Status = "partial"
	run.Entries = []MediaOrganizeEntry{{
		File:      OrganizeFile{ShareFile: mediaCD2ShareFile(source), ParentID: rule.Source, SourcePath: source.Name, MediaType: "video"},
		CD2FileID: source.ID, CD2Hashes: maps.Clone(source.Hashes), Modified: source.Modified.UnixNano(),
		Relative: source.Name, Status: "needs_match", Message: "人工合成的未匹配测试文件",
	}}
	return s, fs, files, &run, source, keeper
}

func mediaFailureDuplicateAssertUnconfirmed(t *testing.T, s *MediaOrganizer, run *MediaOrganizeRun) {
	t.Helper()
	entry := run.Entries[0]
	if entry.Failure == nil || entry.Failure.State == "stored" || entry.Failure.Message == "" || entry.Failure.FileID != "" || entry.Failure.CD2FileID != "" || entry.CD2Current != nil || entry.Status == "done" {
		t.Fatal("未核实的去重不能发布成功、保留身份或 stored", entry)
	}
	saved, err := s.Snapshot(run.ID)
	if err != nil || len(saved.Entries) != 1 || saved.Entries[0].Failure == nil || saved.Entries[0].Failure.State == "stored" || saved.Entries[0].Failure.Message == "" {
		t.Fatal("持久检查点也必须保留未核实结果", saved.Entries, err)
	}
}

func TestMediaFailureDuplicateChecksCD2SuccessfulDeleteWithSourcePresent(t *testing.T) {
	s, fs, files, run, source, keeper := newMediaFailureDuplicateCD2Check(t)
	fs.keepSource = true
	if _, err := s.quarantineEntry(context.Background(), files, run, 0, false); err != nil {
		t.Fatal(err)
	}
	mediaFailureDuplicateAssertUnconfirmed(t, s, run)
	if fs.deleteCalls != 1 || len(fs.deleted) != 0 || fs.files[source.Path].ID != source.ID || fs.files[keeper.Path].ID != keeper.ID {
		t.Fatal("删除接口返回 nil 仍须确认来源消失，不能继续删除或改动目标", fs.deleteCalls, fs.deleted, fs.files)
	}
}

func TestMediaFailureDuplicateChecksCD2KeeperChanges(t *testing.T) {
	for _, phase := range []string{"before-delete", "after-delete"} {
		for _, field := range []string{"id", "sha1"} {
			t.Run(phase+"/"+field, func(t *testing.T) {
				s, fs, files, run, source, keeper := newMediaFailureDuplicateCD2Check(t)
				mutate := func() {
					changed := fs.files[keeper.Path]
					if field == "id" {
						changed.ID = "synthetic-replaced-keeper"
					} else {
						changed.Hashes = map[uint32]string{2: strings.Repeat("B", 40)}
					}
					fs.files[keeper.Path] = changed
				}
				if phase == "before-delete" {
					fs.beforeStat = func(full string) {
						if full == keeper.Path {
							mutate()
							fs.beforeStat = nil
						}
					}
				} else {
					fs.afterDelete = mutate
				}
				if _, err := s.quarantineEntry(context.Background(), files, run, 0, false); err != nil {
					t.Fatal(err)
				}
				mediaFailureDuplicateAssertUnconfirmed(t, s, run)
				changed := fs.files[keeper.Path]
				if field == "id" && changed.ID == keeper.ID || field == "sha1" && strings.EqualFold(changed.Hashes[2], keeper.Hashes[2]) {
					t.Fatal("本场景未触发保留文件身份变化", changed)
				}
				_, sourceExists := fs.files[source.Path]
				wantDeletes := 0
				if phase == "after-delete" {
					wantDeletes = 1
				}
				if fs.deleteCalls != wantDeletes || len(fs.deleted) != wantDeletes || sourceExists != (phase == "before-delete") {
					t.Fatal("目标变化必须停止发布和后续删除", fs.deleteCalls, fs.deleted, sourceExists)
				}
			})
		}
	}
}

type mediaFailureDuplicate115CheckDrive struct {
	*mediaCleanupDrive
	directoryErrors map[string]error
}

func (d *mediaFailureDuplicate115CheckDrive) ReadDirectory(ctx context.Context, cid, cookie string) (p115.DriveDirectory, error) {
	if err := d.directoryErrors[cid]; err != nil {
		return p115.DriveDirectory{}, err
	}
	return d.mediaCleanupDrive.ReadDirectory(ctx, cid, cookie)
}

func newMediaFailureDuplicate115Check(t *testing.T) (*MediaOrganizer, *mediaFailureDuplicate115CheckDrive, *mediaOrganizeFiles, *MediaOrganizeRun, p115.ShareFile) {
	t.Helper()
	s, base, files, entry, keeper := newMedia115DuplicateTest(t)
	files.rule.Failure, files.rule.FailureID = "/failed", "40"
	keeper.Name = entry.File.Name
	base.dirs["40"] = []p115.ShareFile{keeper}
	drive := &mediaFailureDuplicate115CheckDrive{mediaCleanupDrive: base, directoryErrors: make(map[string]error)}
	s.drive, files.drive = drive, drive
	run, err := s.Start(files.rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	entry.Status, entry.Message = "needs_match", "人工合成的未匹配测试文件"
	entry.Failure = &MediaOrganizeFailure{State: "moving", ParentID: "40", Reason: entry.Message, Message: errMediaDuplicateDone.Error()}
	run.Rule, run.Status, run.Entries = files.rule, "partial", []MediaOrganizeEntry{entry}
	return s, drive, files, &run, keeper
}

func TestMediaFailureDuplicateChecks115OldSuccessTextDoesNotHideReadErrors(t *testing.T) {
	for _, scenario := range []string{"source-directory", "source-list", "keeper-list"} {
		t.Run(scenario, func(t *testing.T) {
			s, drive, files, run, _ := newMediaFailureDuplicate115Check(t)
			failure := errors.New("人工合成的目录查询异常")
			if scenario == "source-directory" {
				drive.directoryErrors["10"] = failure
			} else {
				drive.beforeRead = func(cid string, _ int) error {
					if scenario == "source-list" && cid == "10" || scenario == "keeper-list" && cid == "40" {
						return failure
					}
					return nil
				}
			}
			if _, err := s.quarantineEntry(context.Background(), files, run, 0, true); err != nil {
				t.Fatal(err)
			}
			mediaFailureDuplicateAssertUnconfirmed(t, s, run)
			if !strings.Contains(run.Entries[0].Failure.Message, failure.Error()) {
				t.Fatal("必须暴露当前查询异常，不能沿用旧成功文案", run.Entries[0].Failure)
			}
			if len(drive.deleted) != 0 || drive.moves != 0 || drive.renames != 0 || len(drive.dirs["10"]) != 1 || len(drive.dirs["40"]) != 1 {
				t.Fatal("查询异常不能推断来源缺失或依赖旧成功文案继续写入", drive.deleted, drive.moves, drive.renames)
			}
		})
	}
}

func TestMediaFailureDuplicateChecks115KeeperChanges(t *testing.T) {
	for _, phase := range []string{"before-delete", "after-delete"} {
		for _, field := range []string{"id", "sha1"} {
			t.Run(phase+"/"+field, func(t *testing.T) {
				s, drive, files, run, keeper := newMediaFailureDuplicate115Check(t)
				mutate := func() {
					changed := drive.dirs["40"][0]
					if field == "id" {
						changed.ID = "synthetic-replaced-keeper"
					} else {
						changed.SHA1 = strings.Repeat("B", 40)
					}
					drive.dirs["40"][0] = changed
				}
				if phase == "before-delete" {
					drive.beforeRead = func(cid string, _ int) error {
						if cid == "10" {
							mutate()
							drive.beforeRead = nil
						}
						return nil
					}
				} else {
					drive.beforeDelete = mutate
				}
				if _, err := s.quarantineEntry(context.Background(), files, run, 0, true); err != nil {
					t.Fatal(err)
				}
				mediaFailureDuplicateAssertUnconfirmed(t, s, run)
				changed := drive.dirs["40"][0]
				if field == "id" && changed.ID == keeper.ID || field == "sha1" && strings.EqualFold(changed.SHA1, keeper.SHA1) {
					t.Fatal("本场景未触发保留文件身份变化", changed)
				}
				wantDeletes := 0
				if phase == "after-delete" {
					wantDeletes = 1
				}
				if len(drive.deleted) != wantDeletes || len(drive.dirs["10"]) != 1-wantDeletes || len(drive.dirs["40"]) != 1 || drive.moves != 0 || drive.renames != 0 {
					t.Fatal("目标身份或内容变化后不能发布 stored 或继续写入", drive.deleted, drive.dirs, drive.moves, drive.renames)
				}
			})
		}
	}
}
