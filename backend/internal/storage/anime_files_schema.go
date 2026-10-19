package storage

import (
	"context"
	"database/sql"
	"fmt"
)

// Keep directory recovery and the file snapshot on the existing download job.
// No second file registry or scheduler is needed for subscription downloads.
func upgradeAnimeFilesV50(ctx context.Context, tx *sql.Tx) error {
	for _, name := range []string{"directory_operation_json", "files_json"} {
		var exists int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM pragma_table_info('anime_download_jobs') WHERE name=?`, name).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			if _, err := tx.ExecContext(ctx, `ALTER TABLE anime_download_jobs ADD COLUMN `+name+` TEXT CHECK(`+name+` IS NULL OR json_valid(`+name+`))`); err != nil {
				return fmt.Errorf("add anime job file snapshot: %w", err)
			}
		}
	}
	return nil
}
