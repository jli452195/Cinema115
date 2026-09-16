package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"strm115/backend/internal/model"
)

const activeFollow = `EXISTS(SELECT 1 FROM user_follows f WHERE f.kind=follow_checks.kind AND f.tmdb_id=follow_checks.tmdb_id AND f.active=1)`

// 队列持久化；重复事件合并为一次检查，版本防止旧结果覆盖新事件。
func (s *Store) RecheckFollows(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE follow_checks SET due=0,version=version+1 WHERE `+activeFollow)
	if err != nil {
		return fmt.Errorf("安排追更复核: %w", err)
	}
	return nil
}

func (s *Store) DueFollowChecks(ctx context.Context, now int64) ([]model.FollowCheck, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT kind,tmdb_id,version FROM follow_checks WHERE due<=? AND `+activeFollow+` ORDER BY due,kind,tmdb_id LIMIT 10`, now)
	if err != nil {
		return nil, fmt.Errorf("读取追更检查队列: %w", err)
	}
	defer rows.Close()
	items := make([]model.FollowCheck, 0, 10)
	for rows.Next() {
		var item model.FollowCheck
		if err := rows.Scan(&item.Kind, &item.TMDBID, &item.Version); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) FollowProgress(ctx context.Context, items []model.Follow) (map[string]*model.FollowProgress, error) {
	keys := make([]string, 0, len(items))
	for _, f := range items {
		keys = append(keys, fmt.Sprintf("%s:%d", f.Kind, f.TMDBID))
	}
	data, err := json.Marshal(keys)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT kind||':'||tmdb_id,state,aired,owned,checked_at,error FROM follow_checks WHERE (kind,tmdb_id) IN (SELECT substr(value,1,instr(value,':')-1),CAST(substr(value,instr(value,':')+1) AS INTEGER) FROM json_each(?))`, string(data))
	if err != nil {
		return nil, fmt.Errorf("读取追更进度: %w", err)
	}
	defer rows.Close()
	result := make(map[string]*model.FollowProgress, len(items))
	for rows.Next() {
		var key string
		var p model.FollowProgress
		if err := rows.Scan(&key, &p.State, &p.Aired, &p.Owned, &p.CheckedAt, &p.Error); err != nil {
			return nil, err
		}
		result[key] = &p
	}
	return result, rows.Err()
}

// 状态与每位订阅者的通知原子提交；重复 Webhook、定时检查和重启均不会重复通知。
func (s *Store) SaveFollowCheck(ctx context.Context, job model.FollowCheck, p model.FollowProgress, due int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var old model.FollowProgress
	var title string
	var searchClosed bool
	err = tx.QueryRowContext(ctx, `SELECT c.state,c.aired,c.owned,r.title,r.search_closed FROM follow_checks c JOIN follow_requests r USING(kind,tmdb_id) WHERE c.kind=? AND c.tmdb_id=? AND c.version=? AND EXISTS(SELECT 1 FROM user_follows f WHERE f.kind=c.kind AND f.tmdb_id=c.tmdb_id AND f.active=1)`, job.Kind, job.TMDBID, job.Version).Scan(&old.State, &old.Aired, &old.Owned, &title, &searchClosed)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return fmt.Errorf("检查追更结果版本: %w", err)
	}
	if p.Error != "" {
		// 上游异常只标记等待复核，保留上次有效状态用于通知去重。
		_, err = tx.ExecContext(ctx, `UPDATE follow_checks SET error=?,due=?,version=version+1 WHERE kind=? AND tmdb_id=?`, p.Error, due, job.Kind, job.TMDBID)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE follow_checks SET state=?,aired=?,owned=?,checked_at=?,due=?,error='',version=version+1 WHERE kind=? AND tmdb_id=?`, p.State, p.Aired, p.Owned, p.CheckedAt, due, job.Kind, job.TMDBID)
	}
	if err != nil {
		return fmt.Errorf("保存追更检查: %w", err)
	}
	if p.Error == "" {
		automationStatus := "no_need"
		if p.State == "absent" || p.State == "tracking" && p.Owned < p.Aired {
			automationStatus = "queued"
		}
		if _, err = tx.ExecContext(ctx, `UPDATE follow_bot_jobs SET status=?,due=0,last_error='',updated_at=?
WHERE kind=? AND tmdb_id=? AND status='checking' AND cycle=(SELECT cycle FROM follow_requests WHERE kind=? AND tmdb_id=?)`, automationStatus, time.Now().UTC().Format(time.RFC3339Nano), job.Kind, job.TMDBID, job.Kind, job.TMDBID); err != nil {
			return fmt.Errorf("更新追更自动搜索条件: %w", err)
		}
		// 完成由入库结果决定；丢失资源后重新进入待处理。
		if _, err = tx.ExecContext(ctx, `UPDATE follow_requests SET status=CASE WHEN ?='completed' THEN 'completed' ELSE 'pending' END,updated_at=?,revision=revision+1 WHERE kind=? AND tmdb_id=? AND ((?='completed' AND status<>'completed') OR (?<>'completed' AND status='completed'))`, p.State, time.Now().UTC().Format(time.RFC3339Nano), job.Kind, job.TMDBID, p.State, p.State); err != nil {
			return err
		}
		message := ""
		switch {
		case p.State == "completed" && old.State != "completed":
			message = "已入库，追更已完成"
		case p.State == "caught_up" && (old.State != "caught_up" || p.Owned > old.Owned):
			message = fmt.Sprintf("已追平，已播出的 %d 集正片全部入库；继续追踪后续更新", p.Aired)
		case (old.State == "completed" || old.State == "caught_up") && p.State != old.State && p.State != "completed" && p.State != "caught_up":
			if searchClosed {
				message = "入库或播出信息已变化，资源搜索仍为已关闭，请查看最新进度"
			} else {
				message = "入库或播出信息已变化，已恢复追踪，请查看最新进度"
			}
		case p.State == "tracking" && p.Owned > old.Owned:
			message = fmt.Sprintf("剧集更新入库：已播 %d 集，已入库 %d 集", p.Aired, p.Owned)
		}
		if message != "" {
			if _, err = tx.ExecContext(ctx, `INSERT INTO website_notifications(user_id,kind,tmdb_id,title,message,created_at) SELECT user_id,kind,tmdb_id,?,?,? FROM user_follows WHERE kind=? AND tmdb_id=? AND active=1`, title, message, p.CheckedAt, job.Kind, job.TMDBID); err != nil {
				return fmt.Errorf("保存站内追更通知: %w", err)
			}
		}
	}
	return tx.Commit()
}

