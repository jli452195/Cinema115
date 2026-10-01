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
