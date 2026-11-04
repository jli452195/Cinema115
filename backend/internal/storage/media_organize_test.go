package storage

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
)

func TestMediaOrganizeTransactionRollbackAndChangedTaskWrites(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "records.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rows, err := s.LoadMediaOrganize(ctx)
	if err != nil || rows.Initialized {
		t.Fatal("new store must not be considered imported", rows, err)
	}
	run := MediaOrganizeRunRow{ID: "run", RuleID: "rule", At: "2026-10-29", Record: "old-run", Tasks: []MediaOrganizeTaskRow{{FileID: "a", Group: "done", At: "2026-10-29", Search: "first", Record: "first"}, {FileID: "b", Group: "done", At: "2026-10-29", Search: "second", Record: "second"}}}
	if err := s.SaveMediaOrganize(ctx, "old-rules", []MediaOrganizeRunRow{run}, nil); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`CREATE TABLE media_run_writes(run_id TEXT NOT NULL)`,
		`CREATE TABLE media_task_writes(file_id TEXT NOT NULL)`,
		`CREATE TABLE media_settings_writes(record TEXT NOT NULL)`,
		`CREATE TRIGGER audit_media_task_updates AFTER UPDATE ON media_organize_tasks BEGIN INSERT INTO media_task_writes(file_id) VALUES(NEW.file_id); END`,
		`CREATE TRIGGER audit_media_settings_updates AFTER UPDATE ON media_organize_settings BEGIN INSERT INTO media_settings_writes(record) VALUES(NEW.record); END`,
		`CREATE TRIGGER audit_media_run_updates AFTER UPDATE ON media_organize_runs BEGIN INSERT INTO media_run_writes(run_id) VALUES(NEW.id); END`,
		`CREATE TRIGGER reject_media_task BEFORE INSERT ON media_organize_tasks WHEN NEW.file_id='reject' BEGIN SELECT RAISE(ABORT,'injected task write failure'); END`,
	} {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SaveMediaOrganize(ctx, "old-rules", []MediaOrganizeRunRow{run}, nil); err != nil {
		t.Fatal(err)
	}
	var writes int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM media_run_writes`).Scan(&writes); err != nil || writes != 0 {
		t.Fatal("相同批次不应重复写入", writes, err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM media_task_writes`).Scan(&writes); err != nil || writes != 0 {
		t.Fatal("identical checkpoint rewrote completed task rows", writes, err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM media_settings_writes`).Scan(&writes); err != nil || writes != 0 {
		t.Fatal("相同检查点重写设置", writes, err)
	}
	changed := run
	changed.Record = "new-run"
	changed.Tasks = append([]MediaOrganizeTaskRow{}, run.Tasks...)
	changed.Tasks[0].Record = "changed-first"
	if err := s.SaveMediaOrganize(ctx, "new-rules", []MediaOrganizeRunRow{changed}, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM media_run_writes`).Scan(&writes); err != nil || writes != 1 {
		t.Fatal("批次变化应只更新一次", writes, err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM media_task_writes`).Scan(&writes); err != nil || writes != 1 {
		t.Fatal("checkpoint should write only the changed child", writes, err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM media_settings_writes`).Scan(&writes); err != nil || writes != 1 {
		t.Fatal("设置变化应只更新一次", writes, err)
	}
	bad := changed
	bad.Record = "uncommitted-run"
	bad.Tasks = append([]MediaOrganizeTaskRow{}, changed.Tasks...)
	bad.Tasks = append(bad.Tasks, MediaOrganizeTaskRow{FileID: "reject", Group: "failed"})
	if err := s.SaveMediaOrganize(ctx, "uncommitted-rules", []MediaOrganizeRunRow{bad}, nil); err == nil {
		t.Fatal("injected SQL failure must propagate")
	}
	rows, err = s.LoadMediaOrganize(ctx)
	if err != nil || !rows.Initialized || rows.Rules != "new-rules" || !reflect.DeepEqual(rows.Runs, []string{"new-run"}) {
		t.Fatal("run/rules committed despite task insertion failure", rows, err)
	}
	query, err := s.QueryMediaOrganize(ctx, "", "all", 1)
	if err != nil || query.Total != 2 || !reflect.DeepEqual(query.Records, []string{"changed-first", "second"}) {
		t.Fatal("task index diverged from committed run", query, err)
	}
	changed.Tasks = changed.Tasks[1:]
	if err := s.SaveMediaOrganize(ctx, "new-rules", []MediaOrganizeRunRow{changed}, nil); err != nil {
		t.Fatal(err)
	}
	query, err = s.QueryMediaOrganize(ctx, "", "all", 1)
	if err != nil || query.Total != 1 || query.Records[0] != "second" {
		t.Fatal("removed child survived checkpoint", query, err)
	}
	if err := s.SaveMediaOrganize(ctx, "new-rules", nil, []string{run.ID}); err != nil {
		t.Fatal(err)
	}
	query, err = s.QueryMediaOrganize(ctx, "", "all", 1)
	if err != nil || query.Total != 0 {
		t.Fatal("run deletion did not cascade to task index", query, err)
	}
}

func TestMediaOrganizeRemoveDuplicateStatusPreservesRecords(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "records.db")
	s, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if s != nil {
			s.Close()
		}
	})
	// 重建旧结构，验证状态列移除后完整记录、任务索引及清理摘要仍然保留。
	if _, err := s.db.ExecContext(ctx, `
DROP TABLE media_organize_tasks;
DROP TABLE media_organize_runs;
CREATE TABLE media_organize_runs(id TEXT PRIMARY KEY,rule_id TEXT NOT NULL,status TEXT NOT NULL,at TEXT NOT NULL,record TEXT NOT NULL,activity TEXT NOT NULL,cleanup TEXT NOT NULL);
CREATE TABLE media_organize_tasks(run_id TEXT NOT NULL REFERENCES media_organize_runs(id) ON DELETE CASCADE,file_id TEXT NOT NULL,status_group TEXT NOT NULL,at TEXT NOT NULL,search TEXT NOT NULL,record TEXT NOT NULL,PRIMARY KEY(run_id,file_id));
INSERT INTO media_organize_settings(id,record) VALUES(1,'{"rules":[]}');
INSERT INTO media_organize_runs(id,rule_id,status,at,record,activity,cleanup) VALUES('run','rule','partial','2026-11-05','{"status":"partial","entries":[{"file":{"id":"video"},"p115Move":{"phase":"moving"}}]}','{"runId":"run","status":"partial"}','{"runId":"run","files":2}');
INSERT INTO media_organize_tasks(run_id,file_id,status_group,at,search,record) VALUES('run','video','failed','2026-11-05','work','{"fileId":"video","status":"failed"}');
PRAGMA user_version=65;`); err != nil {
		t.Fatal(err)
	}
	before, err := s.LoadMediaOrganize(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	var hasStatus bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pragma_table_info('media_organize_runs') WHERE name='status')`).Scan(&hasStatus); err != nil || hasStatus {
		t.Fatal("重复状态列未移除", hasStatus, err)
	}
	after, err := s.LoadMediaOrganize(ctx)
	if err != nil || !reflect.DeepEqual(after, before) {
		t.Fatal("结构精简改变了整理记录", after, before, err)
	}
	query, err := s.QueryMediaOrganize(ctx, "work", "failed", 1)
	if err != nil || query.Total != 1 || len(query.Activity) != 1 || len(query.Cleanup) != 1 {
		t.Fatal("任务索引或运行摘要丢失", query, err)
	}
	run := MediaOrganizeRunRow{ID: "run", RuleID: "rule", At: "2026-11-05", Record: `{"status":"completed"}`, Tasks: []MediaOrganizeTaskRow{{FileID: "video", Group: "done", At: "2026-11-05", Search: "work", Record: `{"fileId":"video","status":"done"}`}}}
	if err := s.SaveMediaOrganize(ctx, before.Rules, []MediaOrganizeRunRow{run}, nil); err != nil {
		t.Fatal("精简后无法保存任务", err)
	}
	query, err = s.QueryMediaOrganize(ctx, "work", "done", 1)
	if err != nil || query.Total != 1 || len(query.Cleanup) != 1 {
		t.Fatal("精简后任务无法更新或清理摘要丢失", query, err)
	}
	if err := s.SaveMediaOrganize(ctx, before.Rules, nil, []string{"run"}); err != nil {
		t.Fatal(err)
	}
	query, err = s.QueryMediaOrganize(ctx, "", "all", 1)
	if err != nil || query.Total != 0 {
		t.Fatal("精简后删除未级联清除任务", query, err)
	}
}
