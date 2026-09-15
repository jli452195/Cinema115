package storage

import (
	"context"
	"testing"
	"time"

	"strm115/backend/internal/model"
)

func TestNotificationMigrationRetryAndRetention(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	if _, err := s.db.ExecContext(ctx, "DROP TABLE follow_bot_jobs; DROP TABLE website_notifications; DROP TABLE follow_checks; DROP TABLE follow_settings; DROP TABLE user_follows; DROP TABLE follow_requests; DROP TABLE website_users; DROP TABLE telegram_notifications; PRAGMA user_version=6;"); err != nil {
		t.Fatal(err)
	}
	if err := s.initialize(ctx); err != nil {
		t.Fatal(err)
	}
	n := model.TelegramNotification{TaskID: "task", BotID: "123", ChatID: 42, Text: "生成成功", PreviewURL: "https://115.com/s/source"}
	if err := s.EnqueueTelegramNotification(ctx, n); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	list, err := s.DueTelegramNotifications(ctx, "123", 42, now)
	if err != nil || len(list) != 1 {
		t.Fatal("通知未入队", err)
	}
	if err := s.RetryTelegramNotification(ctx, list[0].ID, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	list, err = s.DueTelegramNotifications(ctx, "123", 42, now)
	if err != nil || len(list) != 0 {
		t.Fatal("重试时间不正确", err)
	}
	list, err = s.DueTelegramNotifications(ctx, "123", 42, now.Add(time.Hour))
	if err != nil || len(list) != 1 || list[0].Attempts != 1 {
		t.Fatal("重试次数未持久化", err)
	}
	if err := s.CompleteTelegramNotification(ctx, list[0].ID, false); err != nil {
		t.Fatal(err)
	}
	if err := s.EnqueueTelegramNotification(ctx, n); err != nil {
		t.Fatal(err)
	}
	list, err = s.DueTelegramNotifications(ctx, "123", 42, now.Add(time.Hour))
	if err != nil || len(list) != 0 {
		t.Fatal("已终止任务重新进入发送队列", err)
	}
	var text, preview string
	if err := s.db.QueryRowContext(ctx, "SELECT text,preview_url FROM telegram_notifications").Scan(&text, &preview); err != nil || text != "" || preview != "" {
		t.Fatal("结束后未清除正文", err)
	}
	if err := s.PruneTelegramNotifications(ctx, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM telegram_notifications").Scan(&count); err != nil || count != 0 {
		t.Fatal("过期记录未清理", err)
	}
}
