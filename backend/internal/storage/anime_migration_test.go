package storage

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
)

// The retired prototype already used these names at schema version 38.
// Keep the real historical DDL here rather than simulating an upgrade by only
// changing user_version on tables created by the current application.
const animeLegacyV38Fixture = `
CREATE TABLE anime_subscriptions(id TEXT PRIMARY KEY, tmdb_id INTEGER NOT NULL, bangumi_id TEXT NOT NULL, group_id TEXT NOT NULL, revision INTEGER NOT NULL, enabled INTEGER NOT NULL, due INTEGER NOT NULL, record TEXT NOT NULL, UNIQUE(tmdb_id,bangumi_id,group_id));
CREATE INDEX anime_subscriptions_due ON anime_subscriptions(enabled,due);
CREATE TABLE anime_releases(subscription_id TEXT NOT NULL REFERENCES anime_subscriptions(id), hash TEXT NOT NULL, status TEXT NOT NULL, record TEXT NOT NULL, PRIMARY KEY(subscription_id,hash));
CREATE INDEX anime_releases_status ON anime_releases(subscription_id,status);
CREATE TABLE anime_download_claims(account_id TEXT NOT NULL, hash TEXT NOT NULL, target_key TEXT NOT NULL, subscription_id TEXT NOT NULL, PRIMARY KEY(account_id,hash), UNIQUE(account_id,target_key));
CREATE TABLE anime_transfers(subscription_id TEXT NOT NULL, hash TEXT NOT NULL, revision INTEGER NOT NULL, state TEXT NOT NULL, due INTEGER NOT NULL, record TEXT NOT NULL, PRIMARY KEY(subscription_id,hash));
CREATE INDEX anime_transfers_due ON anime_transfers(state,due);
INSERT INTO anime_subscriptions VALUES('old-sub',335790,'123','45',3,1,100,'{"legacy":"subscription"}');
INSERT INTO anime_releases VALUES('old-sub','hash-a','downloaded','{"legacy":"release-a"}'),('old-sub','hash-b','pending','{"legacy":"release-b"}');
INSERT INTO anime_download_claims VALUES('old-account','hash-a','old-target','old-sub');
INSERT INTO anime_transfers VALUES('old-sub','hash-a',2,'completed',200,'{"legacy":"transfer"}');
`

func legacyAnimeStore(t *testing.T) (*Store, string) {
	t.Helper()
	s, path := testStore(t)
	resetAnimeSchemaToV38(t, s)
	if _, err := s.db.ExecContext(context.Background(), animeLegacyV38Fixture); err != nil {
		t.Fatal(err)
	}
	return s, path
}

func animeMigrationRows(t *testing.T, db *sql.DB, table string) [][]any {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), `SELECT * FROM `+table+` ORDER BY rowid`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	values := make([][]any, 0)
	for rows.Next() {
		row := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for i := range row {
			pointers[i] = &row[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			t.Fatal(err)
		}
		values = append(values, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return values
}

func TestAnimeSchemaUpgradeWithLegacyTables(t *testing.T) {
	s, path := legacyAnimeStore(t)
	ctx := context.Background()
	tables := []string{"subscriptions", "releases", "download_claims", "transfers"}
	before := make(map[string][][]any, len(tables))
	for _, table := range tables {
		before[table] = animeMigrationRows(t, s.db, "anime_"+table)
	}
	job := sampleJob("unrelated-job", "completed")
	if err := s.SaveReshareJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if err := s.initialize(ctx); err != nil {
		t.Fatal(err)
	}
	for _, table := range tables {
		if got := animeMigrationRows(t, s.db, "legacy_anime_v38_"+table); !reflect.DeepEqual(got, before[table]) {
			t.Fatalf("legacy %s records changed", table)
		}
	}
	if got, err := s.ListAnimeSubscriptions(ctx, AnimeSubscriptionFilter{}); err != nil || got.Total != 0 {
		t.Fatalf("legacy work must not be activated: %+v %v", got, err)
	}
	var foreignTable string
	if err := s.db.QueryRowContext(ctx, `SELECT "table" FROM pragma_foreign_key_list('legacy_anime_v38_releases')`).Scan(&foreignTable); err != nil || foreignTable != "legacy_anime_v38_subscriptions" {
		t.Fatalf("legacy FK was redirected to new subscriptions: %q %v", foreignTable, err)
	}
	rows, err := s.db.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	if rows.Next() {
		t.Fatal("foreign key violation after archive")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	e := createAnimeEntry(t, s, animePtr("456"))
	sub := saveAnimeConfig(t, s, animeConfig(e.ID))
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		reopened, err := Open(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		for _, table := range tables {
			if got := animeMigrationRows(t, reopened.db, "legacy_anime_v38_"+table); !reflect.DeepEqual(got, before[table]) {
				t.Fatalf("legacy %s changed on reopen", table)
			}
		}
		if got, err := reopened.GetAnimeSubscription(ctx, sub.ID, false); err != nil || !reflect.DeepEqual(got, sub) {
			t.Fatalf("new subscription changed: %+v %v", got, err)
		}
		if got, err := reopened.GetReshareJob(ctx, job.ID); err != nil || !reflect.DeepEqual(got, job) {
			t.Fatalf("unrelated business data changed: %+v %v", got, err)
		}
		var version int
		if err := reopened.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil || version != 39 {
			t.Fatalf("unexpected version: %d %v", version, err)
		}
		reopened.Close()
	}
}

func TestAnimeLegacyMigrationFailureRollsBack(t *testing.T) {
	cases := []struct {
		name, inject string
	}{
		{"archive_name_conflict", `CREATE TABLE legacy_anime_v38_releases(marker TEXT); INSERT INTO legacy_anime_v38_releases VALUES('preserve-me')`},
		{"unrecognized_table", `ALTER TABLE anime_releases ADD COLUMN unknown_field TEXT`},
		{"new_schema_failure", `CREATE VIEW anime_settings AS SELECT 1 AS id, 1 AS revision`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := legacyAnimeStore(t)
			ctx := context.Background()
			if _, err := s.db.ExecContext(ctx, tc.inject); err != nil {
				t.Fatal(err)
			}
			before := animeMigrationRows(t, s.db, "sqlite_master")
			oldRecords := animeMigrationRows(t, s.db, "anime_subscriptions")
			if err := s.initialize(ctx); err == nil {
				t.Fatal("expected migration failure")
			}
			if got := animeMigrationRows(t, s.db, "sqlite_master"); !reflect.DeepEqual(got, before) {
				t.Fatal("failed migration partially modified schema")
			}
			if got := animeMigrationRows(t, s.db, "anime_subscriptions"); !reflect.DeepEqual(got, oldRecords) {
				t.Fatal("failed migration modified legacy data")
			}
			var version int
			if err := s.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil || version != 38 {
				t.Fatalf("failed migration changed version: %d %v", version, err)
			}
		})
	}
}
