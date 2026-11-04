package service

import (
	"context"
	"time"

	"strm115/backend/internal/model"
)

type AnimeNotifier interface {
	PrepareAnimeNotification(context.Context, model.AnimeNotificationEvent) (*model.TelegramNotification, error)
}

// 动漫任务只写本地事件；独立通知队列负责 Telegram 网络发送与重试。
func (s *AnimeAdminService) DeliverAnimeNotifications(ctx context.Context) {
	if s == nil || s.Store == nil || s.Notifications == nil || ctx.Err() != nil || !s.notificationMu.TryLock() {
		return
	}
	defer s.notificationMu.Unlock()
	if time.Now().Before(s.notificationRetryAt) {
		return
	}
	events, err := s.Store.PendingAnimeNotificationEvents(ctx)
	if err != nil {
		s.animeNotificationFailure(ctx, "", err)
		return
	}
	for _, event := range events {
		if ctx.Err() != nil {
			return
		}
		notification, err := s.Notifications.PrepareAnimeNotification(ctx, event)
		if err != nil {
			s.animeNotificationFailure(ctx, event.JobID, err)
			continue
		}
		if err := s.Store.CompleteAnimeNotificationEvent(ctx, event.ID, notification); err != nil {
			s.animeNotificationFailure(ctx, event.JobID, err)
			return
		}
	}
}

func (s *AnimeAdminService) animeNotificationFailure(ctx context.Context, jobID string, err error) {
	if ctx.Err() != nil {
		return
	}
	s.notificationRetryAt = time.Now().Add(30 * time.Second)
	s.logAnime(jobID, "动漫通知", "WARNING", "通知入队失败，将稍后重试："+err.Error())
}
