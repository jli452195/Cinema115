package storage

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"strm115/backend/internal/model"
)

func animeStatusStoreFixture(t *testing.T) (*downloadClaimFixture, model.AnimeDownloadJob, int64) {
	t.Helper()
	f, job := animeSubmitStoreFixture(t)
	ctx := context.Background()
	op, err := f.store.PrepareAnimeSubmit(ctx, job.ID, job.Generation, f.target)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.DispatchAnimeSubmit(ctx, op.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.store.RecordAnimeSubmitOutcome(ctx, op.ID, "confirmed", "", model.AnimeSubmitObservation{SchemaVersion: 1, Kind: "matched", TargetCID: "200", ResultID: "300"}); err != nil {
		t.Fatal(err)
	}
	job, err = f.store.GetAnimeDownloadJob(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	return f, job, *job.NextRunAt
}

func animeStatusObservation(job model.AnimeDownloadJob, display string, progress float64) model.AnimeOfflineStatus {
	status := int64(1)
	out := model.AnimeOfflineStatus{SchemaVersion: 1, Source: model.AnimeOfflineExact, Present: true, InfoHash: job.Snapshot.InfoHash, TargetCID: "200", ResultID: "300", Status: &status, DisplayStatus: display, Progress: &progress}
	out.State = out.NormalizedState()
	return out
}

func takeAnimeStatusPoll(t *testing.T, f *downloadClaimFixture, now int64) AnimeStatusPoll {
	t.Helper()
	poll, found, err := f.store.ReserveAnimeStatusPoll(context.Background(), now, nil)
	if err != nil || !found {
		t.Fatal("poll unavailable", found, err)
	}
	return poll
}

func TestAnimeContinuationsRespectOtherStagesUIDGuards(t *testing.T) {
	for _, kind := range []string{"status", "execution"} {
		for _, guard := range []string{"lease", "rate limit", "credentials"} {
			t.Run(kind+"/"+guard, func(t *testing.T) {
				var f *downloadClaimFixture
				var job model.AnimeDownloadJob
				now := time.Now().UnixMilli()
				if kind == "status" {
					f, job, now = animeStatusStoreFixture(t)
				} else {
					f, job = animeSubmitStoreFixture(t)
				}
				release, verified := f.selectTitle(t, 0, "rel_uid_guard", strings.Repeat("b", 40), "[字幕组] 测试 - 31 [1080p][HEVC]")
				other := f.claim(t, 0, release, verified).Job
				// A different task has already reached organization. Its locks and
				// cooldown must still protect this UID's earlier download stages.
				if _, err := f.store.db.Exec(`UPDATE anime_download_jobs SET file_state='verified' WHERE id=?`, other.ID); err != nil {
					t.Fatal(err)
				}
				switch guard {
				case "lease":
					_, err := f.store.db.Exec(`UPDATE anime_download_jobs SET lease_token=?,lease_until=? WHERE id=?`, strings.Repeat("c", 32), now+60000, other.ID)
					if err != nil {
						t.Fatal(err)
					}
				case "rate limit":
					_, err := f.store.db.Exec(`UPDATE anime_download_jobs SET block_code='TASK_RATE_LIMITED',next_run_at=? WHERE id=?`, now+60000, other.ID)
					if err != nil {
						t.Fatal(err)
					}
				case "credentials":
					_, err := f.store.db.Exec(`UPDATE anime_download_jobs SET block_code='TASK_ACCESS_DENIED' WHERE id=?`, other.ID)
					if err != nil {
						t.Fatal(err)
					}
				}
				reserve := func() (bool, error) {
					if kind == "status" {
						_, found, err := f.store.ReserveAnimeStatusContinuation(context.Background(), now, job.ID)
						return found, err
					}
					_, found, err := f.store.ReserveAnimeDownloadContinuation(context.Background(), now, job.ID, true)
					return found, err
				}
				if found, err := reserve(); err != nil || found {
					t.Fatal("continuation bypassed another stage's UID guard", found, err)
				}
				if _, err := f.store.db.Exec(`UPDATE anime_download_jobs SET lease_token=NULL,lease_until=NULL,block_code=NULL,next_run_at=NULL WHERE id=?`, other.ID); err != nil {
					t.Fatal(err)
				}
				if found, err := reserve(); err != nil || !found {
					t.Fatal("guard release did not unblock the saved job", found, err)
				}
			})
		}
	}
}

func TestAnimeStatusConcurrentReservationAndDue(t *testing.T) {
	f, job, now := animeStatusStoreFixture(t)
	var wg sync.WaitGroup
	var count atomic.Int32
	var winner AnimeStatusPoll
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			poll, found, err := f.store.ReserveAnimeStatusPoll(context.Background(), now, nil)
			if err != nil {
				t.Error(err)
			}
			if found {
				count.Add(1)
				winner = poll // Exactly one writer; inspected after Wait.
			}
		}()
	}
	wg.Wait()
	if count.Load() != 1 {
		t.Fatal("reservation not exclusive", count.Load())
	}
	observation := animeStatusObservation(job, "running", 52)
	if err := f.store.CompleteAnimeStatusPoll(context.Background(), winner, &observation, "", 0, now+1); err != nil {
		t.Fatal(err)
	}
	got, _ := f.store.GetAnimeDownloadJob(context.Background(), job.ID)
	if got.DownloadState != "downloading" || got.NextRunAt == nil || *got.NextRunAt < now+5000 || *got.NextRunAt > now+20000 || got.Tracking.ObservedAt != now+1 {
		t.Fatal(got)
	}
	if _, found, err := f.store.ReserveAnimeStatusPoll(context.Background(), *got.NextRunAt-1, nil); err != nil || found {
		t.Fatal("polled before due", found, err)
	}
	if _, found, err := f.store.ReserveAnimeStatusPoll(context.Background(), *got.NextRunAt, []string{job.UID}); err != nil || found {
		t.Fatal("UID budget bypassed", found, err)
	}
}

