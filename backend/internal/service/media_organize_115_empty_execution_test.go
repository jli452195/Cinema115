package service

import (
	"context"
	"errors"
	"path"
	"slices"
	"testing"

	"strm115/backend/internal/client/p115"
)

type media115ExecutionDrive struct {
	*mediaCleanupDrive
	beforeBatch func(string, []string)
	probes      int
	reads       int
	readErrors  []error
	probeErrors []error
}

type media115ExecutionTreeDrive struct {
	*media115ExecutionDrive
	treeCalls  int
	treeErr    error
	beforeTree func(string)
}

func (d *media115ExecutionTreeDrive) WalkDriveFiles(ctx context.Context, root, cookie string, visit func([]p115.DriveTreeEntry) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	d.cookies = append(d.cookies, cookie)
	d.treeCalls++
	if d.beforeTree != nil {
		d.beforeTree(root)
	}
	entries := make([]p115.DriveTreeEntry, 0)
	var walk func(string, string)
	walk = func(parent, relative string) {
		for _, file := range d.dirs[parent] {
			childPath := path.Join(relative, file.Name)
			entries = append(entries, p115.DriveTreeEntry{ShareFile: file, ParentCID: parent, RelativePath: childPath})
			if file.Directory {
				walk(file.ID, childPath)
			}
		}
	}
	walk(root, "")
	if err := visit(entries); err != nil {
		return err
	}
	return d.treeErr
}

func (d *media115ExecutionDrive) ProbeEmptyDirectory(ctx context.Context, id, cookie string) (p115.DriveDirectory, bool, error) {
	d.probes++
	if len(d.probeErrors) > 0 {
		err := d.probeErrors[0]
		d.probeErrors = d.probeErrors[1:]
		if err != nil {
			return p115.DriveDirectory{}, false, err
		}
	}
	dir, err := d.ReadDirectory(ctx, id, cookie)
	if err == nil {
		err = ctx.Err()
	}
	return dir, len(d.dirs[id]) == 0, err
}

func (d *media115ExecutionDrive) ReadDirectoryFiles(ctx context.Context, id, cookie string) (p115.DriveDirectory, []p115.ShareFile, error) {
	d.reads++
	if len(d.readErrors) > 0 {
		err := d.readErrors[0]
		d.readErrors = d.readErrors[1:]
		if err != nil {
			return p115.DriveDirectory{}, nil, err
		}
	}
	dir, err := d.ReadDirectory(ctx, id, cookie)
	if err == nil {
		err = ctx.Err()
	}
	return dir, slices.Clone(d.dirs[id]), err
}

func (d *media115ExecutionDrive) DeleteEntriesBatch(ctx context.Context, parent string, ids []string, cookie string) error {
	if d.beforeBatch != nil {
		d.beforeBatch(parent, ids)
	}
	return d.DeleteEntries(ctx, ids, cookie)
}

