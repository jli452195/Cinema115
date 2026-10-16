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

func (s *Store) embyExec(ctx context.Context, query string, args ...any) error {
	if _, err := s.db.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("写入 Emby 数据: %w", err)
	}
	return nil
}
func (s *Store) EmbyState(ctx context.Context, scope string) (model.EmbyState, error) {
	v := model.EmbyState{Scope: scope, Status: "idle"}
	err := s.db.QueryRowContext(ctx, `SELECT active,status,count,scanned,synced_at,last_error,webhook_at,webhook_event,webhook_error,
staging_generation,staging_complete,staging_cutoff,updates_generation,updates_cursor,updates_started_at,updates_ready,updates_error
FROM emby_state WHERE scope=?`, scope).Scan(&v.Active, &v.Status, &v.Count, &v.Scanned, &v.SyncedAt, &v.LastError, &v.WebhookAt, &v.WebhookEvent, &v.WebhookError,
		&v.StagingGeneration, &v.StagingComplete, &v.StagingCutoff, &v.UpdatesGeneration, &v.UpdatesCursor, &v.UpdatesStartedAt, &v.UpdatesReady, &v.UpdatesError)
	if errors.Is(err, sql.ErrNoRows) {
		return v, nil
	}
	if err != nil {
		return v, fmt.Errorf("读取 Emby 状态: %w", err)
	}
	return v, nil
}
func (s *Store) EmbyEnsure(ctx context.Context, scope string) error {
	return s.embyExec(ctx, `INSERT OR IGNORE INTO emby_state(scope) VALUES(?)`, scope)
}
func (s *Store) EmbyRecover(ctx context.Context) error {
	return s.embyExec(ctx, `
UPDATE emby_state SET status='queued',last_error='服务重启，正在重试发布已完成的媒体快照'
 WHERE status IN ('running','failed') AND staging_complete=1 AND staging_generation<>'';
UPDATE emby_state SET status='failed',last_error='服务重启中断同步，请重新执行全量同步',staging_generation='',staging_complete=0,staging_cutoff=0
 WHERE status='running' AND staging_complete=0;
DELETE FROM emby_snapshots WHERE NOT EXISTS (
 SELECT 1 FROM emby_state s WHERE s.scope=emby_snapshots.scope
 AND (s.active=emby_snapshots.generation OR (s.staging_complete=1 AND s.staging_generation=emby_snapshots.generation))
);`)
}
func (s *Store) EmbyQueueSync(ctx context.Context, scope string) error {
	if err := s.EmbyEnsure(ctx, scope); err != nil {
		return err
	}
	return s.embyExec(ctx, `UPDATE emby_state SET status='queued',last_error='',webhook_error='',scanned=0 WHERE scope=? AND status NOT IN ('queued','running')`, scope)
}
func (s *Store) EmbyProgress(ctx context.Context, scope, status string, scanned int, message string) error {
	return s.embyExec(ctx, `UPDATE emby_state SET status=?,scanned=?,last_error=? WHERE scope=?`, status, scanned, message, scope)
}

func (s *Store) EmbyBeginSync(ctx context.Context, scope, generation string, cutoff int64) error {
	return s.embyExec(ctx, `UPDATE emby_state SET status='running',scanned=0,last_error='',staging_generation=?,staging_complete=0,staging_cutoff=? WHERE scope=?`, generation, cutoff, scope)
}

func (s *Store) EmbyCompleteStage(ctx context.Context, scope, generation string, scanned int) error {
	return s.embyExec(ctx, `UPDATE emby_state SET staging_complete=1,scanned=? WHERE scope=? AND staging_generation=?`, scanned, scope, generation)
}

