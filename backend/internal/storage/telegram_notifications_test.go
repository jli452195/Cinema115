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
	if _, err := s.db.ExecContext(ctx, "DROP TABLE follow_bot_jobs; DROP TABLE website_notifications; DROP TABLE follow_checks; DROP TABLE follow_settings; DROP TABLE user_follows; DROP TABLE follow_requests;  DROP TABLE telegram_notifications; PRAGMA user_version=6;"); err != nil {
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

func TestTelegramNotificationPhotoProgressSurvivesRetryAndInitialization(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	n := model.TelegramNotification{TaskID: "anime:photo", BotID: "123", ChatID: 42, Text: "<b>动漫整理完成</b>", ParseMode: "HTML", PhotoURL: "https://example.org/cover.jpg"}
	if err := s.EnqueueTelegramNotification(ctx, n); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Add(time.Second)
	due, err := s.DueTelegramNotifications(ctx, n.BotID, n.ChatID, now)
	if err != nil || len(due) != 1 || due[0].PhotoURL != n.PhotoURL || due[0].PhotoSent {
		t.Fatal("图片未入队或提前标记送达", due, err)
	}
	id := due[0].ID
	if _, err := s.db.ExecContext(ctx, `UPDATE telegram_notifications SET attempts=7,due=? WHERE id=?`, now.Add(time.Hour).Unix(), id); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkTelegramNotificationPhotoSent(ctx, id); err != nil {
		t.Fatal(err)
	}
	due, err = s.DueTelegramNotifications(ctx, n.BotID, n.ChatID, time.Now().Add(time.Second))
	if err != nil || len(due) != 1 || !due[0].PhotoSent || due[0].Attempts != 0 {
		t.Fatal("图片阶段结束后正文没有立即获得独立重试预算", due, err)
	}
	if err := s.RetryTelegramNotification(ctx, id, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := s.initialize(ctx); err != nil {
		t.Fatal(err)
	}
	due, err = s.DueTelegramNotifications(ctx, n.BotID, n.ChatID, now.Add(time.Hour))
	if err != nil || len(due) != 1 || !due[0].PhotoSent || due[0].PhotoURL != n.PhotoURL || due[0].Text != n.Text || due[0].Attempts != 1 {
		t.Fatal("正文重试丢失已发送的图片进度", due, err)
	}
	if err := s.CompleteTelegramNotification(ctx, id, true); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkTelegramNotificationPhotoSent(ctx, id); err != nil {
		t.Fatal(err)
	}
	var photo string
	var sent bool
	if err := s.db.QueryRowContext(ctx, `SELECT photo_url,photo_sent FROM telegram_notifications WHERE id=?`, id).Scan(&photo, &sent); err != nil || photo != "" || sent {
		t.Fatal("完成的通知仍保留图片或被旧请求标记", photo, sent, err)
	}
}
