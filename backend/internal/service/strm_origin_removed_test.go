package service

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

func TestLegacyStrictOriginSettingsAreIgnored(t *testing.T) {
	ctx := context.Background()
	store := openProxyTestStore(t, filepath.Join(t.TempDir(), "proxy.db"))
	proxy, err := NewEmbyProxy(ctx, store, func() (string, error) {
		return `{"enabled":false,"port":8097,"embyPathPrefix":"path\ninvalid","strictStrmOrigin":true,"strmBaseUrl":"not a valid origin"}`, nil
	}, nil, func(EmbyProxyConfig) http.Handler { return http.NotFoundHandler() })
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	raw, err := json.Marshal(proxy.Status().Config)
	if err != nil {
		t.Fatal(err)
	}
	if err := proxy.Apply(ctx, proxy.Status().Config); err != nil {
		t.Fatal("legacy fields prevented configuration", err)
	}
	saved, err := store.EmbyProxyConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if saved == "" {
		t.Fatal("proxy configuration was not saved")
	}
	for _, key := range []string{"embyPathPrefix", "strictStrmOrigin", "strmBaseUrl"} {
		if strings.Contains(string(raw), key) || strings.Contains(saved, key) {
			t.Fatal("obsolete proxy field retained", key)
		}
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
	restored.Playback = s.Playback
	if err := restored.Apply(restored.Status().Config); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(instantStore.Value(), "strictStrmOrigin") || strings.Contains(instantStore.Value(), "strmBaseUrl") {
		t.Fatal("save retained obsolete fields")
	}
}