func (s *Store) EmbyFailSync(ctx context.Context, scope, generation, message string, discard bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开始记录 Emby 同步失败: %w", err)
	}
	defer tx.Rollback()
	if discard {
		if _, err := tx.ExecContext(ctx, `DELETE FROM emby_snapshots WHERE scope=? AND generation=? AND generation<>(SELECT active FROM emby_state WHERE scope=?)`, scope, generation, scope); err != nil {
			return fmt.Errorf("删除未完成的 Emby 快照: %w", err)
		}
	}
	query := `UPDATE emby_state SET status='failed',last_error=? WHERE scope=? AND staging_generation=?`
	if discard {
		query = `UPDATE emby_state SET status='failed',last_error=?,staging_generation='',staging_complete=0,staging_cutoff=0 WHERE scope=? AND staging_generation=?`
	}
	if _, err := tx.ExecContext(ctx, query, message, scope, generation); err != nil {
		return fmt.Errorf("记录 Emby 同步失败: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交 Emby 同步失败状态: %w", err)
	}
	return nil
}
func insertEmby(ctx context.Context, tx *sql.Tx, scope, generation string, items []model.EmbyItem) error {
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO emby_snapshots(scope,generation) VALUES(?,?)`, scope, generation); err != nil {
		return fmt.Errorf("创建媒体快照编号: %w", err)
	}
	var snapshotID int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM emby_snapshots WHERE scope=? AND generation=?`, scope, generation).Scan(&snapshotID); err != nil {
		return fmt.Errorf("读取媒体快照编号: %w", err)
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO emby_items(snapshot_id,id,library_id,type,tmdb,series_id,parent_id,name,season,episode,end_episode,date_created) VALUES(?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(snapshot_id,id) DO UPDATE SET library_id=excluded.library_id,type=excluded.type,tmdb=excluded.tmdb,series_id=excluded.series_id,parent_id=excluded.parent_id,name=excluded.name,season=excluded.season,episode=excluded.episode,end_episode=excluded.end_episode,date_created=excluded.date_created`)
	if err != nil {
		return fmt.Errorf("准备 Emby 索引写入: %w", err)
	}
	defer stmt.Close()
	for _, item := range items {
		if _, err = stmt.ExecContext(ctx, snapshotID, item.ID, item.LibraryID, item.Type, item.TMDB, item.SeriesID, item.ParentID, item.Name, item.Season, item.Episode, item.EndEpisode, item.DateCreated); err != nil {
			return fmt.Errorf("写入 Emby 条目: %w", err)
		}
	}
	return nil
}
func (s *Store) EmbyStage(ctx context.Context, scope, generation string, items []model.EmbyItem) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开始 Emby 索引写入: %w", err)
	}
	defer tx.Rollback()
	if err := insertEmby(ctx, tx, scope, generation, items); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交 Emby 索引: %w", err)
	}
	return nil
}

func queueEmbyMetadata(ctx context.Context, tx *sql.Tx, scope string, items []model.EmbyItem, due int64) error {
	for _, item := range items {
		missing := ((item.Type == "Movie" || item.Type == "Series") && item.TMDB == "") || (item.Type == "Episode" && (item.SeriesID == "" || item.Season < 0 || item.Episode < 0))
		if !missing {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO emby_events(scope,item_id,kind,due) VALUES(?,?,'metadata',?) ON CONFLICT(scope,item_id) DO UPDATE SET kind='metadata',attempts=0,due=excluded.due,last_error='' WHERE emby_events.attempts>=6`, scope, item.ID, due); err != nil {
			return fmt.Errorf("保存刮削补查: %w", err)
		}
	}
	return nil
}

func (s *Store) EmbyStagePage(ctx context.Context, scope, generation string, items []model.EmbyItem) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开始 Emby 分页写入: %w", err)
	}
	defer tx.Rollback()
	if err := insertEmby(ctx, tx, scope, generation, items); err != nil {
		return err
	}
	if err := queueEmbyMetadata(ctx, tx, scope, items, time.Now().Add(30*time.Second).Unix()); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交 Emby 分页写入: %w", err)
	}
	return nil
}
func (s *Store) EmbyPublish(ctx context.Context, scope, generation string, cutoff int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开始切换 Emby 索引: %w", err)
	}
	defer tx.Rollback()
	var snapshotExists int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM emby_snapshots WHERE scope=? AND generation=?`, scope, generation).Scan(&snapshotExists); err != nil {
		return fmt.Errorf("检查待发布 Emby 快照: %w", err)
	}
	if snapshotExists != 1 {
		return fmt.Errorf("待发布 Emby 快照不存在，请重新执行全量同步")
	}
	now := time.Now().UTC()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`UPDATE emby_state SET active=?,status='idle',count=(SELECT COUNT(*) FROM emby_items WHERE snapshot_id=(SELECT id FROM emby_snapshots WHERE scope=? AND generation=?)),synced_at=?,last_error='',
