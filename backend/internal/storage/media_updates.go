package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"strm115/backend/internal/model"
)

const mediaUpdateUpsert = `ON CONFLICT(scope,item_id) DO UPDATE SET
 library_id=excluded.library_id,item_type=excluded.item_type,tmdb=excluded.tmdb,
 series_id=excluded.series_id,parent_id=excluded.parent_id,name=excluded.name,
 season=excluded.season,episode=excluded.episode,end_episode=excluded.end_episode,
 added_at=CASE WHEN media_update_items.deleted_at>0 THEN excluded.added_at WHEN media_update_items.date_created='' AND excluded.date_created<>'' THEN excluded.added_at ELSE media_update_items.added_at END,
 date_created=CASE WHEN media_update_items.deleted_at>0 THEN excluded.date_created WHEN media_update_items.date_created='' AND excluded.date_created<>'' THEN excluded.date_created ELSE media_update_items.date_created END,
 last_seen_at=excluded.last_seen_at,deleted_at=0`

func upsertMediaUpdates(ctx context.Context, tx *sql.Tx, scope string, items []model.EmbyItem, now int64) error {
	query := `INSERT INTO media_update_items(
 scope,item_id,library_id,item_type,tmdb,series_id,parent_id,name,season,episode,end_episode,
 date_created,added_at,first_seen_at,last_seen_at,deleted_at)
 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,COALESCE(unixepoch(NULLIF(?,'')),?),?,?,0) ` + mediaUpdateUpsert
	stmt, err := tx.PrepareContext(ctx, query)
	if err != nil {
		return fmt.Errorf("准备最近更新写入: %w", err)
	}
	defer stmt.Close()
	for _, item := range items {
		if _, err := stmt.ExecContext(ctx, scope, item.ID, item.LibraryID, item.Type, item.TMDB, item.SeriesID, item.ParentID, item.Name, item.Season, item.Episode, item.EndEpisode, item.DateCreated, item.DateCreated, now, now, now); err != nil {
			return fmt.Errorf("写入最近更新条目: %w", err)
		}
	}
	return nil
}

