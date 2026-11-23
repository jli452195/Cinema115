package service

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestDirectoryUnreadableTemporaryDirectoryDoesNotBlockGeneration(t *testing.T) {
	svc, client, generator, config := newMonitorScheduleTest(t)
	tasks := directorySharedTasks(t, svc, generator, config)
	ctx := context.Background()
	if err := svc.ScanNow(ctx, config.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.runDue(ctx); err != nil {
		t.Fatal(err)
	}
	batch := loadMonitor(t, svc, config.ID)
	client.driveHook = func(context.Context, string, int, string) error {
		return errors.New("115 返回了其他目录，请重新选择目录")
	}
	generator.hook = func(_ context.Context, input GenerateInput) error {
		if input.URL != batch.ShareURL || input.Mode != "direct" || input.CID != "0" {
			t.Fatalf("generation did not reuse the saved link: %+v", input)
		}
		return nil
	}
	if err := tasks.retryDue(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	task, err := tasks.Store.GetShareTask(ctx, batch.GenerationTaskID)
	if err != nil || task.Status != "success" || generator.calls != 1 || len(client.deleted) != 0 {
		t.Fatalf("temporary directory blocked direct generation: %+v %v", task, err)
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
	// 模拟旧版本尚未释放的批次，保留结束按钮的并发保护。
	batch.Stage, batch.BatchDirectoryID = "generating", client.nextDirectoryID
	if err := svc.Store.SaveFolderMonitor(ctx, batch); err != nil {
		t.Fatal(err)
	}
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

func TestLegacyConfiguredBatchReleasesWithSavedFailedGeneration(t *testing.T) {
	svc, client, generator, config := newMonitorScheduleTest(t)
	tasks := directorySharedTasks(t, svc, generator, config)
	ctx := context.Background()
	config.Stage, config.LogTaskID, config.GenerationTaskID = "generating", "legacy-share", "legacy-generation"
	config.LastError, config.ShareURL = "115 返回了其他目录，请重新选择目录", "https://115cdn.com/s/own?password=abcd"
	config.BatchDirectoryID, config.BatchDirectoryName = "900", "Cinema115-临时-旧批次"
	if err := svc.Store.SaveFolderMonitor(ctx, config); err != nil {
		t.Fatal(err)
	}
	taskCtx := context.WithValue(ctx, directoryGenerationKey{}, config.LogTaskID)
	task, _, err := tasks.prepareTaskRecord(taskCtx, svc.generationInput(config, config.ShareURL), config.GenerationTaskID, "failed")
	if err != nil {
		t.Fatal(err)
	}
	task.Error = config.LastError
	if err := tasks.Store.SaveShareTask(ctx, *task); err != nil {
		t.Fatal(err)
	}
	svc.Accounts = nil
	if err := svc.runDue(ctx); err != nil {
		t.Fatal(err)
	}
	saved := loadMonitor(t, svc, config.ID)
	if saved.Stage != "completed" || saved.BatchDirectoryID != "" || saved.ShareURL != config.ShareURL || saved.GenerationTaskID != task.ID {
		t.Fatalf("configured share was not released: %+v", saved)
	}
	previous, err := tasks.Store.GetShareTask(ctx, task.ID)
	if err != nil || previous.Error != task.Error || previous.Status != "failed" || len(client.shareCalls) != 0 || len(client.deleted) != 0 {
		t.Fatalf("legacy generation was replaced: %+v %v", previous, err)
	}
}
