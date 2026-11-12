package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"strm115/backend/internal/storage"
)

func mediaReconcileFixture() (MediaOrganizeRun, MediaOrganizeRun) {
	old := mediaRecordFixture("old-failure", "partial", 1)
	old.CreatedAt = old.UpdatedAt.Add(-time.Hour)
	old.Entries[0].Modified = 123
	old.Entries[0].File.Size = 42
	var done MediaOrganizeRun
	cloneMediaRun(old, &done)
	done.ID, done.Status = "confirmed-success", "completed"
	old.Entries[0].Status, old.Entries[0].Result = "error", nil
	return old, done
}

func TestMediaReconcileQueuedFailureBeforeAnyExternalWork(t *testing.T) {
	s, _, _ := newMediaSQLiteTest(t)
	old, done := mediaReconcileFixture()
	// 同批未选择的另一文件保留；过时选择清空后不能退化成整批执行。
	other := old.Entries[0]
	other.File.ID, other.File.Name, other.File.SourcePath = "other.strm", "other.strm", "other.strm"
	old.Entries = append(old.Entries, other)
	saveMediaFixtures(t, s, old)
	result, err := s.RetryBatchContext(context.Background(), MediaOrganizeBatchRetry{Items: []MediaOrganizeDeleteItem{{RunID: old.ID, FileID: old.Entries[0].File.ID}}})
	if err != nil || result.Queued != 1 {
		t.Fatal(result, err)
	}
	if page := mediaHistoryTest(t, s, "", "running", 1); page.Total != 1 {
		t.Fatal("保留失败索引时，运行摘要仍须显示选中项整理中", page)
	}
	// 新成功到达时旧批次已排队，原有清理会跳过，出队必须再次协调。
	done.Entries[0].Status, done.Entries[0].Result = "ready", nil
	done.Status = "running"
	saveMediaFixtures(t, s, done)
	done.Entries[0].Status = "done"
	done.Entries[0].Result = &MediaOrganizeResult{Status: "done", At: old.UpdatedAt}
	if err := s.checkpoint(&done); err != nil {
		t.Fatal(err)
	}
	queued, action, found := s.takeQueuedRunLocked()
	if !found || queued.ID != old.ID {
		t.Fatal("旧批次应仍在队列中等待协调", queued.ID)
	}
	// 未配置真实存储或元数据；若错误地继续执行，此处会失败。
	if err := s.processQueuedRun(context.Background(), queued, action); err != nil {
		t.Fatal(err)
	}
	page := mediaHistoryTest(t, s, "", "all", 1)
	if page.Total != 2 || page.Counts["done"] != 1 || page.Counts["failed"] != 1 || page.RetryBatch.Status != "completed" || page.RetryBatch.Skipped != 1 {
		t.Fatal("只清除已解决的旧任务，保留原成功和未选择失败", page)
	}
	if _, err := s.Task(old.ID, other.File.ID); err != nil {
		t.Fatal("同批未选择文件必须保留", err)
	}
	if task, err := s.Task(done.ID, done.Entries[0].File.ID); err != nil || !task.At.Equal(old.UpdatedAt) {
		t.Fatal("协调不能刷新成功时间", task, err)
	}
}

