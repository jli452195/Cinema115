package storage

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"
)

// Empty previews need configuration/window/choice snapshots independently of
// their zero-or-more items. Inspect columns so replayed legacy schema repairs
// do not attempt to add the same column twice.
func upgradeAnimePreviewsV40(ctx context.Context, tx *sql.Tx) error {
	for _, table := range []string{"anime_preview_batches", "anime_source_previews"} {
		columns, err := animeMigrationColumns(ctx, tx, table)
		if err != nil {
			return err
		}
		if slices.Contains(columns, "snapshot_json") {
			continue
		}
		if _, err := tx.ExecContext(ctx, `ALTER TABLE `+table+` ADD COLUMN snapshot_json TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(snapshot_json))`); err != nil {
			return fmt.Errorf("add %s snapshot: %w", table, err)
		}
	}
	return nil
}

// The user explicitly discarded the retired anime prototype and requested all
// of its data removed. Validate its known layout before dropping only those
// tables; the current subscription/download design must remain untouched.
func discardLegacyAnimeV38(ctx context.Context, tx *sql.Tx) error {
	subColumns, err := animeMigrationColumns(ctx, tx, "anime_subscriptions")
	if err != nil {
		return err
	}
	if len(subColumns) == 0 || slices.Contains(subColumns, "entry_id") {
		return nil
	}
	tables := []struct {
		name, columns string
		exists        bool
	}{
		{name: "anime_subscriptions", columns: "id,tmdb_id,bangumi_id,group_id,revision,enabled,due,record"},
		{name: "anime_releases", columns: "subscription_id,hash,status,record"},
		{name: "anime_download_claims", columns: "account_id,hash,target_key,subscription_id"},
		{name: "anime_transfers", columns: "subscription_id,hash,revision,state,due,record"},
	}
	// Validate every source before dropping anything. Unknown or mixed layouts
	// must be inspected rather than partially deleted.
	for i := range tables {
		table := &tables[i]
		columns, err := animeMigrationColumns(ctx, tx, table.name)
		if err != nil {
			return err
		}
		if len(columns) == 0 {
			continue
		}
		if strings.Join(columns, ",") != table.columns {
			return fmt.Errorf("旧动漫表 %s 结构不匹配，已取消升级并保留原数据", table.name)
		}
		table.exists = true
	}
	// Child releases reference subscriptions. Drop the parent last with FK
	// enforcement enabled, in the same transaction as the replacement schema.
	for i := len(tables) - 1; i >= 0; i-- {
		table := tables[i]
		if !table.exists {
			continue
		}
		if _, err := tx.ExecContext(ctx, `DROP TABLE `+table.name); err != nil {
			return fmt.Errorf("清理废弃动漫表 %s: %w", table.name, err)
		}
	}
	return nil
}

// Version 39 previously retained prototype data under these exact names.
// Version 41 removes those archives as requested, never the new anime tables.
func discardArchivedAnimeV38(ctx context.Context, tx *sql.Tx) error {
	for _, table := range []string{"legacy_anime_v38_releases", "legacy_anime_v38_download_claims", "legacy_anime_v38_transfers", "legacy_anime_v38_subscriptions"} {
		if _, err := tx.ExecContext(ctx, `DROP TABLE IF EXISTS `+table); err != nil {
			return fmt.Errorf("清理废弃动漫归档 %s: %w", table, err)
		}
	}
	return nil
}

func animeMigrationColumns(ctx context.Context, tx *sql.Tx, table string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT name FROM pragma_table_info(?) ORDER BY cid`, table)
	if err != nil {
		return nil, fmt.Errorf("读取动漫表 %s 结构: %w", table, err)
	}
	defer rows.Close()
	columns := make([]string, 0, 16)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("读取动漫表 %s 列: %w", table, err)
		}
		columns = append(columns, name)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历动漫表 %s 列: %w", table, err)
	}
	return columns, nil
}
