package service

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"strm115/backend/internal/client/p115"
)

type media115ReceiptTestDrive struct {
	*media115BatchTestDrive
	confirmed  bool
	receiptErr error
}

func (d *media115ReceiptTestDrive) RenameEntriesWithReceipt(ctx context.Context, entries []p115.RenameTarget, cookie string) (bool, error) {
	err := d.RenameEntries(ctx, entries, cookie)
	return d.confirmed, errors.Join(err, d.receiptErr)
}

func TestMedia115BatchSQLiteRestartRetainsExactPendingIntent(t *testing.T) {
	for _, stage := range []string{"renamed", "moved"} {
		t.Run(stage, func(t *testing.T) {
			s, drive, files, run := newMedia115BatchTest(t, 1)
			drive.dirs["30"] = drive.dirs["10"]
			drive.dirs["10"] = []p115.ShareFile{{ID: "30", Name: "batch", Directory: true}}
			drive.paths["30"] = "/inbox/batch"
			run.Entries[0].File.ParentID, run.Entries[0].File.SourcePath = "30", "batch/old-000.mkv"
			if stage == "renamed" {
				drive.beforeRename = func() { s.persistErr = errors.New("模拟改名后持久化失败") }
			} else {
				drive.beforeSnapshot = func(id string) error {
					if id == "20" && drive.moves > 0 {
						return errors.New("模拟移动后读回失败")
					}
					return nil
				}
			}
			_ = s.execute115Batches(context.Background(), files, run, drive, nil)
			if run.Entries[0].P115Move == nil || run.Entries[0].Status != "error" {
				t.Fatal("需要可恢复的待核实原始意图", run.Entries)
			}
			stored, db, directory := newMediaSQLiteTest(t)
			saveMediaFixtures(t, stored, *run)
			raw, err := json.Marshal(run)
			if err != nil || strings.Contains(string(raw), files.cookie) {
				t.Fatal("持久意图不能包含 Cookie", err)
			}
			reopened, err := OpenMediaOrganizerSQLite(context.Background(), db, directory, "test", nil, organizeDefaultTemplates{}, nil, s.accounts, drive, nil)
			if err != nil {
				t.Fatal(err)
			}
			restored, err := reopened.Snapshot(run.ID)
			if err != nil || !reflect.DeepEqual(restored.Entries[0].P115Move, run.Entries[0].P115Move) {
				t.Fatal("SQLite 重启必须保留全部精确字段", restored.Entries, err)
			}
			// 再次读取快照不能受调用者修改意图指针影响。
			originalName := restored.Entries[0].P115Move.TargetName
			restored.Entries[0].P115Move.TargetName = "caller-mutated.mkv"
			fresh, _ := reopened.Snapshot(run.ID)
			if fresh.Entries[0].P115Move.TargetName != originalName {
				t.Fatal("Snapshot 返回的意图指针必须独立")
			}
			restored = fresh
			drive.beforeSnapshot, drive.beforeRename = nil, nil
			if stage == "moved" {
				delete(drive.dirs, "30")
				drive.dirs["10"] = []p115.ShareFile{}
			}
			recoveredFiles, err := reopened.files(context.Background(), restored.Rule)
			if err != nil {
				t.Fatal(err)
			}
			drive.readOrder = nil
			if err := reopened.execute115Batches(context.Background(), recoveredFiles, &restored, drive, nil); err != nil {
				t.Fatal(err)
			}
			if restored.Entries[0].Status != "done" || restored.Entries[0].P115Move != nil || drive.renames != 1 || drive.moves != 1 {
				t.Fatal("重启后只补剩余步骤，已完成写入不得重放", restored.Entries, drive.renameBatches, drive.moveBatches)
			}
			if stage == "moved" && !reflect.DeepEqual(drive.readOrder, []string{"20"}) {
				t.Fatal("已移动文件仅核对冻结目标，不读取已回收旧来源", drive.readOrder)
			}
		})
	}
}

func TestMedia115BatchCancellationAfterRenameStopsMoveAndReleasesReservation(t *testing.T) {
	s, drive, files, run := newMedia115BatchTest(t, 2)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	active := 0
	s.reserve = func() (func(), error) { active++; return func() { active-- }, nil }
	drive.beforeRename = cancel
	err := s.execute115Batches(ctx, files, run, drive, nil)
	if !errors.Is(err, context.Canceled) || drive.renames != 1 || drive.moves != 0 || active != 0 {
		t.Fatal("已取消的批次不能追加移动且必须释放预留", drive.renames, drive.moves, active, err)
	}
	for _, entry := range run.Entries {
		if entry.P115Move == nil || entry.Status != "error" {
			t.Fatal("已发出的改名保留原始待核实意图", entry)
		}
	}
}

func TestMedia115BatchRecoveryQueryFailureIsNotTargetMiss(t *testing.T) {
	s, drive, files, run := newMedia115BatchTest(t, 1)
	run.Entries[0].P115Move = media115BatchIntent(run.Rule, run.Entries[0], "20")
	run.Entries[0].P115Move.Phase = "moving"
	drive.beforeSnapshot = func(string) error { return errors.New("模拟原目标读取失败") }
	runMedia115BatchTest(t, s, drive, files, run)
	if drive.moves != 0 || drive.renames != 0 || !reflect.DeepEqual(drive.readOrder, []string{"20"}) || run.Entries[0].P115Move == nil {
		t.Fatal("原目标查询异常不能当作未命中并重新写入", drive.readOrder, run.Entries)
	}
	drive.beforeSnapshot, drive.readOrder = nil, nil
	runMedia115BatchTest(t, s, drive, files, run)
	if drive.moves != 1 || drive.renames != 1 || run.Entries[0].Status != "done" {
		t.Fatal("新一次显式执行完整核实未命中且来源仍在后可以继续", drive.readOrder, run.Entries)
	}
}

