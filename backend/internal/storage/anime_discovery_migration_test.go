package storage

import (
	"context"
	"reflect"
	"testing"
)

func resetAnimeDiscoveryToV47(t *testing.T, s *Store) {
	t.Helper()
	for _, query := range []string{`DROP INDEX anime_discovery_pending`, `ALTER TABLE anime_subscription_sources DROP COLUMN poll_failures`, `ALTER TABLE anime_subscription_sources DROP COLUMN check_until`, `ALTER TABLE anime_subscription_sources DROP COLUMN poll_snapshot_json`, `ALTER TABLE anime_release_decisions DROP COLUMN observation_json`, `ALTER TABLE anime_release_decisions DROP COLUMN verify_failures`, `PRAGMA user_version=47`} {
		if _, err := s.db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAnimeDiscoveryMigrationPreservesExistingIntents(t *testing.T) {
	f, job := animeSubmitStoreFixture(t)
	ctx := context.Background()
	op, err := f.store.PrepareAnimeSubmit(ctx, job.ID, job.Generation, f.target)
	if err != nil {
		t.Fatal(err)
	}
	before := animeMigrationRows(t, f.store.db, "anime_release_decisions")
	// Only new empty scheduling columns are removed. Existing job/submission
	// facts must survive an actual close/reopen migration.
	resetAnimeDiscoveryToV47(t, f.store)
	var path string
	if err := f.store.db.QueryRow(`SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&path); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got, err := reopened.GetAnimeDownloadJob(ctx, job.ID)
	if err != nil || !reflect.DeepEqual(got, job) {
		t.Fatal("job changed", got, err)
	}
	saved, err := reopened.GetAnimeSubmitOperation(ctx, job.ID)
	if err != nil || !reflect.DeepEqual(saved, op) {
		t.Fatal("submit operation changed", saved, err)
	}
	after := animeMigrationRows(t, reopened.db, "anime_release_decisions")
	if !reflect.DeepEqual(before, after) {
		t.Fatal("decisions changed during migration")
	}
	var version int
	if err := reopened.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != schemaVersion {
		t.Fatal(version, err)
	}
	rows, err := reopened.db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("invalid foreign key")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestAnimeDiscoveryMigrationFailureRollsBackAllColumns(t *testing.T) {
	f, _ := animeSubmitStoreFixture(t)
	resetAnimeDiscoveryToV47(t, f.store)
	// CREATE INDEX IF NOT EXISTS must not accept an unrelated named table.
	if _, err := f.store.db.Exec(`CREATE TABLE anime_discovery_pending(id TEXT)`); err != nil {
		t.Fatal(err)
	}
	before := animeMigrationRows(t, f.store.db, "sqlite_master")
	if err := f.store.initialize(context.Background()); err == nil {
		t.Fatal("incompatible schema accepted")
	}
	after := animeMigrationRows(t, f.store.db, "sqlite_master")
	if !reflect.DeepEqual(before, after) {
		t.Fatal("partial migration escaped rollback")
	}
	var version int
	if err := f.store.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != 47 {
		t.Fatal(version, err)
	}
}
