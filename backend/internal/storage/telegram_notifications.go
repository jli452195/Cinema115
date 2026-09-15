package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"strm115/backend/internal/model"
)

func (s *Store) EnqueueTelegramNotification(ctx context.Context, n model.TelegramNotification) error {
	return enqueueTelegramNotification(ctx, s.db, n)
}

func enqueueTelegramNotification(ctx context.Context, db interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, n model.TelegramNotification) error {
	now := time.Now().Unix()
	_, err := db.ExecContext(ctx, `INSERT INTO telegram_notifications(task_id,bot_id,chat_id,text,preview_url,due,created_at) VALUES(?,?,?,?,?,?,?) ON CONFLICT(task_id,bot_id,chat_id) DO NOTHING`,
		n.TaskID, n.BotID, n.ChatID, n.Text, n.PreviewURL, now, now)
	if err != nil {
		return fmt.Errorf("保存 Telegram 通知: %w", err)
	}
	return nil
}

func (s *Store) DueTelegramNotifications(ctx context.Context, botID string, chatID int64, now time.Time) ([]model.TelegramNotification, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,task_id,bot_id,chat_id,text,preview_url,attempts FROM telegram_notifications WHERE status='pending' AND bot_id=? AND chat_id=? AND due<=? ORDER BY due,id LIMIT 20`, botID, chatID, now.Unix())
	if err != nil {
		return nil, fmt.Errorf("查询待发送通知: %w", err)
	}
	defer rows.Close()
	result := make([]model.TelegramNotification, 0, 20)
	for rows.Next() {
		var n model.TelegramNotification
		if err := rows.Scan(&n.ID, &n.TaskID, &n.BotID, &n.ChatID, &n.Text, &n.PreviewURL, &n.Attempts); err != nil {
			return nil, fmt.Errorf("读取通知: %w", err)
		}
		result = append(result, n)
	}
	return result, rows.Err()
}

func (s *Store) CompleteTelegramNotification(ctx context.Context, id int64, delivered bool) error {
	status := "failed"
	if delivered {
		status = "sent"
	}
	// 留下轻量去重记录，发送结束后不继续保存消息正文。
	_, err := s.db.ExecContext(ctx, `UPDATE telegram_notifications SET status=?,text='',preview_url='' WHERE id=?`, status, id)
	if err != nil {
		return fmt.Errorf("保存通知投递结果: %w", err)
	}
	return nil
}

func (s *Store) RetryTelegramNotification(ctx context.Context, id int64, next time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE telegram_notifications SET attempts=attempts+1,due=? WHERE id=? AND status='pending'`, next.Unix(), id)
	if err != nil {
		return fmt.Errorf("保存通知重试时间: %w", err)
	}
	return nil
}

func (s *Store) PruneTelegramNotifications(ctx context.Context, before time.Time) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM telegram_notifications WHERE created_at<?`, before.Unix())
	if err != nil {
		return fmt.Errorf("清理过期通知: %w", err)
	}
	return nil
}
