package service

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
)

type animeStatusReaderFunc func(context.Context, string, string) (model.AnimeOfflineStatus, error)

func (f animeStatusReaderFunc) ReadAnimeTaskStatus(ctx context.Context, cookie, hash string) (model.AnimeOfflineStatus, error) {
	return f(ctx, cookie, hash)
}

func animeStatusFixture(t *testing.T) (*animeDownloadFixture, *sql.DB, model.AnimeDownloadJob, *AnimeStatusRunner, *int64) {
	t.Helper()
	f, db, session, job := animeSubmitFixture(t)
	op, err := f.service.Store.PrepareAnimeSubmit(context.Background(), job.ID, job.Generation, session.Target())
	if err != nil {
		t.Fatal(err)
	}
	if err := f.service.Store.DispatchAnimeSubmit(context.Background(), op.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.service.Store.RecordAnimeSubmitOutcome(context.Background(), op.ID, "confirmed", "", model.AnimeSubmitObservation{SchemaVersion: 1, Kind: "matched", TargetCID: "200", ResultID: "300"}); err != nil {
		t.Fatal(err)
	}
	// A status worker has no reason to invoke either of these legacy gateways.
	f.service.SubmitGateway = &animeSubmitGatewayStub{
		inspect: func(context.Context, string, string) (p115.AnimeTaskObservation, error) {
			t.Error("status used submit inspector")
			return p115.AnimeTaskObservation{}, nil
		},
		submit: func(context.Context, string, string, string) error { t.Error("status submitted task"); return nil },
	}
	job, err = f.service.Store.GetAnimeDownloadJob(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	now := *job.NextRunAt
	runner := &AnimeStatusRunner{Admin: f.service, now: func() time.Time { return time.UnixMilli(now) }}
	return f, db, job, runner, &now
}

func serviceAnimeStatus(job model.AnimeDownloadJob, display string, progress float64) model.AnimeOfflineStatus {
	status := int64(1)
	out := model.AnimeOfflineStatus{SchemaVersion: 1, Source: model.AnimeOfflineExact, Present: true, InfoHash: job.Snapshot.InfoHash, ResultID: "300", Status: &status, DisplayStatus: display, Progress: &progress}
	out.State = out.NormalizedState()
	return out
}

func TestAnimeStatusRunnerUsesPinnedExactReadAndNeverResubmits(t *testing.T) {
	f, db, job, runner, now := animeStatusFixture(t)
	f.service.Directories = animeDownloadDirectoryFunc(func(context.Context, string, string) (p115.DriveDirectory, error) {
		t.Error("status polling performed an unnecessary directory read")
		return p115.DriveDirectory{}, errors.New("directory unavailable")
	})
	requests, mode := 0, "running"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != "GET" || r.URL.Path != "/web/" || r.URL.Query().Get("ac") != "get_user_task" || r.URL.Query().Get("info_hash") != job.Snapshot.InfoHash || r.Header.Get("Cookie") != animeDownloadTestCookie {
			t.Error("wrong exact read")
		}
		switch mode {
		case "running":
			w.Write([]byte(`{"state":true,"info_hash":"` + job.Snapshot.InfoHash + `","file_id":"300","status":1,"display_status":"running","percentDone":50}`))
		case "missing":
			w.Write([]byte(`{"state":false,"errcode":10021}`))
		case "list incomplete":
			w.Write([]byte(`{"state":true,"tasks":[],"page":1,"page_count":99}`))
		case "completed":
			w.Write([]byte(`{"state":true,"info_hash":"` + job.Snapshot.InfoHash + `","file_id":"300","status":1,"display_status":"finished","percentDone":100}`))
		}
	}))
	defer server.Close()
	client := p115.NewCloudClient()
	client.HTTP, client.BaseURL = server.Client(), server.URL
	f.service.StatusGateway = client
	for i, stage := range []struct{ mode, state, code string }{
		{"running", "downloading", ""}, {"missing", "unknown", "TASK_MISSING"}, {"list incomplete", "unknown", "TASK_READ_FAILED"}, {"completed", "completed", ""},
	} {
		mode = stage.mode
		if err := runner.PollOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
		got, err := f.service.Store.GetAnimeDownloadJob(context.Background(), job.ID)
		if err != nil || got.DownloadState != stage.state || got.FileState != "pending" || got.WorkflowState != "active" || requests != i+1 || got.NextRunAt == nil {
			t.Fatal(stage, got, requests, err)
		}
		if stage.code == "" && got.BlockCode != nil || stage.code != "" && (got.BlockCode == nil || *got.BlockCode != stage.code) {
			t.Fatal("wrong issue code", got)
		}
		if err := runner.PollOnce(context.Background()); err != nil || requests != i+1 {
			t.Fatal("duplicate poll ignored due time", err, requests)
		}
		*now = *got.NextRunAt
	}
	if _, err := db.Exec(`UPDATE anime_download_jobs SET file_state='verified',next_run_at=NULL,revision=revision+1 WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	*now += 86400000
	if err := runner.PollOnce(context.Background()); err != nil || requests != 4 {
		t.Fatal("verified task still polled", err, requests)
	}
	var operations, jobs int
	if err := db.QueryRow(`SELECT count(*) FROM anime_drive_operations`).Scan(&operations); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM anime_download_jobs`).Scan(&jobs); err != nil || operations != 1 || jobs != 1 {
		t.Fatal("polling created a new operation/job", operations, jobs, err)
	}
}

