package storage

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
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
	var count int
	if err := db.QueryRowContext(context.Background(), `SELECT count(*) FROM pragma_table_info(?)`, table).Scan(&count); err != nil {
		t.Fatal(err)
	}
	order := make([]string, count)
	for i := range order {
		order[i] = strconv.Itoa(i + 1)
	}
	rows, err := db.QueryContext(context.Background(), `SELECT * FROM `+table+` ORDER BY `+strings.Join(order, ","))
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
