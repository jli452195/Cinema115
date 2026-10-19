package storage

import (
	"context"
	"database/sql"
	"fmt"
)

// P2-02 installs identities and intents only. Workers, drive operations and
// verified files are added at their own stages, not implicitly started here.
const animeDownloadSchemaV46 = `
CREATE TABLE IF NOT EXISTS anime_resources (
 id TEXT PRIMARY KEY NOT NULL,
 protocol TEXT NOT NULL CHECK(protocol='bt_v1'),
 info_hash TEXT NOT NULL CHECK(length(info_hash)=40 AND info_hash NOT GLOB '*[^0-9a-f]*'),
 verification_version INTEGER NOT NULL CHECK(verification_version>0),
 verified_at INTEGER NOT NULL,
 evidence_json TEXT NOT NULL CHECK(json_valid(evidence_json)),
 UNIQUE(protocol,info_hash)
);
CREATE TABLE IF NOT EXISTS anime_resource_claims (
 id TEXT PRIMARY KEY NOT NULL,
 uid TEXT NOT NULL CHECK(length(uid)>0),
 resource_id TEXT NOT NULL REFERENCES anime_resources(id),
 account_id TEXT NOT NULL,
 target_cid TEXT NOT NULL,
 created_at INTEGER NOT NULL,
 UNIQUE(uid,resource_id)
);
CREATE TABLE IF NOT EXISTS anime_download_jobs (
 id TEXT PRIMARY KEY NOT NULL,
 claim_id TEXT NOT NULL REFERENCES anime_resource_claims(id),
 attempt INTEGER NOT NULL CHECK(attempt>0),
 account_id TEXT NOT NULL,
 account_revision INTEGER NOT NULL CHECK(account_revision>0),
 uid TEXT NOT NULL,
 settings_revision INTEGER NOT NULL CHECK(settings_revision>0),
 parent_cid TEXT NOT NULL,
 directory_name TEXT NOT NULL,
 directory_cid TEXT,
 snapshot_json TEXT NOT NULL CHECK(json_valid(snapshot_json)),
 download_state TEXT NOT NULL CHECK(download_state IN ('queued','submitting','unknown','accepted','downloading','completed','failed','blocked','cancelled')),
 file_state TEXT NOT NULL CHECK(file_state IN ('pending','checking','verified','needs_review')),
 workflow_state TEXT NOT NULL CHECK(workflow_state IN ('active','original_saved','organized','cancelled')),
 block_code TEXT,
 raw_status_json TEXT CHECK(raw_status_json IS NULL OR json_valid(raw_status_json)),
 result_object_id TEXT,
 result_object_kind TEXT CHECK(result_object_kind IN ('file','directory','unknown')),
 revision INTEGER NOT NULL CHECK(revision>0),
 generation INTEGER NOT NULL CHECK(generation>0),
 lease_token TEXT,
 lease_until INTEGER,
 next_run_at INTEGER,
 created_at INTEGER NOT NULL,
 updated_at INTEGER NOT NULL,
 UNIQUE(claim_id,attempt),
 CHECK((lease_token IS NULL)=(lease_until IS NULL)),
 CHECK(workflow_state='active' OR (next_run_at IS NULL AND lease_token IS NULL)),
 CHECK(workflow_state NOT IN ('original_saved','organized') OR (download_state='completed' AND file_state='verified'))
);
CREATE UNIQUE INDEX IF NOT EXISTS anime_jobs_live_claim ON anime_download_jobs(claim_id)
 WHERE workflow_state='active' AND download_state NOT IN ('failed','cancelled');
CREATE INDEX IF NOT EXISTS anime_jobs_due ON anime_download_jobs(workflow_state,next_run_at);
CREATE TABLE IF NOT EXISTS anime_job_selections (
 job_id TEXT NOT NULL REFERENCES anime_download_jobs(id),
 source_id TEXT NOT NULL REFERENCES anime_subscription_sources(id),
 release_id TEXT NOT NULL REFERENCES anime_releases(id),
 status TEXT NOT NULL CHECK(status IN ('authorized','withdrawn')),
 source_revision INTEGER NOT NULL CHECK(source_revision>0),
 snapshot_json TEXT NOT NULL CHECK(json_valid(snapshot_json)),
 created_at INTEGER NOT NULL,
 PRIMARY KEY(job_id,source_id,release_id)
);
CREATE INDEX IF NOT EXISTS anime_selections_release ON anime_job_selections(source_id,release_id);
`

func upgradeAnimeDownloadsV46(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, animeDownloadSchemaV46); err != nil {
		return fmt.Errorf("create anime download intents: %w", err)
	}
	for _, name := range []string{"anime_resources", "anime_resource_claims", "anime_download_jobs", "anime_job_selections"} {
		var kind string
		if err := tx.QueryRowContext(ctx, `SELECT type FROM sqlite_master WHERE name=?`, name).Scan(&kind); err != nil {
			return fmt.Errorf("check anime download table: %w", err)
		}
		if kind != "table" {
			return fmt.Errorf("unexpected anime download schema object %s", name)
		}
	}
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM pragma_table_info('anime_releases') WHERE name='resource_id'`).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		if _, err := tx.ExecContext(ctx, `ALTER TABLE anime_releases ADD COLUMN resource_id TEXT REFERENCES anime_resources(id)`); err != nil {
			return fmt.Errorf("add verified anime resource association: %w", err)
		}
	}
	return nil
}
