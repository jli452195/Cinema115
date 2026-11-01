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

func TestUnconfiguredDisabledBotCanHoldModules(t *testing.T) {
	dir := t.TempDir()
	hub, err := NewHub(dir, "test-secret", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	id, err := hub.Add("Placeholder Bot")
	if err != nil {
		t.Fatal(err)
	}
	settings, err := service.NewAutomationStore(filepath.Join(dir, "automation.json"), dir, "http://nas:8115")
	if err != nil {
		t.Fatal(err)
	}
	settings.ValidateBot = func(string, bool) error { return errors.New("runtime config is incomplete") }
	hub.settings = settings

	if err := hub.SetModule(context.Background(), "share_notifications", "", id); err != nil {
		t.Fatalf("share notification placeholder should not require a ready Bot: %v", err)
	}
	if err := hub.SetModule(context.Background(), "cloud_download", "", id); err != nil {
		t.Fatalf("cloud-download placeholder should not require a ready service or directory: %v", err)
	}

	profile, err := hub.Profiles().Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if profile.Enabled || profile.Token != "" || !profile.CloudDownload.Enabled {
		t.Fatalf("placeholder assignment unexpectedly enabled or rewrote Bot credentials: %+v", profile)
	}
	profile.Name = "Placeholder Bot Edited"
	if err := hub.Update(context.Background(), profile, profile.Revision); err != nil {
		t.Fatalf("editing an incomplete assigned Bot should remain possible: %v", err)
	}

	modules, err := hub.Modules(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, module := range modules {
		if (module.ID == "share_notifications" || module.ID == "cloud_download") && module.AssignedBotID != id {
			t.Fatalf("module %q placeholder was not preserved: %+v", module.ID, module)
		}
	}
}

func TestCloudDownloadTransfersAtomicallyBetweenBots(t *testing.T) {
	dir := t.TempDir()
	hub, err := NewHub(dir, "test-secret", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	firstID, err := hub.Add("Original Bot")
	if err != nil {
		t.Fatal(err)
	}
	secondID, err := hub.Add("Target Bot")
	if err != nil {
		t.Fatal(err)
	}
	first, _ := hub.Profiles().Get(firstID)
	first.CloudDownload = BotCloudDownloadConfig{AccountID: "source-account", TargetCID: "101", TargetPath: "/source"}
	if err := hub.Profiles().Update(first, first.Revision); err != nil {
		t.Fatal(err)
	}
	second, _ := hub.Profiles().Get(secondID)
	second.CloudDownload = BotCloudDownloadConfig{AccountID: "target-account", TargetCID: "202", TargetPath: "/target"}
	if err := hub.Profiles().Update(second, second.Revision); err != nil {
		t.Fatal(err)
	}

	if err := hub.SetModule(context.Background(), "cloud_download", "", firstID); err != nil {
		t.Fatal(err)
	}
	if err := hub.SetModule(context.Background(), "cloud_download", firstID, secondID); err != nil {
		t.Fatalf("direct Bot-to-Bot transfer failed: %v", err)
	}
	first, _ = hub.Profiles().Get(firstID)
	second, _ = hub.Profiles().Get(secondID)
	if first.CloudDownload.Enabled || first.CloudDownload.AccountID != "source-account" || first.CloudDownload.TargetCID != "101" || first.CloudDownload.TargetPath != "/source" {
		t.Fatalf("source Bot assignment/configuration was not preserved correctly: %+v", first.CloudDownload)
	}
	if !second.CloudDownload.Enabled || second.CloudDownload.AccountID != "target-account" || second.CloudDownload.TargetCID != "202" || second.CloudDownload.TargetPath != "/target" {
		t.Fatalf("target Bot did not keep its own download config: %+v", second.CloudDownload)
	}

	modules, err := hub.Modules(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, module := range modules {
		if module.ID == "cloud_download" && module.AssignedBotID != secondID {
			t.Fatalf("cloud-download owner after transfer = %q, want %q", module.AssignedBotID, secondID)
		}
	}
	if err := hub.SetModule(context.Background(), "cloud_download", firstID, ""); !errors.Is(err, ErrModuleChanged) {
		t.Fatalf("stale owner was allowed to release the transferred module: %v", err)
	}

	select {
	case <-hub.wake:
	default:
	}
	secondRevision := second.Revision
	if err := hub.SetModule(context.Background(), "cloud_download", secondID, secondID); err != nil {
		t.Fatal(err)
	}
	second, _ = hub.Profiles().Get(secondID)
	if second.Revision != secondRevision {
		t.Fatalf("no-op assignment wrote the profile: revision %d -> %d", secondRevision, second.Revision)
	}
	select {
	case <-hub.wake:
		t.Fatal("no-op assignment restarted Bot workers")
	default:
	}

	restarted, err := NewHub(dir, "test-secret", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	modules, err = restarted.Modules(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, module := range modules {
		if module.ID == "cloud_download" && module.AssignedBotID != secondID {
			t.Fatalf("cloud-download transfer did not persist after restart: %+v", module)
		}
	}
}

func TestCloudDownloadTransferConflictKeepsOriginalOwner(t *testing.T) {
	hub, err := NewHub(t.TempDir(), "test-secret", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 3)
	for i, name := range []string{"Owner", "Target A", "Target B"} {
		ids[i], err = hub.Add(name)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := hub.SetModule(context.Background(), "cloud_download", "", ids[0]); err != nil {
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
			results <- hub.SetModule(context.Background(), "cloud_download", ids[0], id)
		}(targetID)
	}
	close(start)
	workers.Wait()
	close(results)
	wins := 0
	for err := range results {
		if err == nil {
			wins++
		} else if !errors.Is(err, ErrModuleChanged) {
			t.Fatalf("unexpected transfer conflict: %v", err)
		}
	}
	if wins != 1 {
		t.Fatalf("expected exactly one transfer winner, got %d", wins)
	}
	ownerCount := 0
	for _, profile := range hub.Profiles().List() {
		if profile.CloudDownload.Enabled {
			ownerCount++
		}
	}
	if ownerCount != 1 {
		t.Fatalf("expected one cloud-download owner after concurrent transfers, got %d", ownerCount)
	}
}

func TestCloudDownloadCommitFailureKeepsOriginalOwner(t *testing.T) {
	dir := t.TempDir()
	hub, err := NewHub(dir, "test-secret", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	firstID, err := hub.Add("Owner")
	if err != nil {
		t.Fatal(err)
	}
	secondID, err := hub.Add("Target")
	if err != nil {
		t.Fatal(err)
	}
	if err := hub.SetModule(context.Background(), "cloud_download", "", firstID); err != nil {
		t.Fatal(err)
	}
	profilePath := filepath.Join(dir, "telegram-bots.enc")
	if err := os.Remove(profilePath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(profilePath, 0700); err != nil {
		t.Fatal(err)
	}
	if err := hub.SetModule(context.Background(), "cloud_download", firstID, secondID); err == nil {
		t.Fatal("expected profile commit failure")
	}
	first, _ := hub.Profiles().Get(firstID)
	second, _ := hub.Profiles().Get(secondID)
	if !first.CloudDownload.Enabled || second.CloudDownload.Enabled {
		t.Fatalf("failed transfer changed module ownership: old=%v new=%v", first.CloudDownload.Enabled, second.CloudDownload.Enabled)
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
	if !pollsMessages(BotProfile{ID: "cloud-bot", CloudDownload: BotCloudDownloadConfig{Enabled: true}}, "") {
		t.Fatal("cloud-only Bot must start getUpdates")
	}
	if pollsMessages(BotProfile{ID: "notifications-only"}, "") {
		t.Fatal("notification-only Bot must not start a getUpdates consumer")
	}
	if !pollsMessages(BotProfile{ID: "share-bot"}, "share-bot") {
		t.Fatal("share Bot must start getUpdates")
	}
}
