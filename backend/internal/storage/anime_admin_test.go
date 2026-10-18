package storage

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"strm115/backend/internal/model"
)

func animeAdminKey(key string) AnimeWriteKey {
	return AnimeWriteKey{ActorKey: "admin", Operation: "POST /api/admin/anime/subscriptions", Key: key, Digest: strings.Repeat("a", 64)}
}
func animeVerifiedNewEntry() model.AnimeEntry {
	return model.AnimeEntry{BangumiID: animePtr("999"), EditionKey: "original", Title: "可信标题", Metadata: model.AnimeMetadata{SchemaVersion: 1}}
}

func TestAnimeAdminAtomicChapterAndReceipt(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	settings, err := s.GetAnimeSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	guard := AnimeConfigGuard{Entry: animeVerifiedNewEntry(), Settings: settings}
	c := animeConfig("pending")
	c.Status = "draft"
	// A final-set conflict happens after the new chapter is tentatively written.
	c.Sources = append(c.Sources, animeMainSource("duplicate", "45"))
	c.Sources[1].Role = "standby"
	if _, err := s.SaveAnimeConfiguration(ctx, c, animeAdminKey("atomic_bad"), guard); err == nil {
		t.Fatal("duplicate sources accepted")
	}
	for _, table := range []string{"anime_entries", "anime_subscriptions", "anime_request_keys"} {
		var n int
		if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM `+table).Scan(&n); err != nil || n != 0 {
			t.Fatal("failed write left data", table, n, err)
		}
	}
	c.Sources = c.Sources[:1]
	first, err := s.SaveAnimeConfiguration(ctx, c, animeAdminKey("atomic_good"), guard)
	if err != nil {
		t.Fatal(err)
	}
	if first.EntryID == "" || first.Revision != 1 || first.SourceRevisions[first.SourceIDs["main"]] != 1 {
		t.Fatal(first)
	}
	// A stale settings snapshot and changed input cannot prevent the same-key
	// transaction from returning its original committed receipt.
	guard.Settings.Revision++
	c.Status = "enabled"
	got, err := s.SaveAnimeConfiguration(ctx, c, animeAdminKey("atomic_good"), guard)
	if err != nil || !reflect.DeepEqual(first, got) {
		t.Fatal("receipt not replayed first", got, err)
	}
	receipt, err := s.LookupAnimeReceipt(ctx, animeAdminKey("atomic_good"))
	if err != nil || receipt == nil || receipt.Status != 201 {
		t.Fatal(receipt, err)
	}
	k := animeAdminKey("atomic_good")
	k.Digest = strings.Repeat("b", 64)
	if _, err := s.LookupAnimeReceipt(ctx, k); !errors.Is(err, ErrAnimeIdempotency) {
		t.Fatal(err)
	}
	for _, table := range []string{"anime_source_baselines", "anime_baseline_releases", "anime_release_decisions"} {
		var n int
		if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM `+table).Scan(&n); err != nil || n != 0 {
			t.Fatal("configuration authorized execution", table, n, err)
		}
	}
	var executionTables int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='table' AND name IN ('anime_resource_claims','anime_download_jobs')`).Scan(&executionTables); err != nil || executionTables != 0 {
		t.Fatal("P2 tables unexpectedly installed", executionTables, err)
	}
}

func TestAnimeAdminSettingsAccountCASAndReplay(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	state := P115AccountState{Accounts: []P115AccountRow{{ID: "primary", Name: "主账号", UID: "1234", Primary: true, Ownership: "admin", Cookie: []byte("synthetic ciphertext"), Revision: 2}}}
	if err := s.SaveP115Accounts(ctx, state); err != nil {
		t.Fatal(err)
	}
	v := model.AnimeSettings{Revision: 1, AccountID: animePtr("primary"), AccountRevision: animePtr(int64(2)), UID: animePtr("1234"), DownloadCID: animePtr("0"), DownloadPath: animePtr("/")}
	k := animeAdminKey("settings_good")
	k.Operation = "PATCH /api/admin/anime/settings"
	first, err := s.SaveAnimeSettings(ctx, v, k)
	if err != nil || first.Revision != 2 {
		t.Fatal(first, err)
	}
	state.Revision = 1
	state.Accounts[0].Revision++
	if err := s.SaveP115Accounts(ctx, state); err != nil {
		t.Fatal(err)
	}
	got, err := s.SaveAnimeSettings(ctx, v, k)
	if err != nil || !reflect.DeepEqual(got, first) {
		t.Fatal("lost response cannot replay", got, err)
	}
	k.Key = "changed_account"
	if _, err := s.SaveAnimeSettings(ctx, first, k); !errors.Is(err, ErrAnimeAccountChanged) {
		t.Fatal("stale primary committed", err)
	}
	guard := AnimeConfigGuard{Entry: animeVerifiedNewEntry(), Settings: first, RequireAccount: true}
	c := animeConfig("pending")
	if _, err := s.SaveAnimeConfiguration(ctx, c, animeAdminKey("paused_stale"), guard); !errors.Is(err, ErrAnimeAccountChanged) {
		t.Fatal("paused accepted stale account", err)
	}
	guard.RequireAccount = false
	guard.Settings.Revision = 1
	if _, err := s.SaveAnimeConfiguration(ctx, c, animeAdminKey("stale_settings"), guard); !errors.Is(err, ErrAnimeSettingsChanged) {
		t.Fatal("stale directory accepted", err)
	}
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM anime_entries`).Scan(&n); err != nil || n != 0 {
		t.Fatal("CAS failure left chapter", n, err)
	}
}
