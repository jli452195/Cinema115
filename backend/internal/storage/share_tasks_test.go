package storage

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"strm115/backend/internal/model"
)

func TestShareTaskStatusGroupsBeforePagination(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for i, status := range []string{"success", "failed", "interrupted", "cancelled", "running", "review", "waiting_review"} {
		task := model.ShareTask{ID: status, Status: status, Mode: "direct", SourceURL: "https://115.com/s/demo", UpdatedAt: time.Unix(int64(i), 0)}
		if err := s.SaveShareTask(ctx, task); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		status        string
		limit, offset int
		want          []string
	}{
		{"failed", 10, 0, []string{"cancelled", "interrupted", "failed"}},
		{"failed", 1, 1, []string{"interrupted"}},
		{"review", 10, 0, []string{"waiting_review", "review"}},
		{"running", 10, 0, []string{"running"}},
		{"success", 10, 0, []string{"success"}},
	} {
		tasks, err := s.ListShareTasks(ctx, tc.status, "direct", "demo", tc.limit, tc.offset)
		if err != nil {
			t.Fatal(err)
		}
		got := make([]string, 0, len(tasks))
		for _, task := range tasks {
			got = append(got, task.Status)
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("状态 %s 分组或分页错误：%v", tc.status, got)
		}
	}
}

func TestShareTaskMigrationPersistenceFiltersAndClear(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	// 模拟旧版数据库，验证从版本 1 自动新增任务表。
	if _, err := s.db.ExecContext(ctx, "DROP TABLE share_tasks; PRAGMA user_version=1;"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().UTC()
	next := now.Add(time.Hour)
	for _, status := range []string{"success", "waiting_review", "running", "failed"} {
		task := model.ShareTask{ID: status, Status: status, Mode: "direct", SourceURL: "https://115.com/s/demo", UpdatedAt: now, CreatedAt: now, Request: json.RawMessage(`{"input":{}}`)}
		if status == "waiting_review" {
			task.NextRetryAt = &next
		}
		if err := s.SaveShareTask(ctx, task); err != nil {
			t.Fatal(err)
		}
	}
	filtered, err := s.ListShareTasks(ctx, "success", "direct", "demo", 20, 0)
	if err != nil || len(filtered) != 1 {
		t.Fatal("筛选失败", err)
	}
	data, _ := json.Marshal(filtered[0])
	var visible map[string]any
	json.Unmarshal(data, &visible)
	if _, exists := visible["request"]; exists {
		t.Fatal("暴露内部重试参数")
	}
	if due, err := s.DueShareTasks(ctx, now); err != nil || len(due) != 0 {
		t.Fatal("提前重试")
	}
	if due, err := s.DueShareTasks(ctx, next); err != nil || len(due) != 1 {
		t.Fatal("重试时间错误")
	}
	if count, err := s.ClearShareTasks(ctx, []string{"success", "waiting_review"}); err != nil || count != 1 {
		t.Fatal("清理所选误删待重试任务", err)
	}
	if count, err := s.ClearShareTasks(ctx, nil); err != nil || count != 1 {
		t.Fatal("清空误删活动任务", err)
	}
	if err := s.InterruptShareTasks(ctx); err != nil {
		t.Fatal(err)
	}
	rows, err := s.ListShareTasks(ctx, "interrupted", "", "", 20, 0)
	if err != nil || len(rows) != 1 {
		t.Fatal("中断状态恢复失败", err)
	}
	if due, err := s.DueShareTasks(ctx, next); err != nil || len(due) != 1 {
		t.Fatal("恢复中断影响审核等待任务")
	}
}
