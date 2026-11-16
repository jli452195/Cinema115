package storage

import (
	"context"
	"path/filepath"
	"testing"

	"strm115/backend/internal/model"
)

func TestEmbySyncScheduleMigration(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	const scope = "migration"
	if err := s.EmbyEnsure(ctx, scope); err != nil {
		t.Fatal(err)
	}
	if err := s.EmbyProgress(ctx, scope, "failed", 42, "同步失败"); err != nil {
		t.Fatal(err)
	}
	before, err := s.EmbyState(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	// 模拟版本 70，确认新增字段使用零值并保留原同步状态和删除索引。
	if _, err := s.db.ExecContext(ctx, `ALTER TABLE emby_state DROP COLUMN sync_retry_count; ALTER TABLE emby_state DROP COLUMN sync_retry_at; PRAGMA user_version=70;`); err != nil {
		t.Fatal(err)
	}
	if err := s.initialize(ctx); err != nil {
		t.Fatal(err)
	}
	after, err := s.EmbyState(ctx, scope)
	if err != nil || after != before {
		t.Fatalf("迁移改变了原同步状态: %+v %v", after, err)
	}
	var version, indexes int
	if err := s.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil || version != schemaVersion {
		t.Fatalf("迁移版本错误: %d %v", version, err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_schema WHERE type='index' AND name IN ('emby_parent','emby_library')`).Scan(&indexes); err != nil || indexes != 2 {
		t.Fatalf("原层级删除索引丢失: %d %v", indexes, err)
	}
	if err := s.EmbyRecordSyncRetry(ctx, scope, 2, "2026-11-18T03:00:00+08:00"); err != nil {
		t.Fatal(err)
	}
	before, err = s.EmbyState(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	// 已有字段时重复迁移不能覆盖重试计划，也不能重复添加列。
	if _, err := s.db.ExecContext(ctx, `PRAGMA user_version=70`); err != nil {
		t.Fatal(err)
	}
	if err := s.initialize(ctx); err != nil {
		t.Fatal(err)
	}
	after, err = s.EmbyState(ctx, scope)
	if err != nil || after != before {
		t.Fatalf("重复迁移改变了重试计划: %+v %v", after, err)
	}
}

func TestEmbySyncScheduleQueueGuards(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	for _, operation := range []struct {
		name         string
		queue        func(context.Context, string) error
		resetRetry   bool
		clearWebhook bool
	}{
		{"manual", s.EmbyQueueSync, true, true},
		{"scheduled-retry", func(ctx context.Context, scope string) error {
			return s.EmbyQueueScheduledSync(ctx, scope, false)
		}, false, false},
		{"scheduled-cycle", func(ctx context.Context, scope string) error {
			return s.EmbyQueueScheduledSync(ctx, scope, true)
		}, true, false},
	} {
		t.Run(operation.name, func(t *testing.T) {
			firstScope := operation.name + "-first"
			if err := operation.queue(ctx, firstScope); err != nil {
				t.Fatal(err)
			}
			first, err := s.EmbyState(ctx, firstScope)
			if err != nil || first.Status != "queued" || first.SyncRetryCount != 0 || first.SyncRetryAt != "" {
				t.Fatalf("首次入队失败: %+v %v", first, err)
			}
			for _, status := range []string{"idle", "failed", "queued", "running"} {
				t.Run(status, func(t *testing.T) {
					scope := operation.name + "-" + status
					if _, err := s.db.ExecContext(ctx, `INSERT INTO emby_state(scope,status,scanned,last_error,webhook_error,sync_retry_count,sync_retry_at) VALUES(?,?,42,'同步失败','通知失败',2,'2026-11-18T03:00:00+08:00')`, scope, status); err != nil {
						t.Fatal(err)
					}
					want, err := s.EmbyState(ctx, scope)
					if err != nil {
						t.Fatal(err)
					}
					if status != "queued" && status != "running" {
						want.Status, want.Scanned, want.LastError, want.SyncRetryAt = "queued", 0, "", ""
						if operation.resetRetry {
							want.SyncRetryCount = 0
						}
						if operation.clearWebhook {
							want.WebhookError = ""
						}
					}
					if err := operation.queue(ctx, scope); err != nil {
						t.Fatal(err)
					}
					got, err := s.EmbyState(ctx, scope)
					if err != nil || got != want {
						t.Fatalf("入队覆盖了受保护状态或未清理重试: got=%+v want=%+v err=%v", got, want, err)
					}
				})
			}
		})
	}
}

func TestEmbySyncScheduleRetryGuards(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	for _, scenario := range []struct{ name, status, retryAt string }{
		{"first-failure", "failed", ""},
		{"existing-plan", "failed", "2026-11-18T03:00:00+08:00"},
		{"manual-queued", "queued", ""},
		{"running", "running", ""},
		{"idle", "idle", ""},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			if _, err := s.db.ExecContext(ctx, `INSERT INTO emby_state(scope,status,sync_retry_count,sync_retry_at) VALUES(?,?,2,?)`, scenario.name, scenario.status, scenario.retryAt); err != nil {
				t.Fatal(err)
			}
			want, err := s.EmbyState(ctx, scenario.name)
			if err != nil {
				t.Fatal(err)
			}
			const retryAt = "2026-11-19T03:00:00+08:00"
			if scenario.status == "failed" && scenario.retryAt == "" {
				want.SyncRetryCount, want.SyncRetryAt = 3, retryAt
			}
			if err := s.EmbyRecordSyncRetry(ctx, scenario.name, 3, retryAt); err != nil {
				t.Fatal(err)
			}
			got, err := s.EmbyState(ctx, scenario.name)
			if err != nil || got != want {
				t.Fatalf("重试记录覆盖了已有计划或新任务: got=%+v want=%+v err=%v", got, want, err)
			}
		})
	}
}

func TestEmbySyncScheduleRecoveryPersistsRetry(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "emby-sync.db")
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	const retryAt = "2026-11-18T03:00:00+08:00"
	scenarios := []struct {
		scope, status, wantStatus string
		complete                  bool
	}{
		{"failed-complete", "failed", "failed", true},
		{"running-complete", "running", "queued", true},
		{"running-incomplete", "running", "failed", false},
	}
	for _, scenario := range scenarios {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO emby_state(scope,status,staging_generation,staging_complete,staging_cutoff,sync_retry_count,sync_retry_at) VALUES(?,?,'stage',?,12,2,?)`, scenario.scope, scenario.status, scenario.complete, retryAt); err != nil {
			t.Fatal(err)
		}
		if err := s.EmbyStage(ctx, scenario.scope, "stage", nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, path)
	if err != nil {
		// 清理函数仍引用原句柄，避免在打开失败时解引用空指针。
		t.Fatal(err)
	}
	s = reopened
	if err := s.EmbyRecover(ctx); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range scenarios {
		got, err := s.EmbyState(ctx, scenario.scope)
		if err != nil || got.Status != scenario.wantStatus || got.SyncRetryCount != 2 || got.SyncRetryAt != retryAt {
			t.Fatalf("恢复改变了持久重试或绕过退避: scope=%s state=%+v err=%v", scenario.scope, got, err)
		}
		if got.StagingComplete != scenario.complete || (scenario.complete && (got.StagingGeneration != "stage" || got.StagingCutoff != 12)) || (!scenario.complete && (got.StagingGeneration != "" || got.StagingCutoff != 0)) {
			t.Fatalf("恢复暂存快照状态错误: %+v", got)
		}
		var snapshots int
		if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM emby_snapshots WHERE scope=?`, scenario.scope).Scan(&snapshots); err != nil || (scenario.complete && snapshots != 1) || (!scenario.complete && snapshots != 0) {
			t.Fatalf("恢复错误地清理或保留了暂存快照: scope=%s count=%d err=%v", scenario.scope, snapshots, err)
		}
	}
}

func TestEmbySyncSchedulePublishClearsRetry(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	const scope = "publish"
	if err := s.EmbyEnsure(ctx, scope); err != nil {
		t.Fatal(err)
	}
	for _, generation := range []string{"old", "new"} {
		if err := s.EmbyBeginSync(ctx, scope, generation, 0); err != nil {
			t.Fatal(err)
		}
		if err := s.EmbyStage(ctx, scope, generation, []model.EmbyItem{{ID: generation, Type: "Movie", TMDB: "1"}}); err != nil {
			t.Fatal(err)
		}
		if err := s.EmbyCompleteStage(ctx, scope, generation, 1); err != nil {
			t.Fatal(err)
		}
		if generation == "old" {
			if err := s.EmbyPublish(ctx, scope, generation, 0); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := s.EmbyFailSync(ctx, scope, "new", "发布失败", false); err != nil {
		t.Fatal(err)
	}
	if err := s.EmbyRecordSyncRetry(ctx, scope, 2, "2026-11-18T03:00:00+08:00"); err != nil {
		t.Fatal(err)
	}
	before, err := s.EmbyState(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.EmbyPublish(ctx, scope, "missing", 0); err == nil {
		t.Fatal("发布不存在的快照应失败")
	}
	after, err := s.EmbyState(ctx, scope)
	if err != nil || after != before || after.Active != "old" {
		t.Fatalf("发布失败改变了旧快照或重试计划: %+v %v", after, err)
	}
	if err := s.EmbyPublish(ctx, scope, "new", 0); err != nil {
		t.Fatal(err)
	}
	after, err = s.EmbyState(ctx, scope)
	if err != nil || after.Active != "new" || after.Status != "idle" || after.Count != 1 || after.SyncedAt == "" || after.SyncRetryCount != 0 || after.SyncRetryAt != "" {
		t.Fatalf("发布成功没有清理重试计划: %+v %v", after, err)
	}
}
