package storage

import (
	"context"
	"database/sql"
	"fmt"
)

// 长范围标识和快照标识每批只保存一次；季集号直接存列，保留所有独立版本。
func migrateMediaCache(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
CREATE TABLE emby_snapshots (id INTEGER PRIMARY KEY, scope TEXT NOT NULL, generation TEXT NOT NULL, UNIQUE(scope,generation));
INSERT INTO emby_snapshots(scope,generation) SELECT DISTINCT scope,generation FROM emby_items;
CREATE TABLE emby_items_new (
 snapshot_id INTEGER NOT NULL REFERENCES emby_snapshots(id) ON DELETE CASCADE,
 id TEXT NOT NULL, library_id TEXT NOT NULL, type TEXT NOT NULL, tmdb TEXT NOT NULL,
 series_id TEXT NOT NULL, parent_id TEXT NOT NULL, name TEXT NOT NULL,
 season INTEGER NOT NULL, episode INTEGER NOT NULL, end_episode INTEGER NOT NULL,
 PRIMARY KEY(snapshot_id,id)
) WITHOUT ROWID;
INSERT INTO emby_items_new
SELECT s.id,i.id,i.library_id,i.type,i.tmdb,i.series_id,i.parent_id,
 COALESCE(json_extract(i.record,'$.name'),''),COALESCE(json_extract(i.record,'$.season'),-1),
 COALESCE(json_extract(i.record,'$.episode'),-1),COALESCE(json_extract(i.record,'$.endEpisode'),-1)
FROM emby_items i JOIN emby_snapshots s ON s.scope=i.scope AND s.generation=i.generation;
DROP TABLE emby_items;
ALTER TABLE emby_items_new RENAME TO emby_items;
CREATE INDEX emby_tmdb ON emby_items(snapshot_id,type,tmdb);
CREATE INDEX emby_series ON emby_items(snapshot_id,series_id,season,episode);
CREATE TABLE tmdb_cache (key TEXT PRIMARY KEY NOT NULL, data BLOB NOT NULL, fetched_at INTEGER NOT NULL) WITHOUT ROWID;
PRAGMA user_version=4;`)
	if err != nil {
		return fmt.Errorf("迁移精简媒体索引与 TMDB 缓存: %w", err)
	}
	return nil
}