func newMedia115ExecutionTest(t *testing.T) (*MediaOrganizer, *media115ExecutionDrive, *mediaOrganizeFiles, *MediaOrganizeRun) {
	t.Helper()
	s, base, rule := newMediaCleanupCloud(t)
	drive := &media115ExecutionDrive{mediaCleanupDrive: base}
	s.drive = drive
	run, err := s.Start(rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	files, err := s.files(context.Background(), rule)
	if err != nil {
		t.Fatal(err)
	}
	return s, drive, files, &run
}

func TestMedia115EmptyExecutionKeepsSubtitleAndCleansBeforeNextVideo(t *testing.T) {
	s, drive, files, run := newMedia115ExecutionTest(t)
	drive.dirs["10"] = []p115.ShareFile{{ID: "30", Name: "first", Directory: true}, {ID: "31", Name: "later", Directory: true}}
	drive.paths["30"], drive.paths["31"] = "/inbox/first", "/inbox/later"
	drive.dirs["30"] = []p115.ShareFile{{ID: "501", Name: "First.S01E01.mkv", Size: 10}, {ID: "502", Name: "First.S01E01.ass", Size: 3}}
	drive.dirs["31"] = []p115.ShareFile{{ID: "503", Name: "Later.S01E01.mkv", Size: 11}}
	entries, err := files.scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	run.Entries = append([]MediaOrganizeEntry{}, entries[0], entries[1], entries[2])
	for i := range run.Entries {
		entry := &run.Entries[i]
		entry.Status, entry.Relative, entry.Target = "ready", entry.File.Name, path.Join(run.Rule.Target, entry.File.Name)
		if entry.File.MediaType == "subtitle" {
			entry.VideoID = "501"
		}
	}
	drive.beforeBatch = func(_ string, ids []string) {
		if ids[0] == "30" && drive.moves != 2 {
			t.Fatal("必须等字幕一起移动后、下一部视频之前清理目录", drive.moves)
		}
		snapshot, err := s.Snapshot(run.ID)
		if err != nil {
			t.Fatal(err)
		}
		last := snapshot.Entries[len(snapshot.Entries)-1]
		if last.File.ID != ids[0] || last.Status != "error" || last.Result == nil {
			t.Fatal("外部删除之前必须持久保存清理意图", last)
		}
	}
	drive.afterMove = func() {
		if drive.moves == 1 {
			if _, exists := drive.dirs["30"]; !exists {
				t.Fatal("视频移动后字幕仍在，不能删除来源目录")
			}
		}
		if drive.moves == 3 {
			if _, exists := drive.dirs["30"]; exists {
				t.Fatal("下一部视频移动前应完成前一个空目录的清理")
			}
		}
	}
	if err := s.execute(context.Background(), files, run); err != nil {
		t.Fatal(err)
	}
	if err := s.cleanupEmptyDirectories(context.Background(), files, run); err != nil {
		t.Fatal(err)
	}
	if drive.moves != 3 || len(drive.deleted) != 2 || len(drive.dirs["20"]) != 3 || len(drive.dirs["10"]) != 0 {
		t.Fatal("视频、字幕或目录清理结果不完整", drive.moves, drive.deleted, drive.dirs)
	}
}

func TestMedia115EmptyIntentFailureAndCancellationDoNotWrite(t *testing.T) {
	for _, scenario := range []string{"persistence", "canceled"} {
		t.Run(scenario, func(t *testing.T) {
			s, drive, files, run := newMedia115ExecutionTest(t)
			drive.dirs["10"] = []p115.ShareFile{{ID: "30", Name: "empty", Directory: true}}
			drive.paths["30"], drive.dirs["30"] = "/inbox/empty", []p115.ShareFile{}
			if _, err := files.scan(context.Background()); err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			if scenario == "persistence" {
				s.persistErr = errors.New("模拟检查点写入失败")
			} else {
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = canceled
			}
			if err := s.cleanupEmptyDirectories(ctx, files, run); err == nil || len(drive.deleted) != 0 {
				t.Fatal("检查点失败或已取消时不得删除", drive.deleted, err)
			}
		})
	}
}

func TestMedia115EmptyQuarantineUsesFrozenCredentials(t *testing.T) {
	s, drive, files, run := newMedia115ExecutionTest(t)
	run.Rule.Failure, run.Rule.FailureID = "/failed", "40"
	files.rule = run.Rule
	drive.dirs["10"] = []p115.ShareFile{{ID: "30", Name: "bad", Directory: true}}
	drive.paths["30"] = "/inbox/bad"
	drive.dirs["30"] = []p115.ShareFile{{ID: "501", Name: "Bad.mkv", Size: 7}}
	entries, err := files.scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	run.Entries = entries
	run.Entries[0].Status, run.Entries[0].Message = "invalid", "模拟识别失败"
	newCookie := "UID=123_snapshot; SEID=replaced"
	if err := s.accounts.Update(run.Rule.AccountID, 2, "整理账号", &newCookie); err != nil {
		t.Fatal(err)
	}
	if err := s.quarantine(context.Background(), files, run, false); err != nil {
		t.Fatal(err)
	}
	if files.cloudEmpty.err != nil || len(drive.deleted) != 1 || drive.deleted[0] != "30" || run.Entries[0].Failure.State != "stored" {
		t.Fatal("隔离成功后应及时清理原空目录", drive.deleted, files.cloudEmpty.err, run.Entries)
	}
	for _, cookie := range drive.cookies {
		if cookie != files.cookie {
			t.Fatal("任务中途切换了凭据")
		}
	}
}

func TestMedia115EmptyFailedRescanDiscardsPreviousCompleteSnapshot(t *testing.T) {
	drive := newMedia115RegressionDrive(t, 1)
	s, files, run := media115RegressionScan(t, drive)
	drive.scanErrorID, drive.scanErr = "100", errors.New("模拟第二次扫描失败")
	if _, err := files.scan(context.Background()); err == nil {
		t.Fatal("再次扫描应返回错误")
	}
	if files.cloudEmpty == nil || files.cloudEmpty.complete {
		t.Fatal("不完整的再次扫描不能沿用上次完整快照")
	}
	if err := s.cleanupEmptyDirectories(context.Background(), files, run); err != nil || len(drive.batches) != 0 {
		t.Fatal("不完整快照不能启动空目录清理", drive.batches, err)
	}
}

func TestMedia115EmptyReadbackRetriesOnlyOneTransientSnapshot(t *testing.T) {
	for _, scenario := range []string{"recovers", "still-changing", "identity-error"} {
		t.Run(scenario, func(t *testing.T) {
			s, drive, files, run := newMedia115ExecutionTest(t)
			drive.dirs["10"] = []p115.ShareFile{{ID: "30", Name: "empty", Directory: true}}
			drive.paths["30"], drive.dirs["30"] = "/inbox/empty", []p115.ShareFile{}
			if _, err := files.scan(context.Background()); err != nil {
				t.Fatal(err)
			}
			drive.readErrors = []error{p115.ErrDriveDirectorySnapshotChanged}
			if scenario == "still-changing" {
				drive.readErrors = append(drive.readErrors, p115.ErrDriveDirectorySnapshotChanged)
			} else if scenario == "identity-error" {
				drive.readErrors = []error{errors.New("来源路径已变化")}
			}
			err := s.cleanup115EmptyDirectories(context.Background(), files, run)
			if scenario == "recovers" {
				if err != nil || drive.reads != 2 || run.Entries[0].Status != "done" {
					t.Fatal("一次短暂失配后完整严格读回应确认成功", drive.reads, run.Entries, err)
				}
			} else {
				expectedReads := 2
				if scenario == "identity-error" {
					expectedReads = 1
				}
				if err == nil || drive.reads != expectedReads || run.Entries[0].Status != "error" {
					t.Fatal("确认失败必须保留未知结果，身份错误不得重试", drive.reads, run.Entries, err)
				}
				if nextErr := s.cleanupEmptyDirectories(context.Background(), files, run); nextErr == nil {
					t.Fatal("收尾必须保留未知写入错误")
				}
			}
			if len(drive.batches) != 1 {
				t.Fatal("只读确认不能重放批量写入", drive.batches)
			}
		})
	}
}

func TestMedia115EmptyPrewriteVerificationRetriesOneTransientSnapshot(t *testing.T) {
	for _, nested := range []bool{false, true} {
		for _, recovers := range []bool{false, true} {
			name := "leaf"
			if nested {
				name = "nested"
			}
			if recovers {
				name += "/recovers"
			} else {
				name += "/still-changing"
			}
			t.Run(name, func(t *testing.T) {
				s, drive, files, run := newMedia115ExecutionTest(t)
				drive.dirs["10"] = []p115.ShareFile{{ID: "30", Name: "empty", Directory: true}}
				drive.paths["30"], drive.dirs["30"] = "/inbox/empty", []p115.ShareFile{}
				if nested {
					drive.dirs["30"] = []p115.ShareFile{{ID: "31", Name: "child", Directory: true}}
					drive.paths["31"], drive.dirs["31"] = "/inbox/empty/child", []p115.ShareFile{}
				}
				if _, err := files.scan(context.Background()); err != nil {
					t.Fatal(err)
				}
				errorsToReturn := []error{p115.ErrDriveDirectorySnapshotChanged}
				if !recovers {
					errorsToReturn = append(errorsToReturn, p115.ErrDriveDirectorySnapshotChanged)
				}
				if nested {
					drive.readErrors = errorsToReturn
				} else {
					drive.probeErrors = errorsToReturn
				}
				err := s.cleanup115EmptyDirectories(context.Background(), files, run)
				if recovers {
					if err != nil || len(drive.batches) != 1 || run.Entries[0].Status != "done" {
						t.Fatal("复核恢复完整后应正常删除一次", drive.batches, err)
					}
				} else if !errors.Is(err, p115.ErrDriveDirectorySnapshotChanged) || len(drive.batches) != 0 {
					t.Fatal("持续失配时不得删除", drive.batches, err)
				}
				expectedReads := 2
				if recovers {
					expectedReads++
				}
				if nested && drive.reads != expectedReads || !nested && drive.probes != 2 {
					t.Fatal("短暂失配最多补充一次只读请求", drive.reads, drive.probes)
				}
			})
		}
	}
}

func TestMedia115EmptyChangedReadbackUsesCompleteTree(t *testing.T) {
	for _, scenario := range []string{"deleted", "tree-error", "moved-into-child", "new-empty-directory", "new-subtitle"} {
		t.Run(scenario, func(t *testing.T) {
			s, base, files, run := newMedia115ExecutionTest(t)
			drive := &media115ExecutionTreeDrive{media115ExecutionDrive: base}
			files.drive, s.drive = drive, drive
			drive.dirs["10"] = []p115.ShareFile{{ID: "30", Name: "empty", Directory: true}}
			drive.paths["30"], drive.dirs["30"] = "/inbox/empty", []p115.ShareFile{}
			if _, err := files.scan(context.Background()); err != nil {
				t.Fatal(err)
			}
			drive.readErrors = []error{p115.ErrDriveDirectorySnapshotChanged}
			switch scenario {
			case "tree-error":
				drive.treeErr = errors.New("模拟完整树在返回部分内容后失败")
			case "moved-into-child":
				drive.noDelete = true
				drive.beforeTree = func(string) {
					drive.dirs["10"] = []p115.ShareFile{{ID: "31", Name: "new-parent", Directory: true}}
					drive.dirs["31"] = []p115.ShareFile{{ID: "30", Name: "empty", Directory: true}}
					drive.paths["31"], drive.paths["30"] = "/inbox/new-parent", "/inbox/new-parent/empty"
				}
			case "new-empty-directory":
				drive.beforeTree = func(string) {
					drive.dirs["10"] = []p115.ShareFile{{ID: "31", Name: "arrived", Directory: true}}
					drive.dirs["31"], drive.paths["31"] = []p115.ShareFile{}, "/inbox/arrived"
				}
			case "new-subtitle":
				drive.beforeTree = func(string) {
					drive.dirs["10"] = []p115.ShareFile{{ID: "501", Name: "late.ass", Size: 7}}
				}
			}
			err := s.cleanup115EmptyDirectories(context.Background(), files, run)
			confirmed := scenario != "tree-error" && scenario != "moved-into-child"
			if confirmed {
				if err != nil || run.Entries[0].Status != "done" || !files.cloudEmpty.removed["30"] {
					t.Fatal("完整新树证明目录消失后应确认成功", run.Entries, err)
				}
			} else {
				if err == nil || run.Entries[0].Status != "error" || files.cloudEmpty.removed["30"] {
					t.Fatal("不完整树或移入其他子目录不能确认删除", run.Entries, err)
				}
				if err := s.cleanupEmptyDirectories(context.Background(), files, run); err == nil {
					t.Fatal("收尾应保留未知结果")
				}
			}
			if drive.reads != 1 || drive.treeCalls != 1 || len(drive.batches) != 1 {
				t.Fatal("仅改读一次完整新树，列表和写入不能重放", drive.reads, drive.treeCalls, drive.batches)
			}
			if scenario == "new-empty-directory" {
				if _, kept := files.cloudEmpty.children["10"]["31"]; !kept || slices.Contains(drive.deleted, "31") {
					t.Fatal("新到目录应记录并保守留待下轮")
				}
			}
			if scenario == "new-subtitle" {
				if _, kept := files.cloudEmpty.children["10"]["501"]; !kept || len(drive.dirs["10"]) != 1 {
					t.Fatal("新树必须保留父目录的完整文件子项")
				}
			}
		})
	}
}

func TestMedia115EmptyChangedPrewriteSnapshotUsesCompleteTree(t *testing.T) {
	for _, nested := range []bool{false, true} {
		for _, scenario := range []string{"empty", "new-subtitle", "tree-error"} {
			name := "probe/" + scenario
			if nested {
				name = "nested/" + scenario
			}
			t.Run(name, func(t *testing.T) {
				s, base, files, _ := newMedia115ExecutionTest(t)
				drive := &media115ExecutionTreeDrive{media115ExecutionDrive: base}
				files.drive, s.drive = drive, drive
				drive.dirs["10"] = []p115.ShareFile{{ID: "30", Name: "empty", Directory: true}}
				drive.paths["30"], drive.dirs["30"] = "/inbox/empty", []p115.ShareFile{}
				if nested {
					drive.dirs["30"] = []p115.ShareFile{{ID: "31", Name: "child", Directory: true}}
					drive.paths["31"], drive.dirs["31"] = "/inbox/empty/child", []p115.ShareFile{}
					drive.readErrors = []error{p115.ErrDriveDirectorySnapshotChanged}
				} else {
					drive.probeErrors = []error{p115.ErrDriveDirectorySnapshotChanged}
				}
				if _, err := files.scan(context.Background()); err != nil {
					t.Fatal(err)
				}
				if scenario == "new-subtitle" {
					drive.beforeTree = func(string) {
						parent := "30"
						if nested {
							parent = "31"
						}
						drive.dirs[parent] = append(drive.dirs[parent], p115.ShareFile{ID: "501", Name: "late.ass", Size: 7})
					}
				} else if scenario == "tree-error" {
					drive.treeErr = errors.New("模拟完整树中断")
				}
				empty, err := files.verify115EmptyBranch(context.Background(), drive, "30", "empty", 0)
				if scenario == "tree-error" {
					if err == nil || empty {
						t.Fatal("不完整树不能证明分支为空", empty, err)
					}
				} else if err != nil || empty != (scenario == "empty") {
					t.Fatal("必须使用完整新树中的所有文件判空", empty, err)
				}
				if drive.treeCalls != 1 || nested && drive.reads != 1 || !nested && drive.probes != 1 || len(drive.batches) != 0 {
					t.Fatal("计数失配只回退一次完整树", drive.treeCalls, drive.reads, drive.probes, drive.batches)
				}
			})
		}
	}
}
