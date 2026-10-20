package storage

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"strm115/backend/internal/model"
)

func takeAnimeWork(t *testing.T, s *Store, now int64) AnimeDownloadWork {
	t.Helper()
	work, found, err := s.ReserveAnimeDownloadWork(context.Background(), now, nil)
	if err != nil || !found {
		t.Fatal("work unavailable", found, err)
	}
	return work
}

func TestAnimeOrganizationRecoverySuccessContinuesWithoutFailureBackoff(t *testing.T) {
	for _, code := range []string{"", "ORGANIZE_UNCERTAIN", "ORGANIZE_READ_FAILED", "TASK_RATE_LIMITED"} {
		t.Run("code="+code, func(t *testing.T) {
			f, job := animeOrganizeStoreFixture(t)
			ctx := context.Background()
			work := takeAnimeWork(t, f.store, time.Now().UnixMilli())
			// The checkpoint of a successfully reconciled step makes the next
			// stage due now. Only unresolved failures should add five minutes.
			now := time.Now().UnixMilli()
			if _, err := f.store.db.Exec(`UPDATE anime_download_jobs SET block_code=NULLIF(?,''),next_run_at=? WHERE id=?`, code, now, job.ID); err != nil {
				t.Fatal(err)
			}
			if err := f.store.CompleteAnimeDownloadWork(ctx, work, "", now); err != nil {
				t.Fatal(err)
			}
			got, err := f.store.GetAnimeDownloadJob(ctx, job.ID)
			wantDelay := int64(300000)
			if code == "" {
				wantDelay = 5000
			}
			if err != nil || got.NextRunAt == nil || *got.NextRunAt != now+wantDelay {
				t.Fatal("wrong next stage time", got.NextRunAt, err)
			}
			if _, found, err := f.store.ReserveAnimeDownloadWork(ctx, now+4999, nil); err != nil || found {
				t.Fatal("per-UID interval bypassed", found, err)
			}
			_, found, err := f.store.ReserveAnimeDownloadWork(ctx, now+5001, nil)
			if err != nil || found != (code == "") {
				t.Fatal("successful recovery delayed or failure retried early", found, err)
			}
		})
	}
}