// 在 Emby 事务内找到变化条目原先及当前所属的影视，避免扫描全库或漏掉删除事件。
func dirtyEmbyFollows(ctx context.Context, tx *sql.Tx, scope, generation, ids string, removedID string) error {
	// 常见单集更新走主键索引，不使用带父目录条件的 OR 导致扫描整个快照。
	touched := `SELECT e.* FROM emby_items e WHERE e.snapshot_id=(SELECT id FROM emby_snapshots WHERE scope=? AND generation=?) AND e.id IN (SELECT value FROM json_each(?))`
	args := []any{scope, generation, ids}
	if removedID != "" {
		touched += ` UNION SELECT e.* FROM emby_items e WHERE e.snapshot_id=(SELECT id FROM emby_snapshots WHERE scope=? AND generation=?) AND (e.series_id=? OR e.parent_id=? OR e.library_id=?)`
		args = append(args, scope, generation, removedID, removedID, removedID)
	}
	_, err := tx.ExecContext(ctx, `WITH touched AS (`+touched+`), media AS (
 SELECT CASE WHEN type='Movie' THEN 'movie' ELSE 'tv' END kind,CAST(tmdb AS INTEGER) tmdb_id FROM touched WHERE type IN ('Movie','Series')
 UNION SELECT 'tv',CAST(s.tmdb AS INTEGER) FROM touched e JOIN emby_items s ON s.snapshot_id=e.snapshot_id AND s.id=e.series_id WHERE s.type='Series'
)
UPDATE follow_checks SET due=0,version=version+1 WHERE (kind,tmdb_id) IN (SELECT kind,tmdb_id FROM media) AND `+activeFollow, args...)
	if err != nil {
		return fmt.Errorf("安排 Emby 影视追更检查: %w", err)
	}
	return nil
}
