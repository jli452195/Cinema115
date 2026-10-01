package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
)

func TestDirectoryDeletedFilesStopRetryAndEndUnlocksConfiguration(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(map[bool]string{true: "legacy-task", false: "new-task"}[legacy], func(t *testing.T) {
			svc, client, generator, config := newMonitorScheduleTest(t)
			tasks := directorySharedTasks(t, svc, generator, config)
			tasks.Settings.value.ReviewRetryEnabled = true
			ctx := context.Background()
			if err := svc.ScanNow(ctx, config.ID); err != nil {
				t.Fatal(err)
			}
			if err := svc.runDue(ctx); err != nil {
				t.Fatal(err)
			}
			batch := loadMonitor(t, svc, config.ID)
			if legacy {
				task, err := tasks.Store.GetShareTask(ctx, batch.GenerationTaskID)
				if err != nil {
					t.Fatal(err)
				}
				var req shareTaskRequest
				if err := json.Unmarshal(task.Request, &req); err != nil {
					t.Fatal(err)
				}
				req.DirectoryID = ""
				task.Request, err = json.Marshal(req)
				if err != nil {
					t.Fatal(err)
				}
				if err := tasks.Store.SaveShareTask(ctx, task); err != nil {
					t.Fatal(err)
				}
			}
			// The owner removes the video while generation is still queued.
			client.drive[batch.BatchDirectoryID] = nil
			if err := tasks.retryDue(ctx, time.Now()); err != nil {
				t.Fatal(err)
			}
			task, err := tasks.Store.GetShareTask(ctx, batch.GenerationTaskID)
			if err != nil {
				t.Fatal(err)
			}
			if task.Status != "failed" || task.NextRetryAt != nil || generator.calls != 0 || !strings.Contains(task.Error, "已删除") {
				t.Fatalf("deletion was treated as review: %+v", task)
			}
			if err := svc.runDue(ctx); err != nil {
				t.Fatal(err)
			}
			ended, err := svc.EndBatch(ctx, config.ID, batch.LogTaskID)
			if err != nil {
				t.Fatal(err)
			}
			if ended.Stage != "ended" || ended.TaskPending || ended.BatchDirectoryID != "" || len(ended.EndedBatches) != 1 {
				t.Fatalf("not released: %+v", ended)
			}
			if ended.EndedBatches[0].DirectoryID != batch.BatchDirectoryID || ended.EndedBatches[0].ShareURL != batch.ShareURL {
				t.Fatal("lost original recovery location")
			}
			if len(client.deleted) != 1 || client.deleted[0] != batch.BatchDirectoryID {
				t.Fatal("automatic recovery must remove only the verified empty staging directory")
			}
			results, err := tasks.RetryTasks(ctx, []string{task.ID})
			if err != nil || len(results) != 1 || results[0].Accepted {
				t.Fatal("retired share task can still restart")
			}
			// Save new options and share the newly arrived file in a new batch.
			if _, err := svc.Save(ctx, ended); err != nil {
				t.Fatal(err)
			}
			client.drive["100"] = []p115.ShareFile{{ID: "new", Name: "new.mkv", Size: 10}}
			client.nextDirectoryID = "901"
			if err := svc.ScanNow(ctx, config.ID); err != nil {
				t.Fatal(err)
			}
			if err := svc.runDue(ctx); err != nil {
				t.Fatal(err)
			}
			next := loadMonitor(t, svc, config.ID)
			if next.LogTaskID == batch.LogTaskID || next.GenerationTaskID == batch.GenerationTaskID || len(next.EndedBatches) != 1 {
				t.Fatal("new task reused retired batch")
			}
			if err := tasks.retryDue(ctx, time.Now()); err != nil {
				t.Fatal(err)
			}
			if err := svc.runDue(ctx); err != nil {
				t.Fatal(err)
			}
			if loadMonitor(t, svc, config.ID).Stage != "completed" || generator.calls != 1 {
				t.Fatal("new task could not finish")
			}
		})
	}
}

