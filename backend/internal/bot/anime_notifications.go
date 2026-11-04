package bot

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"strm115/backend/internal/anime/notification"
	"strm115/backend/internal/model"
)

var ErrAnimeNotificationsUnavailable = errors.New("动漫订阅通知尚未初始化")

func (h *Hub) AnimeNotificationSettings(ctx context.Context) (model.AnimeNotificationSettings, error) {
	if h.Anime == nil || h.Anime.Store == nil {
		return model.AnimeNotificationSettings{}, ErrAnimeNotificationsUnavailable
	}
	return h.Anime.Store.GetAnimeNotificationSettings(ctx)
}

func (h *Hub) SaveAnimeNotificationSettings(ctx context.Context, input model.AnimeNotificationSettings) (model.AnimeNotificationSettings, error) {
	h.moduleMu.Lock()
	defer h.moduleMu.Unlock()
	if h.Anime == nil || h.Anime.Store == nil {
		return model.AnimeNotificationSettings{}, ErrAnimeNotificationsUnavailable
	}
	if err := notification.Validate(input.Template); err != nil {
		return model.AnimeNotificationSettings{}, err
	}
	var err error
	input.ImageMode, input.ImageURL, err = notification.NormalizeImage(input.ImageMode, input.ImageURL)
	if err != nil {
		return model.AnimeNotificationSettings{}, err
	}
	if strings.TrimSpace(input.Template) == "" {
		input.Template = ""
	}
	if input.BotID != "" {
		if _, err := h.profiles.Get(input.BotID); err != nil {
			return model.AnimeNotificationSettings{}, err
		}
	}
	out, err := h.Anime.Store.SaveAnimeNotificationSettings(ctx, input)
	if err == nil {
		h.Refresh()
	}
	return out, err
}

func animeEventEnabled(settings model.AnimeNotificationSettings, kind string) bool {
	switch kind {
	case "downloaded":
		return settings.DownloadComplete
	case "organized":
		return settings.Organized
	case "failed":
		return settings.Failed
	}
	return false
}

// 消息使用事件发生时的模板和 Bot 身份，不把旧事件改派到新的 Bot。
func (h *Hub) PrepareAnimeNotification(ctx context.Context, event model.AnimeNotificationEvent) (*model.TelegramNotification, error) {
	settings, err := h.AnimeNotificationSettings(ctx)
	if err != nil {
		return nil, err
	}
	if event.BotID == "" || settings.BotID != event.BotID || !animeEventEnabled(settings, event.Kind) {
		return nil, nil
	}
	if err := h.ValidateSelection(event.BotID, true); err != nil {
		return nil, err
	}
	profile, err := h.profiles.Get(event.BotID)
	if err != nil {
		return nil, err
	}
	chatID, err := strconv.ParseInt(profile.NotificationChatID, 10, 64)
	if err != nil || chatID == 0 {
		return nil, fmt.Errorf("%w：通知聊天 ID 无效", ErrConfig)
	}
	text, err := notification.Render(event.Template, notification.Values(event))
	if err != nil {
		return nil, err
	}
	return &model.TelegramNotification{
		TaskID: event.TelegramTaskID(),
		BotID:  strings.SplitN(profile.Token, ":", 2)[0], ChatID: chatID,
		Text: text, ParseMode: "HTML", PhotoURL: notification.PhotoURL(event.ImageMode, event.ImageURL, event.CoverURL),
		AnimeJobID: event.JobID, AnimeGeneration: event.Generation, AnimeKind: event.Kind,
		AnimeBlockCode: event.BlockCode, AnimeBotID: event.BotID, AnimePhase: event.Phase,
	}, nil
}
