package storage

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/model"
)

func animeNotificationObserve(t *testing.T, s *Store, jobID, code string, at int64, count int) {
	t.Helper()
	_, err := s.db.ExecContext(context.Background(), `UPDATE anime_download_jobs SET block_code=NULLIF(?,''),raw_status_json=json_set(COALESCE(raw_status_json,'{"schemaVersion":1}'),'$.attemptedAt',?,'$.failures',?),updated_at=? WHERE id=?`, code, at, count, at, jobID)
	if err != nil {
		t.Fatal(err)
	}
}

func animeNotificationExpectCount(t *testing.T, s *Store, at time.Time, want int) []model.AnimeNotificationEvent {
	t.Helper()
	events, err := s.pendingAnimeNotificationEvents(context.Background(), at)
	if err != nil || len(events) != want {
		t.Fatalf("通知候选数量: want=%d got=%+v err=%v", want, events, err)
	}
	return events
}

func TestAnimeNotificationTransientConfirmationCountsActualReadsAndMaturesByClock(t *testing.T) {
	f, job, _ := animeStatusStoreFixture(t)
	saveAnimeNotificationTestSettings(t, f.store)
	first := time.Now().Add(-4 * time.Minute)
	for count := 1; count <= 3; count++ {
		animeNotificationObserve(t, f.store, job.ID, "TASK_READ_FAILED", first.UnixMilli()+int64(count-1)*1000, count)
		animeNotificationExpectCount(t, f.store, first.Add(4*time.Minute), 0)
	}
	for count := 0; count < 4; count++ {
		if _, err := f.store.db.Exec(`UPDATE anime_download_jobs SET next_run_at=?,revision=revision+1,updated_at=? WHERE id=?`, first.UnixMilli()+5000, first.UnixMilli()+5000, job.ID); err != nil {
			t.Fatal(err)
		}
	}
	var attempts int
	if err := f.store.db.QueryRow(`SELECT failure_count FROM anime_notification_events WHERE kind='failed'`).Scan(&attempts); err != nil || attempts != 3 {
		t.Fatal("调度记账被当成失败检测", attempts, err)
	}
	events := animeNotificationExpectCount(t, f.store, first.Add(5*time.Minute), 1)
	if events[0].Phase != "download" {
		t.Fatal("下载阶段归类不正确", events[0].Phase)
	}
}

func TestAnimeNotificationRecoveryResetsContinuousWindow(t *testing.T) {
	f, job, _ := animeStatusStoreFixture(t)
	saveAnimeNotificationTestSettings(t, f.store)
	first := time.Now().Add(-10 * time.Minute)
	for i := 0; i < 3; i++ {
		animeNotificationObserve(t, f.store, job.ID, "TASK_READ_FAILED", first.UnixMilli()+int64(i)*1000, i+1)
	}
	animeNotificationExpectCount(t, f.store, time.Now(), 1)
	animeNotificationObserve(t, f.store, job.ID, "", first.Add(6*time.Minute).UnixMilli(), 0)
	for i := 0; i < 3; i++ {
		animeNotificationObserve(t, f.store, job.ID, "TASK_READ_FAILED", first.Add(7*time.Minute).UnixMilli()+int64(i)*1000, i+1)
	}
	animeNotificationExpectCount(t, f.store, time.Now(), 0)
	animeNotificationExpectCount(t, f.store, first.Add(12*time.Minute), 1)
}

