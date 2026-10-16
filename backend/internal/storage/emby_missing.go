package storage

import (
	"context"
	"fmt"

	"strm115/backend/internal/model"
)

func (s *Store) EmbyMissingIncompleteEpisodes(ctx context.Context, scope, generation string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM emby_items
 WHERE snapshot_id=(SELECT id FROM emby_snapshots WHERE scope=? AND generation=?)
 AND type='Episode' AND season<>0 AND (series_id='' OR season<0 OR episode<0)`, scope, generation).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("读取缺集检测分集资料状态: %w", err)
	}
	return count, nil
}

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

// EmbyMissingItems reads only the published generation; staging rows never enter a report.
// Episode TMDB IDs identify episodes, so show identity always comes from the Series row.
func (s *Store) EmbyMissingItems(ctx context.Context, scope, generation string) ([]model.EmbyItem, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,type,tmdb,series_id,library_id,
 CASE WHEN type='Series' THEN name ELSE '' END,season,episode,end_episode
 FROM emby_items WHERE snapshot_id=(SELECT id FROM emby_snapshots WHERE scope=? AND generation=?)
 AND (type='Series' OR (type='Episode' AND series_id<>'' AND season>0 AND episode>0))
 ORDER BY id`, scope, generation)
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
