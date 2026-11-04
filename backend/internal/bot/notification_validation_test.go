package bot

import (
	"context"
	"database/sql"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
)

type notificationAfterReadStore struct {
	NotificationStore
	afterRead func() error
}

func (s *notificationAfterReadStore) DueTelegramNotifications(ctx context.Context, botID string, chatID int64, now time.Time) ([]model.TelegramNotification, error) {
	pending, err := s.NotificationStore.DueTelegramNotifications(ctx, botID, chatID, now)
	if err != nil {
		return nil, err
	}
	if s.afterRead != nil {
		if err := s.afterRead(); err != nil {
			return nil, err
		}
		s.afterRead = nil
	}
	return pending, nil
}

func pendingAnimeNotificationManager(t *testing.T, handler http.HandlerFunc, text string) (*Manager, *storage.Store, *sql.DB) {
	t.Helper()
	m, dir := configTestManager(t, handler)
	ctx := context.Background()
	if err := m.Save(ctx, BotConfig{Token: "123:valid-token", NotificationChatID: "42"}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "anime-send-guard.db")
	store, err := storage.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	m.Notifications = store
	settings, err := store.GetAnimeNotificationSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	settings.BotID = "anime-profile"
	if _, err := store.SaveAnimeNotificationSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	queries := []string{
		`INSERT INTO anime_entries(id,edition_key,title,original_title,metadata_json,revision,created_at,updated_at) VALUES('entry','','测试','测试','{}',1,1,1)`,
		`INSERT INTO anime_subscriptions(id,entry_id,status,revision,run_generation,form_json,created_at,updated_at) VALUES('sub','entry','enabled',1,1,'{}',1,1)`,
		`INSERT INTO anime_resources(id,protocol,info_hash,verification_version,verified_at,evidence_json) VALUES('resource','bt_v1','0123456789012345678901234567890123456789',1,1,'{}')`,
		`INSERT INTO anime_resource_claims(id,uid,resource_id,account_id,target_cid,created_at) VALUES('claim','uid','resource','main','0',1)`,
		`INSERT INTO anime_download_jobs(id,claim_id,attempt,account_id,uid,settings_revision,parent_cid,directory_name,snapshot_json,download_state,file_state,workflow_state,revision,generation,created_at,updated_at) VALUES('job','claim',1,'main','uid',1,'0','目录','{"source":{"subscriptionId":"sub"}}','downloading','pending','active',1,1,1,1)`,
		`UPDATE anime_download_jobs SET block_code='TASK_TARGET_CONFLICT',file_state='needs_review',updated_at=unixepoch('now')*1000 WHERE id='job'`,
	}
	for _, query := range queries {
		if _, err := db.ExecContext(ctx, query); err != nil {
			t.Fatal(err)
		}
	}
	events, err := store.PendingAnimeNotificationEvents(ctx)
	if err != nil || len(events) != 1 {
		t.Fatal(events, err)
	}
	n := &model.TelegramNotification{TaskID: "anime:" + strconv.FormatInt(events[0].ID, 10), BotID: "123", ChatID: 42, Text: text, ParseMode: "HTML", PhotoURL: "https://images.example.com/poster.jpg"}
	if err := store.CompleteAnimeNotificationEvent(ctx, events[0].ID, n); err != nil {
		t.Fatal(err)
	}
	return m, store, db
}

func TestAnimeNotificationRecoversBeforeSendWithoutNetwork(t *testing.T) {
	for _, action := range []string{"recovered", "new-generation", "disabled"} {
		t.Run(action, func(t *testing.T) {
			sends := 0
			m, store, db := pendingAnimeNotificationManager(t, func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/sendPhoto") || strings.HasSuffix(r.URL.Path, "/sendMessage") {
					sends++
					_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
					return
				}
				identityHandler(t, w, r)
			}, "<b>旧异常</b>")
			ctx := context.Background()
			var err error
			switch action {
			case "recovered":
				_, err = db.ExecContext(ctx, `UPDATE anime_download_jobs SET block_code=NULL,file_state='pending' WHERE id='job'`)
			case "new-generation":
				_, err = db.ExecContext(ctx, `UPDATE anime_download_jobs SET generation=generation+1 WHERE id='job'`)
			case "disabled":
				settings, readErr := store.GetAnimeNotificationSettings(ctx)
				if readErr != nil {
					t.Fatal(readErr)
				}
				settings.Failed = false
				_, err = store.SaveAnimeNotificationSettings(ctx, settings)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := m.deliverNotification(ctx, time.Now().Add(time.Hour)); err != nil {
				t.Fatal(err)
			}
			pending, err := store.DueTelegramNotifications(ctx, "123", 42, time.Now().Add(time.Hour))
			if err != nil || len(pending) != 0 || sends != 0 {
				t.Fatal("失效异常仍发送或重试", sends, pending, err)
			}
			var text, photo string
			if err := db.QueryRowContext(ctx, `SELECT text,photo_url FROM telegram_notifications`).Scan(&text, &photo); err != nil || text != "" || photo != "" {
				t.Fatal("取消后仍保留通知内容", text, photo, err)
			}
		})
	}
}

