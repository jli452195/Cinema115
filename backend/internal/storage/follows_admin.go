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
	var value string
	if err := s.db.QueryRowContext(ctx, "SELECT base_url FROM follow_settings WHERE id=1").Scan(&value); err != nil {
		return "", fmt.Errorf("读取追更网站地址: %w", err)
	}
	return value, nil
}
func (s *Store) SaveFollowBaseURL(ctx context.Context, value string) error {
	if _, err := s.db.ExecContext(ctx, "UPDATE follow_settings SET base_url=? WHERE id=1", value); err != nil {
		return fmt.Errorf("保存追更网站地址: %w", err)
	}
	return nil
}
