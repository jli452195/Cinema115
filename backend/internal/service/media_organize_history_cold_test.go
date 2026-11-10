package service

import (
	"context"
	"errors"
	"testing"

	"strm115/backend/internal/storage"
)

type mediaColdReadCounter struct {
	mediaOrganizeStorage
	tasks, runs int
}

func (s *mediaColdReadCounter) LoadMediaOrganizeTask(ctx context.Context, id, fileID string) (string, error) {
	s.tasks++
	return s.mediaOrganizeStorage.LoadMediaOrganizeTask(ctx, id, fileID)
}

func (s *mediaColdReadCounter) LoadMediaOrganizeRuns(ctx context.Context, ids []string, ruleID, status string) ([]string, error) {
	s.runs++
	return s.mediaOrganizeStorage.LoadMediaOrganizeRuns(ctx, ids, ruleID, status)
}

func TestMediaHistoryColdPageAndTaskDoNotRetainCompletedBatches(t *testing.T) {
	s, db, dir := newMediaSQLiteTest(t)
	run := mediaRecordFixture("large-cold-batch", "completed", 2000)
	saveMediaFixtures(t, s, run)
	restarted, err := OpenMediaOrganizerSQLite(context.Background(), db, dir, "test", nil, s.templates, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(restarted.doc.Runs) != 0 {
		t.Fatal("启动不能载入已结束历史", len(restarted.doc.Runs))
	}
	counter := &mediaColdReadCounter{mediaOrganizeStorage: restarted.db}
	restarted.db = counter
	page := mediaHistoryTest(t, restarted, "", "all", 1)
	if page.Total != 2000 || len(page.Items) != 20 || counter.tasks != 0 || counter.runs != 0 {
		t.Fatal("成功记录分页不应读取批次详情", page.Total, counter.tasks, counter.runs)
	}
	for _, item := range page.Items {
		if !item.CanDelete || item.CanRetry {
			t.Fatal("冷成功记录权限错误", item)
		}
	}
	task, err := restarted.Task(run.ID, run.Entries[1000].File.ID)
	if err != nil || len(task.Entries) != 1 || counter.tasks != 1 || len(restarted.doc.Runs) != 0 {
		t.Fatal("单条详情不能常驻或解码全批次", task, err)
	}
}

func TestMediaHistoryColdFailureRetryAndClearKeepOtherRecords(t *testing.T) {
	s, _, _ := newMediaSQLiteTest(t)
	done := mediaRecordFixture("cold-done", "completed", 2)
	failed := mediaRecordFixture("cold-failed", "partial", 1)
	failed.Entries[0].Status, failed.Entries[0].Result = "error", nil
	saveMediaFixtures(t, s, done, failed)
	s.releaseHistoryLocked()
	page := mediaHistoryTest(t, s, "", "failed", 1)
	if page.Total != 1 || !page.Items[0].CanDelete || !page.Items[0].CanRetry || len(s.doc.Runs) != 0 {
		t.Fatal("冷失败可重试但不能因浏览进入工作集", page)
	}
	if _, err := s.RetryContext(context.Background(), failed.ID, MediaOrganizeCorrection{FileID: failed.Entries[0].File.ID}); err != nil {
		t.Fatal(err)
	}
	if len(s.doc.Runs) != 1 || s.doc.Runs[0].ID != failed.ID || s.actions[failed.ID] != "retry" {
		t.Fatal("重试只恢复选中的批次", s.doc.Runs)
	}
	deleted, err := s.DeleteHistory(context.Background(), MediaOrganizeDelete{Status: "done"})
	if err != nil || deleted != 2 || len(s.doc.Runs) != 1 || s.actions[failed.ID] != "retry" {
		t.Fatal("清理成功历史不能影响排队重试", deleted, err, s.doc.Runs)
	}
	if _, err := s.Task(done.ID, done.Entries[0].File.ID); !errors.Is(err, ErrMediaOrganizeTaskNotFound) {
		t.Fatal("已删除冷记录不能再次查询", err)
	}
}

func TestMediaHistoryColdResolvedFailureUsesSameSourceEvidence(t *testing.T) {
	s, _, _ := newMediaSQLiteTest(t)
	current := mediaRecordFixture("new-success", "running", 1)
	current.Rule.Storage = "115"
	current.Rule.AccountID = "frozen-account"
	current.Entries[0].File.Size = 123
	current.Entries[0].File.SHA1 = "same-hash"
	previous := mediaRecordFixture("old-failure", "partial", 1)
	previous.Rule = current.Rule
	previous.Entries[0] = current.Entries[0]
	previous.Entries[0].Status, previous.Entries[0].Result = "error", nil
	other := previous
	other.ID = "other-source"
	other.Rule.AccountID = "other-account"
	saveMediaFixtures(t, s, previous, other)
	s.releaseHistoryLocked()
	current.Entries[0].Status, current.Entries[0].Result = "ready", nil
	saveMediaFixtures(t, s, current)
	s.active = current.ID
	var worker MediaOrganizeRun
	cloneMediaRun(current, &worker)
	current = worker
	current.Entries[0].Status = "done"
	current.Entries[0].Result = &MediaOrganizeResult{Status: "done"}
	if err := s.checkpoint(&current); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Snapshot(previous.ID); !errors.Is(err, ErrMediaOrganizeInput) {
		t.Fatal("同源冷失败应在成功检查点事务中清除", err)
	}
	if _, err := s.Snapshot(other.ID); err != nil {
		t.Fatal("不同来源的失败证据必须保留", err)
	}
	if len(s.doc.Runs) != 1 || s.doc.Runs[0].ID != current.ID {
		t.Fatal("查询和清除冷失败后应释放临时工作集", s.doc.Runs)
	}
}

func TestMediaHistoryColdDeletedBatchCannotBeRevivedByCheckpoint(t *testing.T) {
	s, _, _ := newMediaSQLiteTest(t)
	run := mediaRecordFixture("cold-deleted", "completed", 1)
	saveMediaFixtures(t, s, run)
	s.releaseHistoryLocked()
	if n, err := s.DeleteHistory(context.Background(), MediaOrganizeDelete{Status: "done"}); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if err := s.checkpoint(&run); !errors.Is(err, ErrMediaOrganizeInput) {
		t.Fatal("旧工作副本不能复活已删除的冷批次", err)
	}
	rows, err := s.db.QueryMediaOrganizeLive(context.Background(), "", "all", 1, []storage.MediaOrganizeLiveTaskRow{})
	if err != nil || rows.Total != 0 || len(s.doc.Runs) != 0 {
		t.Fatal(rows.Total, err, s.doc.Runs)
	}
}

func TestMediaHistoryColdWorkerExitAndRuleDeletion(t *testing.T) {
	s, _, _, rule := newMediaOrganizeTest(t)
	persistMediaColdFixture(t, s)
	mediaTestFile(t, rule.Source, "Work.S01E01.strm", "https://example.test/original-file")
	queued, err := s.Start(rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	run, action, found := s.takeQueuedRunLocked()
	s.mu.Unlock()
	if !found || run.ID != queued.ID {
		t.Fatal("未取到排队任务", found, run.ID)
	}
	if err := s.processQueuedRun(context.Background(), run, action); err != nil {
		t.Fatal(err)
	}
	page := mediaHistoryTest(t, s, "", "done", 1)
	if len(s.doc.Runs) != 0 || page.Total != 1 {
		t.Fatal("执行结束后释放工作集，成功结果仍可查询", len(s.doc.Runs), page.Total)
	}
	if err := s.DeleteRule(rule.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Task(run.ID, page.Items[0].FileID); !errors.Is(err, ErrMediaOrganizeTaskNotFound) {
		t.Fatal("删除规则必须一并删除其冷历史", err)
	}
	if page := mediaHistoryTest(t, s, "", "all", 1); page.Total != 0 || len(s.doc.Runs) != 0 {
		t.Fatal("删除后不能遗留冷摘要或常驻历史", page.Total, s.doc.Runs)
	}
}
