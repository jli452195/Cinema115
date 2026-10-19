package storage

import (
	"context"
	"database/sql"
	"fmt"
)

// Only submit operations exist in P2-04. Directory creation and P3 operations
// are installed with their own stages rather than adding unused foreign keys.
const animeSubmitSchemaV47 = `
CREATE TABLE IF NOT EXISTS anime_drive_operations (
 id TEXT PRIMARY KEY NOT NULL,
 download_job_id TEXT NOT NULL REFERENCES anime_download_jobs(id),
 operation_key TEXT NOT NULL UNIQUE,
 kind TEXT NOT NULL CHECK(kind='submit'),
 state TEXT NOT NULL CHECK(state IN ('prepared','dispatched','confirmed','unknown','failed')),
 generation INTEGER NOT NULL CHECK(generation>0),
 request_json TEXT NOT NULL CHECK(json_valid(request_json)),
 result_json TEXT CHECK(result_json IS NULL OR json_valid(result_json)),
 error_code TEXT,
 authorized_at INTEGER,
 updated_at INTEGER NOT NULL,
 UNIQUE(download_job_id,kind),
 CHECK(state NOT IN ('dispatched','confirmed','unknown') OR authorized_at IS NOT NULL)
);
`

func upgradeAnimeSubmitV47(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, animeSubmitSchemaV47); err != nil {
		return fmt.Errorf("create anime submit operations: %w", err)
	}
	var kind string
	if err := tx.QueryRowContext(ctx, `SELECT type FROM sqlite_master WHERE name='anime_drive_operations'`).Scan(&kind); err != nil {
		return err
	}
	if kind != "table" {
		return fmt.Errorf("unexpected anime submit schema object")
	}
	return nil
}
