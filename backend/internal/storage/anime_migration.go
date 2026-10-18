package storage

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"
)

// The retired anime prototype used two of the P1 table names at version 38.
// Preserve its configuration and execution history verbatim in archive tables;
// they cannot be safely interpreted as confirmed sources or executable P1 work.
// The caller's schema transaction also covers these renames and all new DDL.
func archiveLegacyAnimeV38(ctx context.Context, tx *sql.Tx) error {
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
	// Validate every source and archive name before renaming anything. Unknown or
	// mixed layouts must be inspected rather than partially repaired or dropped.
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
		archive := "legacy_anime_v38_" + strings.TrimPrefix(table.name, "anime_")
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE name=?`, archive).Scan(&count); err != nil {
			return fmt.Errorf("检查动漫归档表 %s: %w", archive, err)
		}
		if count != 0 {
			return fmt.Errorf("动漫归档名 %s 已存在，已取消升级并保留原数据", archive)
		}
	}
	for _, table := range tables {
		if !table.exists {
			continue
		}
		archive := "legacy_anime_v38_" + strings.TrimPrefix(table.name, "anime_")
		// Both identifiers come only from the fixed table list above. SQLite
		// updates the old releases FK to reference the archived subscriptions.
		if _, err := tx.ExecContext(ctx, `ALTER TABLE `+table.name+` RENAME TO `+archive); err != nil {
			return fmt.Errorf("归档旧动漫表 %s: %w", table.name, err)
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
