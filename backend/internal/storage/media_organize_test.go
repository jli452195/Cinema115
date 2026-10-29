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
	run := MediaOrganizeRunRow{ID: "run", RuleID: "rule", Status: "completed", At: "2026-10-29", Record: "old-run", Tasks: []MediaOrganizeTaskRow{{FileID: "a", Group: "done", At: "2026-10-29", Search: "first", Record: "first"}, {FileID: "b", Group: "done", At: "2026-10-29", Search: "second", Record: "second"}}}
	if err := s.SaveMediaOrganize(ctx, "old-rules", []MediaOrganizeRunRow{run}, nil); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`CREATE TABLE media_task_writes(file_id TEXT NOT NULL)`,
		`CREATE TRIGGER audit_media_task_updates AFTER UPDATE ON media_organize_tasks BEGIN INSERT INTO media_task_writes(file_id) VALUES(NEW.file_id); END`,
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
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM media_task_writes`).Scan(&writes); err != nil || writes != 0 {
		t.Fatal("identical checkpoint rewrote completed task rows", writes, err)
	}
	changed := run
	changed.Record = "new-run"
	changed.Tasks = append([]MediaOrganizeTaskRow{}, run.Tasks...)
	changed.Tasks[0].Record = "changed-first"
	if err := s.SaveMediaOrganize(ctx, "new-rules", []MediaOrganizeRunRow{changed}, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM media_task_writes`).Scan(&writes); err != nil || writes != 1 {
		t.Fatal("checkpoint should write only the changed child", writes, err)
	}
	bad := changed
	bad.Record = "uncommitted-run"
	bad.Tasks = append([]MediaOrganizeTaskRow{}, changed.Tasks...)
	bad.Tasks = append(bad.Tasks, MediaOrganizeTaskRow{FileID: "reject", Group: "attention"})
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
