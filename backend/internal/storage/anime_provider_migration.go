package storage

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// SQLite cannot alter CHECK constraints. Rebuild only these two tables, in the
// startup transaction on a dedicated connection with FK enforcement suspended.
// IDs, snapshots, indices and all referencing rows are preserved and checked
// before commit. No prototype data or source ownership is changed.
func upgradeAnimeProvidersV43(ctx context.Context, tx *sql.Tx) error {
	for _, table := range []string{"anime_subscription_sources", "anime_releases"} {
		var ddl string
		if err := tx.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&ddl); err != nil {
			return fmt.Errorf("read anime provider schema: %w", err)
		}
		if !strings.Contains(ddl, "CHECK(provider='mikan')") {
			continue
		}
		rows, err := tx.QueryContext(ctx, `SELECT sql FROM sqlite_master WHERE type='index' AND tbl_name=? AND sql IS NOT NULL`, table)
		if err != nil {
			return err
		}
		indexes := make([]string, 0, 4)
		for rows.Next() {
			var index string
			if err := rows.Scan(&index); err != nil {
				rows.Close()
				return err
			}
			indexes = append(indexes, index)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
		start := strings.Index(ddl, "(")
		if start < 0 {
			return fmt.Errorf("unexpected anime table schema")
		}
		copyName := table + "_v43"
		ddl = "CREATE TABLE " + copyName + " " + strings.ReplaceAll(ddl[start:], "CHECK(provider='mikan')", "CHECK(provider IN ('mikan','anibt'))")
		for _, statement := range []string{ddl, "INSERT INTO " + copyName + " SELECT * FROM " + table, "DROP TABLE " + table, "ALTER TABLE " + copyName + " RENAME TO " + table} {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("extend anime provider table %s: %w", table, err)
			}
		}
		for _, index := range indexes {
			if _, err := tx.ExecContext(ctx, index); err != nil {
				return fmt.Errorf("restore anime provider index: %w", err)
			}
		}
	}
	rows, err := tx.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return err
	}
	defer rows.Close()
	if rows.Next() {
		return fmt.Errorf("anime provider migration found invalid foreign keys")
	}
	return rows.Err()
}
