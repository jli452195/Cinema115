package service

import (
	"context"
	"errors"
	"slices"
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

func TestFolderScheduleQueuedCheckSurvivesRestartAndProcessesNextBatch(t *testing.T) {
	svc, client, _, value := newMonitorScheduleTest(t)
	ctx := context.Background()
	if _, err := svc.UpdateSchedule(ctx, value.ID, true, 300); err != nil {
		t.Fatal(err)
	}
	reads := 0
	client.driveHook = func(_ context.Context, cid string, _ int, _ string) error {
		if cid == value.SourceCID {
			reads++
		}
		return nil
	}
	queued := scheduleCheck(t, svc, value.ID)
	if !queued.TaskPending || queued.TaskTrigger != "scheduled" || reads != 0 {
		t.Fatalf("queued=%+v reads=%d", queued, reads)
	}
	svc = NewFolderMonitorService(svc.Store, svc.Accounts, client, svc.Root, nil)
	if err := svc.runDue(ctx); err != nil {
		t.Fatal(err)
	}
	completed := loadMonitor(t, svc, value.ID)
	if completed.Stage != "completed" || !completed.Enabled || reads != 1 || len(completed.RecentRuns) != 1 {
		t.Fatalf("completed=%+v", completed)
	}
	first := completed.RecentRuns[0]
	if first.Trigger != "scheduled" || first.DirectoryID != "900" || first.DirectoryName != client.createdNames[0] || first.ShareURL == "" {
		t.Fatalf("first=%+v", first)
	}
	if !scheduledFolderDue(completed, time.Now()) {
		t.Fatal("completion deferred the next batch")
	}
	client.drive["100"] = []p115.ShareFile{{ID: "12", Name: "next.mkv", Size: 20}}
	client.nextDirectoryID = "901"
	if err := svc.runDue(ctx); err != nil {
		t.Fatal(err)
	}
	next := loadMonitor(t, svc, value.ID)
	if next.Stage != "completed" || next.TaskPending || next.SchedulePaused != "" || reads != 2 || len(next.RecentRuns) != 2 || next.RecentRuns[1] != first {
		t.Fatalf("next=%+v", next)
	}
	if len(client.moveCalls) != 2 || len(client.shareCalls) != 2 || len(client.deleted) != 0 || len(client.drive["900"]) != 1 || len(client.drive["901"]) != 1 {
		t.Fatal("scheduled batches were not shared independently")
	}
}

func TestFolderScheduleUsesSavedOutputRoot(t *testing.T) {
	svc, _, generator, value := newMonitorScheduleTest(t)
	ctx := context.Background()
	tasks := directorySharedTasks(t, svc, generator, value)
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
	settings.Generation.OutputDir = "changed-after-submission"
	if err := svc.runOne(ctx, value.ID); err != nil {
		t.Fatal(err)
	}
	if completed := loadMonitor(t, svc, value.ID); completed.Stage != "completed" || len(completed.RecentRuns) != 1 || completed.RecentRuns[0].GenerationTaskID == "" || generator.calls != 0 {
		t.Fatalf("generation was not queued separately: %+v", completed)
	}
	if err := tasks.retryDue(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	if generator.calls != 1 {
		t.Fatalf("generation calls=%d", generator.calls)
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
				if !scheduledFolderDue(got, time.Now()) || got.TaskPending || got.ScheduleVersion <= value.ScheduleVersion {
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
	scheduleCheck(t, svc, value.ID)
	if err := svc.runOne(ctx, value.ID); err != nil {
		t.Fatal(err)
	}
	empty := loadMonitor(t, svc, value.ID)
	if delay := time.Until(empty.NextScanAt); delay < 590*time.Second || delay > 600*time.Second {
		t.Fatalf("empty directory must use normal interval, got %v", delay)
	}
	if calls != 1 || empty.Stage != "empty" || empty.LogTaskID != "" || empty.TaskPending || len(empty.RecentRuns) != 0 {
		t.Fatalf("calls=%d empty=%+v", calls, empty)
	}
	svc.ReserveWork = func() (func(), error) { return nil, errors.New("organize busy") }
	scheduleCheck(t, svc, value.ID)
	if err := svc.runOne(ctx, value.ID); err != nil {
		t.Fatal(err)
	}
	queued := loadMonitor(t, svc, value.ID)
	if calls != 1 || !queued.TaskPending || queued.TaskTrigger != "scheduled" || queued.ScheduleMessage == "" || len(client.createdNames) != 0 {
		t.Fatalf("busy writer did not retain queued task: calls=%d queued=%+v", calls, queued)
	}
	if delay := folderWakeDelay([]model.FolderMonitor{queued}, time.Now()); delay <= 0 || delay > 5*time.Second {
		t.Fatalf("queued retry delayed %v", delay)
	}
	if monitorDue(queued, time.Now()) || !monitorDue(queued, queued.NextScanAt) {
		t.Fatal("queued retry ignored deadline")
	}
	// 重启后仍执行原已提交任务。
	svc = NewFolderMonitorService(svc.Store, svc.Accounts, client, svc.Root, nil)
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
	svc, client, _, value := newMonitorScheduleTest(t)
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
	if got.Stage != "completed" || !got.NextScanAt.IsZero() || got.Enabled || len(client.moveCalls) != 1 || len(client.shareCalls) != 1 {
		t.Fatalf("off task did not complete: %+v", got)
	}
}

func TestFolderScheduleChecksCurrentScheduleBeforeEnqueue(t *testing.T) {
	for _, mode := range []string{"disabled", "rescheduled"} {
		t.Run(mode, func(t *testing.T) {
			svc, client, _, value := newMonitorScheduleTest(t)
			ctx := context.Background()
			if _, err := svc.SetSchedule(ctx, value.ID, true); err != nil {
				t.Fatal(err)
			}
			due := loadMonitor(t, svc, value.ID)
			due.NextScanAt = time.Now().Add(-time.Second)
			if err := svc.Store.SaveFolderMonitor(ctx, due); err != nil {
				t.Fatal(err)
			}
			enabled := mode == "rescheduled"
			updated, err := svc.UpdateSchedule(ctx, value.ID, enabled, 1800)
			if err != nil {
				t.Fatal(err)
			}
			client.driveHook = func(context.Context, string, int, string) error {
				t.Fatal("schedule inspection read 115")
				return nil
			}
			if err := svc.inspectScheduled(ctx, due.ID); err != nil {
				t.Fatal(err)
			}
			got := loadMonitor(t, svc, value.ID)
			if got.Enabled != enabled || got.TaskPending || !got.NextScanAt.Equal(updated.NextScanAt) || got.SchedulePaused != "" {
				t.Fatalf("current schedule not respected: %+v", got)
			}
		})
	}
}

func TestFolderScheduleWorkerUsesCurrentContents(t *testing.T) {
	svc, client, _, value := newMonitorScheduleTest(t)
	ctx := context.Background()
	if _, err := svc.SetSchedule(ctx, value.ID, true); err != nil {
		t.Fatal(err)
	}
	scheduleCheck(t, svc, value.ID)
	client.drive["100"] = append(client.drive["100"], p115.ShareFile{ID: "12", Name: "later.mkv", Size: 20})
	if err := svc.runOne(ctx, value.ID); err != nil {
		t.Fatal(err)
	}
	got := loadMonitor(t, svc, value.ID)
	if got.TaskPending || got.Stage != "completed" || len(client.moveCalls) != 1 || !slices.Equal(client.moveCalls[0], []string{"11", "12"}) {
		t.Fatalf("current contents not picked up: moved=%v monitor=%+v", client.moveCalls, got)
	}
}

func TestFolderScheduleFailurePausesAndRetainsFiles(t *testing.T) {
	svc, client, _, value := newMonitorScheduleTest(t)
	ctx := context.Background()
	if _, err := svc.SetSchedule(ctx, value.ID, true); err != nil {
		t.Fatal(err)
	}
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
	scheduleCheck(t, svc, value.ID)
	if err := svc.runOne(context.Background(), value.ID); err == nil {
		t.Fatal("expected read failure")
	}
	got := loadMonitor(t, svc, value.ID)
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
	svc, client, _, value := newMonitorScheduleTest(t)
	ctx := context.Background()
	if _, err := svc.SetSchedule(ctx, value.ID, true); err != nil {
		t.Fatal(err)
	}
	scheduleCheck(t, svc, value.ID)
	if _, err := svc.SetSchedule(ctx, value.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := svc.runOne(ctx, value.ID); err != nil {
		t.Fatal(err)
	}
	got := loadMonitor(t, svc, value.ID)
	if got.Stage != "completed" || got.Enabled || got.TaskPending || !got.NextScanAt.IsZero() || len(client.shareCalls) != 1 || len(client.deleted) != 0 {
		t.Fatalf("disabled queued=%+v", got)
	}
}

func TestFolderScheduleSameAccountRefreshKeepsQueuedBatch(t *testing.T) {
	svc, client, _, value := newMonitorScheduleTest(t)
	ctx := context.Background()
	if _, err := svc.SetSchedule(ctx, value.ID, true); err != nil {
		t.Fatal(err)
	}
	scheduleCheck(t, svc, value.ID)
	account, err := svc.Accounts.Primary(value.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	cookie := "UID=42_A; CID=changed"
	if err := svc.Accounts.Update(account.ID, account.Revision, account.Name, &cookie); err != nil {
		t.Fatal(err)
	}
	client.driveHook = func(_ context.Context, _ string, _ int, got string) error {
		if got != cookie {
			t.Fatal("queued execution did not take current credentials")
		}
		return nil
	}
	if err := svc.runOne(ctx, value.ID); err != nil {
		t.Fatal(err)
	}
	got := loadMonitor(t, svc, value.ID)
	if got.Stage != "completed" || got.SchedulePaused != "" || len(client.createdNames) != 1 || len(client.shareCalls) != 1 {
		t.Fatalf("same-account refresh stopped queued batch: %+v", got)
	}
}

func TestFolderRunningBatchKeepsCookieAndChecksOriginalAccount(t *testing.T) {
	for _, action := range []string{"refresh", "replace", "clear"} {
		t.Run(action, func(t *testing.T) {
			svc, client, _, value := newMonitorScheduleTest(t)
			account, err := svc.Accounts.Primary(value.AccountID)
			if err != nil {
				t.Fatal(err)
			}
			client.onMove = func() {
				cookie := "UID=42_B; SEID=refreshed"
				if action == "replace" {
					cookie = "UID=99_A; SEID=other-account"
				} else if action == "clear" {
					cookie = ""
				}
				if err := svc.Accounts.Update(account.ID, account.Revision, "renamed", &cookie); err != nil {
					t.Fatal(err)
				}
			}
			client.driveHook = func(_ context.Context, _ string, _ int, got string) error {
				if got != account.Cookie {
					t.Fatal("running batch switched credentials")
				}
				return nil
			}
			ctx := context.Background()
			if err := svc.ScanNow(ctx, value.ID); err != nil {
				t.Fatal(err)
			}
			if err := svc.runDue(ctx); err != nil {
				t.Fatal(err)
			}
			got := loadMonitor(t, svc, value.ID)
			if action == "refresh" {
				if got.Stage != "completed" || len(client.shareCalls) != 1 || len(client.deleted) != 0 {
					t.Fatal("same-account refresh stopped running batch", got.Stage, got.LastError)
				}
			} else if got.LastError == "" || len(client.deleted) != 0 || len(client.shareCalls) != 0 {
				t.Fatal("unavailable original account permitted another cloud write", got.Stage)
			}
		})
	}
}

func TestFolderMovingBatchCanResumeAfterSigningBackIntoSameAccount(t *testing.T) {
	svc, client, _, value := newMonitorScheduleTest(t)
	account, err := svc.Accounts.Primary(value.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	cleared := false
	client.driveHook = func(_ context.Context, cid string, _ int, _ string) error {
		if cid == "900" && !cleared {
			cleared = true
			cookie := ""
			if err := svc.Accounts.Update(account.ID, account.Revision, account.Name, &cookie); err != nil {
				t.Fatal(err)
			}
		}
		return nil
	}
	ctx := context.Background()
	if err := svc.ScanNow(ctx, value.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.runDue(ctx); err != nil {
		t.Fatal(err)
	}
	paused := loadMonitor(t, svc, value.ID)
	if paused.Stage != "moving" || paused.LastError == "" || len(client.moveCalls) != 0 || len(client.createdNames) != 1 {
		t.Fatal("sign-out lost the resumable moving stage", paused.Stage, paused.LastError)
	}
	account, err = svc.Accounts.Get(account.ID)
	if err != nil {
		t.Fatal(err)
	}
	cookie := "UID=42_B; SEID=refreshed"
	if err := svc.Accounts.Update(account.ID, account.Revision, account.Name, &cookie); err != nil {
		t.Fatal(err)
	}
	client.driveHook = nil
	if err := svc.ScanNow(ctx, value.ID); err != nil {
		t.Fatal("same-account login could not resume original batch", err)
	}
	if err := svc.runDue(ctx); err != nil {
		t.Fatal(err)
	}
	completed := loadMonitor(t, svc, value.ID)
	if completed.Stage != "completed" || len(client.createdNames) != 1 || len(client.moveCalls) != 1 || len(client.shareCalls) != 1 || len(client.deleted) != 0 {
		t.Fatal("resume repeated or skipped original work", completed.Stage)
	}
}
