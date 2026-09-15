package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"strm115/backend/internal/model"
)

var ErrFollowLimit = errors.New("追更数量已达上限，请先取消不需要的追更")

const followColumns = "r.kind,r.tmdb_id,r.title,r.poster_url,r.release_date,r.status,f.subscribed_at,r.note"

func scanFollow(row interface{ Scan(...any) error }) (model.Follow, error) {
	var f model.Follow
	err := row.Scan(&f.Kind, &f.TMDBID, &f.Title, &f.PosterURL, &f.Date, &f.Status, &f.SubscribedAt, &f.Note)
	return f, err
}
func (s *Store) Follow(ctx context.Context, userID, kind string, id int) (*model.Follow, error) {
	f, err := scanFollow(s.db.QueryRowContext(ctx, "SELECT "+followColumns+" FROM user_follows f JOIN follow_requests r USING(kind,tmdb_id) WHERE f.user_id=? AND f.kind=? AND f.tmdb_id=? AND f.active=1", userID, kind, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取追更申请: %w", err)
	}
	return &f, nil
}

// 影视申请与用户订阅在同一事务提交，重复提交不改变申请时间或创建重复任务。
func (s *Store) Subscribe(ctx context.Context, userID string, f model.Follow) error {
	return s.SubscribeWithNotification(ctx, userID, f, nil)
}

func (s *Store) SubscribeWithNotification(ctx context.Context, userID string, f model.Follow, notification *model.TelegramNotification) error {
	return s.SubscribeWithAutomation(ctx, userID, f, notification, false)
}

func (s *Store) SubscribeWithAutomation(ctx context.Context, userID string, f model.Follow, notification *model.TelegramNotification, auto bool) error {
	return s.subscribe(ctx, userID, f, notification, &auto)
}

// SubscribeWithConfiguredAutomation 在订阅事务内读取开关，避免设置切换与首位订阅并发时漏建任务。
func (s *Store) SubscribeWithConfiguredAutomation(ctx context.Context, userID string, f model.Follow, notification *model.TelegramNotification) error {
	return s.subscribe(ctx, userID, f, notification, nil)
}

func (s *Store) subscribe(ctx context.Context, userID string, f model.Follow, notification *model.TelegramNotification, requestedAuto *bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开始追更事务: %w", err)
	}
	defer tx.Rollback()
	auto := false
	if requestedAuto != nil {
		auto = *requestedAuto
	} else if err := tx.QueryRowContext(ctx, "SELECT auto_enabled FROM follow_settings WHERE id=1").Scan(&auto); err != nil {
		return fmt.Errorf("读取追更自动搜索设置: %w", err)
	}
	var count, existing int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM user_follows WHERE user_id=? AND active=1", userID).Scan(&count); err != nil {
		return fmt.Errorf("读取追更数量: %w", err)
	}
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM user_follows WHERE user_id=? AND kind=? AND tmdb_id=? AND active=1", userID, f.Kind, f.TMDBID).Scan(&existing); err != nil {
		return fmt.Errorf("读取现有订阅: %w", err)
	}
	if count >= 500 && existing == 0 {
		return ErrFollowLimit
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var followers int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM user_follows WHERE kind=? AND tmdb_id=? AND active=1", f.Kind, f.TMDBID).Scan(&followers); err != nil {
		return fmt.Errorf("读取影视订阅人数: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO follow_requests(kind,tmdb_id,title,poster_url,release_date,created_at) VALUES(?,?,?,?,?,?) ON CONFLICT(kind,tmdb_id) DO NOTHING`, f.Kind, f.TMDBID, f.Title, f.PosterURL, f.Date, now); err != nil {
		return fmt.Errorf("保存影视申请: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO user_follows(user_id,kind,tmdb_id,active,subscribed_at) VALUES(?,?,?,1,?) ON CONFLICT(user_id,kind,tmdb_id) DO UPDATE SET active=1,subscribed_at=excluded.subscribed_at WHERE user_follows.active=0`, userID, f.Kind, f.TMDBID, now); err != nil {
		return fmt.Errorf("保存用户订阅: %w", err)
	}
	if followers == 0 {
		if _, err := tx.ExecContext(ctx, `INSERT INTO follow_checks(kind,tmdb_id) VALUES(?,?) ON CONFLICT(kind,tmdb_id) DO UPDATE SET state='unknown',aired=0,owned=0,checked_at=0,due=0,error='',version=version+1`, f.Kind, f.TMDBID); err != nil {
			return fmt.Errorf("重置追更入库检查: %w", err)
		}
		// 没有有效订阅时开启新一轮申请；后续加入者不重复通知。
		var cycle int64
		if err := tx.QueryRowContext(ctx, `UPDATE follow_requests SET status='pending',note='',updated_at=?,revision=revision+1,cycle=cycle+1 WHERE kind=? AND tmdb_id=? RETURNING cycle`, now, f.Kind, f.TMDBID).Scan(&cycle); err != nil {
			return fmt.Errorf("启动追更申请: %w", err)
		}
		if notification != nil {
			n := *notification
			n.TaskID = fmt.Sprintf("follow:%s:%d:%d", f.Kind, f.TMDBID, cycle)
			if err := enqueueTelegramNotification(ctx, tx, n); err != nil {
				return err
			}
		}
		if auto {
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO follow_bot_jobs(kind,tmdb_id,cycle,status,created_at,updated_at) VALUES(?,?,?,'checking',?,?)`, f.Kind, f.TMDBID, cycle, now, now); err != nil {
				return fmt.Errorf("创建追更自动搜索任务: %w", err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交追更申请: %w", err)
	}
	return nil
}
func (s *Store) CancelFollow(ctx context.Context, userID, kind string, id int) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开始取消追更: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "UPDATE user_follows SET active=0 WHERE user_id=? AND kind=? AND tmdb_id=? AND active=1", userID, kind, id); err != nil {
		return fmt.Errorf("取消追更: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE follow_checks SET due=9223372036854775807,version=version+1 WHERE kind=? AND tmdb_id=? AND NOT EXISTS(SELECT 1 FROM user_follows f WHERE f.kind=follow_checks.kind AND f.tmdb_id=follow_checks.tmdb_id AND f.active=1)`, kind, id); err != nil {
		return fmt.Errorf("停止无人订阅的入库检查: %w", err)
	}
	// 最后一人取消时撤销尚未发送的通知，其他用户仍追更则保持原通知。
	if _, err := tx.ExecContext(ctx, `UPDATE telegram_notifications SET status='cancelled',text='',preview_url='' WHERE status='pending' AND task_id=(SELECT 'follow:'||kind||':'||tmdb_id||':'||cycle FROM follow_requests WHERE kind=? AND tmdb_id=?) AND NOT EXISTS(SELECT 1 FROM user_follows WHERE kind=? AND tmdb_id=? AND active=1)`, kind, id, kind, id); err != nil {
		return fmt.Errorf("取消待发送追更通知: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE follow_bot_jobs SET status='cancelled',last_error='',updated_at=? WHERE kind=? AND tmdb_id=? AND status IN ('checking','queued','running') AND NOT EXISTS(SELECT 1 FROM user_follows WHERE kind=? AND tmdb_id=? AND active=1)`, time.Now().UTC().Format(time.RFC3339Nano), kind, id, kind, id); err != nil {
		return fmt.Errorf("取消追更自动搜索任务: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交取消追更: %w", err)
	}
	return nil
}
func (s *Store) Follows(ctx context.Context, userID, kind, status string, offset int) ([]model.Follow, error) {
	query := "SELECT " + followColumns + " FROM user_follows f JOIN follow_requests r USING(kind,tmdb_id) WHERE f.user_id=? AND f.active=1"
	args := []any{userID}
	// 先在数据库筛选再分页，已完成记录不会占用追更中的页面名额。
	if status == "completed" {
		query += " AND r.status='completed'"
	} else {
		query += " AND r.status<>'completed'"
	}
	if kind != "" {
		query += " AND f.kind=?"
		args = append(args, kind)
	}
	query += " ORDER BY f.subscribed_at DESC,f.kind,f.tmdb_id LIMIT ? OFFSET ?"
	args = append(args, model.FollowPageSize+1, offset)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("查询我的追更: %w", err)
	}
	defer rows.Close()
	items := make([]model.Follow, 0, model.FollowPageSize+1)
	for rows.Next() {
		f, err := scanFollow(rows)
		if err != nil {
			return nil, fmt.Errorf("读取追更列表: %w", err)
		}
		items = append(items, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("读取追更列表: %w", err)
	}
	return items, nil
}
