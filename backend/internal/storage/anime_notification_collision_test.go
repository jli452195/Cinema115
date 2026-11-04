package storage

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/model"
)

func TestAnimeNotificationPhaseUpgradePreservesSequenceAboveRetainedRows(t *testing.T) {
	for _, keepEvent := range []bool{false, true} {
		name := "empty"
		if keepEvent {
			name = "retained-row"
		}
		t.Run(name, func(t *testing.T) {
			f, job, _ := animeStatusStoreFixture(t)
			saveAnimeNotificationTestSettings(t, f.store)
			ctx := context.Background()
			if keepEvent {
				if _, err := f.store.db.ExecContext(ctx, `UPDATE anime_download_jobs SET download_state='completed' WHERE id=?`, job.ID); err != nil {
					t.Fatal(err)
				}
			}
			var schema string
			if err := f.store.db.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE name='anime_notification_events'`).Scan(&schema); err != nil {
				t.Fatal(err)
			}
			legacySchema := strings.Replace(schema, "UNIQUE(job_id,generation,kind,phase,block_code)", "UNIQUE(job_id,generation,kind,block_code)", 1)
			if legacySchema == schema {
				t.Fatal("未找到阶段唯一约束")
			}
			if _, err := f.store.db.ExecContext(ctx, `DROP TRIGGER anime_notification_downloaded; DROP TRIGGER anime_notification_organized; DROP TRIGGER anime_notification_failed; CREATE TEMP TABLE saved_anime_sequence_events AS SELECT * FROM anime_notification_events; DROP TABLE anime_notification_events`); err != nil {
				t.Fatal(err)
			}
			if _, err := f.store.db.ExecContext(ctx, legacySchema); err != nil {
				t.Fatal(err)
			}
			if _, err := f.store.db.ExecContext(ctx, `INSERT INTO anime_notification_events SELECT * FROM saved_anime_sequence_events; DROP TABLE saved_anime_sequence_events; DELETE FROM sqlite_sequence WHERE name='anime_notification_events'; INSERT INTO sqlite_sequence(name,seq) VALUES('anime_notification_events',30)`); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 2; i++ {
				if err := f.store.initialize(ctx); err != nil {
					t.Fatal(err)
				}
			}
			var sequence int64
			if err := f.store.db.QueryRowContext(ctx, `SELECT seq FROM sqlite_sequence WHERE name='anime_notification_events'`).Scan(&sequence); err != nil || sequence != 30 {
				t.Fatal("表重建丢失已使用的事件序号", sequence, err)
			}
			animeNotificationObserve(t, f.store, job.ID, "TASK_TARGET_CONFLICT", time.Now().UnixMilli(), 1)
			var eventID int64
			if err := f.store.db.QueryRowContext(ctx, `SELECT id FROM anime_notification_events WHERE kind='failed'`).Scan(&eventID); err != nil || eventID != 31 {
				t.Fatal("新事件重用了历史通知序号", eventID, err)
			}
		})
	}
}

func TestAnimeNotificationTransferRejectsDeduplicationKeyOwnedByOtherEvent(t *testing.T) {
	for _, mismatch := range []string{"legacy-sent", "job", "generation", "kind", "phase", "code", "config-bot"} {
		t.Run(mismatch, func(t *testing.T) {
			f, job, _ := animeStatusStoreFixture(t)
			saveAnimeNotificationTestSettings(t, f.store)
			ctx := context.Background()
			animeNotificationObserve(t, f.store, job.ID, "TASK_TARGET_CONFLICT", time.Now().UnixMilli(), 1)
			event := animeNotificationExpectCount(t, f.store, time.Now(), 1)[0]
			existing := model.TelegramNotification{TaskID: "anime:reused-key", BotID: "123", ChatID: 42, Text: "历史消息", AnimeJobID: event.JobID, AnimeGeneration: event.Generation, AnimeKind: event.Kind, AnimePhase: event.Phase, AnimeBlockCode: event.BlockCode, AnimeBotID: event.BotID}
			switch mismatch {
			case "legacy-sent":
				existing.AnimeJobID, existing.AnimeGeneration, existing.AnimeKind, existing.AnimePhase, existing.AnimeBlockCode, existing.AnimeBotID = "", 0, "", "", "", ""
			case "job":
				existing.AnimeJobID = "another-job"
			case "generation":
				existing.AnimeGeneration++
			case "kind":
				existing.AnimeKind = "organized"
			case "phase":
				existing.AnimePhase = "files"
			case "code":
				existing.AnimeBlockCode = "TASK_RESULT_CHANGED"
			case "config-bot":
				existing.AnimeBotID = "another-config-bot"
			}
			if err := f.store.EnqueueTelegramNotification(ctx, existing); err != nil {
				t.Fatal(err)
			}
			if mismatch == "legacy-sent" {
				if _, err := f.store.db.ExecContext(ctx, `UPDATE telegram_notifications SET status='sent',text='' WHERE task_id=?`, existing.TaskID); err != nil {
					t.Fatal(err)
				}
			}
			incoming := &model.TelegramNotification{TaskID: existing.TaskID, BotID: existing.BotID, ChatID: existing.ChatID, Text: "本次事件", PhotoURL: "https://example.org/cover.jpg"}
			if err := f.store.CompleteAnimeNotificationEvent(ctx, event.ID, incoming); !errors.Is(err, ErrAnimeConflict) {
				t.Fatal("去重键错绑却确认了新事件", err)
			}
			retained := animeNotificationExpectCount(t, f.store, time.Now(), 1)[0]
			if retained.ID != event.ID || retained.Title != event.Title || retained.Template != event.Template || retained.Snapshot.InfoHash != event.Snapshot.InfoHash {
				t.Fatal("队列冲突后丢失事件快照", retained)
			}
			var text string
			if err := f.store.db.QueryRowContext(ctx, `SELECT text FROM telegram_notifications WHERE task_id=?`, existing.TaskID).Scan(&text); err != nil || mismatch != "legacy-sent" && text != existing.Text || mismatch == "legacy-sent" && text != "" {
				t.Fatal("队列冲突覆盖了历史消息", text, err)
			}
		})
	}
}

func TestAnimeNotificationDeduplicationAcceptsSameTypedEventAndKeepsOrdinaryBehavior(t *testing.T) {
	f, job, _ := animeStatusStoreFixture(t)
	saveAnimeNotificationTestSettings(t, f.store)
	ctx := context.Background()
	animeNotificationObserve(t, f.store, job.ID, "TASK_TARGET_CONFLICT", time.Now().UnixMilli(), 1)
	event := animeNotificationExpectCount(t, f.store, time.Now(), 1)[0]
	n := model.TelegramNotification{TaskID: "anime:stable-key", BotID: "123", ChatID: 42, Text: "首次保存", AnimeJobID: event.JobID, AnimeGeneration: event.Generation, AnimeKind: event.Kind, AnimePhase: event.Phase, AnimeBlockCode: event.BlockCode, AnimeBotID: event.BotID}
	if err := f.store.EnqueueTelegramNotification(ctx, n); err != nil {
		t.Fatal(err)
	}
	if err := f.store.EnqueueTelegramNotification(ctx, n); err != nil {
		t.Fatal("同一待发事件不能重复确认", err)
	}
	due, err := f.store.DueTelegramNotifications(ctx, n.BotID, n.ChatID, time.Now().Add(time.Second))
	if err != nil || len(due) != 1 {
		t.Fatal(due, err)
	}
	if err := f.store.CompleteTelegramNotification(ctx, due[0].ID, true); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := f.store.CompleteAnimeNotificationEvent(ctx, event.ID, &n); err != nil {
			t.Fatal("同一业务事件未保持幂等", err)
		}
	}
	animeNotificationExpectCount(t, f.store, time.Now(), 0)
	ordinary := model.TelegramNotification{TaskID: "ordinary-task", BotID: "123", ChatID: 42, Text: "普通通知"}
	if err := f.store.EnqueueTelegramNotification(ctx, ordinary); err != nil {
		t.Fatal(err)
	}
	ordinary.Text, ordinary.AnimeGeneration = "重复内容", 99
	if err := f.store.EnqueueTelegramNotification(ctx, ordinary); err != nil {
		t.Fatal("普通模块原有幂等行为改变", err)
	}
	var count int
	if err := f.store.db.QueryRowContext(ctx, `SELECT count(*) FROM telegram_notifications`).Scan(&count); err != nil || count != 2 {
		t.Fatal("幂等入队生成了重复消息", count, err)
	}
}
