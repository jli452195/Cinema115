package storage

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/model"
)

func saveAnimeNotificationTestSettings(t *testing.T, s *Store) model.AnimeNotificationSettings {
	t.Helper()
	ctx := context.Background()
	in, err := s.GetAnimeNotificationSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	in.BotID, in.Template, in.DownloadComplete = "anime-bot", "<b>{title}</b>", true
	out, err := s.SaveAnimeNotificationSettings(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestAnimeNotificationSettingsRevisionAndBinding(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	initial, err := s.GetAnimeNotificationSettings(ctx)
	if err != nil || initial.Revision != 1 || initial.BotID != "" || initial.Template != "" || initial.DownloadComplete || !initial.Organized || !initial.Failed || initial.ImageMode != "cover" || initial.ImageURL != "" {
		t.Fatal("通知默认设置不正确", initial, err)
	}
	updated := saveAnimeNotificationTestSettings(t, s)
	if updated.Revision != initial.Revision+1 {
		t.Fatal("设置保存未推进版本", updated)
	}
	if _, err := s.SaveAnimeNotificationSettings(ctx, initial); !errors.Is(err, ErrAnimeConflict) {
		t.Fatal("旧设置覆盖了当前配置", err)
	}
	if err := s.BindAnimeNotificationBot(ctx, "", "other"); !errors.Is(err, ErrAnimeConflict) {
		t.Fatal("旧功能分配覆盖了当前 Bot", err)
	}
	if err := s.BindAnimeNotificationBot(ctx, updated.BotID, "other"); err != nil {
		t.Fatal(err)
	}
	actual, err := s.GetAnimeNotificationSettings(ctx)
	if err != nil || actual.BotID != "other" || actual.Template != updated.Template || actual.Revision != updated.Revision+1 {
		t.Fatal("功能分配丢失模板或版本", actual, err)
	}
	if _, err := s.SaveAnimeNotificationSettings(ctx, updated); !errors.Is(err, ErrAnimeConflict) {
		t.Fatal("分配后的旧版本仍可保存", err)
	}
	if err := s.initialize(ctx); err != nil {
		t.Fatal(err)
	}
	reloaded, err := s.GetAnimeNotificationSettings(ctx)
	if err != nil || reloaded != actual {
		t.Fatal("重复初始化改变了通知设置", reloaded, actual, err)
	}
}

func TestAnimeNotificationImageSettingsValidationAndEventSnapshots(t *testing.T) {
	f, job, _ := animeStatusStoreFixture(t)
	ctx := context.Background()
	settings := saveAnimeNotificationTestSettings(t, f.store)
	for _, invalid := range []model.AnimeNotificationSettings{
		{Revision: settings.Revision, ImageMode: "unknown"},
		{Revision: settings.Revision, ImageMode: "custom", ImageURL: strings.Repeat("x", 2049)},
	} {
		if _, err := f.store.SaveAnimeNotificationSettings(ctx, invalid); !errors.Is(err, ErrAnimeInvalid) {
			t.Fatal("无效图片设置被保存", invalid.ImageMode, err)
		}
	}
	settings.ImageMode, settings.ImageURL = "custom", "https://example.org/first.jpg"
	settings, err := f.store.SaveAnimeNotificationSettings(ctx, settings)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.db.ExecContext(ctx, `UPDATE anime_entries SET metadata_json=json_set(metadata_json,'$.coverUrl','/api/anime/image/cover1'); UPDATE anime_download_jobs SET download_state='completed' WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	settings.ImageMode, settings.ImageURL = "", ""
	settings, err = f.store.SaveAnimeNotificationSettings(ctx, settings)
	if err != nil || settings.ImageMode != "cover" {
		t.Fatal("未传图片模式未使用封面默认值", settings, err)
	}
	if _, err := f.store.db.ExecContext(ctx, `UPDATE anime_entries SET metadata_json=json_set(metadata_json,'$.coverUrl','/api/anime/image/cover2'); UPDATE anime_download_jobs SET block_code='TASK_ACCESS_DENIED' WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	var frozenMode, frozenCover string
	if err := f.store.db.QueryRowContext(ctx, `SELECT image_mode,cover_url FROM anime_notification_events WHERE kind='failed'`).Scan(&frozenMode, &frozenCover); err != nil || frozenMode != "cover" || frozenCover != "/api/anime/image/cover2" {
		t.Fatal("候选图片设置未按事件发生时冻结", frozenMode, frozenCover, err)
	}
	settings.ImageMode = "none"
	if _, err := f.store.SaveAnimeNotificationSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.db.ExecContext(ctx, `UPDATE anime_entries SET metadata_json=json_set(metadata_json,'$.coverUrl','/api/anime/image/cover3'); UPDATE anime_download_jobs SET file_state='verified',workflow_state='organized',next_run_at=NULL,lease_token=NULL,lease_until=NULL WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	events, err := f.store.PendingAnimeNotificationEvents(ctx)
	if err != nil || len(events) != 2 {
		t.Fatal(events, err)
	}
	if events[0].Kind != "downloaded" || events[0].ImageMode != "custom" || events[0].ImageURL != "https://example.org/first.jpg" || events[0].CoverURL != "/api/anime/image/cover1" || events[1].Kind != "organized" || events[1].ImageMode != "none" || events[1].CoverURL != "/api/anime/image/cover3" {
		t.Fatal("图片设置或封面未按事件时刻冻结", events)
	}
}

func TestAnimeNotificationImageColumnsUpgradeIsIdempotent(t *testing.T) {
	f, job, _ := animeStatusStoreFixture(t)
	ctx := context.Background()
	settings := saveAnimeNotificationTestSettings(t, f.store)
	if _, err := f.store.db.ExecContext(ctx, `UPDATE anime_download_jobs SET block_code='TASK_TARGET_CONFLICT' WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`DROP TRIGGER anime_notification_downloaded`, `DROP TRIGGER anime_notification_organized`, `DROP TRIGGER anime_notification_failed`,
		`ALTER TABLE anime_notification_settings DROP COLUMN image_mode`, `ALTER TABLE anime_notification_settings DROP COLUMN image_url`,
		`ALTER TABLE anime_notification_events DROP COLUMN image_mode`, `ALTER TABLE anime_notification_events DROP COLUMN image_url`, `ALTER TABLE anime_notification_events DROP COLUMN cover_url`,
		`ALTER TABLE telegram_notifications DROP COLUMN photo_url`, `ALTER TABLE telegram_notifications DROP COLUMN photo_sent`,
	} {
		if _, err := f.store.db.ExecContext(ctx, query); err != nil {
			t.Fatal(query, err)
		}
	}
	for i := 0; i < 2; i++ {
		if err := f.store.initialize(ctx); err != nil {
			t.Fatal(err)
		}
	}
	actual, err := f.store.GetAnimeNotificationSettings(ctx)
	if err != nil || actual != settings {
		t.Fatal("补齐图片字段改变了原通知设置", actual, settings, err)
	}
	events, err := f.store.PendingAnimeNotificationEvents(ctx)
	if err != nil || len(events) != 1 || events[0].ImageMode != "cover" || events[0].ImageURL != "" || events[0].CoverURL != "" {
		t.Fatal("已有事件未获得图片字段默认值", events, err)
	}
	if _, err := f.store.db.ExecContext(ctx, `UPDATE anime_download_jobs SET block_code='TASK_RESULT_CHANGED' WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	events, err = f.store.PendingAnimeNotificationEvents(ctx)
	if err != nil || len(events) != 1 || events[0].BlockCode != "TASK_RESULT_CHANGED" {
		t.Fatal("补齐字段后事件触发器未恢复", events, err)
	}
}

func TestAnimeNotificationEventsCommitTogetherAndFreezeSettings(t *testing.T) {
	f, job, _ := animeStatusStoreFixture(t)
	ctx := context.Background()
	settings := saveAnimeNotificationTestSettings(t, f.store)
	created := time.Now().UnixMilli()
	tx, err := f.store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE anime_download_jobs SET download_state='completed',updated_at=? WHERE id=?`, created, job.ID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	events, err := f.store.PendingAnimeNotificationEvents(ctx)
	if err != nil || len(events) != 0 {
		t.Fatal("任务回滚后仍生成通知", events, err)
	}
	for i := 0; i < 2; i++ {
		if _, err := f.store.db.ExecContext(ctx, `UPDATE anime_download_jobs SET download_state='completed',updated_at=? WHERE id=?`, created, job.ID); err != nil {
			t.Fatal(err)
		}
	}
	events, err = f.store.PendingAnimeNotificationEvents(ctx)
	if err != nil || len(events) != 1 {
		t.Fatal("完成事件丢失或重复", events, err)
	}
	first := events[0]
	if first.Kind != "downloaded" || first.JobID != job.ID || first.Generation != job.Generation || first.CreatedAt != created || first.Title == "" || first.Snapshot.InfoHash != job.Snapshot.InfoHash || first.BotID != settings.BotID || first.Template != settings.Template {
		t.Fatal("通知未使用完整任务快照", first)
	}
	settings.BotID, settings.Template = "replacement-bot", "新模板"
	if _, err := f.store.SaveAnimeNotificationSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.db.ExecContext(ctx, `UPDATE anime_entries SET title='新名称'; UPDATE anime_download_jobs SET file_state='verified',workflow_state='organized',next_run_at=NULL,lease_token=NULL,lease_until=NULL,updated_at=? WHERE id=?`, created+1, job.ID); err != nil {
		t.Fatal(err)
	}
	events, err = f.store.PendingAnimeNotificationEvents(ctx)
	if err != nil || len(events) != 2 || events[0].Title != first.Title || events[0].BotID != first.BotID || events[0].Template != first.Template || events[1].Kind != "organized" || events[1].Title != "新名称" || events[1].BotID != settings.BotID || events[1].Template != settings.Template {
		t.Fatal("事件被后续配置覆盖或整理事件未生成", events, err)
	}
	if _, err := f.store.db.ExecContext(ctx, `DELETE FROM anime_drive_operations WHERE download_job_id=?; DELETE FROM anime_job_selections WHERE job_id=?; DELETE FROM anime_download_jobs WHERE id=?`, job.ID, job.ID, job.ID); err != nil {
		t.Fatal(err)
	}
	events, err = f.store.PendingAnimeNotificationEvents(ctx)
	if err != nil || len(events) != 0 {
		t.Fatal("删除任务后残留通知事件", events, err)
	}
}

func TestAnimeNotificationDisabledAndRepeatedErrors(t *testing.T) {
	f, job, _ := animeStatusStoreFixture(t)
	ctx := context.Background()
	update := func(state, code string) {
		t.Helper()
		if _, err := f.store.db.ExecContext(ctx, `UPDATE anime_download_jobs SET download_state=?,block_code=? WHERE id=?`, state, code, job.ID); err != nil {
			t.Fatal(err)
		}
	}
	update("failed", "TASK_FAILED")
	saveAnimeNotificationTestSettings(t, f.store)
	update("failed", "TASK_FAILED")
	events, err := f.store.PendingAnimeNotificationEvents(ctx)
	if err != nil || len(events) != 0 {
		t.Fatal("开启通知追溯了旧异常或重复状态生成事件", events, err)
	}
	update("blocked", "TASK_TARGET_CONFLICT")
	update("blocked", "TASK_TARGET_CONFLICT")
	events, err = f.store.PendingAnimeNotificationEvents(ctx)
	if err != nil || len(events) != 1 || events[0].Kind != "failed" || events[0].BlockCode != "TASK_TARGET_CONFLICT" {
		t.Fatal("异常未通知或相同异常重复通知", events, err)
	}
	update("cancelled", "CANCELLED")
	events, err = f.store.PendingAnimeNotificationEvents(ctx)
	if err != nil || len(events) != 0 {
		t.Fatal("取消任务生成了异常通知", events, err)
	}
}

func TestAnimeNotificationTransferAtomicAndHTMLPersists(t *testing.T) {
	f, job, _ := animeStatusStoreFixture(t)
	ctx := context.Background()
	saveAnimeNotificationTestSettings(t, f.store)
	if _, err := f.store.db.ExecContext(ctx, `UPDATE anime_download_jobs SET download_state='failed',block_code='TASK_FAILED' WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	events, err := f.store.PendingAnimeNotificationEvents(ctx)
	if err != nil || len(events) != 1 {
		t.Fatal(events, err)
	}
	n := &model.TelegramNotification{TaskID: "anime:test-event", BotID: "123", ChatID: 42, Text: "<b>动漫更新</b>", ParseMode: "HTML"}
	if _, err := f.store.db.ExecContext(ctx, `CREATE TRIGGER reject_anime_notification BEFORE INSERT ON telegram_notifications BEGIN SELECT RAISE(ABORT,'test'); END`); err != nil {
		t.Fatal(err)
	}
	if err := f.store.CompleteAnimeNotificationEvent(ctx, events[0].ID, n); err == nil {
		t.Fatal("发送队列拒绝入队后仍确认事件")
	}
	retained, err := f.store.PendingAnimeNotificationEvents(ctx)
	if err != nil || len(retained) != 1 || retained[0].ID != events[0].ID {
		t.Fatal("入队失败丢失事件", retained, err)
	}
	if _, err := f.store.db.ExecContext(ctx, `DROP TRIGGER reject_anime_notification`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := f.store.CompleteAnimeNotificationEvent(ctx, events[0].ID, n); err != nil {
			t.Fatal(err)
		}
	}
	due, err := f.store.DueTelegramNotifications(ctx, n.BotID, n.ChatID, time.Now().Add(time.Second))
	if err != nil || len(due) != 1 || due[0].ParseMode != "HTML" || due[0].Text != n.Text {
		t.Fatal("转交重复通知或丢失格式", due, err)
	}
	if err := f.store.CompleteTelegramNotification(ctx, due[0].ID, true); err != nil {
		t.Fatal(err)
	}
	var text, mode string
	if err := f.store.db.QueryRowContext(ctx, `SELECT text,parse_mode FROM telegram_notifications WHERE id=?`, due[0].ID).Scan(&text, &mode); err != nil || text != "" || mode != "" {
		t.Fatal("投递完成未清除正文和格式", text, mode, err)
	}
}

func TestAnimeNotificationTransferRechecksCurrentAssignment(t *testing.T) {
	for _, change := range []string{"unbind", "disable-event"} {
		t.Run(change, func(t *testing.T) {
			f, job, _ := animeStatusStoreFixture(t)
			ctx := context.Background()
			settings := saveAnimeNotificationTestSettings(t, f.store)
			if _, err := f.store.db.ExecContext(ctx, `UPDATE anime_download_jobs SET download_state='failed',block_code='TASK_FAILED' WHERE id=?`, job.ID); err != nil {
				t.Fatal(err)
			}
			events, err := f.store.PendingAnimeNotificationEvents(ctx)
			if err != nil || len(events) != 1 {
				t.Fatal(events, err)
			}
			if change == "unbind" {
				settings.BotID = ""
			} else {
				settings.Failed = false
			}
			if _, err := f.store.SaveAnimeNotificationSettings(ctx, settings); err != nil {
				t.Fatal(err)
			}
			n := &model.TelegramNotification{TaskID: "anime:stale-event", BotID: "123", ChatID: 42, Text: "过时的通知"}
			if err := f.store.CompleteAnimeNotificationEvent(ctx, events[0].ID, n); err != nil {
				t.Fatal(err)
			}
			pending, err := f.store.PendingAnimeNotificationEvents(ctx)
			if err != nil || len(pending) != 0 {
				t.Fatal("失效事件未清理", pending, err)
			}
			due, err := f.store.DueTelegramNotifications(ctx, n.BotID, n.ChatID, time.Now().Add(time.Second))
			if err != nil || len(due) != 0 {
				t.Fatal("解绑或关闭后仍转交通知", due, err)
			}
		})
	}
}

func TestAnimeNotificationNormalWaitingDoesNotRaiseFailure(t *testing.T) {
	for _, code := range []string{"TASK_RESULT_PENDING", "TASK_STATUS_UNKNOWN", "TASK_POLL_CANCELLED"} {
		t.Run(code, func(t *testing.T) {
			f, job, _ := animeStatusStoreFixture(t)
			ctx := context.Background()
			saveAnimeNotificationTestSettings(t, f.store)
			for _, state := range []string{"accepted", "failed", "blocked"} {
				if _, err := f.store.db.ExecContext(ctx, `UPDATE anime_download_jobs SET download_state=?,block_code=? WHERE id=?`, state, code, job.ID); err != nil {
					t.Fatal(err)
				}
			}
			events, err := f.store.PendingAnimeNotificationEvents(ctx)
			if err != nil || len(events) != 0 {
				t.Fatal("正常等待或轮询取消被误报为异常", events, err)
			}
			if _, err := f.store.db.ExecContext(ctx, `UPDATE anime_download_jobs SET block_code='TASK_TARGET_CONFLICT' WHERE id=?`, job.ID); err != nil {
				t.Fatal(err)
			}
			events, err = f.store.PendingAnimeNotificationEvents(ctx)
			if err != nil || len(events) != 1 || events[0].BlockCode != "TASK_TARGET_CONFLICT" {
				t.Fatal("正常等待过滤影响了真实异常通知", events, err)
			}
		})
	}
}

func TestAnimeNotificationExpiredEventsArePrunedInBoundedBatches(t *testing.T) {
	f, job, _ := animeStatusStoreFixture(t)
	ctx := context.Background()
	saveAnimeNotificationTestSettings(t, f.store)
	old := time.Now().Add(-8 * 24 * time.Hour).UnixMilli()
	if _, err := f.store.db.ExecContext(ctx, `UPDATE anime_download_jobs SET block_code='TASK_TARGET_CONFLICT',updated_at=? WHERE id=?`, time.Now().UnixMilli(), job.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.db.ExecContext(ctx, `WITH RECURSIVE batch(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM batch WHERE n<125)
 INSERT INTO anime_notification_events(job_id,subscription_id,generation,kind,title,bangumi_id,snapshot_json,block_code,created_at,bot_id,template)
 SELECT e.job_id,e.subscription_id,e.generation,e.kind,e.title,e.bangumi_id,e.snapshot_json,'OLD_'||batch.n,?,e.bot_id,e.template FROM anime_notification_events e CROSS JOIN batch`, old); err != nil {
		t.Fatal(err)
	}
	events, err := f.store.PendingAnimeNotificationEvents(ctx)
	if err != nil || len(events) != 1 || events[0].BlockCode != "TASK_TARGET_CONFLICT" {
		t.Fatal("过期积压阻塞了新通知", events, err)
	}
	var expired int
	if err := f.store.db.QueryRowContext(ctx, `SELECT count(*) FROM anime_notification_events WHERE created_at=? AND processed=0`, old).Scan(&expired); err != nil || expired != 25 {
		t.Fatal("单轮清理未遵守100条上限", expired, err)
	}
	if _, err := f.store.PendingAnimeNotificationEvents(ctx); err != nil {
		t.Fatal(err)
	}
	if err := f.store.db.QueryRowContext(ctx, `SELECT count(*) FROM anime_notification_events WHERE created_at=? AND processed=0`, old).Scan(&expired); err != nil || expired != 0 {
		t.Fatal("后续轮次未清理完过期事件", expired, err)
	}
}
