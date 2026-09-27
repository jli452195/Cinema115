package service

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestLegacyStrictOriginSettingsAreIgnored(t *testing.T) {
	store, err := NewSessionStore(filepath.Join(t.TempDir(), "proxy.enc"), "secret")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(`{"enabled":false,"port":8097,"strictStrmOrigin":true,"strmBaseUrl":"not a valid origin"}`); err != nil {
		t.Fatal(err)
	}
	proxy, err := NewEmbyProxy(context.Background(), store, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	raw, err := json.Marshal(proxy.Status().Config)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "strictStrmOrigin") || strings.Contains(string(raw), "strmBaseUrl") {
		t.Fatal("obsolete proxy fields still exposed")
	}
	if _, err := normalizeEmbyProxy(proxy.Status().Config); err != nil {
		t.Fatal("legacy origin prevented configuration", err)
	}

	s, _, config, instantStore := setupInstantService(t)
	data, _ := json.Marshal(config)
	var old map[string]any
	if err := json.Unmarshal(data, &old); err != nil {
		t.Fatal(err)
	}
	old["strictStrmOrigin"] = true
	old["strmBaseUrl"] = "not a valid origin"
	doc, _ := json.Marshal(map[string]any{"config": old, "records": []any{}})
	if err := instantStore.Save(string(doc)); err != nil {
		t.Fatal(err)
	}
	restored, err := NewInstantUploads(instantStore, s.accounts, s.client, s.logs)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Status().Config != config {
		t.Fatal("legacy migration changed account selections")
	}
	if err := restored.Apply(restored.Status().Config); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(instantStore.Value(), "strictStrmOrigin") || strings.Contains(instantStore.Value(), "strmBaseUrl") {
		t.Fatal("save retained obsolete fields")
	}
}