func TestAnimeStatusCompletionIsNotFileVerification(t *testing.T) {
	f, job, now := animeStatusStoreFixture(t)
	poll := takeAnimeStatusPoll(t, f, now)
	observation := animeStatusObservation(job, "finished", 100)
	if err := f.store.CompleteAnimeStatusPoll(context.Background(), poll, &observation, "", 0, now+1); err != nil {
		t.Fatal(err)
	}
	got, _ := f.store.GetAnimeDownloadJob(context.Background(), job.ID)
	if got.DownloadState != "completed" || got.FileState != "pending" || got.WorkflowState != "active" || got.ResultObjectID == nil || *got.ResultObjectID != "300" || got.ResultObjectKind == nil || *got.ResultObjectKind != "unknown" || got.NextRunAt == nil {
		t.Fatal("remote status invented file facts", got)
	}
	// A subsequent missing task preserves completed evidence and never queues a
	// new submit; P2-07 must still obtain the actual files.
	poll = takeAnimeStatusPoll(t, f, *got.NextRunAt)
	missing := model.AnimeOfflineStatus{SchemaVersion: 1, Source: model.AnimeOfflineExact, State: "unknown"}
	if err := f.store.CompleteAnimeStatusPoll(context.Background(), poll, &missing, "", 0, *got.NextRunAt+1); err != nil {
		t.Fatal(err)
	}
	got, _ = f.store.GetAnimeDownloadJob(context.Background(), job.ID)
	if got.DownloadState != "completed" || got.BlockCode == nil || *got.BlockCode != "TASK_MISSING" || got.FileState != "pending" {
		t.Fatal(got)
	}
	// Model the P2-07 transaction's established file fact. No write API to fake
	// verification is added by this stage; the scheduler simply stops selecting.
	if _, err := f.store.db.Exec(`UPDATE anime_download_jobs SET file_state='verified',next_run_at=NULL,revision=revision+1 WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	if _, found, err := f.store.ReserveAnimeStatusPoll(context.Background(), now+86400000, nil); err != nil || found {
		t.Fatal("verified task still polled", found, err)
	}
}

func TestAnimeStatusEarlyCompletionWithoutResultIDKeepsFastChecks(t *testing.T) {
	f, job, now := animeStatusStoreFixture(t)
	ctx := context.Background()
	if _, err := f.store.db.Exec(`UPDATE anime_drive_operations SET result_json=json_remove(result_json,'$.resultId') WHERE download_job_id=? AND kind='submit'`, job.ID); err != nil {
		t.Fatal(err)
	}
	poll := takeAnimeStatusPoll(t, f, now)
	observation := animeStatusObservation(job, "finished", 100)
	observation.ResultID = ""
	if err := f.store.CompleteAnimeStatusPoll(ctx, poll, &observation, "", 0, now+1); err != nil {
		t.Fatal(err)
	}
	got, err := f.store.GetAnimeDownloadJob(ctx, job.ID)
	if err != nil || got.DownloadState != "completed" || got.ResultObjectID != nil || got.FileState != "pending" || got.NextRunAt == nil || *got.NextRunAt > now+20000 || got.BlockCode == nil || *got.BlockCode != "TASK_RESULT_PENDING" {
		t.Fatal("early completion with delayed result ID was postponed for minutes", got, err)
	}
	poll = takeAnimeStatusPoll(t, f, *got.NextRunAt)
	observation.ResultID = "300"
	if err := f.store.CompleteAnimeStatusPoll(ctx, poll, &observation, "", 0, *got.NextRunAt+1); err != nil {
		t.Fatal(err)
	}
	got, err = f.store.GetAnimeDownloadJob(ctx, job.ID)
	if err != nil || got.ResultObjectID == nil || *got.ResultObjectID != "300" || got.BlockCode != nil {
		t.Fatal("delayed result ID was not adopted", got, err)
	}
}

func TestAnimeStatusInitializingResultUsesOnlyBoundedEarlyChecks(t *testing.T) {
	for _, readFailure := range []bool{false, true} {
		t.Run(fmt.Sprint("read failure=", readFailure), func(t *testing.T) {
			f, job := animeSubmitStoreFixture(t)
			ctx := context.Background()
			op, err := f.store.PrepareAnimeSubmit(ctx, job.ID, job.Generation, f.target)
			if err != nil {
				t.Fatal(err)
			}
			if err := f.store.DispatchAnimeSubmit(ctx, op.ID); err != nil {
				t.Fatal(err)
			}
			if err := f.store.RecordAnimeSubmitOutcome(ctx, op.ID, "confirmed", "", model.AnimeSubmitObservation{SchemaVersion: 1, Kind: "accepted", TargetCID: "200"}); err != nil {
				t.Fatal(err)
			}
			op, err = f.store.GetAnimeSubmitOperation(ctx, job.ID)
			if err != nil || op.AuthorizedAt == nil {
				t.Fatal(op, err)
			}
			when := *op.AuthorizedAt + 1
			for i := 0; i < 3; i++ {
				poll := takeAnimeStatusPoll(t, f, when)
				observation := animeStatusObservation(job, "", 100)
				zero := int64(0)
				observation.Status, observation.ResultID, observation.Malformed, observation.State = &zero, "", true, "unknown"
				remote, code := &observation, ""
				if readFailure {
					remote, code = nil, "TASK_READ_FAILED"
				}
				if err := f.store.CompleteAnimeStatusPoll(ctx, poll, remote, code, 0, when); err != nil {
					t.Fatal(err)
				}
				got, err := f.store.GetAnimeDownloadJob(ctx, job.ID)
				if err != nil || got.ResultObjectID != nil || got.FileState != "pending" || got.NextRunAt == nil || got.WorkflowState != "active" {
					t.Fatal("initial observation invented file completion", got, err)
				}
				if readFailure || i == 2 {
					if got.Tracking.Failures != 1 || *got.NextRunAt < when+animeDiscoveryBackoff(1) {
						t.Fatal("unreadable or persistently unknown result bypassed backoff", got)
					}
					break
				}
				if got.Tracking.Failures != 0 || *got.NextRunAt > *op.AuthorizedAt+20001 {
					t.Fatal("initializing result incurred a minute of error backoff", got)
				}
				when = *got.NextRunAt
			}
		})
	}
}

func TestAnimeStatusUnknownAndFailurePreserveLastSuccessfulRead(t *testing.T) {
	f, job, now := animeStatusStoreFixture(t)
	poll := takeAnimeStatusPoll(t, f, now)
	observation := animeStatusObservation(job, "running", 100)
	if err := f.store.CompleteAnimeStatusPoll(context.Background(), poll, &observation, "", 0, now+1); err != nil {
		t.Fatal(err)
	}
	got, _ := f.store.GetAnimeDownloadJob(context.Background(), job.ID)
	for i := 0; i < 4; i++ {
		when := *got.NextRunAt
		poll = takeAnimeStatusPoll(t, f, when)
		if err := f.store.CompleteAnimeStatusPoll(context.Background(), poll, nil, "TASK_READ_FAILED", 0, when+1); err != nil {
			t.Fatal(err)
		}
		got, _ = f.store.GetAnimeDownloadJob(context.Background(), job.ID)
		if got.DownloadState != "downloading" || got.Tracking.ObservedAt != now+1 || !reflect.DeepEqual(got.Tracking.Observation, &observation) || got.Tracking.Failures != i+1 || *got.NextRunAt-when < animeDiscoveryBackoff(i+1) {
			t.Fatal("failed read replaced success or lost backoff", got)
		}
	}
	poll = takeAnimeStatusPoll(t, f, *got.NextRunAt)
	unknown := animeStatusObservation(job, "", 100)
	unknown.Malformed, unknown.State = true, "unknown"
	if err := f.store.CompleteAnimeStatusPoll(context.Background(), poll, &unknown, "", 0, *got.NextRunAt+1); err != nil {
		t.Fatal(err)
	}
	got, _ = f.store.GetAnimeDownloadJob(context.Background(), job.ID)
	if got.DownloadState != "unknown" || *got.BlockCode != "TASK_STATUS_UNKNOWN" {
		t.Fatal("invalid status inferred completed", got)
	}
	poll = takeAnimeStatusPoll(t, f, *got.NextRunAt)
	failed := animeStatusObservation(job, "failed", 45)
	if err := f.store.CompleteAnimeStatusPoll(context.Background(), poll, &failed, "", 0, *got.NextRunAt+1); err != nil {
		t.Fatal(err)
	}
	got, _ = f.store.GetAnimeDownloadJob(context.Background(), job.ID)
	if got.DownloadState != "failed" || got.NextRunAt != nil || got.FileState != "pending" {
		t.Fatal(got)
	}
}

func TestAnimeStatusIdentityConflictsStopWithoutReplacingEvidence(t *testing.T) {
	for _, mode := range []string{"hash", "target", "result", "missing relation", "credential"} {
		t.Run(mode, func(t *testing.T) {
			f, job, now := animeStatusStoreFixture(t)
			poll := takeAnimeStatusPoll(t, f, now)
			observation := animeStatusObservation(job, "finished", 100)
			code := ""
			switch mode {
			case "hash":
				observation.InfoHash = strings.Repeat("b", 40)
			case "target":
				observation.TargetCID = "999"
			case "result":
				observation.ResultID = "999"
			case "missing relation":
				observation.TargetCID, observation.ResultID = "", ""
			case "credential":
				code = "DOWNLOAD_ACCOUNT_CHANGED"
			}
			var remote *model.AnimeOfflineStatus = &observation
			if mode == "credential" {
				remote = nil
			}
			if err := f.store.CompleteAnimeStatusPoll(context.Background(), poll, remote, code, 0, now+1); err != nil {
				t.Fatal(err)
			}
			got, _ := f.store.GetAnimeDownloadJob(context.Background(), job.ID)
			if got.DownloadState != "accepted" || got.FileState != "needs_review" || got.NextRunAt != nil || got.Tracking.ObservedAt != 0 || got.Tracking.Observation != nil || *got.ResultObjectID != "300" {
				t.Fatal("foreign evidence adopted", got)
			}
			if _, found, err := f.store.ReserveAnimeStatusPoll(context.Background(), now+86400000, nil); err != nil || found {
				t.Fatal("blocked task still polled", found, err)
			}
		})
	}
}

func TestAnimeStatusStaleReceiptsCannotReleaseNewReservation(t *testing.T) {
	for _, mode := range []string{"expired token", "generation", "revision", "verified", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			f, job, now := animeStatusStoreFixture(t)
			old := takeAnimeStatusPoll(t, f, now)
			when := now + 1
			switch mode {
			case "expired token":
				when = old.Until
				_ = takeAnimeStatusPoll(t, f, when)
			case "generation":
				_, _ = f.store.db.Exec(`UPDATE anime_download_jobs SET generation=generation+1 WHERE id=?`, job.ID)
			case "revision":
				_, _ = f.store.db.Exec(`UPDATE anime_download_jobs SET revision=revision+1 WHERE id=?`, job.ID)
			case "verified":
				_, _ = f.store.db.Exec(`UPDATE anime_download_jobs SET download_state='completed',file_state='verified' WHERE id=?`, job.ID)
			case "cancelled":
				_, _ = f.store.db.Exec(`UPDATE anime_download_jobs SET workflow_state='cancelled',download_state='cancelled',lease_token=NULL,lease_until=NULL,next_run_at=NULL WHERE id=?`, job.ID)
			}
			before := animeMigrationRows(t, f.store.db, "anime_download_jobs")
			observation := animeStatusObservation(job, "finished", 100)
			if err := f.store.CompleteAnimeStatusPoll(context.Background(), old, &observation, "", 0, when); !errors.Is(err, ErrAnimeConflict) {
				t.Fatal("stale result accepted", err)
			}
			if after := animeMigrationRows(t, f.store.db, "anime_download_jobs"); !reflect.DeepEqual(before, after) {
				t.Fatal("stale receipt mutated current task")
			}
		})
	}
}

func TestAnimeStatusReceiptRollbackAndRestart(t *testing.T) {
	f, job, now := animeStatusStoreFixture(t)
	poll := takeAnimeStatusPoll(t, f, now)
	before := animeMigrationRows(t, f.store.db, "anime_download_jobs")
	if _, err := f.store.db.Exec(`CREATE TRIGGER reject_status BEFORE UPDATE OF raw_status_json ON anime_download_jobs BEGIN SELECT RAISE(ABORT,'write failed'); END`); err != nil {
		t.Fatal(err)
	}
	observation := animeStatusObservation(job, "finished", 100)
	if err := f.store.CompleteAnimeStatusPoll(context.Background(), poll, &observation, "", 0, now+1); err == nil {
		t.Fatal("write failure ignored")
	}
	if after := animeMigrationRows(t, f.store.db, "anime_download_jobs"); !reflect.DeepEqual(before, after) {
		t.Fatal("partial receipt escaped rollback")
	}
	if _, err := f.store.db.Exec(`DROP TRIGGER reject_status`); err != nil {
		t.Fatal(err)
	}
	if err := f.store.CompleteAnimeStatusPoll(context.Background(), poll, &observation, "", 0, now+1); err != nil {
		t.Fatal(err)
	}
	want, _ := f.store.GetAnimeDownloadJob(context.Background(), job.ID)
	var path string
	if err := f.store.db.QueryRow(`SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&path); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got, err := reopened.GetAnimeDownloadJob(context.Background(), job.ID)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatal("observation not durable", got, err)
	}
	if _, found, err := reopened.ReserveAnimeStatusPoll(context.Background(), *got.NextRunAt-1, nil); err != nil || found {
		t.Fatal("restart lost due time", found, err)
	}
}