staging_generation='',staging_complete=0,staging_cutoff=0,
updates_generation=?,updates_cursor='',updates_started_at=?,updates_ready=0,updates_error=''
WHERE scope=?`, []any{generation, scope, generation, now.Format(time.RFC3339), generation, now.Unix(), scope}},
		{`DELETE FROM emby_snapshots WHERE scope=? AND generation<>?`, []any{scope, generation}},
		// 同步开始前已失败的通知由完整快照修复；同步期间的通知仍须重放。
		{`DELETE FROM emby_events WHERE scope=? AND id<=? AND attempts>=6`, []any{scope, cutoff}},
		{`UPDATE follow_checks SET due=0,version=version+1 WHERE ` + activeFollow, nil},
	} {
		if _, err := tx.ExecContext(ctx, q.sql, q.args...); err != nil {
			return fmt.Errorf("切换 Emby 索引: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交 Emby 快照: %w", err)
	}
	return nil
}
func (s *Store) EmbyDiscard(ctx context.Context, scope, generation string) error {
	return s.embyExec(ctx, `DELETE FROM emby_snapshots WHERE scope=? AND generation=? AND generation<>(SELECT active FROM emby_state WHERE scope=?)`, scope, generation, scope)
}
func (s *Store) EmbyWebhook(ctx context.Context, scope, event, message string) error {
	return s.embyExec(ctx, `UPDATE emby_state SET webhook_at=?,webhook_event=?,webhook_error=CASE WHEN ?<>'' THEN ? ELSE webhook_error END WHERE scope=?`, time.Now().UTC().Format(time.RFC3339), event, message, message, scope)
}
func (s *Store) EmbyEnqueue(ctx context.Context, scope, item, kind string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开始保存 Emby 通知: %w", err)
	}
	defer tx.Rollback()
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM emby_events WHERE scope=? AND item_id<>?`, scope, item).Scan(&count); err != nil {
		return fmt.Errorf("读取通知数量: %w", err)
	}
	if count >= 20000 {
		return fmt.Errorf("Emby 通知队列已满，请先处理同步异常")
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM emby_events WHERE scope=? AND item_id=?`, scope, item); err != nil {
		return fmt.Errorf("合并 Emby 通知: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO emby_events(scope,item_id,kind,due) VALUES(?,?,?,?)`, scope, item, kind, time.Now().Add(10*time.Second).Unix()); err != nil {
		return fmt.Errorf("保存 Emby 通知: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交 Emby 通知: %w", err)
	}
	return nil
}

// 首次扫描缺少刮削字段的条目延迟补查，不覆盖已收到的真实通知。
func (s *Store) EmbyQueueMetadata(ctx context.Context, scope string, items []model.EmbyItem) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开始保存刮削补查: %w", err)
	}
	defer tx.Rollback()
	if err := queueEmbyMetadata(ctx, tx, scope, items, time.Now().Add(30*time.Second).Unix()); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交刮削补查: %w", err)
	}
	return nil
}

func (s *Store) EmbyEventError(ctx context.Context, scope string) (string, error) {
	var message string
	err := s.db.QueryRowContext(ctx, `SELECT last_error FROM emby_events WHERE scope=? AND last_error<>'' ORDER BY id DESC LIMIT 1`, scope).Scan(&message)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("读取通知错误: %w", err)
	}
	return message, nil
}
func (s *Store) EmbyEventStats(ctx context.Context, scope string) (pending, failed int, cutoff int64, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(CASE WHEN attempts<6 THEN 1 ELSE 0 END),0),COALESCE(SUM(CASE WHEN attempts>=6 THEN 1 ELSE 0 END),0),COALESCE(MAX(id),0) FROM emby_events WHERE scope=?`, scope).Scan(&pending, &failed, &cutoff)
	if err != nil {
		err = fmt.Errorf("读取 Emby 通知统计: %w", err)
	}
	return
}

func (s *Store) EmbyMetadataStats(ctx context.Context, scope string) (pending, failed int, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(CASE WHEN attempts<6 THEN 1 ELSE 0 END),0),COALESCE(SUM(CASE WHEN attempts>=6 THEN 1 ELSE 0 END),0) FROM emby_events WHERE scope=? AND kind='metadata'`, scope).Scan(&pending, &failed)
	if err != nil {
		err = fmt.Errorf("读取 Emby 刮削补查统计: %w", err)
	}
	return
}
func (s *Store) EmbyNextEvent(ctx context.Context, scope string) (*model.EmbyEvent, error) {
	v := &model.EmbyEvent{}
	err := s.db.QueryRowContext(ctx, `SELECT id,item_id,kind,attempts FROM emby_events WHERE scope=? AND attempts<6 AND due<=? ORDER BY id LIMIT 1`, scope, time.Now().Unix()).Scan(&v.ID, &v.ItemID, &v.Kind, &v.Attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取 Emby 通知: %w", err)
	}
	return v, nil
}

