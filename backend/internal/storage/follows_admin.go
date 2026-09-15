package storage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"strm115/backend/internal/model"
)

var ErrFollowConflict = errors.New("申请已被修改或已无人追更，请刷新后重试")

func (s *Store) AdminFollows(ctx context.Context, kind, status, query string, offset int) ([]model.AdminFollow, error) {
	q := `SELECT r.kind,r.tmdb_id,r.title,r.poster_url,r.release_date,r.status,f.subscribed_at,r.note,f.subscribers,r.created_at,r.updated_at,r.revision FROM follow_requests r JOIN (SELECT kind,tmdb_id,COUNT(*) subscribers,MAX(subscribed_at) subscribed_at FROM user_follows WHERE active=1 GROUP BY kind,tmdb_id) f USING(kind,tmdb_id) WHERE 1=1`
	args := make([]any, 0, 4)
	if kind != "" {
		q += " AND r.kind=?"
		args = append(args, kind)
	}
	if status != "" {
		q += " AND r.status=?"
		args = append(args, status)
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
	r, err := s.db.ExecContext(ctx, `UPDATE follow_requests SET status=CASE WHEN EXISTS(SELECT 1 FROM follow_checks c WHERE c.kind=follow_requests.kind AND c.tmdb_id=follow_requests.tmdb_id AND c.state='completed') THEN 'completed' ELSE ? END,note=?,updated_at=?,revision=revision+1 WHERE kind=? AND tmdb_id=? AND revision=? AND EXISTS(SELECT 1 FROM user_follows f WHERE f.kind=follow_requests.kind AND f.tmdb_id=follow_requests.tmdb_id AND f.active=1)`, status, note, time.Now().UTC().Format(time.RFC3339Nano), kind, id, revision)
	if err != nil {
		return fmt.Errorf("保存追更处理状态: %w", err)
	}
	n, err := r.RowsAffected()
	if err != nil {
		return fmt.Errorf("读取追更保存结果: %w", err)
	}
	if n == 0 {
		return ErrFollowConflict
	}
	return nil
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
	if err := s.db.QueryRowContext(ctx, "SELECT base_url,auto_enabled FROM follow_settings WHERE id=1").Scan(&value.BaseURL, &value.AutoEnabled); err != nil {
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
	if _, err := tx.ExecContext(ctx, "UPDATE follow_settings SET base_url=?,auto_enabled=?,account_id='' WHERE id=1", value.BaseURL, value.AutoEnabled); err != nil {
		return fmt.Errorf("保存追更设置: %w", err)
	}
	if value.AutoEnabled {
		now := time.Now().UTC().Format(time.RFC3339Nano)
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO follow_bot_jobs(kind,tmdb_id,cycle,status,created_at,updated_at)
SELECT r.kind,r.tmdb_id,r.cycle,'checking',?,? FROM follow_requests r
WHERE r.status<>'completed' AND r.cycle>0 AND EXISTS(SELECT 1 FROM user_follows f WHERE f.kind=r.kind AND f.tmdb_id=r.tmdb_id AND f.active=1)`, now, now); err != nil {
			return fmt.Errorf("安排已有追更自动搜索: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE follow_checks SET due=0,version=version+1 WHERE EXISTS(
SELECT 1 FROM follow_bot_jobs j JOIN follow_requests r USING(kind,tmdb_id) WHERE j.kind=follow_checks.kind AND j.tmdb_id=follow_checks.tmdb_id AND j.cycle=r.cycle AND j.status='checking')`); err != nil {
			return fmt.Errorf("安排已有追更入库检查: %w", err)
		}
	}
	return tx.Commit()
}
