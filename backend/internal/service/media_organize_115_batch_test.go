package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"reflect"
	"slices"
	"strings"
	"testing"

	"strm115/backend/internal/client/p115"
)

// 假网盘只在内存执行，严格列表与写入均检查冻结的测试 Cookie。
type media115BatchTestDrive struct {
	*mediaCleanupDrive
	fixedCookie    string
	t              *testing.T
	reads          map[string]int
	readOrder      []string
	beforeSnapshot func(string) error
	beforeMove     func()
}

func (d *media115BatchTestDrive) ReadDirectoryFiles(ctx context.Context, id, cookie string) (p115.DriveDirectory, []p115.ShareFile, error) {
	if err := ctx.Err(); err != nil {
		return p115.DriveDirectory{}, nil, err
	}
	if cookie != d.fixedCookie {
		d.t.Fatal("批处理切换了冻结凭据")
	}
	d.reads[id]++
	d.readOrder = append(d.readOrder, id)
	if d.beforeSnapshot != nil {
		if err := d.beforeSnapshot(id); err != nil {
			return p115.DriveDirectory{}, nil, err
		}
	}
	files, exists := d.dirs[id]
	if !exists {
		return p115.DriveDirectory{}, nil, fmt.Errorf("模拟目录不存在")
	}
	directory, err := d.ReadDirectory(ctx, id, cookie)
	return directory, slices.Clone(files), err
}

func (d *media115BatchTestDrive) MoveEntriesBatch(ctx context.Context, ids []string, target, cookie string) error {
	if d.beforeMove != nil {
		d.beforeMove()
	}
	if cookie != d.fixedCookie {
		d.t.Fatal("移动切换了冻结凭据")
	}
	return d.organizeFakeDrive.MoveEntriesBatch(ctx, ids, target, cookie)
}