func TestAnimeStatusUIDBudgetAndSharedCooldown(t *testing.T) {
	for _, mode := range []string{"normal", "rate limited", "access denied"} {
		t.Run(mode, func(t *testing.T) {
			f, _, now := animeStatusStoreFixture(t)
			release, verified := f.selectTitle(t, 0, "rel_second", strings.Repeat("b", 40), "[字幕组] 测试 - 31 [1080p][HEVC]")
			job := f.claim(t, 0, release, verified).Job
			if _, err := f.store.db.Exec(`UPDATE anime_download_jobs SET directory_cid='201' WHERE id=?`, job.ID); err != nil {
				t.Fatal(err)
			}
			op, err := f.store.PrepareAnimeSubmit(context.Background(), job.ID, job.Generation, f.target)
			if err != nil {
				t.Fatal(err)
			}
			if err := f.store.DispatchAnimeSubmit(context.Background(), op.ID); err != nil {
				t.Fatal(err)
			}
			if err := f.store.RecordAnimeSubmitOutcome(context.Background(), op.ID, "confirmed", "", model.AnimeSubmitObservation{SchemaVersion: 1, Kind: "matched", TargetCID: "201", ResultID: "301"}); err != nil {
				t.Fatal(err)
			}
			poll := takeAnimeStatusPoll(t, f, now)
			if _, found, err := f.store.ReserveAnimeStatusPoll(context.Background(), now, nil); err != nil || found {
				t.Fatal("same UID admitted concurrent status read", found, err)
			}
			code, retry := "TASK_READ_FAILED", int64(0)
			if mode == "rate limited" {
				code, retry = "TASK_RATE_LIMITED", now+600000
			}
			if mode == "access denied" {
				code = "TASK_ACCESS_DENIED"
			}
			if err := f.store.CompleteAnimeStatusPoll(context.Background(), poll, nil, code, retry, now+1); err != nil {
				t.Fatal(err)
			}
			if _, found, err := f.store.ReserveAnimeStatusPoll(context.Background(), now+5000, nil); err != nil || found {
				t.Fatal("shared minimum interval bypassed", found, err)
			}
			when := now + 5001
			if mode == "rate limited" {
				if _, found, err := f.store.ReserveAnimeStatusPoll(context.Background(), retry-1, nil); err != nil || found {
					t.Fatal("other Hash bypassed rate limit", found, err)
				}
				when = retry
			}
			if mode == "access denied" {
				when = now + 86400000
			}
			next, found, err := f.store.ReserveAnimeStatusPoll(context.Background(), when, nil)
			if err != nil || found != (mode != "access denied") || found && next.Job.ID == poll.Job.ID {
				t.Fatal("incorrect UID cooldown/fairness", next.Job.ID, found, err)
			}
		})
	}
}

