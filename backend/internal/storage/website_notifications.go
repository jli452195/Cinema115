package storage

import (
	"context"
	"fmt"
	"strm115/backend/internal/model"
	"time"
)

func (s *Store) WebsiteUnread(ctx context.Context, userID string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM website_notifications WHERE user_id=? AND read_at=0`, userID).Scan(&count)
	return count, err
}

// 按通知编号游标分页，新通知到达时不会造成旧页重复或跳项。
func (s *Store) WebsiteNotifications(ctx context.Context, userID string, before int64) ([]model.WebsiteNotification, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,kind,tmdb_id,title,message,created_at,read_at FROM website_notifications WHERE user_id=? AND (?=0 OR id<?) ORDER BY id DESC LIMIT 21`, userID, before, before)
	if err != nil {
		return nil, fmt.Errorf("读取站内通知: %w", err)
	}
	defer rows.Close()
	items := make([]model.WebsiteNotification, 0, 21)
	for rows.Next() {
		var n model.WebsiteNotification
		if err := rows.Scan(&n.ID, &n.Kind, &n.TMDBID, &n.Title, &n.Message, &n.CreatedAt, &n.ReadAt); err != nil {
			return nil, err
		}
		items = append(items, n)
	}
	return items, rows.Err()
}

// 全部已读限定到用户已经看到的最大编号，避免把同时到达的新通知误标已读。
func (s *Store) ReadWebsiteNotifications(ctx context.Context, userID string, id int64, all bool) error {
	query := `UPDATE website_notifications SET read_at=? WHERE user_id=? AND read_at=0 AND id=?`
	if all {
		query = `UPDATE website_notifications SET read_at=? WHERE user_id=? AND read_at=0 AND id<=?`
	}
	_, err := s.db.ExecContext(ctx, query, time.Now().Unix(), userID, id)
	if err != nil {
		return fmt.Errorf("标记站内通知已读: %w", err)
	}
	return nil
}
