package storage

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"strm115/backend/internal/model"
)

func testStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "中文 # 数据", "cinema115.db")
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s, path
}

func sampleJob(id, stage string) model.ReshareJob {
	return model.ReshareJob{ID: id, Status: stage, SourceURL: "https://115.com/s/test?password=abcd", SourceCID: "0", DirectoryName: "临时-" + id, DirectoryID: "3516652121397134386", ShareURL: "https://115.com/s/own?password=efgh", Finished: stage == "completed", UpdatedAt: time.Date(2026, 9, 13, 1, 2, 3, 0, time.UTC)}
}

func writeOldJob(t *testing.T, directory string, job model.ReshareJob) []byte {
	t.Helper()
	data, err := json.Marshal(job)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, job.ID+".json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	return data
}

func TestLegacyImportPreservesRecordsAndNeverReplaysOldState(t *testing.T) {
	s, path := testStore(t)
	ctx := context.Background()
	dir := filepath.Join(filepath.Dir(path), "reshare-jobs")
	first := sampleJob("a", "deleting")
	first.LastError = "删除结果待核实"
	first.Finished = true
	original := writeOldJob(t, dir, first)
	writeOldJob(t, dir, sampleJob("b", "receiving"))
	// 已存在的数据库进度优先于旧文件。
	newer := sampleJob("b", "completed")
	if err := s.SaveReshareJob(ctx, newer); err != nil {
		t.Fatal(err)
	}
	result, err := s.ImportLegacyJobs(ctx, dir)
	if err != nil || result.Imported != 1 || result.Skipped != 1 || result.AlreadyDone {
		t.Fatalf("迁移结果错误: %+v %v", result, err)
	}
	got, err := s.GetReshareJob(ctx, "a")
	if err != nil || !reflect.DeepEqual(got, first) {
		t.Fatal("字段未完整迁移", err)
	}
	got, err = s.GetReshareJob(ctx, "b")
	if err != nil || !reflect.DeepEqual(got, newer) {
		t.Fatal("旧记录覆盖了数据库进度", err)
	}
	backup, err := os.ReadFile(filepath.Join(dir, "a.json"))
	if err != nil || string(backup) != string(original) {
		t.Fatal("旧 JSON 被修改")
	}
	first.Status = "completed"
	if err := s.SaveReshareJob(ctx, first); err != nil {
		t.Fatal(err)
	}
	// 重启后即使旧文件损坏，也不应重新导入已迁移的数据。
	if err := os.WriteFile(filepath.Join(dir, "a.json"), []byte("损坏的旧备份"), 0600); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	result, err = reopened.ImportLegacyJobs(ctx, dir)
	if err != nil || !result.AlreadyDone {
		t.Fatal("迁移未幂等", err)
	}
	got, err = reopened.GetReshareJob(ctx, "a")
	if err != nil || got.Status != "completed" {
		t.Fatal("重启丢失最新检查点", err)
	}
}

func TestLegacyImportRollsBackOnMalformedRecord(t *testing.T) {
	for _, invalid := range []string{`{"id":`, `null`, `{"id":"wrong","status":"sharing","updatedAt":"2026-09-13T00:00:00Z"}`, `{"id":"z","status":"sharing","updatedAt":"2026-09-13T00:00:00Z"} trailing`} {
		t.Run(invalid, func(t *testing.T) {
			s, path := testStore(t)
			ctx := context.Background()
			dir := filepath.Join(filepath.Dir(path), "reshare-jobs")
			writeOldJob(t, dir, sampleJob("a", "completed"))
			if err := os.WriteFile(filepath.Join(dir, "z.json"), []byte(invalid), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := s.ImportLegacyJobs(ctx, dir); err == nil {
				t.Fatal("损坏记录应阻止迁移")
			}
			status, err := s.Status(ctx)
			if err != nil || status.Jobs != 0 || status.LegacyImported {
				t.Fatal("失败后留下部分数据或成功标记", err)
			}
			writeOldJob(t, dir, sampleJob("z", "receiving"))
			result, err := s.ImportLegacyJobs(ctx, dir)
			if err != nil || result.Imported != 2 {
				t.Fatal("修复后不能重新迁移", err)
			}
		})
	}
}

func TestJobQueriesCancellationAndWALBackup(t *testing.T) {
	s, path := testStore(t)
	ctx := context.Background()
	for _, id := range []string{"a", "b", "c"} {
		job := sampleJob(id, "completed")
		if id == "b" {
			job.Status = "deleting"
		}
		// 固定精度 UTC 时间应保证同一秒内的排序也正确。
		if id == "c" {
			job.UpdatedAt = job.UpdatedAt.Add(time.Nanosecond)
		}
		if err := s.SaveReshareJob(ctx, job); err != nil {
			t.Fatal(err)
		}
	}
	jobs, err := s.ListReshareJobs(ctx, "completed", 1, 0)
	if err != nil || len(jobs) != 1 || jobs[0].ID != "c" {
		t.Fatal("状态筛选或排序错误", err)
	}
	jobs, err = s.ListReshareJobs(ctx, "completed", 1, 1)
	if err != nil || len(jobs) != 1 || jobs[0].ID != "a" {
		t.Fatal("分页错误", err)
	}
	if _, err := s.GetReshareJob(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatal("未找到语义丢失", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := s.SaveReshareJob(canceled, sampleJob("a", "deleting")); !errors.Is(err, context.Canceled) {
		t.Fatal("未响应取消", err)
	}
	got, err := s.GetReshareJob(ctx, "a")
	if err != nil || got.Status != "completed" {
		t.Fatal("已取消操作改变了检查点", err)
	}
	for pragma, want := range map[string]string{"journal_mode": "wal", "synchronous": "2", "foreign_keys": "1", "busy_timeout": "5000"} {
		var value string
		if err := s.db.QueryRowContext(ctx, "PRAGMA "+pragma).Scan(&value); err != nil || value != want {
			t.Fatalf("配置 %s 未生效: %s %v", pragma, value, err)
		}
	}
	backup := filepath.Join(filepath.Dir(path), "backup's.db")
	if err := s.Backup(ctx, backup); err != nil {
		t.Fatal(err)
	}
	if err := s.Backup(ctx, backup); err == nil {
		t.Fatal("覆盖了已有备份")
	}
	if err := s.Backup(ctx, path); err == nil {
		t.Fatal("覆盖了正在使用的数据库")
	}
	restored, err := Open(ctx, backup)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if err := restored.Check(ctx); err != nil {
		t.Fatal(err)
	}
	status, err := restored.Status(ctx)
	if err != nil || status.Jobs != 3 {
		t.Fatal("WAL 中的提交未进入备份", err)
	}
}

func TestRejectsNewerDatabaseVersion(t *testing.T) {
	s, path := testStore(t)
	if _, err := s.db.ExecContext(context.Background(), "PRAGMA user_version=999"); err != nil {
		t.Fatal(err)
	}
	if unexpected, err := Open(context.Background(), path); err == nil {
		unexpected.Close()
		t.Fatal("旧程序不应打开新版本数据库")
	}
}