// EmbyBuildMediaUpdates incrementally derives the update timeline from an already-published snapshot.
func (s *Store) EmbyBuildMediaUpdates(ctx context.Context, scope, generation string, batch int) (bool, error) {
	if batch < 1 || batch > 5000 {
		return false, fmt.Errorf("最近更新构建批量无效")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("开始构建最近更新: %w", err)
	}
	defer tx.Rollback()
	var active, target, cursor string
	var started int64
	var ready bool
	if err := tx.QueryRowContext(ctx, `SELECT active,updates_generation,updates_cursor,updates_started_at,updates_ready FROM emby_state WHERE scope=?`, scope).Scan(&active, &target, &cursor, &started, &ready); err != nil {
		return false, fmt.Errorf("读取最近更新构建状态: %w", err)
	}
	if active != generation || target != generation || ready {
		return true, nil
	}
	if started <= 0 {
		started = time.Now().UTC().Unix()
		if _, err := tx.ExecContext(ctx, `UPDATE emby_state SET updates_started_at=? WHERE scope=? AND active=? AND updates_generation=?`, started, scope, generation, generation); err != nil {
			return false, fmt.Errorf("修复最近更新构建时间: %w", err)
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT i.id,i.library_id,i.type,i.tmdb,i.series_id,i.parent_id,i.name,i.season,i.episode,i.end_episode,i.date_created
FROM emby_items i JOIN emby_snapshots p ON p.id=i.snapshot_id
WHERE p.scope=? AND p.generation=? AND i.id>? ORDER BY i.id LIMIT ?`, scope, generation, cursor, batch)
	if err != nil {
		return false, fmt.Errorf("读取最近更新构建批次: %w", err)
	}
	items := make([]model.EmbyItem, 0, batch)
	for rows.Next() {
		var item model.EmbyItem
		if err := rows.Scan(&item.ID, &item.LibraryID, &item.Type, &item.TMDB, &item.SeriesID, &item.ParentID, &item.Name, &item.Season, &item.Episode, &item.EndEpisode, &item.DateCreated); err != nil {
			_ = rows.Close()
			return false, fmt.Errorf("读取最近更新构建条目: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return false, fmt.Errorf("遍历最近更新构建批次: %w", err)
	}
	if err := rows.Close(); err != nil {
		return false, fmt.Errorf("关闭最近更新构建批次: %w", err)
	}
	if len(items) > 0 {
		if err := upsertMediaUpdates(ctx, tx, scope, items, started); err != nil {
			return false, err
		}
		cursor = items[len(items)-1].ID
		if _, err := tx.ExecContext(ctx, `UPDATE emby_state SET updates_cursor=?,updates_error='' WHERE scope=? AND active=? AND updates_generation=?`, cursor, scope, generation, generation); err != nil {
			return false, fmt.Errorf("保存最近更新构建进度: %w", err)
		}
	}
	done := len(items) < batch
	if done {
		if _, err := tx.ExecContext(ctx, `UPDATE media_update_items SET deleted_at=? WHERE scope=? AND deleted_at=0 AND NOT EXISTS (
 SELECT 1 FROM emby_items i JOIN emby_snapshots p ON p.id=i.snapshot_id
 WHERE p.scope=? AND p.generation=? AND i.id=media_update_items.item_id)`, started, scope, scope, generation); err != nil {
			return false, fmt.Errorf("隐藏已移除的最近更新: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE emby_state SET updates_ready=1,updates_cursor='',updates_error='' WHERE scope=? AND active=? AND updates_generation=?`, scope, generation, generation); err != nil {
			return false, fmt.Errorf("完成最近更新构建: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("提交最近更新构建批次: %w", err)
	}
	return done, nil
}

func (s *Store) EmbyMediaUpdatesError(ctx context.Context, scope, generation, message string) error {
	return s.embyExec(ctx, `UPDATE emby_state SET updates_error=? WHERE scope=? AND active=? AND updates_generation=?`, message, scope, generation, generation)
}

func deleteMediaUpdates(ctx context.Context, tx *sql.Tx, scope, itemID string, now int64) error {
	if _, err := tx.ExecContext(ctx, `UPDATE media_update_items SET deleted_at=? WHERE scope=? AND deleted_at=0 AND (item_id=? OR series_id=? OR parent_id=? OR library_id=?)`, now, scope, itemID, itemID, itemID, itemID); err != nil {
		return fmt.Errorf("移除最近更新条目: %w", err)
	}
	return nil
}

func (s *Store) MediaUpdatesReady(ctx context.Context, scope, generation string) (bool, error) {
	var ready bool
	err := s.db.QueryRowContext(ctx, `SELECT updates_ready FROM emby_state WHERE scope=? AND active=? AND updates_generation=active`, scope, generation).Scan(&ready)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("检查最近更新索引: %w", err)
	}
	return ready, nil
}

func (s *Store) MediaUpdateGroups(ctx context.Context, scope string, start, end int64, limit, offset int) ([]model.MediaUpdateGroup, error) {
	rows, err := s.db.QueryContext(ctx, `WITH candidates AS (
 SELECT date(u.added_at,'unixepoch','+8 hours') date_key,'movie' kind,
  CAST(u.tmdb AS INTEGER) tmdb_id,-1 season,u.name fallback_title,u.added_at,-1 episode,-1 end_episode
 FROM media_update_items u
 WHERE u.scope=? AND u.deleted_at=0 AND u.item_type='Movie' AND CAST(u.tmdb AS INTEGER)>0 AND u.added_at>=? AND u.added_at<?
 UNION ALL
 SELECT date(u.added_at,'unixepoch','+8 hours') date_key,'tv' kind,
  CAST(series.tmdb AS INTEGER) tmdb_id,u.season season,series.name fallback_title,u.added_at,u.episode,u.end_episode
 FROM media_update_items u
 JOIN media_update_items series ON series.scope=u.scope AND series.item_id=u.series_id AND series.item_type='Series' AND series.deleted_at=0
 WHERE u.scope=? AND u.deleted_at=0 AND u.item_type='Episode' AND CAST(series.tmdb AS INTEGER)>0
  AND u.season>=0 AND u.episode>=0 AND u.added_at>=? AND u.added_at<?
), grouped AS (
 SELECT date_key,kind,tmdb_id,season,MAX(fallback_title) fallback_title,MAX(added_at) updated_at,
  json_group_array(json_array(episode,end_episode)) episodes
 FROM candidates GROUP BY date_key,kind,tmdb_id,season
)
SELECT date_key,kind,tmdb_id,season,fallback_title,updated_at,episodes
FROM grouped ORDER BY date_key DESC,updated_at DESC,kind,tmdb_id,season LIMIT ? OFFSET ?`, scope, start, end, scope, start, end, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("查询最近更新: %w", err)
	}
	defer rows.Close()
	groups := make([]model.MediaUpdateGroup, 0, limit)
	for rows.Next() {
		var group model.MediaUpdateGroup
		var encoded []byte
		if err := rows.Scan(&group.Date, &group.Kind, &group.TMDBID, &group.Season, &group.FallbackTitle, &group.UpdatedAt, &encoded); err != nil {
			return nil, fmt.Errorf("读取最近更新: %w", err)
		}
		var pairs [][2]int
		if err := json.Unmarshal(encoded, &pairs); err != nil {
			return nil, fmt.Errorf("解析最近更新集数: %w", err)
		}
		if group.Kind == "tv" {
			group.Episodes = make([]model.MediaUpdateEpisode, 0, len(pairs))
			for _, pair := range pairs {
				group.Episodes = append(group.Episodes, model.MediaUpdateEpisode{First: pair[0], Last: pair[1]})
			}
		}
		groups = append(groups, group)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历最近更新: %w", err)
	}
	return groups, nil
}
