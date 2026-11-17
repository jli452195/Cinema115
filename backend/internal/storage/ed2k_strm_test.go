package storage

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/model"
)

func TestED2KSTRMSettingsAndCacheSurviveReopen(t *testing.T) {
	ctx := context.Background()
	filename := filepath.Join(t.TempDir(), "ed2k.db")
	s, err := Open(ctx, filename)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if s != nil {
			_ = s.Close()
		}
	})
	if _, err := s.GetED2KSTRMSettings(ctx); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unsaved settings: %v", err)
	}
	if _, err := s.GetED2KSTRMCache(ctx, "primary"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unsaved cache: %v", err)
	}
	settings := model.ED2KSTRMSettings{BaseURL: "https://cinema.example", OutputDir: "ed2k"}
	cache := model.ED2KSTRMCache{AccountID: "primary", UID: "123", CID: "10", Name: "Cinema115-ED2K-test", Pending: true}
	if err := s.SaveED2KSTRMSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveED2KSTRMCache(ctx, cache); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, filename)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.GetED2KSTRMSettings(ctx); err != nil || got != settings {
		t.Fatalf("settings=%+v err=%v", got, err)
	}
	if got, err := s.GetED2KSTRMCache(ctx, cache.AccountID); err != nil || got != cache {
		t.Fatalf("cache=%+v err=%v", got, err)
	}
	if _, err := s.GetED2KSTRMCache(ctx, "other-primary"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cache leaked to another account: %v", err)
	}
}

func TestED2KSTRMInternalFieldsPersistWithoutAppearingInAPI(t *testing.T) {
	ctx := context.Background()
	filename := filepath.Join(t.TempDir(), "ed2k.db")
	s, err := Open(ctx, filename)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if s != nil {
			_ = s.Close()
		}
	})
	now := time.Date(2026, 11, 17, 1, 2, 3, 123456789, time.UTC)
	v := model.ED2KSTRMResource{
		Hash: strings.Repeat("a", 32), SHA1: strings.Repeat("B", 40), Size: 1234,
		Name: "影片.mkv", Aliases: []string{"别名.mkv"}, AccountID: "primary", UID: "123",
		AccountRevision: 7, CredentialScope: "credential-digest", CacheCID: "10", DirectoryPending: true,
		Settings: model.ED2KSTRMSettings{BaseURL: "https://cinema.example", OutputDir: "ed2k"},
		FileID:   "11", PickCode: "pickcode", Status: "uncertain", CacheState: "unknown", Message: "等待确认",
		STRMPath: "ed2k/file.strm", Stage: "submit_pending", Attempts: 3, NextRunAt: now.Add(time.Minute), CreatedAt: now, UpdatedAt: now,
	}
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var public map[string]json.RawMessage
	if err := json.Unmarshal(raw, &public); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"accountId", "uid", "accountRevision", "credentialScope", "settings", "directoryPending", "fileId", "pickCode", "stage", "attempts", "nextRunAt", "cookie"} {
		if _, ok := public[key]; ok {
			t.Errorf("internal field %q exposed in API JSON", key)
		}
	}
	if err := s.SaveED2KSTRMResource(ctx, v); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, filename)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.GetED2KSTRMResource(ctx, v.Hash, v.Size)
	if err != nil || !reflect.DeepEqual(got, v) {
		t.Fatalf("resource lost durable fields: got=%+v want=%+v err=%v", got, v, err)
	}
	got.Message = "重启后核对"
	if err := s.SaveED2KSTRMResource(ctx, got); err != nil {
		t.Fatal(err)
	}
	all, err := s.ListED2KSTRMResources(ctx)
	if err != nil || len(all) != 1 || !reflect.DeepEqual(all[0], got) {
		t.Fatalf("resource upsert/list=%+v err=%v", all, err)
	}
	if _, err := s.GetED2KSTRMResource(ctx, v.Hash, v.Size+1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("same hash with different size reused: %v", err)
	}
}
