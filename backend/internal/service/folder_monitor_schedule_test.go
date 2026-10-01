package service

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
)

func newMonitorScheduleTest(t *testing.T) (*FolderMonitorService, *folderMonitorClientFake, *folderMonitorGeneratorFake, model.FolderMonitor) {
	t.Helper()
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "monitor.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	secret, err := NewSessionStore(filepath.Join(t.TempDir(), "accounts.enc"), "secret")
	if err != nil {
		t.Fatal(err)
	}
	accounts, err := newP115Accounts(secret, "")
	if err != nil {
		t.Fatal(err)
	}
	id, err := accounts.AddPrimary("monitor")
	if err != nil {
		t.Fatal(err)
	}
	account, err := accounts.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	cookie := "UID=42_A"
	if err := accounts.Update(id, account.Revision, account.Name, &cookie); err != nil {
		t.Fatal(err)
	}
	client := &folderMonitorClientFake{drive: map[string][]p115.ShareFile{"100": {{ID: "11", Name: "movie.mkv", Size: 10}}}, share: map[string][]p115.ShareFile{}}
	generator := &folderMonitorGeneratorFake{}
	svc := NewFolderMonitorService(db, accounts, client, generator, t.TempDir(), nil)
	value, err := svc.Save(ctx, model.FolderMonitor{Name: "monitor", AccountID: id, SourceCID: "100", OutputDir: "strm", BaseURL: "http://nas:3002", VideoExtensions: "mkv", MetadataExtensions: "srt", Recursive: true, IntervalSeconds: 30})
	if err != nil {
		t.Fatal(err)
	}
	return svc, client, generator, value
}