func TestAnimeStatusRunnerRateLimitAndReadErrorsAreRedacted(t *testing.T) {
	for _, mode := range []string{"429", "503", "403", "private", "invalid"} {
		t.Run(mode, func(t *testing.T) {
			f, db, _, runner, now := animeStatusFixture(t)
			f.service.StatusGateway = animeStatusReaderFunc(func(context.Context, string, string) (model.AnimeOfflineStatus, error) {
				switch mode {
				case "429":
					return model.AnimeOfflineStatus{}, &p115.HTTPError{Status: 429, RetryAfter: 10 * time.Minute}
				case "503":
					return model.AnimeOfflineStatus{}, &p115.HTTPError{Status: 503, RetryAfter: 10 * time.Minute}
				case "403":
					return model.AnimeOfflineStatus{}, &p115.HTTPError{Status: 403, Endpoint: animeDownloadTestCookie}
				case "invalid":
					return model.AnimeOfflineStatus{State: "completed"}, nil
				}
				return model.AnimeOfflineStatus{}, errors.New(animeDownloadTestCookie)
			})
			if err := runner.PollOnce(context.Background()); err != nil {
				t.Fatal("upstream text escaped as worker error", err)
			}
			var raw, state string
			var next *int64
			if err := db.QueryRow(`SELECT raw_status_json||block_code,download_state,next_run_at FROM anime_download_jobs`).Scan(&raw, &state, &next); err != nil || strings.Contains(raw, "UID=") || state != "accepted" {
				t.Fatal(raw, state, next, err)
			}
			if mode == "429" || mode == "503" {
				if next == nil || *next < *now+600000 {
					t.Fatal("Retry-After ignored", next)
				}
			}
			if mode == "403" && (next == nil || *next < *now+(5*time.Minute).Milliseconds()) {
				t.Fatal("access denied lost its recovery cooldown")
			}
		})
	}
}

func TestAnimeStatusRunnerRejectsChangedCredentialBeforeAndAfterRead(t *testing.T) {
	for _, when := range []string{"before", "after"} {
		t.Run(when, func(t *testing.T) {
			f, _, job, runner, _ := animeStatusFixture(t)
			replace := func() {
				a, err := f.accounts.Primary(job.AccountID)
				if err != nil {
					t.Fatal(err)
				}
				cookie := "UID=999_B; SEID=replaced"
				if err := f.accounts.Update(a.ID, a.Revision, a.Name, &cookie); err != nil {
					t.Fatal(err)
				}
			}
			reads := 0
			f.service.StatusGateway = animeStatusReaderFunc(func(_ context.Context, cookie, hash string) (model.AnimeOfflineStatus, error) {
				reads++
				if cookie != animeDownloadTestCookie {
					t.Fatal("credential switched")
				}
				replace()
				return serviceAnimeStatus(job, "finished", 100), nil
			})
			if when == "before" {
				replace()
			}
			if err := runner.PollOnce(context.Background()); err != nil {
				t.Fatal(err)
			}
			got, _ := f.service.Store.GetAnimeDownloadJob(context.Background(), job.ID)
			if got.DownloadState != "accepted" || got.BlockCode == nil || *got.BlockCode != "DOWNLOAD_ACCOUNT_CHANGED" || got.NextRunAt != nil || got.Tracking.Observation != nil || when == "before" && reads != 0 || when == "after" && reads != 1 {
				t.Fatal("stale credential response accepted", got, reads)
			}
		})
	}
}

