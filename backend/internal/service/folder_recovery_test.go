package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
)

type recoveryClient struct {
	*folderMonitorClientFake
	missing      bool
	missingErr   error
	lostCreate   bool
	beforeCreate func()
	beforeRead   func(string)
}

type creationCheckpointFailure struct {
	FolderMonitorStore
	failed bool
}

func (s *creationCheckpointFailure) SaveFolderMonitor(ctx context.Context, value model.FolderMonitor) error {
	if value.Stage == "moving" && !s.failed {
		s.failed = true
		return errors.New("simulated database failure after remote creation")
	}
	return s.FolderMonitorStore.SaveFolderMonitor(ctx, value)
}

func TestFolderCreationRecoversAfterCheckpointFailureAndRestart(t *testing.T) {
	svc, fake, generator, value := newMonitorScheduleTest(t)
	ctx := context.Background()
	store := &creationCheckpointFailure{FolderMonitorStore: svc.Store}
	svc.Store = store
	if err := svc.ScanNow(ctx, value.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.runDue(ctx); err != nil {
		t.Fatal(err)
	}
	pending := loadMonitor(t, svc, value.ID)
	if pending.Stage != "creating" || !pending.BatchCreationPending || len(fake.createdNames) != 1 || len(fake.moveCalls) != 0 {
		t.Fatal("lost creation boundary")
	}
	restarted := NewFolderMonitorService(store.FolderMonitorStore, svc.Accounts, fake, generator, svc.Root, nil)
	if err := restarted.runDue(ctx); err != nil {
		t.Fatal(err)
	}
	if loadMonitor(t, restarted, value.ID).Stage != "ended" || len(fake.createdNames) != 1 || len(fake.deleted) != 1 {
		t.Fatal("replayed creation or lost empty directory")
	}
}

func TestFolderRecoveryClaimRejectsConcurrentMutations(t *testing.T) {
	svc, client, value := failedRecoveryBatch(t)
	entered, release := make(chan struct{}), make(chan struct{})
	first := true
	client.beforeRead = func(string) {
		if first {
			first = false
			close(entered)
			<-release
		}
	}
	done := make(chan error, 1)
	go func() { _, err := svc.Recover(context.Background(), value.ID, value.LogTaskID); done <- err }()
	<-entered
	_, recoverErr := svc.Recover(context.Background(), value.ID, value.LogTaskID)
	_, endErr := svc.EndBatch(context.Background(), value.ID, value.LogTaskID)
	triggerErr := svc.ScanNow(context.Background(), value.ID)
	close(release)
	err := <-done
	if err != nil || recoverErr == nil || endErr == nil || !errors.Is(triggerErr, ErrFolderMonitorBusy) {
		t.Fatal("claim failed", err, recoverErr, endErr, triggerErr)
	}
	if len(client.deleted) != 1 {
		t.Fatal("duplicate cleanup")
	}
}

func (c *recoveryClient) DirectoryMissing(context.Context, string, string) (bool, error) {
	return c.missing, c.missingErr
}
func (c *recoveryClient) CreateDirectory(ctx context.Context, parent, name, cookie string) (string, error) {
	if c.beforeCreate != nil {
		c.beforeCreate()
	}
	id, err := c.folderMonitorClientFake.CreateDirectory(ctx, parent, name, cookie)
	if c.lostCreate {
		return "", errors.New("response lost")
	}
	return id, err
}
func (c *recoveryClient) DriveFiles(ctx context.Context, id string, offset int, cookie string) (*p115.SharePage, error) {
	if c.beforeRead != nil {
		c.beforeRead(id)
	}
	return c.folderMonitorClientFake.DriveFiles(ctx, id, offset, cookie)
}

func failedRecoveryBatch(t *testing.T) (*FolderMonitorService, *recoveryClient, model.FolderMonitor) {
	t.Helper()
	svc, fake, _, value := newMonitorScheduleTest(t)
	value.BatchDirectoryID, value.BatchDirectoryName, value.BatchParentCID = "900", "Cinema115-临时-owned", "0"
	value.Stage, value.LastError, value.LogTaskID = "blocked", "failed", "test-batch"
	fake.drive["0"] = []p115.ShareFile{{ID: "900", Name: value.BatchDirectoryName, Directory: true}}
	fake.drive["900"] = nil
	if err := svc.Store.SaveFolderMonitor(context.Background(), value); err != nil {
		t.Fatal(err)
	}
	client := &recoveryClient{folderMonitorClientFake: fake}
	svc.Client = client
	return svc, client, value
}

func TestFolderRecoveryOnlyRecyclesEmptyOwnedDirectory(t *testing.T) {
	svc, client, value := failedRecoveryBatch(t)
	client.drive["0"] = append(client.drive["0"], p115.ShareFile{ID: "901", Name: "Cinema115-临时-unrelated", Directory: true})
	client.drive["901"] = nil
	result, err := svc.Recover(context.Background(), value.ID, value.LogTaskID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Stage != "ended" || result.BatchDirectoryID != "" || len(result.EndedBatches) != 1 || !result.EndedBatches[0].Cleaned || result.RecoveryMessage == "" {
		t.Fatalf("bad recovery: %+v", result)
	}
	if len(client.deleted) != 1 || client.deleted[0] != "900" || len(client.drive["100"]) != 1 {
		t.Fatal("deleted unowned/source data")
	}
	if _, ok := client.drive["901"]; !ok {
		t.Fatal("swept by prefix")
	}
	if _, err := svc.Recover(context.Background(), value.ID, value.LogTaskID); err != nil || len(client.deleted) != 1 {
		t.Fatal("recovery was not idempotent", err)
	}
	if err := svc.Delete(context.Background(), value.ID); err != nil {
		t.Fatal("claim not released", err)
	}
}

func TestFolderRecoveryPreservesNonemptyMovedAndUnknownDirectory(t *testing.T) {
	for _, scenario := range []string{"nonempty", "moved", "unknown", "account-cleared", "account-replaced", "stale", "running", "new-arrival"} {
		t.Run(scenario, func(t *testing.T) {
			svc, client, value := failedRecoveryBatch(t)
			taskID := value.LogTaskID
			switch scenario {
			case "nonempty":
				client.drive["900"] = []p115.ShareFile{{ID: "15", Name: "movie.mkv", Size: 10}}
			case "moved":
				client.drive["0"] = nil
			case "unknown":
				client.drive["0"] = nil
				client.missingErr = errors.New("timeout")
			case "account-cleared", "account-replaced":
				account, err := svc.Accounts.Primary(value.AccountID)
				if err != nil {
					t.Fatal(err)
				}
				cookie := ""
				if scenario == "account-replaced" {
					cookie = "UID=99_A; SEID=replacement"
				}
				if err := svc.Accounts.Update(account.ID, account.Revision, account.Name, &cookie); err != nil {
					t.Fatal(err)
				}
			case "stale":
				taskID = "old"
			case "running":
				svc.running[value.ID] = true
			case "new-arrival":
				reads := 0
				client.beforeRead = func(id string) {
					if id == "900" {
						reads++
						if reads == 2 {
							client.drive[id] = []p115.ShareFile{{ID: "16", Name: "new.mkv", Size: 10}}
						}
					}
				}
			}
			if _, err := svc.Recover(context.Background(), value.ID, taskID); err == nil {
				t.Fatal("unsafe recovery accepted")
			}
			if len(client.deleted) != 0 || loadMonitor(t, svc, value.ID).Stage == "ended" {
				t.Fatal("unsafe cleanup")
			}
		})
	}
}

func TestFolderRecoveryConfirmedMissingReleasesWithoutSuccess(t *testing.T) {
	svc, client, value := failedRecoveryBatch(t)
	client.drive["0"] = nil
	delete(client.drive, "900")
	client.missing = true
	result, err := svc.Recover(context.Background(), value.ID, value.LogTaskID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Stage != "ended" || len(client.deleted) != 0 || result.EndedBatches[0].Cleaned || !strings.Contains(result.RecoveryMessage, "不存在") {
		t.Fatalf("bad missing outcome: %+v", result)
	}
}

func TestFolderRecoveryAfterSameAccountLoginRefresh(t *testing.T) {
	svc, client, value := failedRecoveryBatch(t)
	account, err := svc.Accounts.Primary(value.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	cookie := "UID=42_B; SEID=refreshed"
	if err := svc.Accounts.Update(account.ID, account.Revision, "renamed", &cookie); err != nil {
		t.Fatal(err)
	}
	client.driveHook = func(_ context.Context, _ string, _ int, got string) error {
		if got != cookie {
			t.Fatal("recovery did not use refreshed credentials")
		}
		return nil
	}
	recovered, err := svc.Recover(context.Background(), value.ID, value.LogTaskID)
	if err != nil || recovered.Stage != "ended" || len(client.deleted) != 1 {
		t.Fatal("same-account refresh prevented recovery", recovered.Stage, err)
	}
}

func TestFolderCreationIntentSurvivesLostResponse(t *testing.T) {
	svc, fake, _, value := newMonitorScheduleTest(t)
	client := &recoveryClient{folderMonitorClientFake: fake, lostCreate: true}
	svc.Client = client
	client.beforeCreate = func() {
		v := loadMonitor(t, svc, value.ID)
		if v.Stage != "creating" || !v.BatchCreationPending || v.BatchDirectoryName == "" || v.AccountID != value.AccountID {
			t.Fatal("intent not durable before creation")
		}
	}
	if err := svc.ScanNow(context.Background(), value.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.runDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	result := loadMonitor(t, svc, value.ID)
	if result.Stage != "ended" || len(client.createdNames) != 1 || len(client.deleted) != 1 || len(client.moveCalls) != 0 {
		t.Fatalf("lost response not recovered: %+v", result)
	}
	if err := svc.runDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(client.createdNames) != 1 {
		t.Fatal("created a second directory")
	}
}

func TestFolderCreationIntentDoesNotGuessAbsentOrAmbiguousDirectory(t *testing.T) {
	for _, duplicate := range []bool{false, true} {
		t.Run(map[bool]string{false: "absent", true: "ambiguous"}[duplicate], func(t *testing.T) {
			svc, client, value := failedRecoveryBatch(t)
			value.BatchDirectoryID = ""
			value.BatchCreationPending = true
			value.Stage = "creating"
			value.LastError = ""
			value.TaskPending = true
			client.drive["0"] = nil
			if duplicate {
				client.drive["0"] = []p115.ShareFile{{ID: "901", Name: value.BatchDirectoryName, Directory: true}, {ID: "902", Name: value.BatchDirectoryName, Directory: true}}
			}
			if err := svc.Store.SaveFolderMonitor(context.Background(), value); err != nil {
				t.Fatal(err)
			}
			if err := svc.runDue(context.Background()); err != nil {
				t.Fatal(err)
			}
			result := loadMonitor(t, svc, value.ID)
			if result.Stage != "blocked" || !result.BatchCreationPending || len(client.deleted) != 0 || len(client.createdNames) != 0 {
				t.Fatalf("guessed uncertain creation: %+v", result)
			}
			if err := svc.ScanNow(context.Background(), value.ID); err == nil {
				t.Fatal("restarted uncertain creation")
			}
		})
	}
}

func TestFolderCleaningRestartRevalidatesManifestAndLocation(t *testing.T) {
	for _, scenario := range []string{"unchanged", "added", "moved", "unknown"} {
		t.Run(scenario, func(t *testing.T) {
			svc, client, value := failedRecoveryBatch(t)
			client.drive["900"] = []p115.ShareFile{{ID: "11", Name: "movie.mkv", Size: 10}}
			manifest, _, err := svc.scanDrive(context.Background(), "900", nil, "cookie")
			if err != nil {
				t.Fatal(err)
			}
			value.BatchFingerprint = manifestFingerprint(manifest)
			value.Stage = "cleaning"
			value.LastError = ""
			value.ShareURL = "https://115.com/s/own?password=abcd"
			switch scenario {
			case "added":
				client.drive["900"] = append(client.drive["900"], p115.ShareFile{ID: "12", Name: "new.mkv", Size: 20})
			case "moved":
				client.drive["0"] = nil
			case "unknown":
				client.drive["0"] = nil
				client.missingErr = errors.New("timeout")
			}
			if err := svc.Store.SaveFolderMonitor(context.Background(), value); err != nil {
				t.Fatal(err)
			}
			if err := svc.runDue(context.Background()); err != nil {
				t.Fatal(err)
			}
			result := loadMonitor(t, svc, value.ID)
			if scenario == "unchanged" {
				if result.Stage != "completed" || len(client.deleted) != 1 {
					t.Fatalf("valid cleanup failed: %+v", result)
				}
			} else if result.Stage == "completed" || len(client.deleted) != 0 {
				t.Fatalf("unsafe cleanup: %+v", result)
			}
		})
	}
}

func TestFolderRecoveryRetiresLinkedTaskBeforeCleanup(t *testing.T) {
	svc, fake, generator, value := newMonitorScheduleTest(t)
	tasks := directorySharedTasks(t, svc, generator, value)
	ctx := context.Background()
	if err := svc.ScanNow(ctx, value.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.runDue(ctx); err != nil {
		t.Fatal(err)
	}
	value = loadMonitor(t, svc, value.ID)
	value.LastError = "failed"
	value.TaskPending = false
	if err := svc.Store.SaveFolderMonitor(ctx, value); err != nil {
		t.Fatal(err)
	}
	fake.drive[value.BatchDirectoryID] = nil
	task, err := tasks.Store.GetShareTask(ctx, value.GenerationTaskID)
	if err != nil {
		t.Fatal(err)
	}
	task.Status = "running"
	if err := tasks.Store.SaveShareTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Recover(ctx, value.ID, value.LogTaskID); err == nil {
		t.Fatal("cleaned while generation running")
	}
	if len(fake.deleted) != 0 {
		t.Fatal("deleted active batch")
	}
	task.Status = "failed"
	if err := tasks.Store.SaveShareTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Recover(ctx, value.ID, value.LogTaskID); err != nil {
		t.Fatal(err)
	}
	results, err := tasks.RetryTasks(ctx, []string{task.ID})
	if err != nil || results[0].Accepted {
		t.Fatal("retired task could retry", err)
	}
}
