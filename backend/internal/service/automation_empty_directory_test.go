package service

import (
	"path/filepath"
	"testing"
)

func TestAutomationEmptyDirectoryRequiresExplicitJobDestination(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "automation.json")
	store, err := NewAutomationStore(path, root, "http://nas:8116")
	if err != nil {
		t.Fatal(err)
	}
	value := store.Value()
	if value.Generation.OutputDir != "" {
		t.Fatalf("unexpected default destination: %q", value.Generation.OutputDir)
	}
	if err := store.Save(value); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewAutomationStore(path, root, "http://nas:8116")
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Value().Generation.OutputDir != "" {
		t.Fatal("reload selected a destination")
	}
	if err := ValidateGenerationOptions(root, value.Generation); err == nil {
		t.Fatal("generation accepted a missing destination")
	}
	value.TelegramEnabled = true
	if err := store.validate(value); err == nil {
		t.Fatal("enabled automation accepted a missing destination")
	}
	value.Generation.OutputDir = "."
	if err := store.Save(value); err == nil {
		t.Fatal("enabled automation accepted a missing bot validator")
	}
	value.TelegramEnabled = false
	if err := store.Save(value); err != nil {
		t.Fatal(err)
	}
}
