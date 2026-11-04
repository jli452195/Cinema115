package bot

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/anime/notification"
	"strm115/backend/internal/model"
	"strm115/backend/internal/service"
	"strm115/backend/internal/storage"
)

func animeNotificationHub(t *testing.T) (*Hub, string) {
	t.Helper()
	ctx := context.Background()
	hub, err := NewHub(t.TempDir(), "test-secret", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "anime-notifications.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	hub.Anime = &service.AnimeAdminService{Store: db}
	id, err := hub.Add("动漫通知 Bot")
	if err != nil {
		t.Fatal(err)
	}
	profile, _ := hub.Profiles().Get(id)
	profile.Token, profile.AllowedChatIDs, profile.NotificationChatID, profile.Enabled = "123:test-token", "42", "-10077", true
	if err := hub.Profiles().Update(profile, profile.Revision); err != nil {
		t.Fatal(err)
	}
	return hub, id
}

func TestAnimeNotificationPreparationEscapesValuesAndUsesHTML(t *testing.T) {
	hub, id := animeNotificationHub(t)
	ctx := context.Background()
	if err := hub.SetModule(ctx, "anime_notifications", "", id); err != nil {
		t.Fatal(err)
	}
	event := notification.Sample("organized")
	event.BotID, event.Template, event.Title = id, `<b>{title}</b> {event}`, `<script>title</script>`
	event.Phase = "organize"
	n, err := hub.PrepareAnimeNotification(ctx, event)
	if err != nil || n == nil || n.BotID != "123" || n.ChatID != -10077 || n.TaskID != event.TelegramTaskID() || n.ParseMode != "HTML" || n.PhotoURL != "https://lain.bgm.tv/pic/cover/l/13/c5/400602_ZI8Y9.jpg" || strings.Contains(n.Text, "<script>") {
		t.Fatal(n, err)
	}
	if n.AnimeJobID != event.JobID || n.AnimeGeneration != event.Generation || n.AnimeKind != event.Kind || n.AnimeBlockCode != event.BlockCode || n.AnimeBotID != event.BotID || n.AnimePhase != event.Phase {
		t.Fatal("发送关联未保留任务与 Bot 身份", n)
	}
	event.ImageMode, event.ImageURL = "custom", "https://images.example.com/custom.jpg"
	if n, err := hub.PrepareAnimeNotification(ctx, event); err != nil || n == nil || n.PhotoURL != event.ImageURL {
		t.Fatal(n, err)
	}
	request := (&pendingReply{ChatID: n.ChatID, Text: n.Text, ParseMode: n.ParseMode}).request()
	if request["parse_mode"] != "HTML" || request["text"] != n.Text {
		t.Fatal(request)
	}
	if _, hasMode := (&pendingReply{ChatID: 42, Text: "existing plain text"}).request()["parse_mode"]; hasMode {
		t.Fatal("普通消息不能自动启用 HTML")
	}
	if kind, _ := notificationLabels(n.TaskID); kind != "动漫通知" {
		t.Fatal(kind)
	}
	if err := hub.SetModule(ctx, "anime_notifications", id, ""); err != nil {
		t.Fatal(err)
	}
	if n, err := hub.PrepareAnimeNotification(ctx, event); err != nil || n != nil {
		t.Fatal("解除分配后旧事件仍可发送", n, err)
	}
}

func TestAnimeNotificationBusinessIdentitySurvivesEventIDReuse(t *testing.T) {
	hub, id := animeNotificationHub(t)
	ctx := context.Background()
	if err := hub.SetModule(ctx, "anime_notifications", "", id); err != nil {
		t.Fatal(err)
	}
	settings, err := hub.AnimeNotificationSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	settings.DownloadComplete = true
	if _, err := hub.SaveAnimeNotificationSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	store := hub.Anime.Store
	if err := store.EnqueueTelegramNotification(ctx, model.TelegramNotification{TaskID: "anime:1", BotID: "123", ChatID: -10077, Text: "旧任务已发送"}); err != nil {
		t.Fatal(err)
	}
	old, err := store.DueTelegramNotifications(ctx, "123", -10077, time.Now().Add(time.Second))
	if err != nil || len(old) != 1 {
		t.Fatal(old, err)
	}
	if err := store.CompleteTelegramNotification(ctx, old[0].ID, true); err != nil {
		t.Fatal(err)
	}
	base := notification.Sample("organized")
	base.BotID, base.Phase = id, "organize"
	variants := []model.AnimeNotificationEvent{base, base, base, base, base, base}
	variants[1].JobID = "another-download-job"
	variants[2].Generation++
	variants[3].Kind = "downloaded"
	variants[4].Phase = "files"
	variants[5].BlockCode = "ORGANIZE_TARGET_CONFLICT"
	keys := make(map[string]bool, len(variants))
	for _, event := range variants {
		n, err := hub.PrepareAnimeNotification(ctx, event)
		if err != nil || n == nil || keys[n.TaskID] || n.TaskID == "anime:1" {
			t.Fatal("新任务与历史事件编号冲突，或不同业务事件合并", n, err)
		}
		keys[n.TaskID] = true
		if err := store.EnqueueTelegramNotification(ctx, *n); err != nil {
			t.Fatal(err)
		}
	}
	base.ID = 9999
	repeated, err := hub.PrepareAnimeNotification(ctx, base)
	if err != nil || repeated == nil {
		t.Fatal(repeated, err)
	}
	if err := store.EnqueueTelegramNotification(ctx, *repeated); err != nil {
		t.Fatal(err)
	}
	due, err := store.DueTelegramNotifications(ctx, "123", -10077, time.Now().Add(time.Second))
	if err != nil || len(due) != len(variants) {
		t.Fatal("重建事件编号丢失新任务或重复发送同一业务事件", due, err)
	}
}

func TestAnimeNotificationSettingsAndModuleUseSameRevision(t *testing.T) {
	hub, id := animeNotificationHub(t)
	ctx := context.Background()
	settings, err := hub.AnimeNotificationSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	settings.BotID, settings.Template = id, `<b>{title}</b>`
	saved, err := hub.SaveAnimeNotificationSettings(ctx, settings)
	if err != nil || saved.Revision != settings.Revision+1 {
		t.Fatal(saved, err)
	}
	if _, err := hub.SaveAnimeNotificationSettings(ctx, settings); !errors.Is(err, storage.ErrAnimeConflict) {
		t.Fatal("旧修订覆盖了新设置", err)
	}
	if err := hub.SetModule(ctx, "anime_notifications", "", id); !errors.Is(err, ErrModuleChanged) {
		t.Fatal("旧模块分配覆盖了通知设置", err)
	}
	invalid := saved
	invalid.Template = `<script>{title}</script>`
	if _, err := hub.SaveAnimeNotificationSettings(ctx, invalid); !errors.Is(err, notification.ErrTemplate) {
		t.Fatal(err)
	}
	if err := hub.DeleteWithModules(ctx, id, hub.List()[0].Revision, true); err != nil {
		t.Fatal(err)
	}
	latest, err := hub.AnimeNotificationSettings(ctx)
	if err != nil || latest.BotID != "" || latest.Template != saved.Template || latest.Revision != saved.Revision+1 {
		t.Fatal(latest, err)
	}
}
