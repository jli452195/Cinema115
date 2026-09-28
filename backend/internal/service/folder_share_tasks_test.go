package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
)

func directorySharedTasks(t *testing.T, svc *FolderMonitorService, generator MediaGenerator, value model.FolderMonitor) *ShareTasks {
	t.Helper()
	settings := &AutomationStore{value: AutomationSettings{AccountID: value.AccountID, ParentCID: "0", Generation: svc.generationInput(value, ""), ReviewRetryMinutes: 1, ReviewRetryMax: 2}}
	tasks := &ShareTasks{Generator: generator, Store: svc.Store.(*storage.Store), Settings: settings, AccountCookie: svc.Accounts.CookieFor, RetryWake: make(chan struct{}, 1)}
	svc.Tasks = tasks
	tasks.DirectoryPrepare = svc.VerifyGeneration
	svc.SetSharedSettings(settings.Value)
	return tasks
}

func TestDirectoryShareUsesDurableCommonTaskAndPreservesMetadataFailure(t *testing.T) {
	svc, client, _, value := newMonitorScheduleTest(t)
	ctx := context.Background()
	failMetadata := true
	calls := 0
	tasks := directorySharedTasks(t, svc, generateFunc(func(ctx context.Context, input GenerateInput) (*GenerationResult, error) {
		calls++
		if input.Mode != "direct" || input.OutputDir != "strm" || generationCookie(ctx, nil) != "UID=42_A" {
			t.Fatal("generation settings were not pinned")
		}
		result := &GenerationResult{Written: 1, Files: []GeneratedSTRM{{Path: "movie.strm"}}}
		if failMetadata {
			result.MetadataFailed = 1
		}
		return result, nil
	}), value)
	first, accepted, err := svc.Trigger(ctx, value.ID)
	if err != nil || !accepted || first.LogTaskID == "" {
		t.Fatalf("submit: %v %v", accepted, err)
	}
	duplicate, accepted, err := svc.Trigger(ctx, value.ID)
	if err != nil || accepted || duplicate.LogTaskID != first.LogTaskID {
		t.Fatal("duplicate created a new task")
	}
	// A queued task must keep the options from the click, not later settings.
	tasks.Settings.value.Generation.OutputDir = "changed"
	if err := svc.runDue(ctx); err != nil {
		t.Fatal(err)
	}
	value = loadMonitor(t, svc, value.ID)
	if value.Stage != "generating" || value.GenerationTaskID == "" || len(client.deleted) != 0 {
		t.Fatal("did not hand off generation safely")
	}
	queued, err := tasks.Store.GetShareTask(ctx, value.GenerationTaskID)
	if err != nil || queued.Status != "queued" || queued.Source != "目录分享" {
		t.Fatalf("common task: %+v %v", queued, err)
	}
	if strings.Contains(string(queued.Request), "UID=") || strings.Contains(string(value.GenerationSettings), "UID=") {
		t.Fatal("persisted cookie")
	}
	// Rebuild the directory service to exercise recovery, retaining the same task.
	svc = NewFolderMonitorService(svc.Store, svc.Accounts, client, svc.Generator, svc.Root, nil)
	svc.Tasks = tasks
	if err := svc.runDue(ctx); err != nil {
		t.Fatal(err)
	}
	if err := tasks.retryDue(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := svc.runDue(ctx); err != nil {
		t.Fatal(err)
	}
	value = loadMonitor(t, svc, value.ID)
	if value.LastError == "" || len(client.deleted) != 0 {
		t.Fatal("metadata failure allowed cleanup")
	}
	if count, err := svc.Store.(*storage.Store).ClearShareTasks(ctx, nil); err != nil || count != 0 {
		t.Fatal("deleted linked task before batch completion", err)
	}
	failMetadata = false
	if err := svc.ScanNow(ctx, value.ID); err != nil {
		t.Fatal(err)
	}
	if err := tasks.retryDue(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := svc.runDue(ctx); err != nil {
		t.Fatal(err)
	}
	value = loadMonitor(t, svc, value.ID)
	if value.Stage != "completed" || value.GenerationTaskID != queued.ID || len(client.shareCalls) != 1 || len(client.deleted) != 1 || calls != 2 {
		t.Fatalf("recovery duplicated work: %+v calls=%d", value, calls)
	}
	if count, err := svc.Store.(*storage.Store).ClearShareTasks(ctx, nil); err != nil || count != 1 {
		t.Fatal("completed task was not clearable", err)
	}
}

func TestDirectorySharePinsBindingAndRejectsCredentialChanges(t *testing.T) {
	svc, client, generator, value := newMonitorScheduleTest(t)
	ctx := context.Background()
	tasks := directorySharedTasks(t, svc, generator, value)
	settings := tasks.Settings.value
	settings.AccountID = "another-account"
	tasks.Settings.value = settings
	if err := svc.ScanNow(ctx, value.ID); err == nil {
		t.Fatal("accepted directory under another account")
	}
	if _, err := svc.SaveUsingSharedSettings(ctx, value); err == nil {
		t.Fatal("saved old directory with new account")
	}
	tasks.Settings.value.AccountID = value.AccountID
	if err := svc.ScanNow(ctx, value.ID); err != nil {
		t.Fatal(err)
	}
	account, err := svc.Accounts.Get(value.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	cookie := "UID=42_A; SEID=refreshed"
	if err := svc.Accounts.Update(account.ID, account.Revision, account.Name, &cookie); err != nil {
		t.Fatal(err)
	}
	if err := svc.runDue(ctx); err != nil {
		t.Fatal(err)
	}
	if len(client.createdNames) != 0 || generator.calls != 0 || loadMonitor(t, svc, value.ID).Stage != "blocked" {
		t.Fatal("queued task changed credentials")
	}
}

func TestDirectorySharePreflightRejectsNoMediaBeforeMoving(t *testing.T) {
	for _, kind := range []string{"extension", "size", "recursive"} {
		t.Run(kind, func(t *testing.T) {
			svc, client, _, value := newMonitorScheduleTest(t)
			switch kind {
			case "extension":
				client.drive["100"][0].Name = "notes.txt"
			case "size":
				value.MinSizeMB = 1
			case "recursive":
				value.Recursive = false
				client.drive["100"] = []p115.ShareFile{{ID: "22", Name: "season", Directory: true}}
				client.drive["22"] = []p115.ShareFile{{ID: "11", Name: "movie.mkv", Size: 10}}
			}
			if err := svc.Store.SaveFolderMonitor(context.Background(), value); err != nil {
				t.Fatal(err)
			}
			if err := svc.ScanNow(context.Background(), value.ID); err != nil {
				t.Fatal(err)
			}
			if err := svc.runDue(context.Background()); err != nil {
				t.Fatal(err)
			}
			if len(client.createdNames) != 0 || len(client.moveCalls) != 0 || len(client.shareCalls) != 0 {
				t.Fatal("empty media selection mutated drive")
			}
		})
	}
}

func TestDirectoryShareFixedRangeLeavesArrivalDuringMove(t *testing.T) {
	svc, client, _, value := newMonitorScheduleTest(t)
	client.onMove = func() {
		client.drive["100"] = append(client.drive["100"], p115.ShareFile{ID: "12", Name: "later.mkv", Size: 20})
	}
	if err := svc.ScanNow(context.Background(), value.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.runDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(client.moveCalls) != 1 || len(client.shareCalls) != 1 || !sameIDs(client.shareCalls[0], []string{"11"}) || len(client.drive["100"]) != 1 || client.drive["100"][0].ID != "12" {
		t.Fatal("new arrival joined active batch")
	}
}

func TestDirectoryShareInterruptedCommonGenerationResumes(t *testing.T) {
	svc, client, generator, value := newMonitorScheduleTest(t)
	tasks := directorySharedTasks(t, svc, generator, value)
	ctx := context.Background()
	if err := svc.ScanNow(ctx, value.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.runDue(ctx); err != nil {
		t.Fatal(err)
	}
	value = loadMonitor(t, svc, value.ID)
	task, err := tasks.Store.GetShareTask(ctx, value.GenerationTaskID)
	if err != nil {
		t.Fatal(err)
	}
	task.Status = "interrupted"
	if err := tasks.Store.SaveShareTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	if err := svc.runDue(ctx); err != nil {
		t.Fatal(err)
	}
	if err := tasks.retryDue(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := svc.runDue(ctx); err != nil {
		t.Fatal(err)
	}
	if generator.calls != 1 || len(client.shareCalls) != 1 || loadMonitor(t, svc, value.ID).Stage != "completed" {
		t.Fatal("interrupted direct task did not resume safely")
	}
}

func TestDirectoryShareReviewUsesCommonRetry(t *testing.T) {
	svc, client, _, value := newMonitorScheduleTest(t)
	underReview := true
	tasks := directorySharedTasks(t, svc, generateFunc(func(context.Context, GenerateInput) (*GenerationResult, error) {
		if underReview {
			return nil, p115.ErrShareUnderReview
		}
		return &GenerationResult{Written: 1, Files: []GeneratedSTRM{{Path: "movie.strm"}}}, nil
	}), value)
	tasks.Settings.value.ReviewRetryEnabled = true
	ctx := context.Background()
	if err := svc.ScanNow(ctx, value.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.runDue(ctx); err != nil {
		t.Fatal(err)
	}
	value = loadMonitor(t, svc, value.ID)
	if err := tasks.retryDue(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	task, err := tasks.Store.GetShareTask(ctx, value.GenerationTaskID)
	if err != nil || task.Status != "waiting_review" {
		t.Fatalf("review: %+v %v", task, err)
	}
	if err := svc.runDue(ctx); err != nil {
		t.Fatal(err)
	}
	if len(client.deleted) != 0 {
		t.Fatal("review task cleaned source")
	}
	underReview = false
	if err := tasks.retryDue(ctx, time.Now().Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := svc.runDue(ctx); err != nil {
		t.Fatal(err)
	}
	if loadMonitor(t, svc, value.ID).Stage != "completed" || len(client.shareCalls) != 1 {
		t.Fatal("review retry did not complete existing batch")
	}
	var req shareTaskRequest
	if err := json.Unmarshal(task.Request, &req); err != nil {
		t.Fatal(err)
	}
	if req.Input.Mode != "direct" {
		t.Fatal("directory task must be direct generation")
	}
}

func TestDirectoryShareSnapshotPreparationRetriesWithoutAnotherShare(t *testing.T) {
	svc, client, generator, value := newMonitorScheduleTest(t)
	tasks := directorySharedTasks(t, svc, generator, value)
	tasks.Settings.value.ReviewRetryEnabled = true
	svc.snapshotTimeout, svc.snapshotInterval = 5*time.Millisecond, time.Millisecond
	client.listHook = func(context.Context, string, int, string) error { return p115.ErrShareSnapshotPending }
	ctx := context.Background()
	if err := svc.ScanNow(ctx, value.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.runDue(ctx); err != nil {
		t.Fatal(err)
	}
	value = loadMonitor(t, svc, value.ID)
	if err := tasks.retryDue(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	task, err := tasks.Store.GetShareTask(ctx, value.GenerationTaskID)
	if err != nil || task.Status != "waiting_review" || generator.calls != 0 {
		t.Fatalf("snapshot wait: %+v %v", task, err)
	}
	client.listHook = nil
	if err := tasks.retryDue(ctx, time.Now().Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := svc.runDue(ctx); err != nil {
		t.Fatal(err)
	}
	if len(client.shareCalls) != 1 || len(client.moveCalls) != 1 || len(client.deleted) != 1 || generator.calls != 1 {
		t.Fatal("snapshot recovery duplicated cloud writes")
	}
}

func TestDirectoryShareCommonLogsKeepTaskAndRedaction(t *testing.T) {
	svc, _, generator, value := newMonitorScheduleTest(t)
	logs := &GenerationLogs{}
	svc.Logs = logs
	generator.hook = func(ctx context.Context, _ GenerateInput) error {
		logGeneration(ctx, "DEBUG", "raw UID=42_A")
		return nil
	}
	logged := &LoggedGenerator{Generator: generator, Logs: logs, Root: svc.Root}
	tasks := directorySharedTasks(t, svc, logged, value)
	tasks.Logs = logs
	ctx := context.Background()
	if err := svc.ScanNow(ctx, value.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.runDue(ctx); err != nil {
		t.Fatal(err)
	}
	if err := tasks.retryDue(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	value = loadMonitor(t, svc, value.ID)
	found := false
	for _, entry := range logs.Snapshot("ALL") {
		if entry.TaskID != value.LogTaskID || strings.Contains(entry.Message, "42_A") {
			t.Fatalf("incorrect log attribution or redaction: %+v", entry)
		}
		if entry.Source == "目录分享" {
			found = true
		}
	}
	if !found {
		t.Fatal("shared generation logs missing")
	}
}
