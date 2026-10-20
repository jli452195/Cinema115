package storage

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func nonAnimeRows(t *testing.T, s *Store) map[string][][]any {
	t.Helper()
	rows, err := s.db.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' AND name NOT LIKE 'anime_%' AND name NOT LIKE 'legacy_anime_%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	out := make(map[string][][]any, len(names))
	for _, name := range names {
		out[name] = animeMigrationRows(t, s.db, name)
	}
	return out
}

func assertAnimeReset(t *testing.T, s *Store) {
	t.Helper()
	rows, err := s.db.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name LIKE 'anime_%'`)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if len(names) != 17 {
		t.Fatal("incomplete current schema", len(names))
	}
	for _, name := range names {
		var n int
		want := 0
		if name == "anime_settings" {
			want = 1
		}
		if err := s.db.QueryRow(`SELECT count(*) FROM ` + name).Scan(&n); err != nil || n != want {
			t.Fatal(name, n, err)
		}
	}
	for _, query := range []string{
		`SELECT poll_snapshot_json,poll_failures,check_until FROM anime_subscription_sources LIMIT 0`,
		`SELECT observation_json,verify_failures FROM anime_release_decisions LIMIT 0`,
		`SELECT files_json,directory_operation_json FROM anime_download_jobs LIMIT 0`,
		`SELECT naming_json FROM anime_subscriptions LIMIT 0`,
		`SELECT snapshot_json FROM anime_source_previews LIMIT 0`,
	} {
		r, err := s.db.Query(query)
		if err != nil {
			t.Fatal(err)
		}
		r.Close()
	}
	var version, fk int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != schemaVersion {
		t.Fatal(version, err)
	}
	if err := s.db.QueryRow(`PRAGMA foreign_keys`).Scan(&fk); err != nil || fk != 1 {
		t.Fatal(fk, err)
	}
	r, err := s.db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	if r.Next() {
		t.Fatal("foreign key violation")
	}
	if err := r.Err(); err != nil {
		t.Fatal(err)
	}
	r.Close()
}

func TestAnimeDevelopmentResetRepairsV50MissingPollSnapshot(t *testing.T) {
	for _, version := range []int{39, 44, 47, 48, 49, 50} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			f, _ := animeSubmitStoreFixture(t)
			ctx := context.Background()
			if err := f.store.SaveReshareJob(ctx, sampleJob("keep-other-feature", "completed")); err != nil {
				t.Fatal(err)
			}
			before := nonAnimeRows(t, f.store)
			// Reproduce the reported database: its global version already passed
			// v48, but a column added during development is missing.
			if _, err := f.store.db.Exec(`ALTER TABLE anime_subscription_sources DROP COLUMN poll_snapshot_json`); err != nil {
				t.Fatal(err)
			}
			if _, err := f.store.db.Exec(fmt.Sprintf(`PRAGMA user_version=%d`, version)); err != nil {
				t.Fatal(err)
			}
			var databasePath string
			if err := f.store.db.QueryRow(`SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&databasePath); err != nil {
				t.Fatal(err)
			}
			if err := f.store.Close(); err != nil {
				t.Fatal(err)
			}
			s, err := Open(ctx, databasePath)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			assertAnimeReset(t, s)
			if after := nonAnimeRows(t, s); !reflect.DeepEqual(before, after) {
				t.Fatal("another feature's data changed")
			}
			entry := createAnimeEntry(t, s, animePtr("123"))
			sub := saveAnimeConfig(t, s, animeConfig(entry.ID))
			if err := s.initialize(ctx); err != nil {
				t.Fatal(err)
			}
			got, err := s.GetAnimeSubscription(ctx, sub.ID, false)
			if err != nil || !reflect.DeepEqual(got, sub) {
				t.Fatal("v51 startup cleared new data", err)
			}
		})
	}
}

