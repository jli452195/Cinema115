package bot

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"strm115/backend/internal/service"
)

func TestModuleAssignmentEnablesAndUnassigningDisables(t *testing.T) {
	dir := t.TempDir()
	hub, err := NewHub(dir, "test-secret", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	first, err := hub.Add("Bot A")
	if err != nil {
		t.Fatal(err)
	}
	second, err := hub.Add("Bot B")
	if err != nil {
		t.Fatal(err)
	}
	settings, err := service.NewAutomationStore(filepath.Join(dir, "automation.json"), dir, "http://nas:8115")
	if err != nil {
		t.Fatal(err)
	}
	value := settings.Value()
	value.Generation.OutputDir = "strm"
	if err := settings.Save(value); err != nil {
		t.Fatal(err)
	}
	settings.ValidateBot = func(string, bool) error { return nil }
	hub.settings = settings
	for _, id := range []string{first, second} {
		profile, err := hub.Profiles().Get(id)
		if err != nil {
			t.Fatal(err)
		}
		profile.Token, profile.NotificationChatID, profile.Enabled = "123456:token", "42", true
		if err := hub.Profiles().Update(profile, profile.Revision); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-hub.wake:
	default:
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for _, id := range []string{first, second} {
		workers.Add(1)
		go func(botID string) {
			defer workers.Done()
			<-start
			results <- hub.SetModule(context.Background(), "share_notifications", "", botID)
		}(id)
	}
	close(start)
	workers.Wait()
	close(results)
	wins := 0
	for err := range results {
		if err == nil {
			wins++
		} else if !errors.Is(err, ErrModuleChanged) {
			t.Fatalf("unexpected concurrent assignment error: %v", err)
		}
	}
	if wins != 1 {
		t.Fatalf("expected one winner, got %d", wins)
	}
	select {
	case <-hub.wake:
	default:
		t.Fatal("module assignment did not wake the Bot runtime")
	}

	modules, err := hub.Modules(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	owner := ""
	for _, module := range modules {
		if module.ID == "share_notifications" {
			owner = module.AssignedBotID
		}
	}
	if !settings.Value().TelegramEnabled {
		t.Fatal("assigning share notifications did not enable message handling")
	}
	if owner != first && owner != second {
		t.Fatalf("cloud module has invalid owner %q", owner)
	}

	restarted, err := NewHub(dir, "test-secret", nil, settings)
	if err != nil {
		t.Fatal(err)
	}
	modules, err = restarted.Modules(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, module := range modules {
		if module.ID == "share_notifications" && module.AssignedBotID != owner {
			t.Fatalf("cloud module assignment did not persist: got %q want %q", module.AssignedBotID, owner)
		}
	}
	if err := restarted.SetModule(context.Background(), "share_notifications", owner, ""); err != nil {
		t.Fatal(err)
	}
	modules, err = restarted.Modules(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, module := range modules {
		if module.ID == "share_notifications" && module.AssignedBotID != "" {
			t.Fatalf("unassigning a module did not disable it: %+v", module)
		}
	}
	if settings.Value().TelegramEnabled {
		t.Fatal("unassigning share notifications did not disable message handling")
	}
}

func newCloudTargetSettings(t *testing.T, path string) *service.CloudDownloadSettings {
	t.Helper()
	store, err := service.NewSessionStore(path, "test-cloud-secret")
	if err != nil {
		t.Fatal(err)
	}
	settings, err := service.NewCloudDownloadSettings(store)
	if err != nil {
		t.Fatal(err)
	}
	settings.PrimaryID = func() string { return "account" }
	settings.ValidateAccount = func(id string) error {
		if id != "account" {
			return service.ErrAccountMissing
		}
		return nil
	}
	return settings
}

func addCloudTarget(t *testing.T, settings *service.CloudDownloadSettings, name, path string) service.CloudDownloadTarget {
	t.Helper()
	target, err := settings.SaveTarget(service.CloudDownloadTarget{Name: name, AccountID: "account", TargetCID: "88", TargetPath: path})
	if err != nil {
		t.Fatal(err)
	}
	return target
}

func TestUnconfiguredDisabledBotCanHoldCloudTarget(t *testing.T) {
	dir := t.TempDir()
	hub, err := NewHub(dir, "test-secret", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	id, err := hub.Add("Placeholder Bot")
	if err != nil {
		t.Fatal(err)
	}
	hub.CloudDownloadSettings = newCloudTargetSettings(t, filepath.Join(dir, "cloud-download.enc"))
	target := addCloudTarget(t, hub.CloudDownloadSettings, "转存 XX", "/XX")
	if err := hub.SetModule(context.Background(), cloudDownloadModuleID(target.ID), "", id); err != nil {
		t.Fatalf("cloud target assignment should not require an enabled Bot: %v", err)
	}
	profile, err := hub.Profiles().Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if profile.Enabled || profile.Token != "" {
		t.Fatalf("placeholder assignment rewrote Bot credentials: %+v", profile)
	}
	modules, err := hub.Modules(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(modules) != 4 || modules[3].ID != cloudDownloadModuleID(target.ID) || modules[3].AssignedBotID != id {
		t.Fatalf("cloud target was not exposed as its own module: %+v", modules)
	}
}

func TestCloudTargetCanBeAssignedAndTransferredWithoutCopyingPath(t *testing.T) {
	dir := t.TempDir()
	hub, err := NewHub(dir, "test-secret", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	hub.CloudDownloadSettings = newCloudTargetSettings(t, filepath.Join(dir, "cloud-download.enc"))
	target := addCloudTarget(t, hub.CloudDownloadSettings, "转存 XX", "/XX")
	firstID, _ := hub.Add("Original Bot")
	secondID, _ := hub.Add("Target Bot")
	moduleID := cloudDownloadModuleID(target.ID)
	if err := hub.SetModule(context.Background(), moduleID, "", firstID); err != nil {
		t.Fatal(err)
	}
	if err := hub.SetModule(context.Background(), moduleID, firstID, secondID); err != nil {
		t.Fatalf("direct Bot-to-Bot transfer failed: %v", err)
	}
	current, ok := hub.CloudDownloadSettings.Target(target.ID)
	if !ok || current.AssignedBotID != secondID || current.TargetPath != "/XX" {
		t.Fatalf("target assignment/path was not preserved: %+v", current)
	}
	if err := hub.SetModule(context.Background(), moduleID, firstID, ""); !errors.Is(err, ErrModuleChanged) {
		t.Fatalf("stale owner was allowed to release transferred target: %v", err)
	}
	if err := hub.SetModule(context.Background(), moduleID, secondID, secondID); err != nil {
		t.Fatal(err)
	}
	reopened := newCloudTargetSettings(t, filepath.Join(dir, "cloud-download.enc"))
	stored, ok := reopened.Target(target.ID)
	if !ok || stored.AssignedBotID != secondID || stored.TargetPath != "/XX" {
		t.Fatalf("target assignment did not persist across restart: %+v", stored)
	}
}

func TestCloudTargetTransferConflictKeepsSingleOwner(t *testing.T) {
	dir := t.TempDir()
	hub, err := NewHub(dir, "test-secret", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	hub.CloudDownloadSettings = newCloudTargetSettings(t, filepath.Join(dir, "cloud-download.enc"))
	target := addCloudTarget(t, hub.CloudDownloadSettings, "转存 XX", "/XX")
	ids := make([]string, 3)
	for i, name := range []string{"Owner", "Target A", "Target B"} {
		ids[i], err = hub.Add(name)
		if err != nil {
			t.Fatal(err)
		}
	}
	moduleID := cloudDownloadModuleID(target.ID)
	if err := hub.SetModule(context.Background(), moduleID, "", ids[0]); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for _, targetID := range ids[1:] {
		workers.Add(1)
		go func(id string) {
			defer workers.Done()
			<-start
			results <- hub.SetModule(context.Background(), moduleID, ids[0], id)
		}(targetID)
	}
	close(start)
	workers.Wait()
	close(results)
	wins := 0
	for err := range results {
		if err == nil {
			wins++
			continue
		}
		if !errors.Is(err, ErrModuleChanged) {
			t.Fatalf("unexpected transfer conflict: %v", err)
		}
	}
	if wins != 1 {
		t.Fatalf("expected one transfer winner, got %d", wins)
	}
	stored, _ := hub.CloudDownloadSettings.Target(target.ID)
	if stored.AssignedBotID == "" {
		t.Fatal("concurrent transfer left target unassigned")
	}
}

func TestCloudTargetCommitFailureKeepsOriginalOwner(t *testing.T) {
	dir := t.TempDir()
	hub, err := NewHub(dir, "test-secret", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(dir, "cloud-download.enc")
	hub.CloudDownloadSettings = newCloudTargetSettings(t, settingsPath)
	target := addCloudTarget(t, hub.CloudDownloadSettings, "转存 XX", "/XX")
	firstID, _ := hub.Add("Owner")
	secondID, _ := hub.Add("Target")
	moduleID := cloudDownloadModuleID(target.ID)
	if err := hub.SetModule(context.Background(), moduleID, "", firstID); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(settingsPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(settingsPath, 0700); err != nil {
		t.Fatal(err)
	}
	if err := hub.SetModule(context.Background(), moduleID, firstID, secondID); err == nil {
		t.Fatal("expected config commit failure")
	}
	stored, _ := hub.CloudDownloadSettings.Target(target.ID)
	if stored.AssignedBotID != firstID {
		t.Fatalf("failed commit changed target owner: %+v", stored)
	}
}

func TestPoolNotificationsAreDisabledByUnassigning(t *testing.T) {
	hub, err := NewHub(t.TempDir(), "test-secret", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	modules, err := hub.Modules(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, module := range modules {
		if module.ID == "pool_notifications" && module.AssignedBotID != "" {
			t.Fatalf("pool notifications should remain unassigned: %+v", module)
		}
	}
}

func TestCloudOnlyBotStartsMessagePollingWithoutShareProcessing(t *testing.T) {
	if !pollsMessages(BotProfile{ID: "cloud-bot"}, "", true) {
		t.Fatal("cloud-only Bot must start getUpdates")
	}
	if pollsMessages(BotProfile{ID: "notifications-only"}, "", false) {
		t.Fatal("notification-only Bot must not start a getUpdates consumer")
	}
	if !pollsMessages(BotProfile{ID: "share-bot"}, "share-bot", false) {
		t.Fatal("share Bot must start getUpdates")
	}
}