func TestDirectoryEndRejectsRunningAndStaleRequests(t *testing.T) {
	svc, client, generator, config := newMonitorScheduleTest(t)
	tasks := directorySharedTasks(t, svc, generator, config)
	ctx := context.Background()
	if err := svc.ScanNow(ctx, config.ID); err != nil {
		t.Fatal(err)
	}
	batch := loadMonitor(t, svc, config.ID)
	if _, err := svc.EndBatch(ctx, config.ID, "stale-task"); err == nil {
		t.Fatal("stale request accepted")
	}
	svc.running[config.ID] = true
	if _, err := svc.EndBatch(ctx, config.ID, batch.LogTaskID); err == nil {
		t.Fatal("running cloud mutation released")
	}
	delete(svc.running, config.ID)
	if err := svc.runDue(ctx); err != nil {
		t.Fatal(err)
	}
	batch = loadMonitor(t, svc, config.ID)
	task, err := tasks.Store.GetShareTask(ctx, batch.GenerationTaskID)
	if err != nil {
		t.Fatal(err)
	}
	task.Status = "running"
	if err := tasks.Store.SaveShareTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.EndBatch(ctx, config.ID, batch.LogTaskID); err == nil {
		t.Fatal("running STRM task released")
	}
	task.Status = "waiting_review"
	next := time.Now().Add(-time.Minute)
	task.NextRetryAt = &next
	if err := tasks.Store.SaveShareTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.EndBatch(ctx, config.ID, batch.LogTaskID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.EndBatch(ctx, config.ID, batch.LogTaskID); err != nil {
		t.Fatal("duplicate end must be idempotent", err)
	}
	if err := tasks.retryDue(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := svc.runDue(ctx); err != nil {
		t.Fatal(err)
	}
	if generator.calls != 0 || len(client.deleted) != 0 || len(loadMonitor(t, svc, config.ID).EndedBatches) != 1 {
		t.Fatal("ending a waiting task failed to stop it")
	}
}

func TestDirectoryBlockedBatchCanEndWithoutCredentials(t *testing.T) {
	svc, client, _, config := newMonitorScheduleTest(t)
	ctx := context.Background()
	config.Stage, config.LogTaskID, config.BatchDirectoryID = "blocked", "old-task", "900"
	if err := svc.Store.SaveFolderMonitor(ctx, config); err != nil {
		t.Fatal(err)
	}
	svc.Accounts = nil
	if _, err := svc.EndBatch(ctx, config.ID, config.LogTaskID); err != nil {
		t.Fatal(err)
	}
	if len(client.deleted) != 0 {
		t.Fatal("end must not call cloud API")
	}
	if err := svc.Delete(ctx, config.ID); err != nil {
		t.Fatal("released config cannot be deleted", err)
	}
}

// A share snapshot mismatch is retryable only while the owned batch is intact.
func TestDirectoryActualDeletionDiffersFromShareSnapshotDelay(t *testing.T) {
	svc, client, _, config := newMonitorScheduleTest(t)
	ctx := context.WithValue(context.Background(), generationCookieKey{}, "UID=42_A")
	client.drive["900"] = []p115.ShareFile{{ID: "11", Name: "movie.mkv", Size: 10}}
	manifest := []folderManifestEntry{{ID: "11", Path: "movie.mkv", Size: 10}}
	input := svc.generationInput(config, "https://115cdn.com/s/own?password=abcd")
	if err := svc.VerifyGeneration(ctx, input, manifestFingerprint(manifest), "900"); !errors.Is(err, p115.ErrShareSnapshotPending) {
		t.Fatalf("snapshot delay: %v", err)
	}
	delete(client.drive, "900")
	if err := svc.VerifyGeneration(ctx, input, manifestFingerprint(manifest), "900"); !errors.Is(err, ErrDirectoryBatchChanged) || errors.Is(err, p115.ErrShareSnapshotPending) {
		t.Fatalf("actual deletion: %v", err)
	}
}