func TestMediaReconcileStartupRequiresConfirmedEarlierSameSource(t *testing.T) {
	for _, scenario := range []string{"same", "new-scan", "unknown-time", "no-proof", "modified", "size", "binding"} {
		t.Run(scenario, func(t *testing.T) {
			s, db, dir := newMediaSQLiteTest(t)
			old, done := mediaReconcileFixture()
			switch scenario {
			case "new-scan":
				old.CreatedAt = done.Entries[0].Result.At.Add(time.Second)
			case "unknown-time":
				done.Entries[0].Result.At = time.Time{}
			case "no-proof":
				done.Entries[0].Result = nil
			case "modified":
				done.Entries[0].Modified++
			case "size":
				done.Entries[0].File.Size++
			case "binding":
				done.Rule.Target = "/different-target"
			}
			saveMediaFixtures(t, s, old, done)
			restarted, err := OpenMediaOrganizerSQLite(context.Background(), db, dir, "test", nil, s.templates, nil, nil, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			page := mediaHistoryTest(t, restarted, "", "failed", 1)
			if (page.Total == 0) != (scenario == "same") || len(restarted.doc.Runs) != 0 {
				t.Fatal("启动仅按明确成功及来源身份收敛，历史不常驻", page.Total, restarted.doc.Runs)
			}
		})
	}
}

func TestMediaReconcileFailedSaveKeepsQueuedEvidence(t *testing.T) {
	s, _, store, rule := newMediaOrganizeTest(t)
	old, done := mediaReconcileFixture()
	old.Rule, done.Rule = rule, rule
	saveMediaFixtures(t, s, old)
	if _, err := s.RetryContext(context.Background(), old.ID, MediaOrganizeCorrection{FileID: old.Entries[0].File.ID}); err != nil {
		t.Fatal(err)
	}
	saveMediaFixtures(t, s, done)
	store.err = errors.New("模拟协调事务失败")
	if err := s.reconcileResolvedFailuresLocked(context.Background(), []string{old.ID}); err == nil {
		t.Fatal("模拟事务失败必须返回错误")
	}
	if s.actions[old.ID] != "retry" {
		t.Fatal("保存失败不能提前撤销排队动作", s.actions)
	}
	if _, err := s.Task(old.ID, old.Entries[0].File.ID); errors.Is(err, ErrMediaOrganizeTaskNotFound) || err != nil {
		t.Fatal("事务失败后仍须保留旧失败证据", err)
	}
}

type mediaReconcileDequeuedFaultStore struct {
	mediaOrganizeStorage
	queryErr, saveErr           error
	queryFailures, saveFailures int
	beforeQuery                 func()
	queries                     int
}

func (s *mediaReconcileDequeuedFaultStore) LoadMediaOrganizeResolvedCandidates(ctx context.Context, ids []string) ([]storage.MediaOrganizeResolutionCandidate, error) {
	s.queries++
	if s.beforeQuery != nil {
		s.beforeQuery()
	}
	if s.queryFailures > 0 {
		s.queryFailures--
		return nil, s.queryErr
	}
	return s.mediaOrganizeStorage.LoadMediaOrganizeResolvedCandidates(ctx, ids)
}

func TestMediaReconcileWorkerShutdown(t *testing.T) {
	for _, scenario := range []string{"before-cleanup", "during-cleanup", "storage-error"} {
		t.Run(scenario, func(t *testing.T) {
			s, _, _, rule := newMediaOrganizeTest(t)
			mediaTestFile(t, rule.Source, "Work.S01E01.strm", "原文件")
			if _, err := s.Start(rule.ID); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			fault := &mediaReconcileDequeuedFaultStore{mediaOrganizeStorage: s.db, beforeQuery: cancel}
			s.db = fault
			var expected error
			if scenario == "before-cleanup" {
				cancel()
			} else if scenario == "storage-error" {
				expected = errors.New("模拟收尾查询存储故障")
				fault.queryErr, fault.queryFailures = expected, 1
			}
			run, action, found := s.takeQueuedRunLocked()
			if !found {
				t.Fatal("未取到排队任务")
			}
			if err := s.processQueuedRun(ctx, run, action); !errors.Is(err, expected) {
				t.Fatal("正常停机不返回取消错误，独立存储故障仍须返回", err)
			}
			if s.active != "" || len(s.inFlight) != 0 || scenario == "before-cleanup" && fault.queries != 0 {
				t.Fatal("停机应释放执行占用，已取消时不启动收尾查询", s.active, fault.queries)
			}
			if scenario != "before-cleanup" {
				task, err := s.Task(run.ID, "Work.S01E01.strm")
				if err != nil || task.Status != "done" {
					t.Fatal("收尾取消不能改变已保存成功结果", task, err)
				}
			}
		})
	}
}

func (s *mediaReconcileDequeuedFaultStore) SaveMediaOrganize(ctx context.Context, settings string, updates []storage.MediaOrganizeRunRow, removed []string) error {
	if s.saveFailures > 0 {
		s.saveFailures--
		return s.saveErr
	}
	return s.mediaOrganizeStorage.SaveMediaOrganize(ctx, settings, updates, removed)
}

func TestMediaReconcileDequeuedQueryFailureFinishesFailed(t *testing.T) {
	s, db, _ := newMediaSQLiteTest(t)
	old, _ := mediaReconcileFixture()
	saveMediaFixtures(t, s, old)
	if _, err := s.RetryContext(context.Background(), old.ID, MediaOrganizeCorrection{FileID: old.Entries[0].File.ID}); err != nil {
		t.Fatal(err)
	}
	queryErr := errors.New("模拟出队协调查询失败")
	s.db = &mediaReconcileDequeuedFaultStore{mediaOrganizeStorage: db, queryErr: queryErr, queryFailures: 1}
	run, action, found := s.takeQueuedRunLocked()
	if !found || run.ID != old.ID {
		t.Fatal("未取到排队重试", run.ID, found)
	}
	if err := s.processQueuedRun(context.Background(), run, action); err != nil {
		t.Fatal("协调查询错误应保存为任务失败", err)
	}
	stored, err := s.Snapshot(old.ID)
	if err != nil || stored.Status != "failed" || stored.Message == "" || s.active != "" || s.actions[old.ID] != "" {
		t.Fatal("出队查询失败不能留下没有执行动作的排队任务", stored, s.active, s.actions, err)
	}
	page := mediaHistoryTest(t, s, "", "all", 1)
	if page.Counts["failed"] != 1 || page.Counts["running"] != 0 {
		t.Fatal("协调失败应保持可见失败记录", page)
	}
}

func TestMediaReconcileDequeuedFailedCheckpointRestoresAction(t *testing.T) {
	s, db, _ := newMediaSQLiteTest(t)
	old, done := mediaReconcileFixture()
	saveMediaFixtures(t, s, old)
	if _, err := s.RetryContext(context.Background(), old.ID, MediaOrganizeCorrection{FileID: old.Entries[0].File.ID}); err != nil {
		t.Fatal(err)
	}
	// 成功证据在旧任务入队后到达；协调查询失败及随后一次保存失败都不能丢失它。
	saveMediaFixtures(t, s, done)
	queryErr, saveErr := errors.New("模拟出队协调查询失败"), errors.New("模拟协调错误结果保存失败")
	s.db = &mediaReconcileDequeuedFaultStore{mediaOrganizeStorage: db, queryErr: queryErr, queryFailures: 1, saveErr: saveErr, saveFailures: 1}
	run, action, found := s.takeQueuedRunLocked()
	if !found || run.ID != old.ID {
		t.Fatal("未取到排队重试", run.ID, found)
	}
	if err := s.processQueuedRun(context.Background(), run, action); !errors.Is(err, saveErr) {
		t.Fatal("失败结果保存异常必须继续返回", err)
	}
	stored, err := s.Snapshot(old.ID)
	if err != nil || stored.Status != "queued" || stored.TaskFileID != old.Entries[0].File.ID || len(stored.Entries) != 1 || s.active != "" || s.actions[old.ID] != "retry" {
		t.Fatal("保存回滚后应恢复原重试动作和冻结上下文", stored, s.active, s.actions, err)
	}
	if task, err := s.Task(done.ID, done.Entries[0].File.ID); err != nil || task.Status != "done" {
		t.Fatal("保存回滚不能损坏另一批次的成功证据", task, err)
	}
	// 注入故障仅发生一次，再次出队只协调历史，不访问来源或元数据。
	run, action, found = s.takeQueuedRunLocked()
	if !found {
		t.Fatal("恢复的重试动作必须可再次出队")
	}
	if err := s.processQueuedRun(context.Background(), run, action); err != nil {
		t.Fatal(err)
	}
	page := mediaHistoryTest(t, s, "", "all", 1)
	if page.Counts["done"] != 1 || page.Counts["failed"] != 0 || page.Counts["running"] != 0 || s.actions[old.ID] != "" {
		t.Fatal("恢复后应按原成功证据收敛旧失败", page, s.actions)
	}
}