// EmbyEventForItem also sees delayed events, so receipt and retries can be
// correlated, and superseded notifications are not reported as applied.
func (s *Store) EmbyEventForItem(ctx context.Context, scope, item string) (*model.EmbyEvent, error) {
	v := &model.EmbyEvent{}
	err := s.db.QueryRowContext(ctx, `SELECT id,item_id,kind,attempts FROM emby_events WHERE scope=? AND item_id=? ORDER BY id DESC LIMIT 1`, scope, item).Scan(&v.ID, &v.ItemID, &v.Kind, &v.Attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取 Emby 条目通知: %w", err)
	}
	return v, nil
}

func (s *Store) EmbyRetryEvent(ctx context.Context, event model.EmbyEvent, message string) error {
	delay := time.Duration(30*(1<<event.Attempts)) * time.Second
	return s.embyExec(ctx, `UPDATE emby_events SET attempts=attempts+1,last_error=?,due=?,kind=? WHERE id=?`, message, time.Now().Add(delay).Unix(), event.Kind, event.ID)
}
func (s *Store) EmbyApplyEvent(ctx context.Context, scope, generation string, event model.EmbyEvent, items []model.EmbyItem, remove bool) error {
	return s.applyEmbyEvent(ctx, scope, generation, event, items, remove, "")
}

// Missing metadata must not block usable local rows or consume the pending retry.
func (s *Store) EmbyApplyMetadataEvent(ctx context.Context, scope, generation string, event model.EmbyEvent, items []model.EmbyItem, message string) error {
	return s.applyEmbyEvent(ctx, scope, generation, event, items, false, message)
}