func TestAnimeOrganizationRecoveryRetainsLongerRetryTime(t *testing.T) {
	f, job := animeOrganizeStoreFixture(t)
	ctx := context.Background()
	now := time.Now().UnixMilli()
	work := takeAnimeWork(t, f.store, now)
	later := now + 900000
	if _, err := f.store.db.Exec(`UPDATE anime_download_jobs SET next_run_at=? WHERE id=?`, later, job.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.store.CompleteAnimeDownloadWork(ctx, work, "", now); err != nil {
		t.Fatal(err)
	}
	got, err := f.store.GetAnimeDownloadJob(ctx, job.ID)
	if err != nil || got.NextRunAt == nil || *got.NextRunAt != later {
		t.Fatal("saved delay was shortened", got.NextRunAt, err)
	}
}

func TestAnimeRecoverySingleLeaseAndExpiredOwnerFencing(t *testing.T) {
	f, job := animeSubmitStoreFixture(t)
	ctx := context.Background()
	now := time.Now().UnixMilli()
	var wins atomic.Int32
	var wg sync.WaitGroup
	var owner AnimeDownloadWork
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			work, found, err := f.store.ReserveAnimeDownloadWork(ctx, now, nil)
			if err != nil {
				t.Error(err)
			}
			if found {
				owner = work // the SQL lease permits exactly one writer
				wins.Add(1)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatal("lease not exclusive", wins.Load())
	}
	op, err := f.store.PrepareAnimeSubmit(ctx, job.ID, job.Generation, f.target, owner.Token)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.DispatchAnimeSubmit(ctx, op.ID); !errors.Is(err, ErrAnimeConflict) {
		t.Fatal("unleased writer bypassed reservation", err)
	}
	// Simulate process loss and expiry, then a different worker taking over.
	if _, err := f.store.db.Exec(`UPDATE anime_download_jobs SET lease_until=1 WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	next := takeAnimeWork(t, f.store, now+1)
	if next.Token == owner.Token {
		t.Fatal("takeover reused old token")
	}
	if err := f.store.DispatchAnimeSubmit(ctx, op.ID, owner.Token); !errors.Is(err, ErrAnimeConflict) {
		t.Fatal("old worker dispatched", err)
	}
	if err := f.store.CompleteAnimeDownloadWork(ctx, owner, "", now+2); !errors.Is(err, ErrAnimeConflict) {
		t.Fatal("old worker released new lease", err)
	}
	if err := f.store.DispatchAnimeSubmit(ctx, op.ID, next.Token); err != nil {
		t.Fatal(err)
	}
	receipt := model.AnimeSubmitObservation{SchemaVersion: 1, Kind: "accepted", TargetCID: "200"}
	if err := f.store.RecordAnimeSubmitOutcome(ctx, op.ID, "confirmed", "", receipt, owner.Token); !errors.Is(err, ErrAnimeConflict) {
		t.Fatal("old receipt advanced new worker", err)
	}
	if err := f.store.RecordAnimeSubmitOutcome(ctx, op.ID, "confirmed", "", receipt, next.Token); err != nil {
		t.Fatal(err)
	}
	if err := f.store.CompleteAnimeDownloadWork(ctx, next, "", now+3); err != nil {
		t.Fatal(err)
	}
	got, err := f.store.GetAnimeDownloadJob(ctx, job.ID)
	if err != nil || got.DownloadState != "accepted" || got.NextRunAt == nil || *got.NextRunAt < now+120000 {
		t.Fatal("success did not defer first status GET", got, err)
	}
	if _, found, err := f.store.ReserveAnimeStatusPoll(ctx, *got.NextRunAt-1, nil); err != nil || found {
		t.Fatal("immediate duplicate status query", found, err)
	}
	if _, found, err := f.store.ReserveAnimeStatusPoll(ctx, *got.NextRunAt, nil); err != nil || !found {
		t.Fatal("accepted receipt not trackable", found, err)
	}
}

func TestAnimeRecoveryDirectoryAndManifestRejectOldLease(t *testing.T) {
	ctx := context.Background()
	t.Run("directory", func(t *testing.T) {
		f, job := animeDirectoryStoreFixture(t)
		owner := takeAnimeWork(t, f.store, time.Now().UnixMilli())
		started, create, err := f.store.StartAnimeDirectory(ctx, job.ID, job.Generation, f.target, owner.Token)
		if err != nil || !create {
			t.Fatal(started, create, err)
		}
		if _, err := f.store.db.Exec(`UPDATE anime_download_jobs SET lease_until=1`); err != nil {
			t.Fatal(err)
		}
		next := takeAnimeWork(t, f.store, time.Now().UnixMilli())
		root := model.AnimeDriveEntry{ID: "200", ParentCID: job.ParentCID, Kind: "directory", Name: job.DirectoryName}
		if err := f.store.FinishAnimeDirectory(ctx, started, &root, "", owner.Token); !errors.Is(err, ErrAnimeConflict) {
			t.Fatal("stale create receipt was applied", err)
		}
		if err := f.store.CheckAnimeDownloadWork(ctx, next); err != nil {
			t.Fatal("old receipt damaged current lease", err)
		}
	})
	t.Run("manifest", func(t *testing.T) {
		f, job := animeFilesStoreFixture(t)
		owner := takeAnimeWork(t, f.store, time.Now().UnixMilli())
		if _, err := f.store.db.Exec(`UPDATE anime_download_jobs SET lease_until=1`); err != nil {
			t.Fatal(err)
		}
		next := takeAnimeWork(t, f.store, time.Now().UnixMilli())
		manifest := animeFilesStoreManifest(job)
		if err := f.store.SaveAnimeFiles(ctx, job, manifest, owner.Token); !errors.Is(err, ErrAnimeConflict) {
			t.Fatal("stale manifest was saved", err)
		}
		if err := f.store.MarkAnimeFilesReview(ctx, job, "FILES_READ_FAILED", owner.Token); !errors.Is(err, ErrAnimeConflict) {
			t.Fatal("stale error cleared current lease", err)
		}
		if err := f.store.SaveAnimeFiles(ctx, job, manifest, next.Token); err != nil {
			t.Fatal(err)
		}
		if _, found, err := f.store.ReserveAnimeDownloadWork(ctx, time.Now().Add(24*time.Hour).UnixMilli(), nil); err != nil || found {
			t.Fatal("finished job reclaimed", found, err)
		}
	})
}

func TestAnimeRecoveryStageEligibilityAndStop(t *testing.T) {
	for _, phase := range []string{"queued", "prepared", "dispatched", "unknown", "accepted", "failed", "blocked", "completed", "verified", "original_saved", "needs_review", "organized", "cancelled"} {
		t.Run(phase, func(t *testing.T) {
			f, job := animeSubmitStoreFixture(t)
			ctx := context.Background()
			if phase == "prepared" || phase == "dispatched" || phase == "unknown" {
				op, err := f.store.PrepareAnimeSubmit(ctx, job.ID, job.Generation, f.target)
				if err != nil {
					t.Fatal(err)
				}
				if phase != "prepared" {
					if err := f.store.DispatchAnimeSubmit(ctx, op.ID); err != nil {
						t.Fatal(err)
					}
				}
				if phase == "unknown" {
					if err := f.store.RecordAnimeSubmitOutcome(ctx, op.ID, "unknown", "SUBMIT_UNCERTAIN", model.AnimeSubmitObservation{SchemaVersion: 1, Kind: "unreadable"}); err != nil {
						t.Fatal(err)
					}
				}
			} else if phase != "queued" {
				if phase == "original_saved" || phase == "organized" {
					if _, err := f.store.db.Exec(`UPDATE anime_download_jobs SET download_state='completed',file_state='verified'`); err != nil {
						t.Fatal(err)
					}
				}
				column := "download_state"
				if phase == "verified" || phase == "needs_review" {
					column = "file_state"
				}
				if phase == "original_saved" || phase == "organized" || phase == "cancelled" {
					column = "workflow_state"
				}
				if _, err := f.store.db.Exec(`UPDATE anime_download_jobs SET `+column+`=?,result_object_id='300'`, phase); err != nil {
					t.Fatal(err)
				}
			}
			work, found, err := f.store.ReserveAnimeDownloadWork(ctx, time.Now().Add(time.Hour).UnixMilli(), nil)
			want := phase == "queued" || phase == "prepared" || phase == "dispatched" || phase == "unknown" || phase == "completed"
			if err != nil || found != want {
				t.Fatal("wrong recovered stage", phase, found, err)
			}
			if found {
				if err := f.store.CompleteAnimeDownloadWork(ctx, work, "DOWNLOAD_ACCOUNT_CHANGED", time.Now().Add(time.Hour).UnixMilli()); err != nil {
					t.Fatal(err)
				}
				got, _ := f.store.GetAnimeDownloadJob(ctx, job.ID)
				if got.NextRunAt != nil || got.FileState != "needs_review" {
					t.Fatal("invalid account did not stop", got)
				}
			}
		})
	}
}

func TestAnimeRecoveryUnknownOnlyReadsOnceAndFailureBackoff(t *testing.T) {
	f, job := animeSubmitStoreFixture(t)
	ctx := context.Background()
	op, err := f.store.PrepareAnimeSubmit(ctx, job.ID, job.Generation, f.target)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.DispatchAnimeSubmit(ctx, op.ID); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UnixMilli()
	work := takeAnimeWork(t, f.store, now)
	if err := f.store.RecordAnimeSubmitOutcome(ctx, op.ID, "unknown", "TASK_MISSING", model.AnimeSubmitObservation{SchemaVersion: 1, Kind: "missing"}, work.Token); err != nil {
		t.Fatal(err)
	}
	if err := f.store.CompleteAnimeDownloadWork(ctx, work, "", now+1); err != nil {
		t.Fatal(err)
	}
	got, _ := f.store.GetAnimeDownloadJob(ctx, job.ID)
	if got.FileState != "needs_review" || got.NextRunAt != nil {
		t.Fatal("missing result kept scanning", got)
	}
	if _, found, err := f.store.ReserveAnimeDownloadWork(ctx, now+86400000, nil); err != nil || found {
		t.Fatal(found, err)
	}

	f, _ = animeSubmitStoreFixture(t)
	for i := 1; i <= 5; i++ {
		work = takeAnimeWork(t, f.store, now)
		if err := f.store.CompleteAnimeDownloadWork(ctx, work, "TASK_READ_FAILED", now+1); err != nil {
			t.Fatal(err)
		}
		got, _ := f.store.GetAnimeDownloadJob(ctx, work.Job.ID)
		if got.Tracking.Failures != i || *got.NextRunAt-now < max(300000, animeDiscoveryBackoff(i)) {
			t.Fatal("failure did not back off", got)
		}
		now = *got.NextRunAt
	}
}

func TestAnimeRecoverySharesUIDLeaseIntervalAndRateLimitWithStatus(t *testing.T) {
	f, _, now := animeStatusStoreFixture(t)
	release, verified := f.selectTitle(t, 0, "rel_waiting", strings.Repeat("b", 40), "[字幕组] 测试 - 31 [1080p][HEVC]")
	queued := f.claim(t, 0, release, verified).Job
	ctx := context.Background()
	work := takeAnimeWork(t, f.store, now)
	if work.Job.ID != queued.ID {
		t.Fatal("accepted work entered execution queue")
	}
	if _, found, err := f.store.ReserveAnimeStatusPoll(ctx, now, nil); err != nil || found {
		t.Fatal("status overlapped same UID execution", found, err)
	}
	if err := f.store.CompleteAnimeDownloadWork(ctx, work, "TASK_READ_FAILED", now+1); err != nil {
		t.Fatal(err)
	}
	if _, found, err := f.store.ReserveAnimeStatusPoll(ctx, now+5000, nil); err != nil || found {
		t.Fatal("status bypassed recovery interval", found, err)
	}
	poll := takeAnimeStatusPoll(t, f, now+5001)
	retryAt := now + 600000
	if err := f.store.CompleteAnimeStatusPoll(ctx, poll, nil, "TASK_RATE_LIMITED", retryAt, now+5002); err != nil {
		t.Fatal(err)
	}
	if _, found, err := f.store.ReserveAnimeDownloadWork(ctx, retryAt-1, nil); err != nil || found {
		t.Fatal("recovery bypassed shared Retry-After", found, err)
	}
	if _, found, err := f.store.ReserveAnimeDownloadWork(ctx, retryAt, []string{queued.UID}); err != nil || found {
		t.Fatal("same pass repeated UID", found, err)
	}
	work = takeAnimeWork(t, f.store, retryAt)
	if work.Job.ID != queued.ID {
		t.Fatal("cooldown lost pending work")
	}
	if _, found, err := f.store.ReserveAnimeStatusPoll(ctx, retryAt, nil); err != nil || found {
		t.Fatal("status bypassed recovery lease", found, err)
	}
}