func TestAnimeStatusUnconfirmedSubmitNeverEntersTracker(t *testing.T) {
	for _, state := range []string{"prepared", "dispatched", "unknown"} {
		t.Run(state, func(t *testing.T) {
			f, job := animeSubmitStoreFixture(t)
			op, err := f.store.PrepareAnimeSubmit(context.Background(), job.ID, job.Generation, f.target)
			if err != nil {
				t.Fatal(err)
			}
			if state != "prepared" {
				if err := f.store.DispatchAnimeSubmit(context.Background(), op.ID); err != nil {
					t.Fatal(err)
				}
			}
			if state == "unknown" {
				if err := f.store.RecordAnimeSubmitOutcome(context.Background(), op.ID, "unknown", "TASK_MISSING", model.AnimeSubmitObservation{SchemaVersion: 1, Kind: "missing"}); err != nil {
					t.Fatal(err)
				}
			}
			if _, found, err := f.store.ReserveAnimeStatusPoll(context.Background(), time.Now().UnixMilli(), nil); err != nil || found {
				t.Fatal("tracker adopted an unconfirmed submit", found, err)
			}
		})
	}
}

func TestAnimeStatusRegressedCompletedTaskRequiresReview(t *testing.T) {
	f, job, now := animeStatusStoreFixture(t)
	poll := takeAnimeStatusPoll(t, f, now)
	completed := animeStatusObservation(job, "finished", 100)
	if err := f.store.CompleteAnimeStatusPoll(context.Background(), poll, &completed, "", 0, now+1); err != nil {
		t.Fatal(err)
	}
	got, _ := f.store.GetAnimeDownloadJob(context.Background(), job.ID)
	poll = takeAnimeStatusPoll(t, f, *got.NextRunAt)
	running := animeStatusObservation(job, "running", 1)
	if err := f.store.CompleteAnimeStatusPoll(context.Background(), poll, &running, "", 0, *got.NextRunAt+1); err != nil {
		t.Fatal(err)
	}
	got, _ = f.store.GetAnimeDownloadJob(context.Background(), job.ID)
	if got.DownloadState != "completed" || got.FileState != "needs_review" || *got.BlockCode != "TASK_STATUS_CONFLICT" || got.NextRunAt != nil || !reflect.DeepEqual(got.Tracking.Observation, &completed) {
		t.Fatal("completed evidence overwritten with conflicting active task", got)
	}
}
