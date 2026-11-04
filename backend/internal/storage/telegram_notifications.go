package storage

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"strm115/backend/internal/model"
)

func (s *Store) EnqueueTelegramNotification(ctx context.Context, n model.TelegramNotification) error {
	return enqueueTelegramNotification(ctx, s.db, n)
}

func enqueueTelegramNotification(ctx context.Context, db interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, n model.TelegramNotification) error {
	now := time.Now().Unix()
	result, err := db.ExecContext(ctx, `INSERT INTO telegram_notifications(task_id,bot_id,chat_id,text,parse_mode,preview_url,photo_url,photo_sent,due,created_at,anime_job_id,anime_generation,anime_kind,anime_phase,anime_block_code,anime_bot_id) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(task_id,bot_id,chat_id) DO NOTHING`,
		n.TaskID, n.BotID, n.ChatID, n.Text, n.ParseMode, n.PreviewURL, n.PhotoURL, n.PhotoSent, now, now, n.AnimeJobID, n.AnimeGeneration, n.AnimeKind, n.AnimePhase, n.AnimeBlockCode, n.AnimeBotID)
	if err != nil {
		return fmt.Errorf("保存 Telegram 通知: %w", err)
	}
	if n.AnimeJobID == "" && !strings.HasPrefix(n.TaskID, "anime:") {
		return nil
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("读取 Telegram 通知保存结果: %w", err)
	}
	if inserted == 0 {
		var sameEvent bool
		if n.AnimeJobID != "" {
			if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM telegram_notifications WHERE task_id=? AND bot_id=? AND chat_id=? AND anime_job_id=? AND anime_generation=? AND anime_kind=? AND anime_phase=? AND anime_block_code=? AND anime_bot_id=?)`, n.TaskID, n.BotID, n.ChatID, n.AnimeJobID, n.AnimeGeneration, n.AnimeKind, n.AnimePhase, n.AnimeBlockCode, n.AnimeBotID).Scan(&sameEvent); err != nil {
				return fmt.Errorf("复核 Telegram 通知去重归属: %w", err)
			}
		}
		if !sameEvent {
			return fmt.Errorf("Telegram 通知去重键已属于其他事件: %w", ErrAnimeConflict)
		}
	}
	return nil
}

func (s *Store) DueTelegramNotifications(ctx context.Context, botID string, chatID int64, now time.Time) ([]model.TelegramNotification, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,task_id,bot_id,chat_id,text,parse_mode,preview_url,photo_url,photo_sent,attempts,anime_job_id,anime_generation,anime_kind,anime_phase,anime_block_code,anime_bot_id FROM telegram_notifications WHERE status='pending' AND bot_id=? AND chat_id=? AND due<=? ORDER BY due,id LIMIT 20`, botID, chatID, now.Unix())
	if err != nil {
		return nil, fmt.Errorf("查询待发送通知: %w", err)
	}
	defer rows.Close()
	result := make([]model.TelegramNotification, 0, 20)
	for rows.Next() {
		var n model.TelegramNotification
		if err := rows.Scan(&n.ID, &n.TaskID, &n.BotID, &n.ChatID, &n.Text, &n.ParseMode, &n.PreviewURL, &n.PhotoURL, &n.PhotoSent, &n.Attempts, &n.AnimeJobID, &n.AnimeGeneration, &n.AnimeKind, &n.AnimePhase, &n.AnimeBlockCode, &n.AnimeBotID); err != nil {
			return nil, fmt.Errorf("读取通知: %w", err)
		}
		result = append(result, n)
	}
	return result, rows.Err()
}

// ValidateTelegramNotification 在每次外部投递前复核动漫任务，丢弃已恢复或旧代次消息。
func (s *Store) ValidateTelegramNotification(ctx context.Context, n model.TelegramNotification) (bool, error) {
	if n.AnimeJobID == "" && !strings.HasPrefix(n.TaskID, "anime:") {
		return true, nil
	}
	var current bool
	if n.AnimeJobID != "" {
		err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM telegram_notifications t JOIN anime_notification_events e ON e.job_id=t.anime_job_id AND e.generation=t.anime_generation AND e.kind=t.anime_kind AND e.phase=t.anime_phase AND e.block_code=t.anime_block_code AND e.bot_id=t.anime_bot_id JOIN anime_download_jobs j ON j.id=e.job_id JOIN anime_subscriptions p ON p.id=e.subscription_id JOIN anime_notification_settings c ON c.id=1 WHERE t.id=? AND t.status='pending' AND e.processed=1 AND e.invalidated=0 AND c.bot_id=e.bot_id AND CASE e.kind WHEN 'downloaded' THEN c.download_complete WHEN 'organized' THEN c.organized WHEN 'failed' THEN c.failed ELSE 0 END AND `+animeNotificationCurrentSQL+`)`, n.ID, time.Now().UnixMilli()).Scan(&current)
		if err != nil {
			return false, fmt.Errorf("复核待发送动漫通知: %w", err)
		}
	}
	if current {
		return true, nil
	}
	// 旧版本缺少类型化关联的动漫消息不能从正文推断任务授权。
	if err := s.CompleteTelegramNotification(ctx, n.ID, false); err != nil {
		return false, err
	}
	return false, nil
}

func (s *Store) CompleteTelegramNotification(ctx context.Context, id int64, delivered bool) error {
	status := "failed"
	if delivered {
		status = "sent"
	}
	// 留下轻量去重记录，发送结束后不继续保存消息正文。
	_, err := s.db.ExecContext(ctx, `UPDATE telegram_notifications SET status=?,text='',parse_mode='',preview_url='',photo_url='',photo_sent=0 WHERE id=?`, status, id)
	if err != nil {
		return fmt.Errorf("保存通知投递结果: %w", err)
	}
	return nil
}

// MarkTelegramNotificationPhotoSent 记录图片阶段完成或跳过，正文从独立重试预算开始。
func (s *Store) MarkTelegramNotificationPhotoSent(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE telegram_notifications SET photo_sent=1,attempts=0,due=? WHERE id=? AND status='pending'`, time.Now().Unix(), id)
	if err != nil {
		return fmt.Errorf("保存通知图片投递进度: %w", err)
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
