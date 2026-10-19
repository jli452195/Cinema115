package storage

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"strm115/backend/internal/model"
)

func animeSubmitStoreFixture(t *testing.T) (*downloadClaimFixture, model.AnimeDownloadJob) {
	t.Helper()
	f := newDownloadClaimFixture(t)
	r, v := f.selectRelease(t, 0, "rel_dmhy_1", strings.Repeat("a", 40))
	job := f.claim(t, 0, r, v).Job
	if _, err := f.store.db.Exec(`UPDATE anime_download_jobs SET directory_cid='200' WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	job.DirectoryCID = animePtr("200")
	return f, job
}

func TestAnimeSubmitAtomicPreparationAndSingleDispatch(t *testing.T) {
	f, job := animeSubmitStoreFixture(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	var dispatched atomic.Int32
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			op, err := f.store.PrepareAnimeSubmit(ctx, job.ID, job.Generation, f.target)
			if err != nil {
				t.Error(err)
				return
			}
			err = f.store.DispatchAnimeSubmit(ctx, op.ID)
			if err == nil {
				dispatched.Add(1)
			} else if !errors.Is(err, ErrAnimeConflict) {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if dispatched.Load() != 1 {
		t.Fatal("multiple dispatch permits", dispatched.Load())
	}
	var count int
	if err := f.store.db.QueryRow(`SELECT count(*) FROM anime_drive_operations`).Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	op, err := f.store.GetAnimeSubmitOperation(ctx, job.ID)
	if err != nil || op.State != "dispatched" || op.AuthorizedAt == nil {
		t.Fatal(op, err)
	}
	if _, err := f.store.PrepareAnimeSubmit(ctx, job.ID, job.Generation, f.target); err != nil {
		t.Fatal("dispatched intent not recoverable", err)
	}
}

func TestAnimeSubmitBackupAuthorizationSurvivesMainWithdrawal(t *testing.T) {
	for _, backup := range []bool{false, true} {
		t.Run(map[bool]string{false: "all withdrawn", true: "backup remains"}[backup], func(t *testing.T) {
			f, job := animeSubmitStoreFixture(t)
			if backup {
				r, v := f.selectRelease(t, 1, "rel_mikan_1", job.Snapshot.InfoHash)
				if f.claim(t, 1, r, v).Job.ID != job.ID {
					t.Fatal("same hash changed job")
				}
			}
			op, err := f.store.PrepareAnimeSubmit(context.Background(), job.ID, job.Generation, f.target)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.store.db.Exec(`UPDATE anime_job_selections SET status='withdrawn' WHERE source_id=?`, f.sub.Sources[0].ID); err != nil {
				t.Fatal(err)
			}
			err = f.store.DispatchAnimeSubmit(context.Background(), op.ID)
			if backup && err != nil || !backup && !errors.Is(err, ErrAnimeWriteUnauthorized) {
				t.Fatal("independent authorizations not respected", err)
			}
		})
	}
}

func TestAnimeSubmitOutcomeFencesAndValidation(t *testing.T) {
	f, job := animeSubmitStoreFixture(t)
	ctx := context.Background()
	op, err := f.store.PrepareAnimeSubmit(ctx, job.ID, job.Generation, f.target)
	if err != nil {
		t.Fatal(err)
	}
	matched := model.AnimeSubmitObservation{SchemaVersion: 1, Kind: "matched", TargetCID: "200"}
	if err := f.store.RecordAnimeSubmitOutcome(ctx, op.ID, "confirmed", "", matched); !errors.Is(err, ErrAnimeConflict) {
		t.Fatal("unissued intent confirmed", err)
	}
	if err := f.store.DispatchAnimeSubmit(ctx, op.ID); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		state, code string
		observation model.AnimeSubmitObservation
	}{
		{"prepared", "", matched}, {"unknown", "Cookie=private", matched}, {"confirmed", "", model.AnimeSubmitObservation{SchemaVersion: 1, Kind: "matched", TargetCID: "999"}},
		{"failed", "TASK_TARGET_CONFLICT", model.AnimeSubmitObservation{SchemaVersion: 1, Kind: "conflict"}},
		{"failed", "SUBMIT_REJECTED", model.AnimeSubmitObservation{SchemaVersion: 1, Kind: "conflict"}},
		{"unknown", "", model.AnimeSubmitObservation{SchemaVersion: 1, Kind: "unreadable"}},
		{"unknown", "SUBMIT_REJECTED", model.AnimeSubmitObservation{SchemaVersion: 1, Kind: "rejected"}},
	} {
		if err := f.store.RecordAnimeSubmitOutcome(ctx, op.ID, test.state, test.code, test.observation); err == nil {
			t.Fatal("invalid outcome accepted")
		}
		got, err := f.store.GetAnimeSubmitOperation(ctx, job.ID)
		if err != nil || got.State != "dispatched" || got.Result != nil {
			t.Fatal("invalid outcome mutated dispatched intent", got, err)
		}
	}
	if err := f.store.RecordAnimeSubmitOutcome(ctx, op.ID, "confirmed", "", matched); err != nil {
		t.Fatal(err)
	}
	if err := f.store.RecordAnimeSubmitOutcome(ctx, op.ID, "unknown", "TASK_MISSING", model.AnimeSubmitObservation{SchemaVersion: 1, Kind: "missing"}); err != nil {
		t.Fatal(err)
	}
	got, err := f.store.GetAnimeSubmitOperation(ctx, job.ID)
	if err != nil || got.State != "confirmed" {
		t.Fatal("late result regressed confirmation", got, err)
	}
	if err := f.store.DispatchAnimeSubmit(ctx, op.ID); !errors.Is(err, ErrAnimeConflict) {
		t.Fatal("confirmed request dispatched again", err)
	}
}
