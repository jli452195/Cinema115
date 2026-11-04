package service

import (
	"context"
	"errors"
	"testing"

	"strm115/backend/internal/client/p115"
)

func TestMediaDeleteDuringOtherOwnerExecution(t *testing.T) {
	s, _, _ := newMediaSQLiteTest(t)
	run := mediaRecordFixture("concurrent", "running", 2)
	a, b := run.Entries[0].File.ID, run.Entries[1].File.ID
	run.Entries[1].Status, run.Entries[1].Result = "ready", nil
	run.Entries = append(run.Entries, MediaOrganizeEntry{File: OrganizeFile{ShareFile: p115.ShareFile{ID: "a-sub"}, MediaType: "subtitle"}, VideoID: a, Status: "ready"})
	saveMediaFixtures(t, s, run)
	s.active = run.ID
	var worker MediaOrganizeRun
	cloneMediaRun(run, &worker)
	worker.seenTasks = mediaVideoIDs(worker)
	indices, release := s.beginMediaEntries(&worker, []int{1})
	defer release()
	if len(indices) != 1 {
		t.Fatal(indices)
	}
	for id, want := range map[string]bool{a: true, b: false} {
		task, err := s.Task(run.ID, id)
		if err != nil || task.CanDelete != want {
			t.Fatal(task, err)
		}
	}
	items := []MediaOrganizeDeleteItem{{RunID: run.ID, FileID: a}, {RunID: run.ID, FileID: b}}
	if n, err := s.DeleteHistory(context.Background(), MediaOrganizeDelete{Items: items}); n != 0 || !errors.Is(err, ErrMediaOrganizeBusy) {
		t.Fatal("混合请求必须原子拒绝", n, err)
	}
	if n, err := s.DeleteHistory(context.Background(), MediaOrganizeDelete{Items: items[:1]}); n != 1 || err != nil {
		t.Fatal(n, err)
	}
	if err := s.checkpoint(&worker); err != nil {
		t.Fatal(err)
	}
	if len(worker.Entries) != 3 || worker.Entries[1].File.ID != b {
		t.Fatal("工作副本索引不能变化", worker.Entries)
	}
	if current, err := s.Snapshot(run.ID); err != nil || len(current.Entries) != 1 || current.Entries[0].File.ID != b {
		t.Fatal("已删除任务不能被检查点复活", current, err)
	}
	for _, index := range []int{0, 2} {
		selected, done := s.beginMediaEntries(&worker, []int{index})
		done()
		if len(selected) != 0 {
			t.Fatal("已删除主文件及其附件不能继续操作", selected)
		}
	}
	_, nestedRelease := s.beginMediaEntries(&worker, []int{1})
	release()
	release()
	if task, _ := s.Task(run.ID, b); task.CanDelete {
		t.Fatal("嵌套操作未完成不能解除保护")
	}
	nestedRelease()
	if n, err := s.DeleteHistory(context.Background(), MediaOrganizeDelete{Items: items[1:]}); n != 1 || err != nil {
		t.Fatal(n, err)
	}
	if err := s.checkpoint(&worker); err != nil {
		t.Fatal(err)
	}
	if current, err := s.Snapshot(run.ID); err != nil || len(current.Entries) != 0 {
		t.Fatal(current, err)
	}
	newEntry := run.Entries[0]
	newEntry.File.ID = "new-video"
	worker.Entries = append(worker.Entries, newEntry)
	if err := s.checkpoint(&worker); err != nil {
		t.Fatal(err)
	}
	if current, _ := s.Snapshot(run.ID); len(current.Entries) != 1 || current.Entries[0].File.ID != "new-video" {
		t.Fatal("后续新发现的视频应正常发布", current)
	}
}

func TestMediaDeleteQueuedRetryDoesNotWidenSelection(t *testing.T) {
	s, _, _ := newMediaSQLiteTest(t)
	run := mediaRecordFixture("queued-delete", "partial", 2)
	for i := range run.Entries {
		run.Entries[i].Status, run.Entries[i].Message, run.Entries[i].Result = "error", "原失败", nil
	}
	saveMediaFixtures(t, s, run)
	id := run.Entries[0].File.ID
	if _, err := s.Retry(run.ID, MediaOrganizeCorrection{FileID: id}); err != nil {
		t.Fatal(err)
	}
	if n, err := s.DeleteHistory(context.Background(), MediaOrganizeDelete{Items: []MediaOrganizeDeleteItem{{RunID: run.ID, FileID: id}}}); n != 1 || err != nil {
		t.Fatal(n, err)
	}
	if _, _, found := s.takeQueuedRunLocked(); found {
		t.Fatal("删除选中重试后不得运行其他兄弟任务")
	}
	remaining, err := s.Task(run.ID, run.Entries[1].File.ID)
	if err != nil || remaining.Status != "failed" || !remaining.CanRetry || !remaining.CanDelete {
		t.Fatal(remaining, err)
	}
}

func TestMediaDeleteQueuedRetryRollbackKeepsAction(t *testing.T) {
	s, _, store, rule := newMediaOrganizeTest(t)
	run := mediaRecordFixture("delete-rollback", "partial", 1)
	run.Rule = rule
	run.Entries[0].Status, run.Entries[0].Message, run.Entries[0].Result = "error", "失败", nil
	saveMediaFixtures(t, s, run)
	id := run.Entries[0].File.ID
	if _, err := s.Retry(run.ID, MediaOrganizeCorrection{FileID: id}); err != nil {
		t.Fatal(err)
	}
	store.err = errors.New("模拟删除保存失败")
	input := MediaOrganizeDelete{Items: []MediaOrganizeDeleteItem{{RunID: run.ID, FileID: id}}}
	if n, err := s.DeleteHistory(context.Background(), input); n != 0 || err == nil {
		t.Fatal(n, err)
	}
	if task, err := s.Task(run.ID, id); err != nil || task.Status != "running" || s.actions[run.ID] != "retry" {
		t.Fatal("提交失败必须同时保留任务和动作", task, err, s.actions)
	}
	store.err = nil
	if n, err := s.DeleteHistory(context.Background(), input); n != 1 || err != nil {
		t.Fatal(n, err)
	}
	if len(s.actions) != 0 {
		t.Fatal("成功删除后移除排队动作", s.actions)
	}
}

func TestMediaCheckpointFailureDoesNotDiscardNewDiscovery(t *testing.T) {
	s, _, store, rule := newMediaOrganizeTest(t)
	run, err := s.Start(rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	run.seenTasks = mediaVideoIDs(run)
	entry := mediaRecordFixture("discovery", "partial", 1).Entries[0]
	entry.Status, entry.Result = "error", nil
	run.Entries = append(run.Entries, entry)
	store.err = errors.New("模拟检查点失败")
	if err := s.checkpoint(&run); err == nil {
		t.Fatal("应报告保存失败")
	}
	store.err = nil
	if err := s.checkpoint(&run); err != nil {
		t.Fatal(err)
	}
	if current, err := s.Snapshot(run.ID); err != nil || len(current.Entries) != 1 || current.Entries[0].File.ID != entry.File.ID {
		t.Fatal("回滚不能把未发布的新文件当作用户删除", current, err)
	}
}
