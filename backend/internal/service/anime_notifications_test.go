package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"strm115/backend/internal/model"
)

type animeNotifierFunc func(context.Context, model.AnimeNotificationEvent) (*model.TelegramNotification, error)

func (f animeNotifierFunc) PrepareAnimeNotification(ctx context.Context, event model.AnimeNotificationEvent) (*model.TelegramNotification, error) {
	return f(ctx, event)
}

func TestAnimeNotificationsRetryLocallyWithoutReplayingDownload(t *testing.T) {
	f, db, job, _, _ := animeStatusFixture(t)
	ctx := context.Background()
	settings, err := f.service.Store.GetAnimeNotificationSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	settings.BotID, settings.DownloadComplete = "anime-bot", true
	if _, err := f.service.Store.SaveAnimeNotificationSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE anime_download_jobs SET download_state='completed',updated_at=? WHERE id=?`, time.Now().UnixMilli(), job.ID); err != nil {
		t.Fatal(err)
	}
	calls := 0
	f.service.Notifications = animeNotifierFunc(func(_ context.Context, event model.AnimeNotificationEvent) (*model.TelegramNotification, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("本地通知存储暂不可用")
		}
		return &model.TelegramNotification{TaskID: "anime:test", BotID: "123", ChatID: 42, Text: "<b>下载完成</b>", ParseMode: "HTML"}, nil
	})
	f.service.DeliverAnimeNotifications(ctx)
	events, err := f.service.Store.PendingAnimeNotificationEvents(ctx)
	if err != nil || len(events) != 1 || calls != 1 {
		t.Fatal(events, calls, err)
	}
	f.service.DeliverAnimeNotifications(ctx)
	if calls != 1 {
		t.Fatal("失败重试没有执行退避")
	}
	f.service.notificationRetryAt = time.Time{}
	f.service.DeliverAnimeNotifications(ctx)
	f.service.DeliverAnimeNotifications(ctx)
	events, err = f.service.Store.PendingAnimeNotificationEvents(ctx)
	if err != nil || len(events) != 0 || calls != 2 {
		t.Fatal(events, calls, err)
	}
	queued, err := f.service.Store.DueTelegramNotifications(ctx, "123", 42, time.Now().Add(time.Second))
	if err != nil || len(queued) != 1 || queued[0].ParseMode != "HTML" {
		t.Fatal(queued, err)
	}
	current, err := f.service.Store.GetAnimeDownloadJob(ctx, job.ID)
	if err != nil || current.DownloadState != "completed" {
		t.Fatal("通知入队改变了下载状态", current.DownloadState, err)
	}
}