func TestAnimeNotificationRecoveryAfterQueueReadDoesNotSend(t *testing.T) {
	sends := 0
	m, store, db := pendingAnimeNotificationManager(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/sendPhoto") || strings.HasSuffix(r.URL.Path, "/sendMessage") {
			sends++
			return
		}
		identityHandler(t, w, r)
	}, "<b>读取队列后恢复的异常</b>")
	// 保留工作器已经读取的旧行，在真正发送前让真实任务恢复。
	m.Notifications = &notificationAfterReadStore{NotificationStore: store, afterRead: func() error {
		_, err := db.Exec(`UPDATE anime_download_jobs SET block_code=NULL,file_state='pending' WHERE id='job'`)
		return err
	}}
	if err := m.deliverNotification(context.Background(), time.Now().Add(time.Hour)); err != nil || sends != 0 {
		t.Fatal("读取队列后未重新验证任务", sends, err)
	}
}

func TestAnimeNotificationRecoversBetweenPhotoAndLongText(t *testing.T) {
	photos, messages := 0, 0
	m, _, db := pendingAnimeNotificationManager(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/sendPhoto"):
			photos++
			_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
		case strings.HasSuffix(r.URL.Path, "/sendMessage"):
			messages++
			_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
		default:
			identityHandler(t, w, r)
		}
	}, strings.Repeat("长正文", 400))
	ctx, now := context.Background(), time.Now().Add(time.Second)
	if err := m.deliverNotification(ctx, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE anime_download_jobs SET block_code=NULL,file_state='pending' WHERE id='job'`); err != nil {
		t.Fatal(err)
	}
	if err := m.deliverNotification(ctx, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if photos != 1 || messages != 0 {
		t.Fatal("恢复后继续发送旧异常长正文", photos, messages)
	}
}

func TestAnimeNotificationRecoveryCancelsPhotoAndTextRetries(t *testing.T) {
	for _, stage := range []string{"photo", "text"} {
		t.Run(stage, func(t *testing.T) {
			sends := 0
			m, store, db := pendingAnimeNotificationManager(t, func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/sendPhoto") || strings.HasSuffix(r.URL.Path, "/sendMessage") {
					sends++
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				identityHandler(t, w, r)
			}, "<b>待重试异常</b>")
			ctx, now := context.Background(), time.Now().Add(time.Second)
			if stage == "text" {
				if _, err := db.ExecContext(ctx, `UPDATE telegram_notifications SET photo_sent=1`); err != nil {
					t.Fatal(err)
				}
			}
			if err := m.deliverNotification(ctx, now); err != nil || sends != 1 {
				t.Fatal("未进入发送重试", sends, err)
			}
			if _, err := db.ExecContext(ctx, `UPDATE anime_download_jobs SET block_code=NULL,file_state='pending' WHERE id='job'`); err != nil {
				t.Fatal(err)
			}
			if err := m.deliverNotification(ctx, now.Add(time.Hour)); err != nil || sends != 1 {
				t.Fatal("恢复后仍重试旧异常", sends, err)
			}
			pending, err := store.DueTelegramNotifications(ctx, "123", 42, now.Add(time.Hour))
			if err != nil || len(pending) != 0 {
				t.Fatal(pending, err)
			}
		})
	}
}

func TestLegacyAnimeNotificationWithoutAssociationDoesNotSend(t *testing.T) {
	sends := 0
	m, store, _ := photoNotificationManager(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/sendPhoto") || strings.HasSuffix(r.URL.Path, "/sendMessage") {
			sends++
			return
		}
		identityHandler(t, w, r)
	}, "ordinary")
	defer store.Close()
	ctx := context.Background()
	pending, err := store.DueTelegramNotifications(ctx, "123", 42, time.Now().Add(time.Hour))
	if err != nil || len(pending) != 1 {
		t.Fatal(pending, err)
	}
	if err := store.CompleteTelegramNotification(ctx, pending[0].ID, true); err != nil {
		t.Fatal(err)
	}
	if err := store.EnqueueTelegramNotification(ctx, model.TelegramNotification{TaskID: "anime:legacy", BotID: "123", ChatID: 42, Text: "旧异常"}); err != nil {
		t.Fatal(err)
	}
	if err := m.deliverNotification(ctx, time.Now().Add(time.Hour)); err != nil || sends != 0 {
		t.Fatal(sends, err)
	}
}
