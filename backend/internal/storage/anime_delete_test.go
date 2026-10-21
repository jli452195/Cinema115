package storage

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestAnimeDeleteUpgradePurgesOldTrashWithoutTouchingOtherSubscriptions(t *testing.T) {
	f, job := animeSubmitStoreFixture(t)
	ctx := context.Background()
	entry := createAnimeEntry(t, f.store, nil)
	config := animeConfig(entry.ID)
	config.Status, config.Sources = "draft", nil
	other := saveAnimeConfig(t, f.store, config)
	settings, _ := f.store.GetAnimeSettings(ctx)
	// A persisted worker lease from the old process cannot keep trash alive.
	takeAnimeWork(t, f.store, time.Now().UnixMilli())
	if _, err := f.store.db.Exec(`UPDATE anime_subscriptions SET status='removed' WHERE id=?`, f.sub.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.db.Exec(`PRAGMA user_version=54`); err != nil {
		t.Fatal(err)
	}
	var path string
	if err := f.store.db.QueryRow(`SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&path); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		store, err := Open(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.GetAnimeSubscription(ctx, f.sub.ID, true); !errors.Is(err, sql.ErrNoRows) {
			t.Fatal("old trash survived", err)
		}
		if _, err := store.GetAnimeDownloadJob(ctx, job.ID); !errors.Is(err, sql.ErrNoRows) {
			t.Fatal("old job survived", err)
		}
		retained, err := store.GetAnimeSubscription(ctx, other.ID, true)
		if err != nil || !reflect.DeepEqual(retained, other) {
			t.Fatal("unrelated subscription changed", retained, err)
		}
		retainedSettings, err := store.GetAnimeSettings(ctx)
		if err != nil || !reflect.DeepEqual(retainedSettings, settings) {
			t.Fatal("settings changed", err)
		}
		rows, err := store.db.Query(`PRAGMA foreign_key_check`)
		if err != nil {
			t.Fatal(err)
		}
		if rows.Next() {
			t.Fatal("foreign key violation")
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAnimeDeletePurgesOwnedRecordsAndReplaysOnlyReceipt(t *testing.T) {
	f, job := animeSubmitStoreFixture(t)
	ctx := context.Background()
	if _, err := f.store.PrepareAnimeSubmit(ctx, job.ID, job.Generation, f.target); err != nil {
		t.Fatal(err)
	}
	beforeSettings, _ := f.store.GetAnimeSettings(ctx)
	createKey := animeAdminKey("deleted_create_replay")
	createKey.Operation = "POST /api/admin/anime/subscriptions"
	if _, err := f.store.db.Exec(`INSERT INTO anime_request_keys(actor_key,operation,request_key,request_digest,http_status,receipt_json,created_at) VALUES(?,?,?,?,201,json_object('id',?),1)`, createKey.ActorKey, createKey.Operation, createKey.Key, createKey.Digest, f.sub.ID); err != nil {
		t.Fatal(err)
	}
	key := animeAdminKey("delete_pure_local")
	first, err := f.store.ControlAnimeSubscription(ctx, f.sub.ID, "remove", f.sub.Revision, &key)
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"anime_subscriptions", "anime_subscription_sources", "anime_source_baselines", "anime_release_decisions", "anime_job_selections", "anime_download_jobs", "anime_drive_operations", "anime_resource_claims", "anime_entries", "anime_releases", "anime_resources", "anime_preview_items", "anime_source_previews", "anime_preview_batches", "anime_source_activity", "anime_subscription_archives"} {
		var count int
		if err := f.store.db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&count); err != nil || count != 0 {
			t.Fatal("owned records remain", table, count, err)
		}
	}
	again, err := f.store.ControlAnimeSubscription(ctx, f.sub.ID, "remove", f.sub.Revision, &key)
	if err != nil || !reflect.DeepEqual(first, again) {
		t.Fatal("lost deletion response cannot replay", again, err)
	}
	afterSettings, _ := f.store.GetAnimeSettings(ctx)
	if !reflect.DeepEqual(beforeSettings, afterSettings) {
		t.Fatal("delete touched shared settings")
	}
	if _, err := f.store.LookupAnimeReceipt(ctx, createKey); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("old creation request could resurrect deleted subscription", err)
	}
	if _, err := f.store.GetAnimeSubscription(ctx, f.sub.ID, true); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("deleted subscription visible", err)
	}
	key.Digest = strings.Repeat("b", 64)
	if _, err := f.store.ControlAnimeSubscription(ctx, f.sub.ID, "remove", f.sub.Revision, &key); !errors.Is(err, ErrAnimeIdempotency) {
		t.Fatal("changed deletion replay accepted", err)
	}
}