func newMedia115BatchTest(t *testing.T, count int) (*MediaOrganizer, *media115BatchTestDrive, *mediaOrganizeFiles, *MediaOrganizeRun) {
	t.Helper()
	s, base, rule := newMediaCleanupCloud(t)
	run, err := s.Start(rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	files, err := s.files(context.Background(), rule)
	if err != nil {
		t.Fatal(err)
	}
	drive := &media115BatchTestDrive{mediaCleanupDrive: base, fixedCookie: files.cookie, t: t, reads: make(map[string]int)}
	files.drive, s.drive = drive, drive
	for i := range count {
		file := p115.ShareFile{ID: fmt.Sprint(500 + i), Name: fmt.Sprintf("old-%03d.mkv", i), Size: int64(i + 1), SHA1: fmt.Sprintf("%040x", i+1)}
		drive.dirs["10"] = append(drive.dirs["10"], file)
		relative := fmt.Sprintf("new-%03d.mkv", i)
		run.Entries = append(run.Entries, MediaOrganizeEntry{File: OrganizeFile{ShareFile: file, ParentID: "10", SourcePath: file.Name, MediaType: "video"}, Relative: relative, Target: path.Join(rule.Target, relative), Status: "ready"})
	}
	return s, drive, files, &run
}

func runMedia115BatchTest(t *testing.T, s *MediaOrganizer, drive *media115BatchTestDrive, files *mediaOrganizeFiles, run *MediaOrganizeRun) {
	t.Helper()
	if err := s.execute115Batches(context.Background(), files, run, drive, mediaFailureEntries(run)); err != nil {
		t.Fatal(err)
	}
}

func TestMedia115BatchSplitsFiftyAndPersistsBeforeEachWrite(t *testing.T) {
	s, d, files, run := newMedia115BatchTest(t, 105)
	active, reservations := 0, 0
	s.reserve = func() (func(), error) {
		active++
		reservations++
		if active != 1 {
			t.Fatal("批量写入预留发生嵌套")
		}
		return func() { active-- }, nil
	}
	d.beforeRename = func() {
		snapshot, err := s.Snapshot(run.ID)
		if err != nil {
			t.Fatal(err)
		}
		batch := d.renameBatches[len(d.renameBatches)-1]
		for _, target := range batch {
			index := slices.IndexFunc(snapshot.Entries, func(e MediaOrganizeEntry) bool { return e.File.ID == target.ID })
			intent := snapshot.Entries[index].P115Move
			if intent == nil || intent.Phase != "renaming" || intent.TargetCID != "20" || intent.TargetName != target.Name || active != 1 {
				t.Fatal("改名前必须保存精确意图并短暂预留", intent, active)
			}
		}
	}
	d.beforeMove = func() {
		snapshot, _ := s.Snapshot(run.ID)
		for _, entry := range snapshot.Entries {
			if entry.P115Move != nil && entry.P115Move.Phase == "renaming" {
				t.Fatal("改名结果未持久确认之前不得移动")
			}
		}
	}
	newCookie := "UID=123_snapshot; SEID=replaced"
	if err := s.accounts.Update(run.Rule.AccountID, 2, "整理账号", &newCookie); err != nil {
		t.Fatal(err)
	}
	runMedia115BatchTest(t, s, d, files, run)
	if d.renames != 3 || d.moves != 3 || len(d.renameBatches[0]) != 50 || len(d.moveBatches[2]) != 5 || active != 0 || reservations != 3 {
		t.Fatal("应按 50/50/5 分组且释放全部预留", d.renameBatches, d.moveBatches, active, reservations)
	}
	for _, entry := range run.Entries {
		if entry.Status != "done" || entry.P115Move != nil || !strings.HasPrefix(entry.File.Name, "old-") {
			t.Fatal("必须逐项确认并保留原始文件身份", entry)
		}
	}
	if d.reads["10"] != 6 || d.reads["20"] != 6 {
		t.Fatal("读请求应按批次合并，不能逐文件读取", d.reads)
	}
}

func TestMedia115BatchPartialRenameAndMoveResumeOnlyRemainingIDs(t *testing.T) {
	for _, stage := range []string{"rename", "move"} {
		t.Run(stage, func(t *testing.T) {
			s, d, files, run := newMedia115BatchTest(t, 2)
			if stage == "rename" {
				d.renameErr = context.DeadlineExceeded
				d.renameApplyIDs = map[string]bool{"500": true}
			} else {
				d.moveErr = context.DeadlineExceeded
				d.moveApplyIDs = map[string]bool{"500": true}
			}
			runMedia115BatchTest(t, s, d, files, run)
			if run.Entries[0].Status != "done" || run.Entries[1].Status != "error" || run.Entries[1].P115Move == nil || d.moves != 1 {
				t.Fatal("部分回执不能把整批标记完成", run.Entries, d.moveBatches)
			}
			if stage == "rename" && !reflect.DeepEqual(d.moveBatches[0], []string{"500"}) {
				t.Fatal("只可推进已确认改名的 ID", d.moveBatches)
			}
			d.renameErr, d.moveErr, d.renameApplyIDs, d.moveApplyIDs = nil, nil, nil, nil
			runMedia115BatchTest(t, s, d, files, run)
			if run.Entries[1].Status != "done" || d.moves != 2 || !reflect.DeepEqual(d.moveBatches[1], []string{"501"}) {
				t.Fatal("显式再次执行只推进剩余文件", run.Entries, d.moveBatches)
			}
			expectedRenames := 1
			if stage == "rename" {
				expectedRenames = 2
			}
			if d.renames != expectedRenames {
				t.Fatal("已确认改名的文件不可重复改名", d.renameBatches)
			}
		})
	}
}

func TestMedia115BatchUnknownMoveReadsTargetBeforeMissingOldSource(t *testing.T) {
	s, d, files, run := newMedia115BatchTest(t, 1)
	d.beforeSnapshot = func(id string) error {
		if id == "20" && d.moves > 0 {
			return errors.New("模拟移动后读取中断")
		}
		return nil
	}
	runMedia115BatchTest(t, s, d, files, run)
	if run.Entries[0].Status != "error" || run.Entries[0].P115Move == nil || d.moves != 1 {
		t.Fatal("读取失败必须保留未知意图", run.Entries)
	}
	delete(d.dirs, "10")
	d.beforeSnapshot = nil
	d.readOrder = nil
	runMedia115BatchTest(t, s, d, files, run)
	if run.Entries[0].Status != "done" || run.Entries[0].P115Move != nil || d.moves != 1 || d.renames != 1 || !reflect.DeepEqual(d.readOrder, []string{"20"}) {
		t.Fatal("原来源已回收时仍应只读原目标确认，不得重放", run.Entries, d.readOrder, d.moveBatches)
	}
}

func TestMedia115BatchDependencyAlsoBlocksPendingCompanionWrites(t *testing.T) {
	s, d, files, run := newMedia115BatchTest(t, 2)
	run.Entries[1].File.MediaType = "subtitle"
	run.Entries[1].VideoID = "500"
	d.renameErr, d.renameApplyIDs = errors.New("模拟主视频改名失败"), map[string]bool{}
	runMedia115BatchTest(t, s, d, files, run)
	if d.moves != 0 || d.renames != 1 || run.Entries[1].Status != "error" {
		t.Fatal("视频未完成时不能整理字幕", d.renameBatches, d.moveBatches, run.Entries)
	}
	run.Entries[1].P115Move = media115BatchIntent(run.Rule, run.Entries[1], "20")
	run.processing = map[string]bool{"501": true}
	d.renameErr, d.renameApplyIDs = nil, nil
	runMedia115BatchTest(t, s, d, files, run)
	if d.moves != 0 || d.renames != 1 || run.Entries[1].P115Move == nil {
		t.Fatal("已有待核实意图也不能豁免字幕写入依赖", d.renameBatches, d.moveBatches)
	}
}

func TestMedia115BatchStopsOnCancellationCheckpointAndIdentityFailure(t *testing.T) {
	for _, scenario := range []string{"canceled", "checkpoint", "after-rename-checkpoint", "source-path", "target-path", "changed-source-sha1"} {
		t.Run(scenario, func(t *testing.T) {
			s, d, files, run := newMedia115BatchTest(t, 1)
			active := 0
			s.reserve = func() (func(), error) { active++; return func() { active-- }, nil }
			ctx := context.Background()
			switch scenario {
			case "canceled":
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = canceled
			case "checkpoint":
				s.persistErr = errors.New("模拟检查点失败")
			case "after-rename-checkpoint":
				d.beforeRename = func() { s.persistErr = errors.New("模拟改名后检查点失败") }
			case "source-path":
				d.paths["10"] = "/outside"
			case "target-path":
				d.paths["20"] = "/outside"
			case "changed-source-sha1":
				d.dirs["10"][0].SHA1 = strings.Repeat("F", 40)
			}
			err := s.execute115Batches(ctx, files, run, d, nil)
			if d.moves != 0 || active != 0 || scenario != "after-rename-checkpoint" && d.renames != 0 {
				t.Fatal("异常之后禁止写入且预留必须释放", d.renames, d.moves, active, err)
			}
			if scenario == "checkpoint" || scenario == "after-rename-checkpoint" || scenario == "canceled" {
				if err == nil {
					t.Fatal("取消及持久化失败应向上返回")
				}
			}
			if scenario == "after-rename-checkpoint" {
				s.persistErr, d.beforeRename = nil, nil
				runMedia115BatchTest(t, s, d, files, run)
				if d.renames != 1 || d.moves != 1 || run.Entries[0].Status != "done" {
					t.Fatal("恢复已确认改名仅移动，不重复改名", d.renames, d.moves, run.Entries)
				}
			}
		})
	}
}

func TestMedia115BatchSpecialCollisionsKeepSingleDuplicateSemantics(t *testing.T) {
	for _, sameHash := range []bool{true, false} {
		t.Run(fmt.Sprint(sameHash), func(t *testing.T) {
			s, d, files, run := newMedia115BatchTest(t, 1)
			files.rule.DeleteDuplicates, run.Rule.DeleteDuplicates = true, true
			hash := run.Entries[0].File.SHA1
			if !sameHash {
				hash = strings.Repeat("F", 40)
			}
			d.dirs["20"] = []p115.ShareFile{{ID: "900", Name: path.Base(run.Entries[0].Relative), Size: run.Entries[0].File.Size, SHA1: hash}}
			runMedia115BatchTest(t, s, d, files, run)
			if d.moves != 0 || d.renames != 0 || run.Entries[0].P115Move != nil {
				t.Fatal("同名项不能混入普通批量写入", d.moves, d.renames)
			}
			if sameHash {
				if run.Entries[0].Status != "done" || len(d.deleted) != 1 || run.Entries[0].Result.Action != "duplicate" {
					t.Fatal("相同 SHA1 应保持现有单项去重行为", run.Entries, d.deleted)
				}
			} else if run.Entries[0].Status != "conflict" || len(d.deleted) != 0 {
				t.Fatal("不同 SHA1 同名冲突必须保留来源", run.Entries, d.deleted)
			}
		})
	}
}

func TestMedia115BatchIntentCloneRoundtripAndChangedPlan(t *testing.T) {
	s, d, files, run := newMedia115BatchTest(t, 1)
	run.Entries[0].P115Move = media115BatchIntent(run.Rule, run.Entries[0], "20")
	if err := s.checkpoint(run); err != nil {
		t.Fatal(err)
	}
	run.Entries[0].P115Move.TargetName = "mutated.mkv"
	snapshot, _ := s.Snapshot(run.ID)
	if snapshot.Entries[0].P115Move.TargetName == "mutated.mkv" {
		t.Fatal("持久检查点与调用者的意图指针不能共用")
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var restored MediaOrganizeRun
	if err := json.Unmarshal(encoded, &restored); err != nil || !reflect.DeepEqual(restored.Entries[0].P115Move, snapshot.Entries[0].P115Move) {
		t.Fatal("JSON 必须完整保留意图", err)
	}
	*run = restored
	run.Entries[0].Relative, run.Entries[0].Target = "corrected.mkv", "/library/corrected.mkv"
	runMedia115BatchTest(t, s, d, files, run)
	if d.renames != 0 || d.moves != 0 || run.Entries[0].Status != "error" || run.Entries[0].P115Move == nil || !reflect.DeepEqual(d.readOrder, []string{"20"}) {
		t.Fatal("计划已改变时只能核对原目标，不得写入或误认完成", run.Entries, d.readOrder)
	}
}