func TestMedia115BatchExactRenameReceiptAvoidsOnlyRedundantReads(t *testing.T) {
	for _, scenario := range []string{"exact", "aggregate", "partial", "error"} {
		t.Run(scenario, func(t *testing.T) {
			s, base, files, run := newMedia115BatchTest(t, 2)
			drive := &media115ReceiptTestDrive{media115BatchTestDrive: base, confirmed: scenario == "exact" || scenario == "error"}
			files.drive, s.drive = drive, drive
			if scenario == "partial" {
				drive.renameApplyIDs = map[string]bool{"500": true}
			} else if scenario == "error" {
				drive.receiptErr = context.DeadlineExceeded
			}
			if err := s.execute115Batches(context.Background(), files, run, drive, nil); err != nil {
				t.Fatal(err)
			}
			sourceReads := 2
			if scenario == "exact" {
				sourceReads = 1
			}
			if drive.reads["10"] != sourceReads || drive.reads["20"] != 2 || drive.renames != 1 || drive.moves != 1 {
				t.Fatal("精确回执才省来源读，同批目标复用但最终必须完整核验", drive.reads, drive.renameBatches, drive.moveBatches)
			}
			if scenario == "partial" {
				if run.Entries[0].Status != "done" || run.Entries[1].Status != "error" || !reflect.DeepEqual(drive.moveBatches[0], []string{"500"}) {
					t.Fatal("模糊回执仍逐项回读，只移动已确认成员", run.Entries, drive.moveBatches)
				}
			} else if slices.ContainsFunc(run.Entries, func(e MediaOrganizeEntry) bool { return e.Status != "done" || e.P115Move != nil }) {
				t.Fatal("最终目标必须逐项确认", run.Entries)
			}
		})
	}
}

func TestMedia115BatchReceiptDoesNotReuseSnapshotsAcrossBatches(t *testing.T) {
	for _, changed := range []string{"source", "target"} {
		t.Run(changed, func(t *testing.T) {
			s, base, files, run := newMedia115BatchTest(t, 51)
			drive := &media115ReceiptTestDrive{media115BatchTestDrive: base, confirmed: true}
			files.drive, s.drive = drive, drive
			injected := false
			drive.beforeSnapshot = func(id string) error {
				if injected || drive.moves != 1 {
					return nil
				}
				if changed == "source" && id == "10" {
					drive.dirs["10"][0].SHA1 = strings.Repeat("F", 40)
					injected = true
				} else if changed == "target" && id == "20" && drive.reads[id] == 3 {
					drive.dirs["20"] = append(drive.dirs["20"], p115.ShareFile{ID: "900", Name: "new-050.mkv", Size: 51, SHA1: strings.Repeat("F", 40)})
					injected = true
				}
				return nil
			}
			if err := s.execute115Batches(context.Background(), files, run, drive, nil); err != nil {
				t.Fatal(err)
			}
			if !injected || drive.renames != 1 || drive.moves != 1 || run.Entries[50].Status == "done" || run.Entries[50].P115Move != nil {
				t.Fatal("下一批必须重新读取完整来源和目标，变化项不得写入", drive.reads, drive.renameBatches, drive.moveBatches, run.Entries[50])
			}
		})
	}
}

func TestMedia115BatchLateTargetCollisionNeverConfirmsOrDeletesEitherFile(t *testing.T) {
	s, base, files, run := newMedia115BatchTest(t, 1)
	drive := &media115ReceiptTestDrive{media115BatchTestDrive: base, confirmed: true}
	files.drive, s.drive = drive, drive
	files.rule.DeleteDuplicates, run.Rule.DeleteDuplicates = true, true
	drive.beforeRename = func() {
		// 模拟不受本地预留约束的外部写入；115 移动不会覆盖现有同名文件。
		drive.dirs["20"] = append(drive.dirs["20"], p115.ShareFile{ID: "900", Name: run.Entries[0].Relative, Size: 1, SHA1: run.Entries[0].File.SHA1})
	}
	if err := s.execute115Batches(context.Background(), files, run, drive, nil); err != nil {
		t.Fatal(err)
	}
	if run.Entries[0].Status != "error" || run.Entries[0].P115Move == nil || len(drive.deleted) != 0 || len(drive.dirs["20"]) != 2 || drive.reads["10"] != 1 || drive.reads["20"] != 2 {
		t.Fatal("晚到同名必须保留原文件及待核实意图，不能误认 done 或自动删文件", run.Entries, drive.deleted, drive.dirs["20"], drive.reads)
	}
	drive.beforeRename = nil
	if err := s.execute115Batches(context.Background(), files, run, drive, nil); err != nil {
		t.Fatal(err)
	}
	if drive.moves != 1 || drive.renames != 1 || len(drive.deleted) != 0 || run.Entries[0].Status != "error" {
		t.Fatal("同名未解除前重试只能核对原目标，不得重放或触发去重", drive.moveBatches, drive.renameBatches, drive.deleted)
	}
}
