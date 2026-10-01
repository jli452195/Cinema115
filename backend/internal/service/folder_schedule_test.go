package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
)

func scheduleCheck(t *testing.T, svc *FolderMonitorService, id string) model.FolderMonitor {
	t.Helper()
	value := loadMonitor(t, svc, id)
	value.NextScanAt = time.Now().Add(-time.Second)
	if err := svc.Store.SaveFolderMonitor(context.Background(), value); err != nil {
		t.Fatal(err)
	}
	if err := svc.inspectScheduled(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	return loadMonitor(t, svc, id)
}

func TestFolderScheduleStableRestartAndNextBatch(t *testing.T) {
	svc, client, generator, value := newMonitorScheduleTest(t)
	ctx := context.Background()
	if _, err := svc.UpdateSchedule(ctx, value.ID, true, 300); err != nil {
		t.Fatal(err)
	}
	first := scheduleCheck(t, svc, value.ID)
	if delay := time.Until(first.NextScanAt); delay <= 0 || delay > 5*time.Second {
		t.Fatalf("stability delay=%v", delay)
	}
	if delay := folderWakeDelay([]model.FolderMonitor{first}, time.Now()); delay <= 0 || delay > 5*time.Second {
		t.Fatalf("wake delay=%v", delay)
	}
	if first.TaskPending || first.StableScans != 1 || len(client.moveCalls) != 0 {
		t.Fatalf("first=%+v", first)
	}
	svc = NewFolderMonitorService(svc.Store, svc.Accounts, client, generator, svc.Root, nil)
	second := scheduleCheck(t, svc, value.ID)
	if !second.TaskPending || second.TaskTrigger != "scheduled" {
		t.Fatalf("second=%+v", second)
	}
	if err := svc.runDue(ctx); err != nil {
		t.Fatal(err)
	}
	completed := loadMonitor(t, svc, value.ID)
	if completed.Stage != "completed" || !completed.Enabled || completed.NextScanAt.IsZero() || len(completed.RecentRuns) != 1 || completed.RecentRuns[0].Trigger != "scheduled" {
		t.Fatalf("completed=%+v", completed)
	}
	if !scheduledFolderDue(completed, time.Now()) {
		t.Fatal("completion deferred the next batch")
	}
	client.drive["100"] = []p115.ShareFile{{ID: "12", Name: "next.mkv", Size: 20}}
	if err := svc.runDue(ctx); err != nil {
		t.Fatal(err)
	}
	if generator.calls != 1 {
		t.Fatal("duplicate generation")
	}
	next := loadMonitor(t, svc, value.ID)
	if next.StableScans != 1 || next.TaskPending || next.SchedulePaused != "" {
		t.Fatalf("next=%+v", next)
	}
}

func TestFolderScheduleUsesSavedOutputRoot(t *testing.T) {
	svc, _, generator, value := newMonitorScheduleTest(t)
	ctx := context.Background()
	settings := AutomationSettings{AccountID: value.AccountID, ParentCID: "0", Generation: svc.generationInput(value, "")}
	settings.Generation.OutputDir = "."
	svc.SetSharedSettings(func() AutomationSettings { return settings })
	generator.hook = func(_ context.Context, input GenerateInput) error {
		if input.OutputDir != "." {
			t.Fatalf("root selection lost: %q", input.OutputDir)
		}
		return nil
	}
	if _, err := svc.SetSchedule(ctx, value.ID, true); err != nil {
		t.Fatal(err)
	}
	scheduleCheck(t, svc, value.ID)
	scheduleCheck(t, svc, value.ID)
	settings.Generation.OutputDir = "changed-after-submission"
	if err := svc.runOne(ctx, value.ID); err != nil {
		t.Fatal(err)
	}
	if generator.calls != 1 || loadMonitor(t, svc, value.ID).Stage != "completed" {
		t.Fatal("root task did not complete")
	}
}

func TestFolderScheduleOrganizeNotificationHonorsSwitchAndScope(t *testing.T) {
	for _, mode := range []string{"ready", "off", "paused", "queued", "other-account"} {
		t.Run(mode, func(t *testing.T) {
			svc, _, _, value := newMonitorScheduleTest(t)
			ctx := context.Background()
			if _, err := svc.SetSchedule(ctx, value.ID, true); err != nil {
				t.Fatal(err)
			}
			value = loadMonitor(t, svc, value.ID)
			value.StableFingerprint, value.StableScans = "old-observation", 1
			accountID := value.AccountID
			switch mode {
			case "off":
				value.Enabled = false
			case "paused":
				value.SchedulePaused = "needs review"
			case "queued":
				value.TaskPending = true
			case "other-account":
				accountID = "another-account"
			}
			if err := svc.Store.SaveFolderMonitor(ctx, value); err != nil {
				t.Fatal(err)
			}
			if err := svc.NotifyOrganized(ctx, accountID); err != nil {
				t.Fatal(err)
			}
			got := loadMonitor(t, svc, value.ID)
			if mode == "ready" {
				if !scheduledFolderDue(got, time.Now()) || got.StableScans != 0 || got.TaskPending || got.ScheduleVersion <= value.ScheduleVersion {
					t.Fatalf("not expedited: %+v", got)
				}
			} else if !got.NextScanAt.Equal(value.NextScanAt) || got.ScheduleVersion != value.ScheduleVersion || got.Enabled != value.Enabled {
				t.Fatalf("unexpected notification change: %+v", got)
			}
		})
	}
}

func TestFolderScheduleEmptyAndBusyDoNotCreateJobs(t *testing.T) {
	svc, client, _, value := newMonitorScheduleTest(t)
	ctx := context.Background()
	if _, err := svc.SetSchedule(ctx, value.ID, true); err != nil {
		t.Fatal(err)
	}
	calls := 0
	client.driveHook = func(context.Context, string, int, string) error { calls++; return nil }
	client.drive["100"] = nil
	empty := scheduleCheck(t, svc, value.ID)
	if delay := time.Until(empty.NextScanAt); delay < 590*time.Second || delay > 600*time.Second {
		t.Fatalf("empty directory must use normal interval, got %v", delay)
	}
	if calls != 1 || empty.LogTaskID != "" || empty.TaskPending || len(empty.RecentRuns) != 0 {
		t.Fatalf("calls=%d empty=%+v", calls, empty)
	}
	svc.ReserveWork = func() (func(), error) { return nil, errors.New("organize busy") }
	waiting := scheduleCheck(t, svc, value.ID)
	if delay := time.Until(waiting.NextScanAt); delay <= 0 || delay > 5*time.Second {
		t.Fatalf("busy check delayed %v", delay)
	}
	if calls != 1 || waiting.NextScanAt.IsZero() || waiting.ScheduleMessage == "" {
		t.Fatalf("calls=%d waiting=%+v", calls, waiting)
	}
	if err := svc.ScanNow(ctx, value.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.runOne(ctx, value.ID); err != nil {
		t.Fatal(err)
	}
	if !loadMonitor(t, svc, value.ID).TaskPending || len(client.createdNames) != 0 {
		t.Fatal("busy writer did not retain queued task")
	}
	queued := loadMonitor(t, svc, value.ID)
	if delay := folderWakeDelay([]model.FolderMonitor{queued}, time.Now()); delay <= 0 || delay > 5*time.Second {
		t.Fatalf("queued retry delayed %v", delay)
	}
	if monitorDue(queued, time.Now()) || !monitorDue(queued, queued.NextScanAt) {
		t.Fatal("queued retry ignored deadline")
	}
	// A restart preserves the short retry deadline and the submitted task.
	svc = NewFolderMonitorService(svc.Store, svc.Accounts, client, &folderMonitorGeneratorFake{}, svc.Root, nil)
	queued.NextScanAt = time.Now().Add(-time.Second)
	if err := svc.Store.SaveFolderMonitor(ctx, queued); err != nil {
		t.Fatal(err)
	}
	if err := svc.runDue(ctx); err != nil {
		t.Fatal(err)
	}
	if got := loadMonitor(t, svc, value.ID); got.TaskPending || got.Stage != "empty" {
		t.Fatalf("queued retry did not resume: %+v", got)
	}
}

func TestFolderScheduleBusyQueuedTaskContinuesWithScheduleOff(t *testing.T) {
	svc, client, generator, value := newMonitorScheduleTest(t)
	ctx := context.Background()
	if err := svc.ScanNow(ctx, value.ID); err != nil {
		t.Fatal(err)
	}
	svc.ReserveWork = func() (func(), error) { return nil, errors.New("busy") }
	if err := svc.runOne(ctx, value.ID); err != nil {
		t.Fatal(err)
	}
	queued := loadMonitor(t, svc, value.ID)
	if queued.Enabled || !queued.TaskPending || queued.NextScanAt.IsZero() {
		t.Fatalf("queue lost retry deadline: %+v", queued)
	}
	queued.NextScanAt = time.Now().Add(-time.Second)
	if err := svc.Store.SaveFolderMonitor(ctx, queued); err != nil {
		t.Fatal(err)
	}
	svc.ReserveWork = nil
	if err := svc.runDue(ctx); err != nil {
		t.Fatal(err)
	}
	got := loadMonitor(t, svc, value.ID)
	if got.Stage != "completed" || !got.NextScanAt.IsZero() || got.Enabled || generator.calls != 1 || len(client.moveCalls) != 1 {
		t.Fatalf("off task did not complete: %+v", got)
	}
}

func TestFolderScheduleDisableDuringInspection(t *testing.T) {
	for _, second := range []bool{false, true} {
		t.Run(map[bool]string{false: "first", true: "second"}[second], func(t *testing.T) {
			svc, client, _, value := newMonitorScheduleTest(t)
			ctx := context.Background()
			if _, err := svc.SetSchedule(ctx, value.ID, true); err != nil {
				t.Fatal(err)
			}
			if second {
				scheduleCheck(t, svc, value.ID)
			}
			client.driveHook = func(context.Context, string, int, string) error {
				client.driveHook = nil
				_, err := svc.SetSchedule(ctx, value.ID, false)
				return err
			}
			got := scheduleCheck(t, svc, value.ID)
			if got.Enabled || got.TaskPending || !got.NextScanAt.IsZero() || got.SchedulePaused != "" {
				t.Fatalf("disabled=%+v", got)
			}
			if len(client.moveCalls) != 0 {
				t.Fatal("moved files after disabling inspection")
			}
		})
	}
}

func TestFolderScheduleChangesBeforeWorkerNeverMove(t *testing.T) {
	svc, client, _, value := newMonitorScheduleTest(t)
	ctx := context.Background()
	if _, err := svc.SetSchedule(ctx, value.ID, true); err != nil {
		t.Fatal(err)
	}
	scheduleCheck(t, svc, value.ID)
	scheduleCheck(t, svc, value.ID)
	client.drive["100"] = append(client.drive["100"], p115.ShareFile{ID: "12", Name: "later.mkv", Size: 20})
	if err := svc.runOne(ctx, value.ID); err != nil {
		t.Fatal(err)
	}
	got := loadMonitor(t, svc, value.ID)
	if got.TaskPending || got.StableScans != 1 || got.NextScanAt.IsZero() || len(client.createdNames) != 0 {
		t.Fatalf("changed=%+v", got)
	}
}

func TestFolderScheduleFailurePausesAndRetainsFiles(t *testing.T) {
	svc, client, _, value := newMonitorScheduleTest(t)
	ctx := context.Background()
	if _, err := svc.SetSchedule(ctx, value.ID, true); err != nil {
		t.Fatal(err)
	}
	scheduleCheck(t, svc, value.ID)
	scheduleCheck(t, svc, value.ID)
	client.configureErr = errors.New("share failure")
	if err := svc.runOne(ctx, value.ID); err == nil {
		t.Fatal("expected failure")
	}
	got := loadMonitor(t, svc, value.ID)
	if got.SchedulePaused == "" || !got.NextScanAt.IsZero() || got.BatchDirectoryID == "" || len(client.deleted) != 0 || len(got.RecentRuns) != 1 {
		t.Fatalf("failed=%+v", got)
	}
	if _, err := svc.SetSchedule(ctx, value.ID, true); err == nil {
		t.Fatal("enabled with retained batch")
	}
	calls := len(client.shareCalls)
	if err := svc.runDue(ctx); err != nil {
		t.Fatal(err)
	}
	if len(client.shareCalls) != calls {
		t.Fatal("failed task retried itself")
	}
}

func TestFolderScheduleReadFailurePersistsPause(t *testing.T) {
	svc, client, _, value := newMonitorScheduleTest(t)
	if _, err := svc.SetSchedule(context.Background(), value.ID, true); err != nil {
		t.Fatal(err)
	}
	client.driveHook = func(context.Context, string, int, string) error { return errors.New("directory missing") }
	got := scheduleCheck(t, svc, value.ID)
	if got.SchedulePaused == "" || !got.NextScanAt.IsZero() || got.TaskPending {
		t.Fatalf("missing=%+v", got)
	}
	if err := svc.runDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(client.createdNames) != 0 {
		t.Fatal("created after read failure")
	}
}

func TestFolderScheduleDisablePreservesSubmittedBatch(t *testing.T) {
	svc, client, generator, value := newMonitorScheduleTest(t)
	ctx := context.Background()
	if _, err := svc.SetSchedule(ctx, value.ID, true); err != nil {
		t.Fatal(err)
	}
	scheduleCheck(t, svc, value.ID)
	scheduleCheck(t, svc, value.ID)
	if _, err := svc.SetSchedule(ctx, value.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := svc.runOne(ctx, value.ID); err != nil {
		t.Fatal(err)
	}
	got := loadMonitor(t, svc, value.ID)
	if got.Stage != "completed" || got.Enabled || got.TaskPending || !got.NextScanAt.IsZero() || generator.calls != 1 || len(client.deleted) != 1 {
		t.Fatalf("disabled queued=%+v calls=%d", got, generator.calls)
	}
}

func TestFolderScheduleCredentialChangeAfterSubmitBlocks(t *testing.T) {
	svc, client, _, value := newMonitorScheduleTest(t)
	ctx := context.Background()
	if _, err := svc.SetSchedule(ctx, value.ID, true); err != nil {
		t.Fatal(err)
	}
	scheduleCheck(t, svc, value.ID)
	scheduleCheck(t, svc, value.ID)
	account, err := svc.Accounts.Primary(value.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	cookie := "UID=42_A; CID=changed"
	if err := svc.Accounts.Update(account.ID, account.Revision, account.Name, &cookie); err != nil {
		t.Fatal(err)
	}
	if err := svc.runOne(ctx, value.ID); err == nil {
		t.Fatal("accepted changed credentials")
	}
	got := loadMonitor(t, svc, value.ID)
	if got.Stage != "blocked" || got.SchedulePaused == "" || len(client.createdNames) != 0 {
		t.Fatalf("credential change=%+v", got)
	}
}
