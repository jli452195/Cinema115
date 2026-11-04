package bot

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
)

func photoNotificationManager(t *testing.T, handler http.HandlerFunc, text string) (*Manager, *storage.Store, string) {
	t.Helper()
	m, dir := configTestManager(t, handler)
	ctx := context.Background()
	if err := m.Save(ctx, BotConfig{Token: "123:valid-token", NotificationChatID: "42"}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "photos.db")
	db, err := storage.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	m.Notifications = db
	if err := db.EnqueueTelegramNotification(ctx, model.TelegramNotification{TaskID: "photo:test", BotID: "123", ChatID: 42, Text: text, ParseMode: "HTML", PhotoURL: "https://images.example.com/poster.jpg"}); err != nil {
		t.Fatal(err)
	}
	return m, db, path
}

func TestNotificationPhotoWithShortHTMLCaptionCompletesTogether(t *testing.T) {
	photos, messages := 0, 0
	text := "<b>动漫整理完成</b> &amp; 新一集"
	m, db, _ := photoNotificationManager(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/sendPhoto"):
			photos++
			var input struct {
				ChatID    int64  `json:"chat_id"`
				Photo     string `json:"photo"`
				Caption   string `json:"caption"`
				ParseMode string `json:"parse_mode"`
			}
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil || input.ChatID != 42 || input.Caption != text || input.ParseMode != "HTML" || input.Photo != "https://images.example.com/poster.jpg" {
				t.Error(input, err)
			}
			_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":1}}`))
		case strings.HasSuffix(r.URL.Path, "/sendMessage"):
			messages++
			t.Error("短正文不应额外发送文字消息")
		default:
			identityHandler(t, w, r)
		}
	}, text)
	defer db.Close()
	if err := m.deliverNotification(context.Background(), time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if photos != 1 || messages != 0 {
		t.Fatal(photos, messages)
	}
	pending, err := db.DueTelegramNotifications(context.Background(), "123", 42, time.Now().Add(time.Hour))
	if err != nil || len(pending) != 0 {
		t.Fatal(pending, err)
	}
}

func TestNotificationLongTextRetryDoesNotRepeatPhotoAfterRestart(t *testing.T) {
	photos, messages := 0, 0
	text := "<b>" + strings.Repeat("动漫更新", 400) + "</b>"
	m, db, path := photoNotificationManager(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/sendPhoto"):
			photos++
			var input map[string]any
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil || input["caption"] != nil || input["parse_mode"] != nil {
				t.Error(input, err)
			}
			_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
		case strings.HasSuffix(r.URL.Path, "/sendMessage"):
			messages++
			if messages == 1 {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
		default:
			identityHandler(t, w, r)
		}
	}, text)
	ctx, now := context.Background(), time.Now().Add(time.Second)
	if err := m.deliverNotification(ctx, now); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := storage.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	m.Notifications = db
	for _, instant := range []time.Time{now.Add(time.Second), now.Add(2 * time.Second), now.Add(time.Hour)} {
		if err := m.deliverNotification(ctx, instant); err != nil {
			t.Fatal(err)
		}
	}
	if photos != 1 || messages != 2 {
		t.Fatal("正文重试重复发送图片", photos, messages)
	}
}

func TestNotificationRejectedPhotoFallsBackToText(t *testing.T) {
	for _, envelopeRejection := range []bool{false, true} {
		t.Run(map[bool]string{false: "http400", true: "envelope400"}[envelopeRejection], func(t *testing.T) {
			photos, messages := 0, 0
			m, db, _ := photoNotificationManager(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasSuffix(r.URL.Path, "/sendPhoto"):
					photos++
					if !envelopeRejection {
						w.WriteHeader(http.StatusBadRequest)
					}
					_, _ = w.Write([]byte(`{"ok":false,"error_code":400,"description":"failed to get HTTP URL content"}`))
				case strings.HasSuffix(r.URL.Path, "/sendMessage"):
					messages++
					_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
				default:
					identityHandler(t, w, r)
				}
			}, "<b>图片失效后仍需收到正文</b>")
			defer db.Close()
			for i := 0; i < 3; i++ {
				if err := m.deliverNotification(context.Background(), time.Now().Add(time.Duration(i+1)*time.Second)); err != nil {
					t.Fatal(err)
				}
			}
			if photos != 1 || messages != 1 {
				t.Fatal(photos, messages)
			}
		})
	}
}

func TestNotificationPhotoRetryBudgetPreservesFreshTextBudget(t *testing.T) {
	photos, messages := 0, 0
	m, db, _ := photoNotificationManager(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/sendPhoto"):
			photos++
			w.WriteHeader(http.StatusServiceUnavailable)
		case strings.HasSuffix(r.URL.Path, "/sendMessage"):
			messages++
			if messages == 1 {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
		default:
			identityHandler(t, w, r)
		}
	}, "<b>正文不能随图片重试耗尽丢失</b>")
	defer db.Close()
	for i := 0; i < 10; i++ {
		if err := m.deliverNotification(context.Background(), time.Now().Add(time.Duration(i+1)*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	if photos != 8 || messages != 2 {
		t.Fatal(photos, messages)
	}
}
