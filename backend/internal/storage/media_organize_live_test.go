package storage

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
)

func TestMediaOrganizeLiveQueryMergesWithoutPersistingRuntime(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "records.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	run := MediaOrganizeRunRow{ID: "retry", RuleID: "rule", Status: "partial", Record: "run", Tasks: []MediaOrganizeTaskRow{{FileID: "video", Group: "failed", At: "2026-11-03T00:00:00Z", Search: "work", Record: "saved-failure"}}}
	if err := s.SaveMediaOrganize(ctx, "{}", []MediaOrganizeRunRow{run}, nil); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`CREATE TRIGGER reject_runtime_task BEFORE INSERT ON media_organize_tasks BEGIN SELECT RAISE(ABORT,'runtime writes forbidden'); END`,
		`CREATE TRIGGER reject_runtime_run BEFORE INSERT ON media_organize_runs BEGIN SELECT RAISE(ABORT,'runtime writes forbidden'); END`,
	} {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	runtime := []MediaOrganizeLiveTaskRow{{RunID: "retry", MediaOrganizeTaskRow: MediaOrganizeTaskRow{FileID: "video", Group: "running", At: "2026-11-03T00:00:30Z", Search: "work", Record: "running-retry"}}}
	for i := range 25 {
		search := "work"
		if i == 0 {
			search = "work literal_%_ épisode"
		}
		runtime = append(runtime, MediaOrganizeLiveTaskRow{RunID: "live", MediaOrganizeTaskRow: MediaOrganizeTaskRow{FileID: fmt.Sprint(i), Group: "done", At: fmt.Sprintf("2026-11-03T00:00:%02dZ", i+1), Search: search, Record: fmt.Sprintf("done-%d", i)}})
	}
	result, err := s.QueryMediaOrganizeLive(ctx, "work", "all", 1, runtime)
	if err != nil || result.Total != 26 || result.Pages != 2 || len(result.Records) != 20 || result.Records[0] != "running-retry" || !reflect.DeepEqual(result.Counts, map[string]int{"all": 26, "running": 1, "done": 25, "failed": 0}) {
		t.Fatal("运行摘要应覆盖相同失败键，并在 SQL 中统一计数和分页", result, err)
	}
	last, err := s.QueryMediaOrganizeLive(ctx, "work", "done", 99, runtime)
	if err != nil || last.Page != 2 || last.Total != 25 || len(last.Records) != 5 || last.Counts["running"] != 1 {
		t.Fatal("状态筛选应先于分页，不能改变其他分组计数", last, err)
	}
	for _, query := range []string{"%", "_", "ÉPISODE"} {
		found, err := s.QueryMediaOrganizeLive(ctx, query, "all", 1, runtime)
		if err != nil || found.Total != 1 || found.Records[0] != "done-0" {
			t.Fatal("运行摘要应沿用字面量且不区分大小写的搜索", query, found, err)
		}
	}
	durable, err := s.QueryMediaOrganize(ctx, "", "all", 1)
	if err != nil || durable.Total != 1 || durable.Counts["failed"] != 1 || durable.Records[0] != "saved-failure" {
		t.Fatal("查询不能把运行中和成功摘要写入任务表或改写原失败", durable, err)
	}
	var taskCount int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM media_organize_tasks`).Scan(&taskCount); err != nil || taskCount != 1 {
		t.Fatal("运行摘要不应产生持久任务行", taskCount, err)
	}
}

func TestMediaOrganizeLiveQueryPreservesCancellation(t *testing.T) {
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "records.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.QueryMediaOrganizeLive(ctx, "", "all", 1, nil); !errors.Is(err, context.Canceled) {
		t.Fatal("只读查询取消应继续向上传递", err)
	}
}
