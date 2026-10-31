package storage

import (
	"context"
	"encoding/json"
	"fmt"

	"strm115/backend/internal/model"
)

func (s *Store) EmbyPublishedSeries(ctx context.Context, scope, id string) (bool, error) {
	var exists bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS (
 SELECT 1 FROM emby_items i JOIN emby_snapshots p ON p.id=i.snapshot_id
 JOIN emby_state s ON s.scope=p.scope AND s.active=p.generation
 WHERE p.scope=? AND i.id=? AND i.type='Series'
)`, scope, id).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("读取 Emby 海报归属: %w", err)
	}
	return exists, nil
}

// A nil selection builds the report once. Incremental selections include the
// current same-TMDB sources, including versions in another selected library.
// Incomplete episodes are included so their notice count is cached as well.
func (s *Store) EmbyMissingItems(ctx context.Context, scope, generation string, seriesIDs []string) ([]model.EmbyItem, error) {
	const columns = `id,type,tmdb,series_id,library_id,
 CASE WHEN type='Series' THEN name ELSE '' END,season,episode,end_episode
 FROM emby_items`
	query := `WITH snapshot AS (SELECT id FROM emby_snapshots WHERE scope=? AND generation=?)
 SELECT ` + columns + ` WHERE snapshot_id=(SELECT id FROM snapshot)
 AND type IN ('Series','Episode') ORDER BY id`
	args := []any{scope, generation}
	if seriesIDs != nil {
		data, err := json.Marshal(seriesIDs)
		if err != nil {
			return nil, fmt.Errorf("编码缺集检测来源: %w", err)
		}
		// ORDER BY id can otherwise make SQLite scan the whole snapshot's primary
		// key. Keep episode reads on the selected sources' series index.
		query = `WITH snapshot AS (SELECT id FROM emby_snapshots WHERE scope=? AND generation=?),
 requested AS (SELECT value AS id FROM json_each(?)),
 sources AS (
 SELECT id FROM requested UNION
 SELECT id FROM emby_items WHERE snapshot_id=(SELECT id FROM snapshot) AND type='Series'
 AND tmdb IN (
 SELECT i.tmdb FROM requested CROSS JOIN emby_items i
 WHERE i.snapshot_id=(SELECT id FROM snapshot) AND i.id=requested.id AND i.type='Series'
 AND CAST(i.tmdb AS INTEGER)>0 AND CAST(CAST(i.tmdb AS INTEGER) AS TEXT)=i.tmdb
 ))
 SELECT ` + columns + ` WHERE snapshot_id=(SELECT id FROM snapshot)
 AND type='Series' AND id IN (SELECT id FROM sources)
 UNION ALL SELECT ` + columns + ` INDEXED BY emby_series WHERE snapshot_id=(SELECT id FROM snapshot)
 AND type='Episode' AND series_id IN (SELECT id FROM sources)
 ORDER BY id`
		args = append(args, string(data))
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("读取缺集检测索引: %w", err)
	}
	defer rows.Close()
	items := make([]model.EmbyItem, 0)
	for rows.Next() {
		var item model.EmbyItem
		if err := rows.Scan(&item.ID, &item.Type, &item.TMDB, &item.SeriesID, &item.LibraryID, &item.Name, &item.Season, &item.Episode, &item.EndEpisode); err != nil {
			return nil, fmt.Errorf("读取缺集检测条目: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历缺集检测索引: %w", err)
	}
	return items, nil
}

func (s *Store) EmbyMissingExclusions(ctx context.Context, service string) ([]model.EmbyMissingExclusion, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,series_id,tmdb,title,library_id,episode_count,excluded_at
 FROM emby_missing_exclusions WHERE service=? ORDER BY excluded_at DESC,id`, service)
	if err != nil {
		return nil, fmt.Errorf("读取缺集排除记录: %w", err)
	}
	defer rows.Close()
	items := make([]model.EmbyMissingExclusion, 0)
	for rows.Next() {
		var item model.EmbyMissingExclusion
		if err := rows.Scan(&item.ID, &item.SeriesID, &item.TMDBID, &item.Title, &item.LibraryID, &item.EpisodeCount, &item.ExcludedAt); err != nil {
			return nil, fmt.Errorf("读取缺集排除条目: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历缺集排除记录: %w", err)
	}
	return items, nil
}

// Exclusions are independent of snapshots: sync, event processing and snapshot cleanup
// must not erase the user's decision. All sources in a confirmation commit together.
func (s *Store) SaveEmbyMissingExclusions(ctx context.Context, service string, items []model.EmbyMissingExclusion) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开始保存缺集排除记录: %w", err)
	}
	defer tx.Rollback()
	for _, item := range items {
		if _, err := tx.ExecContext(ctx, `INSERT INTO emby_missing_exclusions
 (id,service,series_id,tmdb,title,library_id,episode_count,excluded_at) VALUES(?,?,?,?,?,?,?,?)`,
			item.ID, service, item.SeriesID, item.TMDBID, item.Title, item.LibraryID, item.EpisodeCount, item.ExcludedAt); err != nil {
			return fmt.Errorf("保存缺集排除条目: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交缺集排除记录: %w", err)
	}
	return nil
}

func (s *Store) RestoreEmbyMissingExclusion(ctx context.Context, service, id string) (bool, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM emby_missing_exclusions WHERE service=? AND id=?`, service, id)
	if err != nil {
		return false, fmt.Errorf("恢复缺集检测: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("读取缺集恢复结果: %w", err)
	}
	return n > 0, nil
}
