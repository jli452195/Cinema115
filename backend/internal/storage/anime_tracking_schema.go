package storage

import (
	"context"
	"database/sql"
	"fmt"
)

// The observation/result/lease columns were reserved in v46. Only an index is
// needed to bound per-UID in-flight/cooldown lookups for the status scheduler.
func upgradeAnimeTrackingV49(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS anime_jobs_status_uid ON anime_download_jobs(uid,lease_until,next_run_at) WHERE workflow_state='active' AND file_state<>'verified'`); err != nil {
		return fmt.Errorf("index anime status reads: %w", err)
	}
	var table, kind string
	if err := tx.QueryRowContext(ctx, `SELECT tbl_name,type FROM sqlite_master WHERE name='anime_jobs_status_uid'`).Scan(&table, &kind); err != nil {
		return err
	}
	if table != "anime_download_jobs" || kind != "index" {
		return fmt.Errorf("unexpected anime status index schema")
	}
	return nil
}
