package storage

import (
	"context"
	"testing"

	"strm115/backend/internal/model"
)

func enqueuePoolNotification(t *testing.T, s *Store, task, bot string, chat int64) model.TelegramNotification {
	t.Helper()
	n := model.TelegramNotification{TaskID: task, BotID: bot, ChatID: chat, Text: "播放池状态"}
	if err := s.EnqueueTelegramNotification(context.Background(), n); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT id FROM telegram_notifications WHERE task_id=? AND bot_id=? AND chat_id=?`, task, bot, chat).Scan(&n.ID); err != nil {
		t.Fatal(err)
	}
	return n
}

func assertPoolNotificationValidity(t *testing.T, s *Store, n model.TelegramNotification, want bool) {
	t.Helper()
	valid, err := s.ValidateTelegramNotification(context.Background(), n)
	if err != nil || valid != want {
		t.Fatal("播放池通知发送条件不正确", valid, err)
	}
	var status, text string
	if err := s.db.QueryRow(`SELECT status,text FROM telegram_notifications WHERE id=?`, n.ID).Scan(&status, &text); err != nil {
		t.Fatal(err)
	}
	if want && status != "pending" || !want && (status != "failed" || text != "") {
		t.Fatal("失效消息未终止或清理正文", status, text)
	}
}

func TestPoolNotificationRechecksIncidentBeforeSend(t *testing.T) {
	for _, action := range []string{"active", "reason-changed", "recovered", "new-incident", "same-millisecond", "bot-changed", "missing-event", "invalid-id"} {
		t.Run(action, func(t *testing.T) {
			s, _ := testStore(t)
			d := model.PlaybackPoolDocument{
				Settings: model.PlaybackPoolSettings{NotificationBotID: "profile"},
				Health: map[string]model.PlaybackPoolHealth{
					"account": {State: "cooldown", FailureSince: 100},
				},
				Events: []model.PlaybackPoolEvent{{ID: 1, AccountID: "account", State: "cooldown", Since: 100, BotID: "profile"}},
			}
			h := d.Health["account"]
			task := "pool-health:1"
			switch action {
			case "reason-changed":
				h.State = "invalid_credentials"
			case "recovered":
				h.State = "ready"
			case "new-incident":
				h.FailureSince = 200
			case "same-millisecond":
				d.Events = append(d.Events, model.PlaybackPoolEvent{ID: 2, AccountID: "account", State: "ready", Since: 100})
			case "bot-changed":
				d.Settings.NotificationBotID = "other-profile"
			case "missing-event":
				d.Events = nil
			case "invalid-id":
				task = "pool-health:invalid"
			}
			d.Health["account"] = h
			if err := s.SavePlaybackPool(context.Background(), d); err != nil {
				t.Fatal(err)
			}
			n := enqueuePoolNotification(t, s, task, "123", 42)
			assertPoolNotificationValidity(t, s, n, action == "active" || action == "reason-changed")
		})
	}
}

func TestPoolRecoveryRequiresDeliveredAlertInSameDestination(t *testing.T) {
	for _, action := range []string{"sent", "pending", "failed", "missing", "other-bot", "other-chat", "missing-reference", "new-incident"} {
		t.Run(action, func(t *testing.T) {
			s, _ := testStore(t)
			ctx := context.Background()
			d := model.PlaybackPoolDocument{
				Settings: model.PlaybackPoolSettings{NotificationBotID: "profile"},
				Health: map[string]model.PlaybackPoolHealth{
					"account": {State: "ready", FailureSince: 100},
				},
				Events: []model.PlaybackPoolEvent{{ID: 2, AccountID: "account", State: "ready", Since: 100, BotID: "profile", RecoveryOf: 1}},
			}
			if action == "missing-reference" {
				d.Events[0].RecoveryOf = 0
			}
			if action == "new-incident" {
				d.Health["account"] = model.PlaybackPoolHealth{State: "cooldown", FailureSince: 200}
			}
			if err := s.SavePlaybackPool(ctx, d); err != nil {
				t.Fatal(err)
			}
			if action != "missing" {
				bot, chat := "123", int64(42)
				if action == "other-bot" {
					bot = "456"
				}
				if action == "other-chat" {
					chat = 43
				}
				alert := enqueuePoolNotification(t, s, "pool-health:1", bot, chat)
				if action != "pending" {
					if err := s.CompleteTelegramNotification(ctx, alert.ID, action != "failed"); err != nil {
						t.Fatal(err)
					}
				}
			}
			n := enqueuePoolNotification(t, s, "pool-health:2", "123", 42)
			assertPoolNotificationValidity(t, s, n, action == "sent")
		})
	}
}

func TestPoolNotificationStorageErrorPreservesPendingMessage(t *testing.T) {
	s, _ := testStore(t)
	n := enqueuePoolNotification(t, s, "pool-health:1", "123", 42)
	if _, err := s.db.Exec(`INSERT INTO playback_pool(id,record) VALUES(1,'invalid-json')`); err != nil {
		t.Fatal(err)
	}
	if valid, err := s.ValidateTelegramNotification(context.Background(), n); err == nil || valid {
		t.Fatal("存储读取失败被当作消息失效", valid, err)
	}
	var status string
	if err := s.db.QueryRow(`SELECT status FROM telegram_notifications WHERE id=?`, n.ID).Scan(&status); err != nil || status != "pending" {
		t.Fatal("读取失败后丢弃了待重试消息", status, err)
	}
}
