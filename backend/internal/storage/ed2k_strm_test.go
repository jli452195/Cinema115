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
	settings := model.ED2KSTRMSettings{BaseURL: "https://cinema.example", CacheAccountID: "primary", CacheCID: "42", CachePath: "/媒体缓存"}
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
		CacheTargetCID: "42", CacheTargetPath: "/媒体缓存", CacheParentCID: "42",
		FileID: "11", PickCode: "pickcode", Status: "uncertain", CacheState: "unknown", Message: "等待确认",
		Stage: "submit_pending", Attempts: 3, NextRunAt: now.Add(time.Minute), CreatedAt: now, UpdatedAt: now,
	}
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var public map[string]json.RawMessage
	if err := json.Unmarshal(raw, &public); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"accountId", "uid", "accountRevision", "credentialScope", "settings", "strmPath", "outputDir", "directoryPending", "cacheTargetCID", "cacheTargetPath", "cacheParentCID", "fileId", "pickCode", "stage", "attempts", "nextRunAt", "cookie"} {
		if _, ok := public[key]; ok {
			t.Errorf("internal field %q exposed in API JSON", key)
		}
	}
	if err := s.SaveED2KSTRMResource(ctx, v); err != nil {
		t.Fatal(err)
	}
	var stored string
	if err := s.db.QueryRowContext(ctx, `SELECT record FROM ed2k_strm_resources WHERE hash=? AND size=?`, v.Hash, v.Size).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"settings"`, `"strmPath"`, `"outputDir"`, `"cookie"`} {
		if strings.Contains(stored, field) {
			t.Errorf("resource persistence retained output location or credentials: %s", field)
		}
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

func TestED2KSTRMTargetEditsDoNotChangeAcceptedTaskSnapshot(t *testing.T) {
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
	now := time.Date(2026, 11, 17, 1, 2, 3, 0, time.UTC)
	target := model.ED2KSTRMTarget{ID: "target-1", Name: "电影目录", OutputDir: "movies", AssignedBotID: "bot-1"}
	task := model.ED2KSTRMTask{ID: "task-1", RequestID: "request-1", Fingerprint: "fingerprint", Hash: strings.Repeat("a", 32), Size: 1234, Name: "电影.mkv", TargetID: target.ID, TargetName: target.Name, OutputDir: target.OutputDir, BaseURL: "https://accepted.example", Status: "waiting", CreatedAt: now, UpdatedAt: now}
	if err := s.SaveED2KSTRMTarget(ctx, target); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveED2KSTRMTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	var public map[string]json.RawMessage
	if err := json.Unmarshal(raw, &public); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"requestId", "fingerprint", "targetId", "outputDir", "baseUrl", "cookie", "strmPath"} {
		if _, ok := public[key]; ok {
			t.Errorf("accepted task exposed private field %q", key)
		}
	}
	target.Name, target.OutputDir, target.AssignedBotID = "更名目录", "other-movies", "bot-2"
	if err := s.SaveED2KSTRMTarget(ctx, target); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteED2KSTRMTarget(ctx, target.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, filename)
	if err != nil {
		t.Fatal(err)
	}
	targets, err := s.ListED2KSTRMTargets(ctx)
	if err != nil || len(targets) != 0 {
		t.Fatalf("deleted target restored: %+v err=%v", targets, err)
	}
	tasks, err := s.ListED2KSTRMTasks(ctx)
	if err != nil || len(tasks) != 1 || !reflect.DeepEqual(tasks[0], task) {
		t.Fatalf("accepted snapshot changed after target edit/delete and restart: %+v err=%v", tasks, err)
	}
}

func TestED2KSTRMImportPersistsResourcesAndTasksAtomically(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "ed2k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().UTC()
	first := model.ED2KSTRMResource{Hash: strings.Repeat("a", 32), Size: 1234, Name: "first.mkv", Status: "queued", CreatedAt: now, UpdatedAt: now}
	task := model.ED2KSTRMTask{ID: "task-1", RequestID: "first-request", Fingerprint: "first-fingerprint", Hash: first.Hash, Size: first.Size, Name: first.Name, TargetID: "target-1", TargetName: "电影", OutputDir: "movies", BaseURL: "https://cinema.example", Status: "waiting", CreatedAt: now, UpdatedAt: now}
	if err := s.SaveED2KSTRMImport(ctx, []model.ED2KSTRMResource{first}, []model.ED2KSTRMTask{task}); err != nil {
		t.Fatal(err)
	}
	second := first
	second.Hash, second.Name = strings.Repeat("b", 32), "second.mkv"
	if err := s.SaveED2KSTRMImport(ctx, []model.ED2KSTRMResource{second}, []model.ED2KSTRMTask{task}); err == nil {
		t.Fatal("duplicate task ID did not reject transaction")
	}
	resources, err := s.ListED2KSTRMResources(ctx)
	if err != nil || len(resources) != 1 || !reflect.DeepEqual(resources[0], first) {
		t.Fatalf("failed import leaked partial resource: %+v err=%v", resources, err)
	}
	tasks, err := s.ListED2KSTRMTasks(ctx)
	if err != nil || len(tasks) != 1 || !reflect.DeepEqual(tasks[0], task) {
		t.Fatalf("failed import changed existing task: %+v err=%v", tasks, err)
	}
}