func TestAnimeStatusRunnerSameAccountLoginResumesOldAccountBlock(t *testing.T) {
	f, db, job, runner, now := animeStatusFixture(t)
	ctx := context.Background()
	if _, err := db.Exec(`UPDATE anime_download_jobs SET file_state='needs_review',block_code='DOWNLOAD_ACCOUNT_CHANGED',next_run_at=NULL WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	account, err := f.accounts.Primary(job.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	newCookie := "UID=123_B; SEID=restored-login"
	if err := f.accounts.Update(account.ID, account.Revision, account.Name, &newCookie); err != nil {
		t.Fatal(err)
	}
	reads := 0
	f.service.StatusGateway = animeStatusReaderFunc(func(_ context.Context, cookie, hash string) (model.AnimeOfflineStatus, error) {
		reads++
		if cookie != newCookie || hash != job.Snapshot.InfoHash {
			t.Fatal("recovery did not use this account's refreshed credential")
		}
		return serviceAnimeStatus(job, "running", 20), nil
	})
	*now += 6000
	if err := runner.PollOnce(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := f.service.Store.GetAnimeDownloadJob(ctx, job.ID)
	if err != nil || reads != 1 || got.BlockCode != nil || got.FileState != "pending" || got.DownloadState != "downloading" || got.ClaimID != job.ClaimID {
		t.Fatal("old account-version block did not resume original task", reads, err)
	}
}

func TestAnimeStatusRunnerSharedCoordinatorAndCancellation(t *testing.T) {
	f, db, job, runner, now := animeStatusFixture(t)
	var reads atomic.Int32
	f.service.StatusGateway = animeStatusReaderFunc(func(ctx context.Context, _, _ string) (model.AnimeOfflineStatus, error) {
		reads.Add(1)
		return serviceAnimeStatus(job, "running", 20), nil
	})
	// Another manual/automatic operation for this resource already holds the
	// shared gate: status reads must not run in parallel or issue any writes.
	release, err := f.service.DownloadCoordinator.Acquire(context.Background(), job.UID, "magnet", job.Snapshot.InfoHash)
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.PollOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	release()
	if reads.Load() != 0 {
		t.Fatal("shared gate bypassed")
	}
	if _, err := db.Exec(`UPDATE anime_download_jobs SET next_run_at=NULL`); err != nil {
		t.Fatal(err)
	}
	*now += 5000
	started := make(chan struct{})
	stopped := make(chan struct{})
	f.service.StatusGateway = animeStatusReaderFunc(func(ctx context.Context, _, _ string) (model.AnimeOfflineStatus, error) {
		close(started)
		<-ctx.Done()
		close(stopped)
		return model.AnimeOfflineStatus{}, ctx.Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("reader did not start")
	}
	// Concurrent manual tick is coalesced while Run owns this pass.
	if err := runner.PollOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not stop")
	}
	select {
	case <-stopped:
	default:
		t.Fatal("request outlived runner")
	}
	var token *string
	var code string
	if err := db.QueryRow(`SELECT lease_token,block_code FROM anime_download_jobs`).Scan(&token, &code); err != nil || token != nil || code != "TASK_POLL_CANCELLED" {
		t.Fatal("cancellation lost durable receipt/lease release", token, code, err)
	}
}

func TestAnimeStatusRunnerPausedSourcesStillReadAcceptedTask(t *testing.T) {
	f, db, job, runner, _ := animeStatusFixture(t)
	if _, err := db.Exec(`UPDATE anime_subscriptions SET status='paused',run_generation=run_generation+1`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE anime_subscription_sources SET status='paused',run_generation=run_generation+1`); err != nil {
		t.Fatal(err)
	}
	reads := 0
	f.service.StatusGateway = animeStatusReaderFunc(func(context.Context, string, string) (model.AnimeOfflineStatus, error) {
		reads++
		return serviceAnimeStatus(job, "finished", 100), nil
	})
	if err := runner.PollOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, _ := f.service.Store.GetAnimeDownloadJob(context.Background(), job.ID)
	if reads != 1 || got.DownloadState != "completed" || got.WorkflowState != "active" || got.FileState != "pending" {
		t.Fatal("pause suppressed required read or invented completion", got, reads)
	}
}
