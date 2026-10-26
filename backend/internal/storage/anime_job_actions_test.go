package storage

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestAnimeJobActionReceiptCASCooldownAndNoReissue(t *testing.T) {
	f, job, _ := animeStatusStoreFixture(t)
	ctx := context.Background()
	key := animeAdminKey("schedule_read_01")
	first, err := f.store.ScheduleAnimeJobAction(ctx, job.ID, "reconcile", job.Revision, key)
	if err != nil {
		t.Fatal(err)
	}
	again, err := f.store.ScheduleAnimeJobAction(ctx, job.ID, "reconcile", job.Revision, key)
	if err != nil || first != again {
		t.Fatal(again, err)
	}
	if _, err := f.store.ScheduleAnimeJobAction(ctx, job.ID, "reconcile", job.Revision, animeAdminKey("stale_read_01")); !errors.Is(err, ErrAnimeConflict) {
		t.Fatal(err)
	}
	current, err := f.store.GetAnimeDownloadJob(ctx, job.ID)
	if err != nil || current.Generation != job.Generation || !reflect.DeepEqual(current.Snapshot, job.Snapshot) || current.DownloadState != "accepted" {
		t.Fatal(current, err)
	}
	op, err := f.store.GetAnimeSubmitOperation(ctx, job.ID)
	if err != nil || op.State != "confirmed" {
		t.Fatal(op, err)
	}
	// A scheduled exact status read is the only work created by this action.
	poll, found, err := f.store.ReserveAnimeStatusPoll(ctx, time.Now().UnixMilli()+1, nil)
	if err != nil || !found {
		t.Fatal(found, err)
	}
	if _, err := f.store.CompleteAnimeStatusPoll(ctx, poll, nil, "TASK_RATE_LIMITED", time.Now().UnixMilli()+600000, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	current, _ = f.store.GetAnimeDownloadJob(ctx, job.ID)
	if _, err := f.store.ScheduleAnimeJobAction(ctx, job.ID, "reconcile", current.Revision, animeAdminKey("limited_read")); !errors.Is(err, ErrAnimeTransition) {
		t.Fatal("bypassed cooldown", err)
	}
}

func TestAnimeJobActionReadFailureRetryAndImmutableTerminal(t *testing.T) {
	for _, kind := range []string{"files_retry", "unknown", "verified", "account", "conflict", "receipt_failure"} {
		t.Run(kind, func(t *testing.T) {
			f, job, _ := animeStatusStoreFixture(t)
			ctx := context.Background()
			switch kind {
			case "files_retry":
				if _, err := f.store.db.Exec(`UPDATE anime_download_jobs SET download_state='completed',result_object_id='300',file_state='needs_review',block_code='FILES_INCOMPLETE',raw_status_json='{"schemaVersion":1,"fileReadFailures":3}' WHERE id=?`, job.ID); err != nil {
					t.Fatal(err)
				}
			case "unknown":
				if _, err := f.store.db.Exec(`UPDATE anime_download_jobs SET download_state='unknown',block_code='SUBMIT_UNCERTAIN' WHERE id=?`, job.ID); err != nil {
					t.Fatal(err)
				}
				if _, err := f.store.db.Exec(`UPDATE anime_drive_operations SET state='unknown'`); err != nil {
					t.Fatal(err)
				}
			case "verified":
				if _, err := f.store.db.Exec(`UPDATE anime_download_jobs SET download_state='completed',file_state='verified',workflow_state='original_saved',next_run_at=NULL WHERE id=?`, job.ID); err != nil {
					t.Fatal(err)
				}
			case "account":
				if _, err := f.store.db.Exec(`UPDATE p115_accounts SET uid='999'`); err != nil {
					t.Fatal(err)
				}
			case "conflict":
				if _, err := f.store.db.Exec(`UPDATE anime_download_jobs SET block_code='TASK_TARGET_CONFLICT' WHERE id=?`, job.ID); err != nil {
					t.Fatal(err)
				}
			case "receipt_failure":
				if _, err := f.store.db.Exec(`CREATE TRIGGER schedule_receipt_failure BEFORE INSERT ON anime_request_keys BEGIN SELECT RAISE(ABORT,'receipt failure'); END`); err != nil {
					t.Fatal(err)
				}
			}
			job, _ = f.store.GetAnimeDownloadJob(ctx, job.ID)
			actions, err := f.store.AnimeDownloadJobActions(ctx, job)
			if err != nil {
				t.Fatal(err)
			}
			if kind == "files_retry" {
				if !actions.Retry {
					t.Fatal(actions)
				}
				if _, err := f.store.ScheduleAnimeJobAction(ctx, job.ID, "retry", job.Revision, animeAdminKey("files_retry")); err != nil {
					t.Fatal(err)
				}
				got, _ := f.store.GetAnimeDownloadJob(ctx, job.ID)
				if got.FileState != "pending" || got.DownloadState != "completed" || got.BlockCode != nil || got.Generation != job.Generation || got.Tracking == nil || got.Tracking.FileReadFailures != 0 {
					t.Fatal(got)
				}
				return
			}
			if kind == "unknown" {
				if !actions.Reconcile || actions.Retry {
					t.Fatal(actions)
				}
				if _, err := f.store.ScheduleAnimeJobAction(ctx, job.ID, "reconcile", job.Revision, animeAdminKey("unknown_read")); err != nil {
					t.Fatal(err)
				}
				return
			}
			if _, err := f.store.ScheduleAnimeJobAction(ctx, job.ID, "reconcile", job.Revision, animeAdminKey("not_allowed")); err == nil {
				t.Fatal("unexpected schedule", kind)
			}
			got, _ := f.store.GetAnimeDownloadJob(ctx, job.ID)
			if !reflect.DeepEqual(got, job) {
				t.Fatal("failed action changed job", kind)
			}
		})
	}
}