func (s *Store) applyEmbyEvent(ctx context.Context, scope, generation string, event model.EmbyEvent, items []model.EmbyItem, remove bool, metadataError string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开始应用 Emby 通知: %w", err)
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM emby_events WHERE id=?`, event.ID).Scan(&exists); err != nil {
		return fmt.Errorf("检查通知版本: %w", err)
	}
	if exists == 0 {
		return nil
	}
	changedIDs := make([]string, 0, len(items)+1)
	changedIDs = append(changedIDs, event.ItemID)
	for _, item := range items {
		changedIDs = append(changedIDs, item.ID)
	}
	changedJSON, err := json.Marshal(changedIDs)
	if err != nil {
		return fmt.Errorf("编码追更变化条目: %w", err)
	}
	removedID := ""
	if remove {
		removedID = event.ItemID
	}
	if err := dirtyEmbyFollows(ctx, tx, scope, generation, string(changedJSON), removedID); err != nil {
		return err
	}
	var delta int64
	if remove {
		if err := deleteMediaUpdates(ctx, tx, scope, event.ItemID, time.Now().UTC().Unix()); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `DELETE FROM emby_items WHERE snapshot_id=(SELECT id FROM emby_snapshots WHERE scope=? AND generation=?) AND (id=? OR series_id=? OR parent_id=? OR library_id=?)`, scope, generation, event.ItemID, event.ItemID, event.ItemID, event.ItemID)
		if err != nil {
			return fmt.Errorf("移除 Emby 条目: %w", err)
		}
		removed, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("读取移除数量: %w", err)
		}
		delta = -removed
	} else {
		ids := make([]string, 0, len(items))
		seen := make(map[string]bool, len(items))
		for _, item := range items {
			if !seen[item.ID] {
				seen[item.ID] = true
				ids = append(ids, item.ID)
			}
		}
		data, err := json.Marshal(ids)
		if err != nil {
			return fmt.Errorf("编码条目 ID: %w", err)
		}
		var existing int64
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM emby_items WHERE snapshot_id=(SELECT id FROM emby_snapshots WHERE scope=? AND generation=?) AND id IN (SELECT value FROM json_each(?))`, scope, generation, string(data)).Scan(&existing); err != nil {
			return fmt.Errorf("读取现有条目数: %w", err)
		}
		if err := insertEmby(ctx, tx, scope, generation, items); err != nil {
			return err
		}
		if err := upsertMediaUpdates(ctx, tx, scope, items, time.Now().UTC().Unix()); err != nil {
			return err
		}
		delta = int64(len(ids)) - existing
	}
	if err := dirtyEmbyFollows(ctx, tx, scope, generation, string(changedJSON), ""); err != nil {
		return err
	}
	if metadataError != "" {
		delay := time.Duration(30*(1<<event.Attempts)) * time.Second
		if _, err := tx.ExecContext(ctx, `UPDATE emby_events SET attempts=attempts+1,last_error=?,due=?,kind='metadata' WHERE id=?`, metadataError, time.Now().Add(delay).Unix(), event.ID); err != nil {
			return fmt.Errorf("保存 Emby 资料补查: %w", err)
		}
	} else {
		if _, err := tx.ExecContext(ctx, `DELETE FROM emby_events WHERE id=?`, event.ID); err != nil {
			return fmt.Errorf("完成 Emby 通知: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE emby_state SET count=MAX(0,count+?) WHERE scope=? AND active=?`, delta, scope, generation); err != nil {
		return fmt.Errorf("更新 Emby 条目计数: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交 Emby 通知: %w", err)
	}
	return nil
}
func (s *Store) EmbyMatch(ctx context.Context, scope, generation, kind, tmdb string, episodes bool) ([]model.EmbyItem, error) {
	typ := "Movie"
	if kind == "tv" {
		typ = "Series"
	}
	query := `SELECT id,library_id,type,tmdb,series_id,parent_id,name,season,episode,end_episode FROM emby_items WHERE snapshot_id=(SELECT id FROM emby_snapshots WHERE scope=? AND generation=?) AND type=? AND tmdb=?`
	args := []any{scope, generation, typ, tmdb}
	if episodes && kind == "tv" {
		query += ` UNION ALL SELECT e.id,e.library_id,e.type,e.tmdb,e.series_id,e.parent_id,e.name,e.season,e.episode,e.end_episode FROM emby_items e WHERE e.snapshot_id=(SELECT id FROM emby_snapshots WHERE scope=? AND generation=?) AND e.type='Episode' AND e.series_id IN (SELECT id FROM emby_items WHERE snapshot_id=(SELECT id FROM emby_snapshots WHERE scope=? AND generation=?) AND type='Series' AND tmdb=?)`
		args = append(args, scope, generation, scope, generation, tmdb)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("查询 Emby 入库状态: %w", err)
	}
	defer rows.Close()
	items := make([]model.EmbyItem, 0)
	for rows.Next() {
		var item model.EmbyItem
		if err := rows.Scan(&item.ID, &item.LibraryID, &item.Type, &item.TMDB, &item.SeriesID, &item.ParentID, &item.Name, &item.Season, &item.Episode, &item.EndEpisode); err != nil {
			return nil, fmt.Errorf("读取 Emby 条目: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历 Emby 条目: %w", err)
	}
	return items, nil
}

// 只枚举当前勾选范围里的剧集编号，供 TMDB 预热使用，不访问 Emby 媒体文件。
func (s *Store) EmbySeriesIDs(ctx context.Context, scope string) ([]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT CAST(tmdb AS INTEGER) FROM emby_items WHERE snapshot_id=(SELECT p.id FROM emby_snapshots p JOIN emby_state s ON s.scope=p.scope AND s.active=p.generation WHERE p.scope=?) AND type='Series' AND CAST(tmdb AS INTEGER)>0 ORDER BY CAST(tmdb AS INTEGER)`, scope)
	if err != nil {
		return nil, fmt.Errorf("查询已入库剧集编号: %w", err)
	}
	defer rows.Close()
	ids := make([]int, 0)
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("读取剧集编号: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历剧集编号: %w", err)
	}
	return ids, nil
}
