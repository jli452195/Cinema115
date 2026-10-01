package service

import (
	"path/filepath"
	"testing"
)

func TestAutomationParentPathPersistenceAndAccountChange(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "automation.json")
	store, err := NewAutomationStore(file, dir, "http://nas:8115")
	if err != nil {
		t.Fatal(err)
	}
	primary := "main"
	store.PrimaryID = func() string { return primary }
	settings := store.Value()
	settings.ParentCID, settings.ParentPath = "12", "/资源/电影"
	if err := store.Save(settings); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewAutomationStore(file, dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := reopened.Value(); got.ParentCID != "12" || got.ParentPath != "/资源/电影" {
		t.Fatalf("lost parent selection: %+v", got)
	}
	primary = "replacement"
	if got := store.Value(); got.AccountID != primary || got.ParentCID != "" || got.ParentPath != "" {
		t.Fatalf("retained old account directory: %+v", got)
	}
	if err := store.Save(settings); err == nil {
		t.Fatal("accepted stale account selection")
	}
	settings = store.Value()
	settings.ParentCID, settings.ParentPath = "0", "/stale"
	if err := store.Save(settings); err != nil {
		t.Fatal(err)
	}
	if got := store.Value(); got.ParentPath != "/" {
		t.Fatalf("root path = %q", got.ParentPath)
	}
}

func TestShareSettingsRootDoesNotChangeMonitorDirectory(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "automation.json")
	store, err := NewAutomationStore(file, dir, "http://nas:8115")
	if err != nil {
		t.Fatal(err)
	}
	primary := "main"
	store.PrimaryID = func() string { return primary }
	settings := store.Value()
	settings.ParentCID, settings.ParentPath = "12", "/monitor-staging"
	if err := store.Save(settings); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"main", "replacement"} {
		primary = id
		share := store.ShareValue()
		if share.AccountID != id || share.ParentCID != "0" || share.ParentPath != "/" {
			t.Fatalf("share needs directory confirmation: %+v", share)
		}
		share.ParentCID, share.ParentPath = "999", "/ignored"
		share.FixedSharePassword = "abcd"
		if err := store.SaveShare(share); err != nil {
			t.Fatal(err)
		}
		reopened, err := NewAutomationStore(file, dir, "")
		if err != nil {
			t.Fatal(err)
		}
		stored := reopened.Value()
		if stored.AccountID != "main" || stored.ParentCID != "12" || stored.ParentPath != "/monitor-staging" || stored.FixedSharePassword != "abcd" {
			t.Fatalf("share save changed monitor ownership or lost options: %+v", stored)
		}
		if id == "replacement" && store.Value().ParentCID != "" {
			t.Fatal("monitor directory followed replacement account")
		}
	}
	stale := store.ShareValue()
	stale.AccountID = "main"
	if err := store.SaveShare(stale); err == nil {
		t.Fatal("accepted a stale share account")
	}
	store.ValidateAccount = func(string) error { return generationInputError("主账号未登录") }
	if err := store.SaveShare(store.ShareValue()); err == nil {
		t.Fatal("root default bypassed account validation")
	}
}