func loadMonitor(t *testing.T, svc *FolderMonitorService, id string) model.FolderMonitor {
	t.Helper()
	value, err := svc.Store.GetFolderMonitor(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestFolderMonitorDisabledManualTaskSurvivesRestart(t *testing.T) {
	svc, client, generator, value := newMonitorScheduleTest(t)
	ctx := context.Background()
	value.NextScanAt = time.Now().Add(-time.Hour)
	if err := svc.Store.SaveFolderMonitor(ctx, value); err != nil {
		t.Fatal(err)
	}
	if err := svc.runDue(ctx); err != nil {
		t.Fatal(err)
	}
	if len(client.createdNames) != 0 {
		t.Fatal("disabled schedule started a task")
	}
	if err := svc.ScanNow(ctx, value.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.ScanNow(ctx, value.ID); !errors.Is(err, ErrFolderMonitorBusy) {
		t.Fatalf("duplicate request: %v", err)
	}
	queued := loadMonitor(t, svc, value.ID)
	if queued.Enabled || !queued.TaskPending || queued.TaskTrigger != "manual" {
		t.Fatalf("queue=%+v", queued)
	}
	// Recreate the service: the one-shot request is durable, not a memory flag.
	svc = NewFolderMonitorService(svc.Store, svc.Accounts, client, generator, svc.Root, nil)
	client.addOnShare = p115.ShareFile{ID: "12", Name: "later.mkv", Size: 20}
	if err := svc.runDue(ctx); err != nil {
		t.Fatal(err)
	}
	value = loadMonitor(t, svc, value.ID)
	if value.Enabled || value.TaskPending || value.Stage != "completed" || generator.calls != 1 {
		t.Fatalf("completed=%+v calls=%d", value, generator.calls)
	}
	if len(client.deleted) != 1 || client.deleted[0] != "900" {
		t.Fatalf("cleanup=%v", client.deleted)
	}
	if len(client.drive["100"]) != 1 || client.drive["100"][0].ID != "12" {
		t.Fatal("new arrival was not retained")
	}
	if err := svc.runDue(ctx); err != nil {
		t.Fatal(err)
	}
	if generator.calls != 1 {
		t.Fatal("one-shot task ran twice")
	}
}

func TestFolderMonitorManualTaskRejectsEditsAndDuplicateTriggers(t *testing.T) {
	svc, _, generator, value := newMonitorScheduleTest(t)
	ctx := context.Background()
	if _, err := svc.SetSchedule(ctx, value.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := svc.ScanNow(ctx, value.ID); err != nil {
		t.Fatal(err)
	}
	generator.hook = func(ctx context.Context, _ GenerateInput) error {
		values, err := svc.List(ctx)
		if err != nil {
			return err
		}
		if !values[0].Running {
			t.Error("running state not exposed")
		}
		if err := svc.ScanNow(ctx, value.ID); !errors.Is(err, ErrFolderMonitorBusy) {
			t.Errorf("running duplicate=%v", err)
		}
		if _, err := svc.Save(ctx, value); err == nil {
			t.Error("edited a running task")
		}
		if err := svc.Delete(ctx, value.ID); err == nil {
			t.Error("deleted a running task")
		}
		_, err = svc.SetSchedule(ctx, value.ID, false)
		return err
	}
	if err := svc.runOne(ctx, value.ID); err != nil {
		t.Fatal(err)
	}
	value = loadMonitor(t, svc, value.ID)
	if value.Enabled || value.Stage != "completed" || value.TaskPending {
		t.Fatalf("after toggle=%+v", value)
	}
}

func TestFolderMonitorRetryRetainsBatchAndPinnedSettings(t *testing.T) {
	svc, client, generator, value := newMonitorScheduleTest(t)
	ctx := context.Background()
	shared := AutomationSettings{AccountID: value.AccountID, ParentCID: "0", FixedSharePassword: "abcd", Generation: svc.generationInput(value, "")}
	svc.SetSharedSettings(func() AutomationSettings { return shared })
	client.configureErr = errors.New("temporary configure failure")
	client.onShare = func() {
		shared.FixedSharePassword = "efgh"
		shared.Generation.OutputDir = "changed"
	}
	if err := svc.ScanNow(ctx, value.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.runOne(ctx, value.ID); err == nil {
		t.Fatal("expected configure failure")
	}
	failed := loadMonitor(t, svc, value.ID)
	if failed.Stage != "configuring" || failed.BatchDirectoryID != "900" || failed.TaskPending {
		t.Fatalf("failed=%+v", failed)
	}
	if failed.SharePassword != "abcd" {
		t.Fatal("password was changed during batch")
	}
	if err := svc.runDue(ctx); err != nil {
		t.Fatal(err)
	}
	if len(client.shareCalls) != 1 {
		t.Fatal("disabled timer retried automatically")
	}
	client.configureErr = nil
	generator.hook = func(_ context.Context, input GenerateInput) error {
		if input.OutputDir != "strm" {
			t.Errorf("generation settings changed: %s", input.OutputDir)
		}
		return nil
	}
	if err := svc.ScanNow(ctx, value.ID); err != nil {
		t.Fatal(err)
	}
	if got := loadMonitor(t, svc, value.ID); got.Stage != "configuring" {
		t.Fatal("retry reset the batch stage")
	}
	if err := svc.runOne(ctx, value.ID); err != nil {
		t.Fatal(err)
	}
	value = loadMonitor(t, svc, value.ID)
	if value.Stage != "completed" || value.Enabled || len(client.createdNames) != 1 || len(client.moveCalls) != 1 || len(client.shareCalls) != 1 {
		t.Fatalf("retry=%+v create=%d move=%d share=%d", value, len(client.createdNames), len(client.moveCalls), len(client.shareCalls))
	}
}

func TestFolderMonitorInterruptedBatchResumesWithScheduleOff(t *testing.T) {
	svc, client, generator, value := newMonitorScheduleTest(t)
	ctx := context.Background()
	value.Stage, value.BatchDirectoryID, value.BatchParentCID, value.TaskTrigger = "moving", "900", "0", "manual"
	if err := svc.Store.SaveFolderMonitor(ctx, value); err != nil {
		t.Fatal(err)
	}
	client.drive["900"] = nil
	client.drive["0"] = []p115.ShareFile{{ID: "900", Name: "Cinema115-临时-test", Directory: true}}
	if err := svc.runDue(ctx); err != nil {
		t.Fatal(err)
	}
	value = loadMonitor(t, svc, value.ID)
	if value.Stage != "completed" || value.Enabled || generator.calls != 1 || len(client.createdNames) != 0 {
		t.Fatalf("resume=%+v", value)
	}
}

func TestFolderMonitorInvalidScheduleNeverScans(t *testing.T) {
	svc, client, _, value := newMonitorScheduleTest(t)
	ctx := context.Background()
	value.Enabled, value.IntervalSeconds, value.NextScanAt = true, 30, time.Now().Add(-time.Hour)
	if err := svc.Store.SaveFolderMonitor(ctx, value); err != nil {
		t.Fatal(err)
	}
	if err := svc.runDue(ctx); err != nil {
		t.Fatal(err)
	}
	if len(client.createdNames) != 0 {
		t.Fatal("legacy timer started a new batch")
	}
	if _, err := svc.SetSchedule(ctx, value.ID, false); err != nil {
		t.Fatal(err)
	}
	value = loadMonitor(t, svc, value.ID)
	if value.Enabled || value.IntervalSeconds != 0 || !value.NextScanAt.IsZero() {
		t.Fatal("legacy timer was not cleared")
	}
	if _, err := svc.SetSchedule(ctx, value.ID, true); err != nil {
		t.Fatal(err)
	}
	client.drive["100"] = nil
	if err := svc.ScanNow(ctx, value.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.runDue(ctx); err != nil {
		t.Fatal(err)
	}
	if value = loadMonitor(t, svc, value.ID); value.Stage != "empty" || value.TaskPending {
		t.Fatalf("empty directory: %+v", value)
	}
}

func TestFolderMonitorConcurrentManualRequestsCoalesce(t *testing.T) {
	svc, _, _, value := newMonitorScheduleTest(t)
	var wg sync.WaitGroup
	results := make(chan error, 12)
	for i := 0; i < cap(results); i++ {
		wg.Add(1)
		go func() { defer wg.Done(); results <- svc.ScanNow(context.Background(), value.ID) }()
	}
	wg.Wait()
	close(results)
	accepted := 0
	for err := range results {
		if err == nil {
			accepted++
		} else if !errors.Is(err, ErrFolderMonitorBusy) {
			t.Fatal(err)
		}
	}
	if accepted != 1 {
		t.Fatalf("accepted=%d", accepted)
	}
}

func TestFolderMonitorBlockedAndLegacyBatchCannotRestart(t *testing.T) {
	for _, stage := range []string{"blocked", "sharing_legacy", "error"} {
		t.Run(stage, func(t *testing.T) {
			svc, client, _, value := newMonitorScheduleTest(t)
			value.Stage, value.BatchDirectoryID, value.LastError = stage, "900", "needs review"
			if err := svc.Store.SaveFolderMonitor(context.Background(), value); err != nil {
				t.Fatal(err)
			}
			if err := svc.ScanNow(context.Background(), value.ID); err == nil {
				t.Fatal("accepted unsafe retry")
			}
			if err := svc.runDue(context.Background()); err != nil {
				t.Fatal(err)
			}
			if len(client.createdNames) != 0 {
				t.Fatal("created another batch")
			}
		})
	}
}