func TestAnimeNotificationFileErrorsWaitForExistingRetryBudget(t *testing.T) {
	f, job, _ := animeStatusStoreFixture(t)
	settings := saveAnimeNotificationTestSettings(t, f.store)
	settings.DownloadComplete = false
	if _, err := f.store.SaveAnimeNotificationSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.db.Exec(`UPDATE anime_download_jobs SET download_state='completed',result_object_id='300',result_object_kind='directory' WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 3; i++ {
		current, err := f.store.GetAnimeDownloadJob(context.Background(), job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.store.RecordAnimeFilesFailure(context.Background(), current, "FILES_READ_FAILED", time.Now().UnixMilli()+1); err != nil {
			t.Fatal(err)
		}
		want := 0
		if i == 3 {
			want = 1
		}
		animeNotificationExpectCount(t, f.store, time.Now(), want)
	}
}

func TestAnimeNotificationPhaseChangeStartsIndependentConfirmation(t *testing.T) {
	f, job, _ := animeStatusStoreFixture(t)
	settings := saveAnimeNotificationTestSettings(t, f.store)
	settings.DownloadComplete = false
	if _, err := f.store.SaveAnimeNotificationSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	first := time.Now().Add(-6 * time.Minute)
	for i := 0; i < 3; i++ {
		animeNotificationObserve(t, f.store, job.ID, "TASK_RATE_LIMITED", first.UnixMilli()+int64(i)*1000, i+1)
	}
	events := animeNotificationExpectCount(t, f.store, time.Now(), 1)
	if err := f.store.CompleteAnimeNotificationEvent(context.Background(), events[0].ID, &model.TelegramNotification{TaskID: "anime:download-rate", BotID: "1", ChatID: 2, Text: "下载限流"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.db.Exec(`UPDATE anime_download_jobs SET download_state='completed',file_state='checking',updated_at=? WHERE id=?`, time.Now().UnixMilli(), job.ID); err != nil {
		t.Fatal(err)
	}
	animeNotificationObserve(t, f.store, job.ID, "TASK_RATE_LIMITED", time.Now().UnixMilli(), 4)
	animeNotificationExpectCount(t, f.store, time.Now().Add(6*time.Minute), 0)
	var candidates int
	if err := f.store.db.QueryRow(`SELECT count(*) FROM anime_notification_events WHERE kind='failed' AND block_code='TASK_RATE_LIMITED'`).Scan(&candidates); err != nil || candidates != 2 {
		t.Fatal("限流码未按阶段独立去重", candidates, err)
	}
	var phase string
	var attempts int
	if err := f.store.db.QueryRow(`SELECT phase,failure_count FROM anime_notification_events WHERE processed=0 AND kind='failed'`).Scan(&phase, &attempts); err != nil || phase != "files" || attempts != 1 {
		t.Fatal("文件阶段继承了下载失败次数", phase, attempts, err)
	}
}

func TestAnimeNotificationStaleHandoffAndQueuedMessagesAreDiscarded(t *testing.T) {
	for _, moment := range []string{"before-transfer", "after-transfer", "after-photo", "generation"} {
		t.Run(moment, func(t *testing.T) {
			f, job, _ := animeStatusStoreFixture(t)
			saveAnimeNotificationTestSettings(t, f.store)
			ctx := context.Background()
			animeNotificationObserve(t, f.store, job.ID, "TASK_TARGET_CONFLICT", time.Now().UnixMilli(), 1)
			events := animeNotificationExpectCount(t, f.store, time.Now(), 1)
			n := &model.TelegramNotification{TaskID: "anime:stale", BotID: "1", ChatID: 2, Text: "待确认异常", PhotoURL: "https://example.org/cover.jpg"}
			if moment != "before-transfer" {
				if err := f.store.CompleteAnimeNotificationEvent(ctx, events[0].ID, n); err != nil {
					t.Fatal(err)
				}
			}
			due, err := f.store.DueTelegramNotifications(ctx, n.BotID, n.ChatID, time.Now().Add(time.Second))
			if err != nil {
				t.Fatal(err)
			}
			if moment == "after-photo" {
				if len(due) != 1 {
					t.Fatal(due)
				}
				if err := f.store.MarkTelegramNotificationPhotoSent(ctx, due[0].ID); err != nil {
					t.Fatal(err)
				}
			}
			if moment == "generation" {
				_, err = f.store.db.Exec(`UPDATE anime_download_jobs SET generation=generation+1 WHERE id=?`, job.ID)
			} else {
				_, err = f.store.db.Exec(`UPDATE anime_download_jobs SET block_code=NULL WHERE id=?`, job.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			if moment == "before-transfer" {
				if err := f.store.CompleteAnimeNotificationEvent(ctx, events[0].ID, n); err != nil {
					t.Fatal(err)
				}
			} else {
				valid, err := f.store.ValidateTelegramNotification(ctx, due[0])
				if err != nil || valid {
					t.Fatal("旧异常仍允许外部发送", valid, err)
				}
				var text, photo string
				if err := f.store.db.QueryRow(`SELECT text,photo_url FROM telegram_notifications WHERE id=?`, due[0].ID).Scan(&text, &photo); err != nil || text != "" || photo != "" {
					t.Fatal("丢弃异常后未清理正文", text, photo, err)
				}
			}
			pending, err := f.store.DueTelegramNotifications(ctx, n.BotID, n.ChatID, time.Now().Add(time.Second))
			if err != nil || len(pending) != 0 {
				t.Fatal("恢复后仍留待发异常", pending, err)
			}
		})
	}
}

func TestAnimeNotificationTransferredIssueKeepsStableDeduplication(t *testing.T) {
	f, job, _ := animeStatusStoreFixture(t)
	saveAnimeNotificationTestSettings(t, f.store)
	ctx := context.Background()
	for i, code := range []string{"TASK_TARGET_CONFLICT", "TASK_RESULT_CHANGED", "TASK_TARGET_CONFLICT"} {
		animeNotificationObserve(t, f.store, job.ID, code, time.Now().UnixMilli()+int64(i), i+1)
		want := 1
		if i == 2 {
			want = 0
		}
		events := animeNotificationExpectCount(t, f.store, time.Now(), want)
		if want == 1 {
			if err := f.store.CompleteAnimeNotificationEvent(ctx, events[0].ID, &model.TelegramNotification{TaskID: fmt.Sprintf("anime:%d", events[0].ID), BotID: "1", ChatID: 2, Text: code}); err != nil {
				t.Fatal(err)
			}
		}
	}
	var tombstones int
	if err := f.store.db.QueryRow(`SELECT count(*) FROM anime_notification_events WHERE processed=1 AND snapshot_json='{}' AND title='' AND template=''`).Scan(&tombstones); err != nil || tombstones != 2 {
		t.Fatal("转交后未留下轻量去重记录", tombstones, err)
	}
	due, err := f.store.DueTelegramNotifications(ctx, "1", 2, time.Now().Add(time.Second))
	if err != nil || len(due) != 2 {
		t.Fatal(due, err)
	}
	for _, n := range due {
		if valid, err := f.store.ValidateTelegramNotification(ctx, n); err != nil || valid {
			t.Fatal("问题变化后旧队列消息复活", n, valid, err)
		}
	}
}

func TestAnimeNotificationValidationLeavesOtherModulesAndDiscardsUntypedAnime(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	for _, taskID := range []string{"ordinary-task", "anime:old-untyped"} {
		if err := s.EnqueueTelegramNotification(ctx, model.TelegramNotification{TaskID: taskID, BotID: "1", ChatID: 2, Text: "消息"}); err != nil {
			t.Fatal(err)
		}
	}
	due, err := s.DueTelegramNotifications(ctx, "1", 2, time.Now().Add(time.Second))
	if err != nil || len(due) != 2 {
		t.Fatal(due, err)
	}
	for _, n := range due {
		valid, err := s.ValidateTelegramNotification(ctx, n)
		if err != nil || valid != (n.TaskID == "ordinary-task") {
			t.Fatal("通知模块互相影响", n.TaskID, valid, err)
		}
	}
}

func TestAnimeNotificationDirectoryReadsCountOnceAndUncertaintyWaitsForReconciliation(t *testing.T) {
	for _, initialCode := range []string{"DIRECTORY_READ_FAILED", "DIRECTORY_CREATE_UNCERTAIN"} {
		t.Run(initialCode, func(t *testing.T) {
			f, job := animeDirectoryStoreFixture(t)
			saveAnimeNotificationTestSettings(t, f.store)
			ctx := context.Background()
			if _, _, err := f.store.StartAnimeDirectory(ctx, job.ID, job.Generation, f.target); err != nil {
				t.Fatal(err)
			}
			first := time.Now()
			for i := 0; i < 3; i++ {
				at := first.Add(time.Duration(i) * (5*time.Minute + time.Second)).UnixMilli()
				work, found, err := f.store.ReserveAnimeDownloadWork(ctx, at, nil)
				if err != nil || !found {
					t.Fatal("目录复核未就绪", i, found, err)
				}
				current, _, err := f.store.StartAnimeDirectory(ctx, job.ID, job.Generation, f.target, work.Token)
				if err != nil {
					t.Fatal(err)
				}
				if err := f.store.FinishAnimeDirectory(ctx, current, nil, initialCode, work.Token); err != nil {
					t.Fatal(err)
				}
				if err := f.store.CompleteAnimeDownloadWork(ctx, work, "", at+1); err != nil {
					t.Fatal(err)
				}
			}
			var count int
			if err := f.store.db.QueryRow(`SELECT failure_count FROM anime_notification_events WHERE kind='failed' AND processed=0`).Scan(&count); err != nil || count != 3 {
				t.Fatal("同轮目录结果和调度完成重复计数", count, err)
			}
			want := 1
			if initialCode == "DIRECTORY_CREATE_UNCERTAIN" {
				want = 0
			}
			animeNotificationExpectCount(t, f.store, first.Add(11*time.Minute), want)
			if initialCode == "DIRECTORY_CREATE_UNCERTAIN" {
				at := first.Add(30 * time.Minute).UnixMilli()
				work, found, err := f.store.ReserveAnimeDownloadWork(ctx, at, nil)
				if err != nil || !found {
					t.Fatal(found, err)
				}
				if err := f.store.FinishAnimeDirectory(ctx, work.Job, nil, "DIRECTORY_NOT_FOUND", work.Token); err != nil {
					t.Fatal(err)
				}
				if err := f.store.CompleteAnimeDownloadWork(ctx, work, "", at+1); err != nil {
					t.Fatal(err)
				}
				confirmed := animeNotificationExpectCount(t, f.store, first.Add(31*time.Minute), 1)
				if confirmed[0].BlockCode != "DIRECTORY_NOT_FOUND" || confirmed[0].Phase != "directory" {
					t.Fatal("目录复核终态未确认", confirmed[0])
				}
			}
		})
	}
}

func TestAnimeNotificationOrganizeUncertaintyWaitsForExistingReviewState(t *testing.T) {
	f, job := animeOrganizeStoreFixture(t)
	saveAnimeNotificationTestSettings(t, f.store)
	ctx := context.Background()
	op, err := f.store.PrepareAnimeOrganization(ctx, job, "")
	if err != nil {
		t.Fatal(err)
	}
	p := op.Progress
	p.Step = &model.AnimeOrganizeStep{Kind: "rename", CID: "300", IDs: []string{"400"}}
	op, err = f.store.CheckpointAnimeOrganization(ctx, op, p, true, "")
	if err != nil {
		t.Fatal(err)
	}
	p.State, p.Code = "unknown", "ORGANIZE_UNCERTAIN"
	op, err = f.store.CheckpointAnimeOrganization(ctx, op, p, false, "")
	if err != nil {
		t.Fatal(err)
	}
	first := time.Now()
	for i := 0; i < 3; i++ {
		animeNotificationObserve(t, f.store, job.ID, "ORGANIZE_UNCERTAIN", first.Add(time.Duration(i)*5*time.Minute).UnixMilli(), i+1)
	}
	animeNotificationExpectCount(t, f.store, first.Add(15*time.Minute), 0)
	p.State, p.RecoveryChecks = "needs_review", 2
	if _, err := f.store.CheckpointAnimeOrganization(ctx, op, p, false, ""); err != nil {
		t.Fatal(err)
	}
	confirmed := animeNotificationExpectCount(t, f.store, first.Add(15*time.Minute), 1)
	if confirmed[0].Phase != "organize" || confirmed[0].BlockCode != "ORGANIZE_UNCERTAIN" {
		t.Fatal("同问题进入需复核终态后未通知", confirmed[0])
	}
}

func TestAnimeNotificationLegacyCandidateAndPhaseConstraintUpgrade(t *testing.T) {
	f, job, _ := animeStatusStoreFixture(t)
	saveAnimeNotificationTestSettings(t, f.store)
	ctx := context.Background()
	first := time.Now().Add(-6 * time.Minute)
	animeNotificationObserve(t, f.store, job.ID, "TASK_RATE_LIMITED", first.UnixMilli(), 1)
	var schema string
	if err := f.store.db.QueryRow(`SELECT sql FROM sqlite_master WHERE name='anime_notification_events'`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	legacySchema := strings.Replace(schema, "UNIQUE(job_id,generation,kind,phase,block_code)", "UNIQUE(job_id,generation,kind,block_code)", 1)
	if legacySchema == schema {
		t.Fatal("未找到阶段唯一约束")
	}
	if _, err := f.store.db.Exec(`DROP TRIGGER anime_notification_downloaded; DROP TRIGGER anime_notification_organized; DROP TRIGGER anime_notification_failed; CREATE TEMP TABLE saved_anime_events AS SELECT * FROM anime_notification_events; DROP TABLE anime_notification_events`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.db.Exec(legacySchema); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.db.Exec(`INSERT INTO anime_notification_events SELECT * FROM saved_anime_events; DROP TABLE saved_anime_events; UPDATE anime_notification_events SET phase='',first_failure_at=0,last_attempt_at=0,failure_count=0`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := f.store.initialize(ctx); err != nil {
			t.Fatal(err)
		}
	}
	for i := 1; i <= 3; i++ {
		animeNotificationObserve(t, f.store, job.ID, "TASK_RATE_LIMITED", first.UnixMilli()+int64(i)*1000, i+1)
	}
	confirmed := animeNotificationExpectCount(t, f.store, time.Now(), 1)
	if confirmed[0].Phase != "download" || confirmed[0].CreatedAt != first.UnixMilli() {
		t.Fatal("升级改变了旧候选或丢失确认窗口", confirmed[0])
	}
	if err := f.store.db.QueryRow(`SELECT sql FROM sqlite_master WHERE name='anime_notification_events'`).Scan(&schema); err != nil || !strings.Contains(schema, "UNIQUE(job_id,generation,kind,phase,block_code)") {
		t.Fatal("阶段唯一约束未升级", schema, err)
	}
}

func TestAnimeNotificationRejectedSubmitKeepsSubmitPhase(t *testing.T) {
	f, job := animeSubmitStoreFixture(t)
	saveAnimeNotificationTestSettings(t, f.store)
	ctx := context.Background()
	op, err := f.store.PrepareAnimeSubmit(ctx, job.ID, job.Generation, f.target)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.DispatchAnimeSubmit(ctx, op.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.store.RecordAnimeSubmitOutcome(ctx, op.ID, "failed", "SUBMIT_REJECTED", model.AnimeSubmitObservation{SchemaVersion: 1, Kind: "rejected"}); err != nil {
		t.Fatal(err)
	}
	confirmed := animeNotificationExpectCount(t, f.store, time.Now(), 1)
	if confirmed[0].Phase != "submit" {
		t.Fatal("提交拒绝被误归下载阶段", confirmed[0].Phase)
	}
}
