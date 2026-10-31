package storage

import (
	"context"
	"database/sql"
	"reflect"
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

func TestAnimeAccountBindingUpgradePreservesDirectoriesAndTaskProgress(t *testing.T) {
	f := newDownloadClaimFixture(t)
	ctx := context.Background()
	release, verified := f.selectRelease(t, 0, "rel_legacy_binding", strings.Repeat("a", 40))
	job := f.claim(t, 0, release, verified).Job
	settings, err := f.store.GetAnimeSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Recreate the prior table CHECK, which prevents directly dropping its
	// account revision/UID columns. Existing business data must be retained.
	for _, query := range []string{
		`ALTER TABLE anime_settings RENAME TO anime_settings_current_fixture`,
		`CREATE TABLE anime_settings(id INTEGER PRIMARY KEY CHECK(id=1),revision INTEGER NOT NULL CHECK(revision>0),account_id TEXT,account_revision INTEGER CHECK(account_revision>0),uid TEXT,download_cid TEXT,download_path TEXT,organize_cid TEXT,organize_path TEXT,CHECK((account_id IS NULL AND account_revision IS NULL AND uid IS NULL AND download_cid IS NULL AND download_path IS NULL) OR (account_id IS NOT NULL AND length(account_id)>0 AND account_revision IS NOT NULL AND uid IS NOT NULL AND length(uid)>0 AND download_cid IS NOT NULL AND length(download_cid)>0 AND download_path IS NOT NULL)),CHECK((organize_cid IS NULL AND organize_path IS NULL) OR (account_id IS NOT NULL AND organize_cid IS NOT NULL AND length(organize_cid)>0 AND organize_path IS NOT NULL)))`,
		`INSERT INTO anime_settings SELECT id,revision,account_id,2,'123',download_cid,download_path,organize_cid,organize_path FROM anime_settings_current_fixture`,
		`DROP TABLE anime_settings_current_fixture`,
		`ALTER TABLE anime_download_jobs ADD COLUMN account_revision INTEGER NOT NULL DEFAULT 2 CHECK(account_revision>0)`,
		`UPDATE anime_download_jobs SET snapshot_json=json_set(snapshot_json,'$.target.accountRevision',2,'$.target.uid','123','$.decision.selection.target.accountRevision',2,'$.decision.selection.target.uid','123')`,
		`UPDATE anime_job_selections SET snapshot_json=json_set(snapshot_json,'$.target.accountRevision',2,'$.target.uid','123','$.decision.selection.target.accountRevision',2,'$.decision.selection.target.uid','123')`,
		`UPDATE anime_release_decisions SET snapshot_json=json_set(snapshot_json,'$.selection.target.accountRevision',2,'$.selection.target.uid','123')`,
		`PRAGMA user_version=64`,
	} {
		if _, err := f.store.db.ExecContext(ctx, query); err != nil {
			t.Fatal(err)
		}
	}
	var filename string
	if err := f.store.db.QueryRowContext(ctx, `SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&filename); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, filename)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got, err := reopened.GetAnimeDownloadJob(ctx, job.ID)
	if err != nil || !reflect.DeepEqual(job, got) {
		t.Fatal("account simplification changed task progress", err)
	}
	gotSettings, err := reopened.GetAnimeSettings(ctx)
	if err != nil || !reflect.DeepEqual(settings, gotSettings) {
		t.Fatal("account simplification lost saved directories", err)
	}
	for _, table := range []string{"anime_settings", "anime_download_jobs"} {
		var n int
		if err := reopened.db.QueryRowContext(ctx, `SELECT count(*) FROM pragma_table_info(?) WHERE name='account_revision'`, table).Scan(&n); err != nil || n != 0 {
			t.Fatal("retired account revision column remains", table, n, err)
		}
	}
	decision, err := reopened.GetAnimeReleaseDecision(ctx, f.sub.Sources[0].ID, release.ID)
	if err != nil || decision.Snapshot.Selection == nil || decision.Snapshot.Selection.Target != job.Snapshot.Target {
		t.Fatal("upgrade lost execution authorization", err)
	}
	if err := reopened.CheckAnimeDownloadAccount(ctx, job.Snapshot.Target); err != nil {
		t.Fatal("upgrade invalidated the original account", err)
	}
}
