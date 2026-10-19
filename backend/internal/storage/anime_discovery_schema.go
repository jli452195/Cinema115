package storage

import (
	"context"
	"database/sql"
	"fmt"
)

func upgradeAnimeDiscoveryV48(ctx context.Context, tx *sql.Tx) error {
	for _, column := range []struct{ table, name, definition string }{
		{"anime_subscription_sources", "poll_failures", "INTEGER NOT NULL DEFAULT 0 CHECK(poll_failures BETWEEN 0 AND 4)"},
		{"anime_subscription_sources", "check_until", "INTEGER"},
		{"anime_subscription_sources", "poll_snapshot_json", "TEXT CHECK(poll_snapshot_json IS NULL OR json_valid(poll_snapshot_json))"},
		{"anime_release_decisions", "observation_json", "TEXT CHECK(observation_json IS NULL OR json_valid(observation_json))"},
		{"anime_release_decisions", "verify_failures", "INTEGER NOT NULL DEFAULT 0 CHECK(verify_failures BETWEEN 0 AND 4)"},
	} {
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM pragma_table_info(?) WHERE name=?`, column.table, column.name).Scan(&count); err != nil {
			return err
		}
		if count == 0 {
			if _, err := tx.ExecContext(ctx, `ALTER TABLE `+column.table+` ADD COLUMN `+column.name+` `+column.definition); err != nil {
				return fmt.Errorf("add anime discovery column %s: %w", column.name, err)
			}
		}
	}
	_, err := tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS anime_discovery_pending ON anime_release_decisions(disposition,next_verify_at,lease_until) WHERE observation_json IS NOT NULL`)
	return err
}
