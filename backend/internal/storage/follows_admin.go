package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"strm115/backend/internal/model"
)

var ErrFollowConflict = errors.New("申请已被修改或已无人追更，请刷新后重试")

func (s *Store) AdminFollows(ctx context.Context, kind, status, query string, offset int) ([]model.AdminFollow, error) {
	q := `SELECT r.kind,r.tmdb_id,r.title,r.poster_url,r.release_date,` + effectiveFollowStatus + `,f.subscribed_at,r.note,f.subscribers,r.created_at,r.updated_at,r.revision FROM follow_requests r JOIN (SELECT kind,tmdb_id,COUNT(*) subscribers,MAX(subscribed_at) subscribed_at FROM user_follows WHERE active=1 GROUP BY kind,tmdb_id) f USING(kind,tmdb_id) WHERE 1=1`
	args := make([]any, 0, 4)
	if kind != "" {
		q += " AND r.kind=?"
		args = append(args, kind)
	}
	if status != "" {
		switch status {
		case "closed":
			q += " AND r.search_closed=1 AND r.status<>'completed'"
		case "completed":
			q += " AND r.status='completed'"
		default:
			q += " AND r.search_closed=0 AND r.status=?"
			args = append(args, status)
		}
	}
	if query != "" {
		q += " AND (instr(lower(r.title),lower(?))>0 OR CAST(r.tmdb_id AS TEXT)=?)"
		args = append(args, query, query)
	}
	q += " ORDER BY f.subscribed_at DESC,r.kind,r.tmdb_id LIMIT ? OFFSET ?"
	args = append(args, model.FollowPageSize+1, offset)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("查询追更管理列表: %w", err)
	}
	defer rows.Close()
	items := make([]model.AdminFollow, 0, model.FollowPageSize+1)
	for rows.Next() {
		var f model.AdminFollow
		if err := rows.Scan(&f.Kind, &f.TMDBID, &f.Title, &f.PosterURL, &f.Date, &f.Status, &f.SubscribedAt, &f.Note, &f.SubscriberCount, &f.CreatedAt, &f.UpdatedAt, &f.Revision); err != nil {
			return nil, fmt.Errorf("读取追更管理记录: %w", err)
		}
		items = append(items, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("读取追更管理列表: %w", err)
	}
	return items, nil
}
func (s *Store) UpdateFollow(ctx context.Context, kind string, id int, status, note string, revision int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开始保存追更处理状态: %w", err)
	}
	defer tx.Rollback()
	var currentStatus, title string
	var currentClosed, libraryCompleted bool
	var cycle int64
	err = tx.QueryRowContext(ctx, `SELECT r.status,r.search_closed,r.title,r.cycle,
		EXISTS(SELECT 1 FROM follow_checks c WHERE c.kind=r.kind AND c.tmdb_id=r.tmdb_id AND c.state='completed')
FROM follow_requests r WHERE r.kind=? AND r.tmdb_id=? AND r.revision=?
AND EXISTS(SELECT 1 FROM user_follows f WHERE f.kind=r.kind AND f.tmdb_id=r.tmdb_id AND f.active=1)`, kind, id, revision).Scan(&currentStatus, &currentClosed, &title, &cycle, &libraryCompleted)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrFollowConflict
	}
	if err != nil {
		return fmt.Errorf("读取追更处理状态: %w", err)
	}
	if libraryCompleted {
		currentStatus = "completed"
	}
	nextStatus, nextClosed := currentStatus, currentClosed
	if currentStatus != "completed" {
		if status == "closed" {
			nextClosed = true
		} else {
			nextStatus, nextClosed = status, false
		}
	}
	closing := status == "closed" && !currentClosed && currentStatus != "completed"
	reopening := status != "closed" && currentClosed && currentStatus != "completed"
	now := time.Now()
	result, err := tx.ExecContext(ctx, `UPDATE follow_requests SET status=?,search_closed=?,note=?,updated_at=?,revision=revision+1
WHERE kind=? AND tmdb_id=? AND revision=?`, nextStatus, nextClosed, note, now.UTC().Format(time.RFC3339Nano), kind, id, revision)
	if err != nil {
		return fmt.Errorf("保存追更处理状态: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("读取追更保存结果: %w", err)
	}
	if changed != 1 {
		return ErrFollowConflict
	}
	if closing {
		if _, err := tx.ExecContext(ctx, `UPDATE follow_bot_jobs SET status='cancelled',due=0,last_error='',updated_at=?
WHERE kind=? AND tmdb_id=? AND cycle=? AND status IN ('checking','queued','running')`, now.UTC().Format(time.RFC3339Nano), kind, id, cycle); err != nil {
			return fmt.Errorf("停止已关闭追更的自动搜索: %w", err)
		}
		message := "追更已关闭：" + note
		if _, err := tx.ExecContext(ctx, `INSERT INTO website_notifications(user_id,kind,tmdb_id,title,message,created_at)
SELECT user_id,kind,tmdb_id,?,?,? FROM user_follows WHERE kind=? AND tmdb_id=? AND active=1`, title, message, now.Unix(), kind, id); err != nil {
			return fmt.Errorf("保存追更关闭通知: %w", err)
		}
	}
	if reopening {
		if _, err := tx.ExecContext(ctx, `UPDATE follow_checks SET due=0,version=version+1 WHERE kind=? AND tmdb_id=?`, kind, id); err != nil {
			return fmt.Errorf("安排恢复追更入库检查: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO follow_bot_jobs(kind,tmdb_id,cycle,status,created_at,updated_at)
VALUES(?,?,?,'checking',?,?)
ON CONFLICT(kind,tmdb_id,cycle) DO UPDATE SET
status=CASE WHEN follow_bot_jobs.status IN ('cancelled','failed','no_match','no_need') THEN 'checking' ELSE follow_bot_jobs.status END,
attempts=CASE WHEN follow_bot_jobs.status IN ('cancelled','failed','no_match','no_need') THEN 0 ELSE follow_bot_jobs.attempts END,
due=CASE WHEN follow_bot_jobs.status IN ('cancelled','failed','no_match','no_need') THEN 0 ELSE follow_bot_jobs.due END,
last_error=CASE WHEN follow_bot_jobs.status IN ('cancelled','failed','no_match','no_need') THEN '' ELSE follow_bot_jobs.last_error END,
updated_at=excluded.updated_at`, kind, id, cycle, now.UTC().Format(time.RFC3339Nano), now.UTC().Format(time.RFC3339Nano)); err != nil {
			return fmt.Errorf("恢复追更自动搜索: %w", err)
		}
		message := "追更已恢复处理"
		if note != "" {
			message += "：" + note
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO website_notifications(user_id,kind,tmdb_id,title,message,created_at)
SELECT user_id,kind,tmdb_id,?,?,? FROM user_follows WHERE kind=? AND tmdb_id=? AND active=1`, title, message, now.Unix(), kind, id); err != nil {
			return fmt.Errorf("保存追更恢复通知: %w", err)
		}
	}
	return tx.Commit()
}
func (s *Store) FollowBaseURL(ctx context.Context) (string, error) {
	settings, err := s.FollowSettings(ctx)
	if err != nil {
		return "", fmt.Errorf("读取追更网站地址: %w", err)
	}
	return settings.BaseURL, nil
}
func (s *Store) SaveFollowBaseURL(ctx context.Context, value string) error {
	if _, err := s.db.ExecContext(ctx, "UPDATE follow_settings SET base_url=? WHERE id=1", value); err != nil {
		return fmt.Errorf("保存追更网站地址: %w", err)
	}
	return nil
}

func (s *Store) FollowSettings(ctx context.Context) (model.FollowSettings, error) {
	var value model.FollowSettings
	if err := s.db.QueryRowContext(ctx, "SELECT base_url,auto_enabled,telegram_enabled,telegram_bot_id FROM follow_settings WHERE id=1").Scan(&value.BaseURL, &value.AutoEnabled, &value.TelegramEnabled, &value.BotID); err != nil {
		return value, fmt.Errorf("读取追更设置: %w", err)
	}
	return value, nil
}

func (s *Store) SaveFollowSettings(ctx context.Context, value model.FollowSettings) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开始保存追更设置: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "UPDATE follow_settings SET base_url=?,auto_enabled=?,account_id='',telegram_enabled=?,telegram_bot_id=? WHERE id=1", value.BaseURL, value.AutoEnabled, value.TelegramEnabled, value.BotID); err != nil {
		return fmt.Errorf("保存追更设置: %w", err)
	}
	if value.AutoEnabled {
		now := time.Now().UTC().Format(time.RFC3339Nano)
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO follow_bot_jobs(kind,tmdb_id,cycle,status,created_at,updated_at)
SELECT r.kind,r.tmdb_id,r.cycle,'checking',?,? FROM follow_requests r
WHERE r.status<>'completed' AND r.search_closed=0 AND r.cycle>0 AND EXISTS(SELECT 1 FROM user_follows f WHERE f.kind=r.kind AND f.tmdb_id=r.tmdb_id AND f.active=1)`, now, now); err != nil {
			return fmt.Errorf("安排已有追更自动搜索: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE follow_checks SET due=0,version=version+1 WHERE EXISTS(
SELECT 1 FROM follow_bot_jobs j JOIN follow_requests r USING(kind,tmdb_id) WHERE j.kind=follow_checks.kind AND j.tmdb_id=follow_checks.tmdb_id AND j.cycle=r.cycle AND j.status='checking')`); err != nil {
			return fmt.Errorf("安排已有追更入库检查: %w", err)
		}
	}
	return tx.Commit()
}

func (s *Store) MigrateFollowTelegramBot(ctx context.Context, id string, enabled bool) error {
	_, err := s.db.ExecContext(ctx, "UPDATE follow_settings SET telegram_bot_id=?,telegram_enabled=? WHERE id=1 AND telegram_bot_id=''", id, enabled)
	if err != nil {
		return fmt.Errorf("迁移追更 Telegram Bot 绑定: %w", err)
	}
	return nil
}