func TestAnimeDevelopmentResetLegacyPrototypeAndArchive(t *testing.T) {
	for _, archive := range []bool{false, true} {
		t.Run(fmt.Sprint(archive), func(t *testing.T) {
			s, _ := testStore(t)
			if archive {
				if _, err := s.db.Exec(strings.ReplaceAll(animeLegacyV38Fixture, "anime_", "legacy_anime_v38_") + `PRAGMA user_version=40`); err != nil {
					t.Fatal(err)
				}
			} else {
				resetAnimeSchemaToV38(t, s)
				if _, err := s.db.Exec(animeLegacyV38Fixture + `ALTER TABLE anime_releases ADD COLUMN unknown_field TEXT`); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.initialize(context.Background()); err != nil {
				t.Fatal(err)
			}
			assertAnimeReset(t, s)
			var n int
			if err := s.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name LIKE 'legacy_anime_%' OR name IN ('anime_transfers','anime_download_claims')`).Scan(&n); err != nil || n != 0 {
				t.Fatal(n, err)
			}
		})
	}
}

func TestAnimeDevelopmentResetFailureIsAtomicAndRestoresForeignKeys(t *testing.T) {
	f, _ := animeSubmitStoreFixture(t)
	if _, err := f.store.db.Exec(`DROP INDEX anime_discovery_pending; CREATE TABLE anime_discovery_pending(id TEXT); PRAGMA user_version=50`); err != nil {
		t.Fatal(err)
	}
	before := animeMigrationRows(t, f.store.db, "sqlite_master")
	jobs := animeMigrationRows(t, f.store.db, "anime_download_jobs")
	if err := f.store.initialize(context.Background()); err == nil {
		t.Fatal("conflicting schema accepted")
	}
	if after := animeMigrationRows(t, f.store.db, "sqlite_master"); !reflect.DeepEqual(before, after) {
		t.Fatal("partial reset escaped transaction")
	}
	if after := animeMigrationRows(t, f.store.db, "anime_download_jobs"); !reflect.DeepEqual(jobs, after) {
		t.Fatal("failed reset deleted test data")
	}
	var version, fk int
	if err := f.store.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != 50 {
		t.Fatal(version, err)
	}
	if err := f.store.db.QueryRow(`PRAGMA foreign_keys`).Scan(&fk); err != nil || fk != 1 {
		t.Fatal(fk, err)
	}
}

func TestAnimeDevelopmentResetV52PreservesSettingsAndRunsOnce(t *testing.T) {
	f, _ := animeSubmitStoreFixture(t)
	ctx := context.Background()
	settings, err := f.store.GetAnimeSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	before := nonAnimeRows(t, f.store)
	// Reproduce the retired execution schema, including its history member table.
	if _, err := f.store.db.Exec(`CREATE TABLE anime_baseline_releases(baseline_id TEXT,release_id TEXT); INSERT INTO anime_baseline_releases VALUES('old','test'); PRAGMA user_version=52`); err != nil {
		t.Fatal(err)
	}
	if err := f.store.initialize(ctx); err != nil {
		t.Fatal(err)
	}
	assertAnimeReset(t, f.store)
	got, err := f.store.GetAnimeSettings(ctx)
	if err != nil || !reflect.DeepEqual(settings, got) {
		t.Fatal("directory/account settings changed", err)
	}
	if !reflect.DeepEqual(before, nonAnimeRows(t, f.store)) {
		t.Fatal("another feature changed")
	}
	var obsolete int
	if err := f.store.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name='anime_baseline_releases'`).Scan(&obsolete); err != nil || obsolete != 0 {
		t.Fatal("retired table retained", obsolete, err)
	}
	entry := createAnimeEntry(t, f.store, animePtr("456"))
	sub := saveAnimeConfig(t, f.store, animeConfig(entry.ID))
	if err := f.store.initialize(ctx); err != nil {
		t.Fatal(err)
	}
	restored, err := f.store.GetAnimeSubscription(ctx, sub.ID, false)
	if err != nil || !reflect.DeepEqual(sub, restored) {
		t.Fatal("normal restart cleared current data", err)
	}
}
