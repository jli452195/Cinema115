package storage

import (
	"context"
	"database/sql"
	"fmt"
)

func migrateMediaUpdates(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, "PRAGMA table_info(emby_items)")
	if err != nil {
		return fmt.Errorf("读取 Emby 索引字段: %w", err)
	}
	hasDateCreated := false
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			_ = rows.Close()
			return fmt.Errorf("读取 Emby 索引字段: %w", err)
		}
		hasDateCreated = hasDateCreated || name == "date_created"
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("遍历 Emby 索引字段: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("关闭 Emby 索引字段查询: %w", err)
	}
	if !hasDateCreated {
		if _, err := tx.ExecContext(ctx, "ALTER TABLE emby_items ADD COLUMN date_created TEXT NOT NULL DEFAULT ''"); err != nil {
			return fmt.Errorf("增加 Emby 入库时间字段: %w", err)
		}
	}
	rows, err = tx.QueryContext(ctx, "PRAGMA table_info(emby_state)")
	if err != nil {
		return fmt.Errorf("读取 Emby 状态字段: %w", err)
	}
	hasUpdatesReady := false
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			_ = rows.Close()
			return fmt.Errorf("读取 Emby 状态字段: %w", err)
		}
		hasUpdatesReady = hasUpdatesReady || name == "updates_ready"
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("遍历 Emby 状态字段: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("关闭 Emby 状态字段查询: %w", err)
	}
	if !hasUpdatesReady {
		if _, err := tx.ExecContext(ctx, "ALTER TABLE emby_state ADD COLUMN updates_ready INTEGER NOT NULL DEFAULT 0 CHECK(updates_ready IN (0,1))"); err != nil {
			return fmt.Errorf("增加最近更新就绪状态: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS media_update_items (
 scope TEXT NOT NULL, item_id TEXT NOT NULL, library_id TEXT NOT NULL,
 item_type TEXT NOT NULL, tmdb TEXT NOT NULL, series_id TEXT NOT NULL,
 parent_id TEXT NOT NULL, name TEXT NOT NULL, season INTEGER NOT NULL,
 episode INTEGER NOT NULL, end_episode INTEGER NOT NULL,
 date_created TEXT NOT NULL, added_at INTEGER NOT NULL,
 first_seen_at INTEGER NOT NULL, last_seen_at INTEGER NOT NULL,
 deleted_at INTEGER NOT NULL DEFAULT 0,
 PRIMARY KEY(scope,item_id)
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS media_updates_range ON media_update_items(scope,deleted_at,added_at DESC);
CREATE INDEX IF NOT EXISTS media_updates_series ON media_update_items(scope,series_id,item_type,season,episode);
PRAGMA user_version=17;`); err != nil {
		return fmt.Errorf("迁移最近更新索引: %w", err)
	}
	return nil
}

func migrateEmbySyncCheckpoints(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, "PRAGMA table_info(emby_state)")
	if err != nil {
		return fmt.Errorf("读取 Emby 状态字段: %w", err)
	}
	columns := make(map[string]bool)
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			_ = rows.Close()
			return fmt.Errorf("读取 Emby 状态字段: %w", err)
		}
		columns[name] = true
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("遍历 Emby 状态字段: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("关闭 Emby 状态字段查询: %w", err)
	}
	definitions := []struct {
		name string
		sql  string
	}{
		{"updates_ready", "ALTER TABLE emby_state ADD COLUMN updates_ready INTEGER NOT NULL DEFAULT 0 CHECK(updates_ready IN (0,1))"},
		{"staging_generation", "ALTER TABLE emby_state ADD COLUMN staging_generation TEXT NOT NULL DEFAULT ''"},
		{"staging_complete", "ALTER TABLE emby_state ADD COLUMN staging_complete INTEGER NOT NULL DEFAULT 0 CHECK(staging_complete IN (0,1))"},
		{"staging_cutoff", "ALTER TABLE emby_state ADD COLUMN staging_cutoff INTEGER NOT NULL DEFAULT 0"},
		{"updates_generation", "ALTER TABLE emby_state ADD COLUMN updates_generation TEXT NOT NULL DEFAULT ''"},
		{"updates_cursor", "ALTER TABLE emby_state ADD COLUMN updates_cursor TEXT NOT NULL DEFAULT ''"},
		{"updates_started_at", "ALTER TABLE emby_state ADD COLUMN updates_started_at INTEGER NOT NULL DEFAULT 0"},
		{"updates_error", "ALTER TABLE emby_state ADD COLUMN updates_error TEXT NOT NULL DEFAULT ''"},
	}
	for _, definition := range definitions {
		if columns[definition.name] {
			continue
		}
		if _, err := tx.ExecContext(ctx, definition.sql); err != nil {
			return fmt.Errorf("增加 Emby 检查点字段 %s: %w", definition.name, err)
		}
	}
	if _, err := tx.ExecContext(ctx, "PRAGMA user_version=18"); err != nil {
		return fmt.Errorf("保存 Emby 检查点迁移版本: %w", err)
	}
	return nil
}
