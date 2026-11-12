package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"testing"
)

func TestMediaBatchRetrySelectedFilesAndProgress(t *testing.T) {
	s, _, _, rule := newMediaOrganizeTest(t)
	for episode := 1; episode <= 3; episode++ {
		mediaTestFile(t, rule.Source, fmt.Sprintf("Work.S01E%02d.strm", episode), "原文件")
	}
	run, err := s.Start(rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	mediaTestProcess(t, s, &run, "scan")
	for _, entry := range run.Entries {
		mediaTestFile(t, rule.Target, entry.Relative, "已有不同文件")
	}
	mediaTestProcess(t, s, &run, "execute")
	for _, entry := range run.Entries[:2] {
		if err := os.Remove(entry.Target); err != nil {
			t.Fatal(err)
		}
	}
	input := MediaOrganizeBatchRetry{Items: []MediaOrganizeDeleteItem{
		{RunID: run.ID, FileID: run.Entries[0].File.ID},
		{RunID: run.ID, FileID: run.Entries[1].File.ID},
		{RunID: run.ID, FileID: run.Entries[0].File.ID},
		{RunID: "missing", FileID: "missing"},
	}}
	result, err := s.RetryBatchContext(context.Background(), input)
	if err != nil || result.Queued != 2 || result.Skipped != 2 || result.Batch == nil || result.Batch.Pending != 2 {
		t.Fatal("同批多文件应一次入队，重复选择和消失记录明确跳过", result, err)
	}
	if _, err := s.RetryBatchContext(context.Background(), input); !errors.Is(err, ErrMediaOrganizeBusy) {
		t.Fatal("正在执行的批量不能重复提交", err)
	}
	queued, action, found := s.takeQueuedRunLocked()
	if !found || action != "retry" || len(queued.TaskFileIDs) != 2 {
		t.Fatal("批量选择不能被最后一个文件覆盖", queued, action, found)
	}
	if err := s.processQueuedRun(context.Background(), queued, action); err != nil {
		t.Fatal(err)
	}
	progress := s.retryBatchProgressLocked()
	if progress.Status != "completed" || progress.Success != 2 || progress.Failed != 0 || progress.Pending != 0 || progress.Skipped != 2 {
		t.Fatal("进度应按执行结果累计，跳过选择不参与搬运", progress)
	}
	untouched, err := s.Task(run.ID, run.Entries[2].File.ID)
	if err != nil || untouched.Status != "failed" {
		t.Fatal("未选择的同批文件不得重试", untouched, err)
	}
}

func TestMediaBatchRetryDeletedActiveSelectionDoesNotBecomeScan(t *testing.T) {
	s, db, dir := newMediaSQLiteTest(t)
	run := mediaRecordFixture("deleted-selection", "partial", 2)
	for i := range run.Entries {
		run.Entries[i].Status, run.Entries[i].Result = "error", nil
	}
	saveMediaFixtures(t, s, run)
	if _, err := s.RetryBatchContext(context.Background(), MediaOrganizeBatchRetry{Items: []MediaOrganizeDeleteItem{{RunID: run.ID, FileID: run.Entries[0].File.ID}}}); err != nil {
		t.Fatal(err)
	}
	queued, action, _ := s.takeQueuedRunLocked()
	if n, err := s.DeleteHistory(context.Background(), MediaOrganizeDelete{Items: []MediaOrganizeDeleteItem{{RunID: run.ID, FileID: run.Entries[0].File.ID}}}); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if err := s.processQueuedRun(context.Background(), queued, action); err != nil {
		t.Fatal("删除全部选择后不得访问实际来源或元数据", err)
	}
	restarted, err := OpenMediaOrganizerSQLite(context.Background(), db, dir, "test", nil, s.templates, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	page := mediaHistoryTest(t, restarted, "", "all", 1)
	if page.Total != 1 || page.Counts["running"] != 0 || page.RetryBatch.Pending != 0 || len(restarted.actions) != 0 {
		t.Fatal("重启只能保留未选失败，不能恢复为整批扫描", page, restarted.actions)
	}
}

func TestMediaBatchRetryFilteredSnapshotStopAndRestart(t *testing.T) {
	s, metadata, store, rule := newMediaOrganizeTest(t)
	matching := mediaRecordFixture("matching", "partial", 23)
	matching.Rule = rule
	for i := range matching.Entries {
		matching.Entries[i].Status, matching.Entries[i].Result = "error", nil
	}
	other := mediaRecordFixture("other", "partial", 1)
	other.Rule = rule
	other.Entries[0].File.ID, other.Entries[0].File.Name = "Other.S01E01.strm", "Other.S01E01.strm"
	other.Entries[0].File.SourcePath = "Other.S01E01.strm"
	other.Entries[0].Target = "/target/Other.S01E01.strm"
	other.Entries[0].Status, other.Entries[0].Result = "error", nil
	saveMediaFixtures(t, s, matching, other)
	result, err := s.RetryBatchContext(context.Background(), MediaOrganizeBatchRetry{Status: "failed", Query: "Work.S01E"})
	if err != nil || result.Queued != 23 || result.Batch == nil {
		t.Fatal("筛选全部应包含第二页，不能误选其他作品", result, err)
	}
	queued, err := s.Snapshot(matching.ID)
	if err != nil || len(queued.TaskFileIDs) != 23 || s.actions[other.ID] != "" {
		t.Fatal("筛选范围必须固定保存为所选文件集合", queued, err, s.actions)
	}
	stopped, err := s.StopRetryBatchContext(context.Background(), result.Batch.ID)
	if err != nil || stopped.Status != "stopped" || stopped.Pending != 0 || stopped.Skipped != 23 || s.actions[matching.ID] != "" {
		t.Fatal("停止应取消本批待执行项并保留原失败记录", stopped, err, s.actions)
	}
	unchanged, err := s.Snapshot(matching.ID)
	if err != nil || len(unchanged.Entries) != 23 || !slices.Equal(unchanged.TaskFileIDs, queued.TaskFileIDs) {
		t.Fatal("停止不能删除原失败条目或执行未选项", unchanged, err)
	}
	result, err = s.RetryBatchContext(context.Background(), MediaOrganizeBatchRetry{Status: "failed", Query: "Work.S01E"})
	if err != nil || result.Queued != 23 {
		t.Fatal(result, err)
	}
	restarted, err := NewMediaOrganizer(store, metadata, s.templates, s.classification, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	progress := restarted.retryBatchProgressLocked()
	if progress == nil || progress.Status != "interrupted" || progress.Pending != 0 || progress.Skipped != 23 || len(restarted.actions) != 0 {
		t.Fatal("重启应中断批量且不自动重放", progress, restarted.actions)
	}
}

func TestMediaBatchRetryDeduplicateIdentityAndFailedPersistence(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprintf("保存失败=%t", fail), func(t *testing.T) {
			s, _, store, rule := newMediaOrganizeTest(t)
			first := mediaRecordFixture("first", "partial", 1)
			first.Rule = rule
			first.Entries[0].Status, first.Entries[0].Result, first.Entries[0].Modified = "error", nil, 10
			second := first
			second.ID = "second"
			saveMediaFixtures(t, s, first, second)
			if fail {
				store.err = errors.New("模拟批量保存失败")
			}
			result, err := s.RetryBatchContext(context.Background(), MediaOrganizeBatchRetry{Items: []MediaOrganizeDeleteItem{
				{RunID: first.ID, FileID: first.Entries[0].File.ID},
				{RunID: second.ID, FileID: second.Entries[0].File.ID},
			}})
			if fail {
				if err == nil || len(s.actions) != 0 || s.doc.RetryBatch != nil {
					t.Fatal("保存失败不得遗留批量进度或幽灵动作", result, err, s.actions, s.doc.RetryBatch)
				}
				return
			}
			if err != nil || result.Queued != 1 || result.Skipped != 1 || len(s.actions) != 1 {
				t.Fatal("同冻结来源同身份的历史不能重复搬运", result, err, s.actions)
			}
		})
	}
}

func TestMediaBatchRetryStopIssuedWriteAndKeepPendingRecords(t *testing.T) {
	s, fs, _, rule := newMediaCD2BatchOrganizer(t)
	entries := make([]MediaOrganizeEntry, 0, 25)
	for i := 1; i <= 25; i++ {
		entries = append(entries, mediaCD2BatchEntry(fs, rule, fmt.Sprintf("original-%02d.mkv", i), fmt.Sprintf("Work/S01E%02d.mkv", i)))
	}
	fs.renameFailures[entries[0].File.ID] = errors.New("本轮真实重命名失败")
	run := mediaCD2BatchRun(t, s, rule, entries...)
	run.TaskFileIDs = make([]string, 0, len(entries))
	for _, entry := range entries {
		run.TaskFileIDs = append(run.TaskFileIDs, entry.File.ID)
	}
	run.RetryBatchID = "stopping-batch"
	s.doc.RetryBatch = &MediaOrganizeRetryProgress{ID: run.RetryBatchID, Status: "running", Total: 25, Pending: 25}
	for i := range s.doc.Runs {
		if s.doc.Runs[i].ID == run.ID {
			cloneMediaRun(run, &s.doc.Runs[i])
		}
	}
	if err := s.persistLocked(run.ID); err != nil {
		t.Fatal(err)
	}
	s.removeActionLocked(run.ID)
	s.active = run.ID
	fs.beforeMove = func([]string) {
		stopping, err := s.StopRetryBatchContext(context.Background(), run.RetryBatchID)
		if err != nil || stopping.Status != "stopping" {
			t.Fatal("在途写入应先进入停止中", stopping, err)
		}
		fs.beforeMove = nil
	}
	if err := s.processQueuedRun(context.Background(), run, "execute"); err != nil {
		t.Fatal(err)
	}
	progress := s.retryBatchProgressLocked()
	if progress.Status != "stopped" || progress.Success != 19 || progress.Skipped != 5 || progress.Failed != 1 || progress.Pending != 0 || len(fs.moves) != 1 {
		t.Fatal("已发出的一批核实完成，剩余项跳过且不得再写入", progress, fs.moves)
	}
	stored, err := s.Snapshot(run.ID)
	if err != nil || len(stored.Entries) != 25 || s.active != "" || len(s.stopping) != 0 {
		t.Fatal("停止必须保留全部原失败上下文并释放执行占用", stored, err, s.active, s.stopping)
	}
}

func TestMediaBatchRetryStopPersistenceRollback(t *testing.T) {
	s, _, store, rule := newMediaOrganizeTest(t)
	run := mediaRecordFixture("stop-rollback", "partial", 1)
	run.Rule = rule
	run.Entries[0].Status, run.Entries[0].Result = "error", nil
	saveMediaFixtures(t, s, run)
	result, err := s.RetryBatchContext(context.Background(), MediaOrganizeBatchRetry{Items: []MediaOrganizeDeleteItem{{RunID: run.ID, FileID: run.Entries[0].File.ID}}})
	if err != nil {
		t.Fatal(err)
	}
	store.err = errors.New("模拟停止持久化失败")
	if _, err := s.StopRetryBatchContext(context.Background(), result.Batch.ID); err == nil {
		t.Fatal("停止保存失败必须报错")
	}
	progress := s.retryBatchProgressLocked()
	if progress == nil || progress.Status != "running" || progress.Pending != 1 || progress.Skipped != 0 || s.actions[run.ID] != "retry" || len(s.stopping) != 0 {
		t.Fatal("停止失败必须保留原进度、队列和停止标记", progress, s.actions, s.stopping)
	}
	store.err = nil
	stopped, err := s.StopRetryBatchContext(context.Background(), result.Batch.ID)
	if err != nil || stopped.Status != "stopped" || stopped.Skipped != 1 || len(s.actions) != 0 {
		t.Fatal("恢复保存后可以再次停止且只累计一次", stopped, err, s.actions)
	}
}

func TestMediaBatchRetryFinalCheckpointKeepsCountsOnRestart(t *testing.T) {
	s, metadata, store, rule := newMediaOrganizeTest(t)
	run := mediaRecordFixture("final-checkpoint", "partial", 2)
	run.Rule = rule
	for i := range run.Entries {
		run.Entries[i].Status, run.Entries[i].Result = "error", nil
	}
	saveMediaFixtures(t, s, run)
	result, err := s.RetryBatchContext(context.Background(), MediaOrganizeBatchRetry{Status: "failed"})
	if err != nil || result.Queued != 2 {
		t.Fatal(result, err)
	}
	worker, _, found := s.takeQueuedRunLocked()
	if !found {
		t.Fatal("批量未入队")
	}
	worker.Status = "partial"
	worker.Entries[0].Status = "done"
	worker.Entries[0].Result = run.Entries[0].Result
	// 附件失败不改变主视频完成事实与批量成功数。
	attachment := worker.Entries[0]
	attachment.File.ID += ".ass"
	attachment.File.MediaType, attachment.Status, attachment.Result = "subtitle", "error", nil
	attachment.VideoID = worker.Entries[0].File.ID
	worker.Entries = append(worker.Entries, attachment)
	if err := s.checkpoint(&worker); err != nil {
		t.Fatal(err)
	}
	// 模拟最后检查点成功、执行器尚未进入 defer 时进程退出。
	restarted, err := NewMediaOrganizer(store, metadata, s.templates, s.classification, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	progress := restarted.retryBatchProgressLocked()
	if progress == nil || progress.Status != "completed" || progress.Success != 1 || progress.Failed != 1 || progress.Skipped != 0 || progress.Pending != 0 {
		t.Fatal("终态结果与批量计数必须在同一事务保存", progress)
	}
}
