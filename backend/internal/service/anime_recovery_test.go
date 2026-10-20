package service

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
)

func reopenAnimeRecoveryFixture(t *testing.T, f *animeDownloadFixture, db *sql.DB) {
	t.Helper()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.service.Store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := storage.Open(context.Background(), f.path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	accounts, err := OpenP115AccountsSQLite(context.Background(), reopened, f.dir, "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	f.service.Store, f.service.Accounts, f.service.DownloadCoordinator = reopened, accounts, &CloudDownloadCoordinator{}
}

func forbidAnimeRecoveryDirectoryRead(t *testing.T, f *animeDownloadFixture) {
	t.Helper()
	f.service.Directories = animeDownloadDirectoryFunc(func(context.Context, string, string) (p115.DriveDirectory, error) {
		t.Error("normal recovery read directory metadata")
		return p115.DriveDirectory{}, errors.New("unexpected read")
	})
}

func TestAnimeRecoveryConfirmedRenameContinuesNextStageWithoutReplaying(t *testing.T) {
	f, _, _, job, drive := animeOrganizeFixture(t)
	cleanup := animeEnableCleanup(f, drive)
	drive.lost = "rename"
	now := time.Now().UnixMilli()
	runner := &AnimeStatusRunner{Admin: f.service, now: func() time.Time { return time.UnixMilli(now) }}
	ctx := context.Background()
	if err := runner.PollOnce(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := f.service.Store.GetAnimeDownloadJob(ctx, job.ID)
	if err != nil || got.NextRunAt == nil || got.BlockCode == nil || *got.BlockCode != "ORGANIZE_UNCERTAIN" || drive.renames != 1 || drive.moves != 0 || cleanup.deletes != 0 {
		t.Fatal("expected a saved uncertain rename", got, err, drive)
	}
	now = *got.NextRunAt
	drive.lost = ""
	if err := runner.PollOnce(ctx); err != nil {
		t.Fatal(err)
	}
	got, err = f.service.Store.GetAnimeDownloadJob(ctx, job.ID)
	if err != nil || got.BlockCode != nil || got.NextRunAt == nil || *got.NextRunAt != now+5000 || drive.renames != 1 || drive.moves != 0 || cleanup.deletes != 0 || drive.reads != 2 {
		t.Fatal("recovery must be read-only and schedule the next stage promptly", got, err, drive)
	}
	now += 4999
	if err := runner.PollOnce(ctx); err != nil || drive.reads != 2 {
		t.Fatal("next stage bypassed the per-UID interval", err)
	}
	now += 2
	if err := runner.PollOnce(ctx); err != nil {
		t.Fatal(err)
	}
	got, err = f.service.Store.GetAnimeDownloadJob(ctx, job.ID)
	if err != nil || got.WorkflowState != "organized" || drive.renames != 1 || drive.moves != 1 || cleanup.deletes != 1 || drive.reads != 3 {
		t.Fatal("continued stage repeated a rename, added reads, or failed to finish", got, err, drive)
	}
}

func TestAnimeRecoveryRunnerMinimalRequestsUntilFinished(t *testing.T) {
	f, db, _, job := animeSubmitFixture(t)
	if _, err := db.Exec(`UPDATE anime_download_jobs SET directory_cid=NULL`); err != nil {
		t.Fatal(err)
	}
	forbidAnimeRecoveryDirectoryRead(t, f)
	creates, submits, lookups, statuses, pages := 0, 0, 0, 0, 0
	files := normalAnimeFileGateway(t, &pages)
	files.create = func(ctx context.Context, parent, name, cookie string) (string, error) {
		creates++
		if parent != job.ParentCID || name != job.DirectoryName || cookie != animeDownloadTestCookie {
			t.Fatal("unfixed target")
		}
		return "200", nil
	}
	f.service.FileGateway = files
	f.service.SubmitGateway = &animeSubmitGatewayStub{
		inspect: func(context.Context, string, string) (p115.AnimeTaskObservation, error) {
			lookups++
			if submits != 0 {
				t.Error("successful submit immediately queried again")
			}
			return p115.AnimeTaskObservation{}, nil
		},
		submit: func(context.Context, string, string, string) error { submits++; return nil },
	}
	f.service.StatusGateway = animeStatusReaderFunc(func(context.Context, string, string) (model.AnimeOfflineStatus, error) {
		statuses++
		// This endpoint can omit the target on the first read. The successful
		// submission receipt already binds it; files stay scoped to our CID.
		return serviceAnimeStatus(job, "finished", 100), nil
	})
	now := time.Now().UnixMilli()
	r := &AnimeStatusRunner{Admin: f.service, now: func() time.Time { return time.UnixMilli(now) }}
	if err := r.PollOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, _ := f.service.Store.GetAnimeDownloadJob(context.Background(), job.ID)
	if creates != 1 || submits != 1 || lookups != 1 || statuses != 0 || pages != 0 || got.DownloadState != "accepted" || got.NextRunAt == nil || *got.NextRunAt-now < 120000 {
		t.Fatal("excess submit requests", got, creates, submits, lookups, statuses, pages)
	}
	now = *got.NextRunAt - 1
	if err := r.PollOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if statuses != 0 {
		t.Fatal("status read before due")
	}
	now++
	if err := r.PollOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, _ = f.service.Store.GetAnimeDownloadJob(context.Background(), job.ID)
	if statuses != 1 || pages != 2 || got.FileState != "verified" || got.WorkflowState != "original_saved" || got.NextRunAt != nil {
		t.Fatal("completion requests or state", got, statuses, pages)
	}
	// User's subsequent rename/move is deliberately not monitored.
	now += 86400000
	for i := 0; i < 3; i++ {
		if err := r.PollOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if creates != 1 || submits != 1 || lookups != 1 || statuses != 1 || pages != 2 {
		t.Fatal("finished task accessed again")
	}
}

func TestAnimeRecoveryRunnerCompletedRestartSkipsStatus(t *testing.T) {
	f, db, _, job := completedAnimeFilesFixture(t)
	if _, err := db.Exec(`UPDATE anime_download_jobs SET next_run_at=NULL`); err != nil {
		t.Fatal(err)
	}
	reopenAnimeRecoveryFixture(t, f, db)
	forbidAnimeRecoveryDirectoryRead(t, f)
	pages := 0
	f.service.FileGateway = normalAnimeFileGateway(t, &pages)
	f.service.StatusGateway = animeStatusReaderFunc(func(context.Context, string, string) (model.AnimeOfflineStatus, error) {
		t.Error("completed restart reread offline state")
		return model.AnimeOfflineStatus{}, nil
	})
	r := &AnimeStatusRunner{Admin: f.service}
	if err := r.PollOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, _ := f.service.Store.GetAnimeDownloadJob(context.Background(), job.ID)
	if pages != 2 || got.FileState != "verified" {
		t.Fatal("completed restart did not collect files", got, pages)
	}
}

func TestAnimeRecoveryRunnerUnknownAndReceiptFailureNeverResubmit(t *testing.T) {
	for _, mode := range []string{"timeout matched", "timeout missing", "receipt failure"} {
		t.Run(mode, func(t *testing.T) {
			f, db, _, job := animeSubmitFixture(t)
			creates, submits, reads := 0, 0, 0
			f.service.FileGateway = &animeFileGatewayStub{create: func(context.Context, string, string, string) (string, error) {
				creates++
				return "", errors.New("unexpected create")
			}}
			f.service.SubmitGateway = &animeSubmitGatewayStub{
				inspect: func(context.Context, string, string) (p115.AnimeTaskObservation, error) {
					reads++
					if submits > 0 && mode != "timeout missing" {
						return matchedAnimeTask(job), nil
					}
					return p115.AnimeTaskObservation{}, nil
				},
				submit: func(context.Context, string, string, string) error {
					submits++
					if mode != "receipt failure" {
						return context.DeadlineExceeded
					}
					return nil
				},
			}
			f.service.StatusGateway = animeStatusReaderFunc(func(context.Context, string, string) (model.AnimeOfflineStatus, error) {
				t.Error("recovery queried status twice")
				return model.AnimeOfflineStatus{}, nil
			})
			now := time.Now().UnixMilli()
			r := &AnimeStatusRunner{Admin: f.service, now: func() time.Time { return time.UnixMilli(now) }}
			if mode == "receipt failure" {
				if _, err := db.Exec(`CREATE TRIGGER fail_receipt BEFORE UPDATE OF state ON anime_drive_operations WHEN NEW.state='confirmed' BEGIN SELECT RAISE(ABORT,'injected'); END`); err != nil {
					t.Fatal(err)
				}
			}
			err := r.PollOnce(context.Background())
			if (err != nil) != (mode == "receipt failure") || submits != 1 || reads != 1 || creates != 0 {
				t.Fatal("first dispatch budget", submits, reads, creates, err)
			}
			got, _ := f.service.Store.GetAnimeDownloadJob(context.Background(), job.ID)
			if mode == "receipt failure" {
				if got.DownloadState != "submitting" {
					t.Fatal("dispatch record lost", got)
				}
				if _, err := db.Exec(`DROP TRIGGER fail_receipt`); err != nil {
					t.Fatal(err)
				}
				if _, err := db.Exec(`UPDATE anime_download_jobs SET lease_until=1`); err != nil {
					t.Fatal(err)
				}
			} else {
				if got.DownloadState != "unknown" || got.NextRunAt == nil || *got.NextRunAt-now < 300000 {
					t.Fatal("uncertain write not deferred", got)
				}
				now = *got.NextRunAt
			}
			reopenAnimeRecoveryFixture(t, f, db)
			if err := r.PollOnce(context.Background()); err != nil {
				t.Fatal(err)
			}
			got, _ = f.service.Store.GetAnimeDownloadJob(context.Background(), job.ID)
			if reads != 2 || submits != 1 || creates != 0 {
				t.Fatal("restart replayed write or extra read", reads, submits, creates)
			}
			if mode == "timeout missing" {
				if got.FileState != "needs_review" || got.NextRunAt != nil {
					t.Fatal("missing result kept scanning", got)
				}
				now += 86400000
				if err := r.PollOnce(context.Background()); err != nil {
					t.Fatal(err)
				}
				if reads != 2 {
					t.Fatal("missing result repeatedly read")
				}
			} else if got.DownloadState != "accepted" {
				t.Fatal("exact lookup not reconciled", got)
			}
		})
	}
}

func TestAnimeRecoveryRunnerLostDirectoryReceiptReadsParentOnce(t *testing.T) {
	f, db, _, job := animeSubmitFixture(t)
	if _, err := db.Exec(`UPDATE anime_download_jobs SET directory_cid=NULL`); err != nil {
		t.Fatal(err)
	}
	forbidAnimeRecoveryDirectoryRead(t, f)
	creates, pages, submits, reads := 0, 0, 0, 0
	f.service.FileGateway = &animeFileGatewayStub{
		create: func(context.Context, string, string, string) (string, error) {
			creates++
			return "", context.DeadlineExceeded
		},
		page: func(_ context.Context, cid string, _ int, _ string) (p115.AnimeDrivePage, error) {
			pages++
			if cid != job.ParentCID {
				t.Fatal("wrong recovery parent")
			}
			return p115.AnimeDrivePage{CID: cid, Count: 1, Entries: []model.AnimeDriveEntry{animeFileRoot(job)}}, nil
		},
	}
	f.service.SubmitGateway = &animeSubmitGatewayStub{inspect: func(context.Context, string, string) (p115.AnimeTaskObservation, error) {
		reads++
		return p115.AnimeTaskObservation{}, nil
	}, submit: func(context.Context, string, string, string) error { submits++; return nil }}
	f.service.StatusGateway = animeStatusReaderFunc(func(context.Context, string, string) (model.AnimeOfflineStatus, error) {
		t.Error("unexpected status")
		return model.AnimeOfflineStatus{}, nil
	})
	now := time.Now().UnixMilli()
	r := &AnimeStatusRunner{Admin: f.service, now: func() time.Time { return time.UnixMilli(now) }}
	if err := r.PollOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, _ := f.service.Store.GetAnimeDownloadJob(context.Background(), job.ID)
	if creates != 1 || pages != 0 || reads != 0 || submits != 0 || got.NextRunAt == nil {
		t.Fatal("uncertain creation immediately scanned", got)
	}
	now = *got.NextRunAt
	reopenAnimeRecoveryFixture(t, f, db)
	if err := r.PollOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, _ = f.service.Store.GetAnimeDownloadJob(context.Background(), job.ID)
	if creates != 1 || pages != 1 || reads != 1 || submits != 1 || got.DownloadState != "accepted" {
		t.Fatal("directory restart budget", got, creates, pages, reads, submits)
	}
}

func TestAnimeRecoveryRunnerAccountChangeStopsLocally(t *testing.T) {
	f, _, _, job := animeSubmitFixture(t)
	a, err := f.accounts.Primary("")
	if err != nil {
		t.Fatal(err)
	}
	cookie := "UID=999_A; SEID=replacement"
	if err := f.accounts.Update(a.ID, a.Revision, a.Name, &cookie); err != nil {
		t.Fatal(err)
	}
	forbidAnimeRecoveryDirectoryRead(t, f)
	f.service.FileGateway = &animeFileGatewayStub{create: func(context.Context, string, string, string) (string, error) {
		t.Error("changed account used old CID")
		return "", nil
	}}
	f.service.SubmitGateway = &animeSubmitGatewayStub{inspect: func(context.Context, string, string) (p115.AnimeTaskObservation, error) {
		t.Error("changed account queried old task")
		return p115.AnimeTaskObservation{}, nil
	}}
	f.service.StatusGateway = animeStatusReaderFunc(func(context.Context, string, string) (model.AnimeOfflineStatus, error) {
		t.Error("changed account queried status")
		return model.AnimeOfflineStatus{}, nil
	})
	r := &AnimeStatusRunner{Admin: f.service}
	if err := r.PollOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, _ := f.service.Store.GetAnimeDownloadJob(context.Background(), job.ID)
	if got.FileState != "needs_review" || got.BlockCode == nil || *got.BlockCode != "DOWNLOAD_ACCOUNT_CHANGED" || got.NextRunAt != nil {
		t.Fatal("account change not locally stopped", got)
	}
}

func TestAnimeRecoveryRunnerCancellationWaitsAndReleasesLease(t *testing.T) {
	f, db, _, job := animeSubmitFixture(t)
	started, stopped := make(chan struct{}), make(chan struct{})
	f.service.FileGateway = &animeFileGatewayStub{}
	f.service.SubmitGateway = &animeSubmitGatewayStub{
		inspect: func(ctx context.Context, _, _ string) (p115.AnimeTaskObservation, error) {
			close(started)
			<-ctx.Done()
			close(stopped)
			return p115.AnimeTaskObservation{}, ctx.Err()
		},
		submit: func(context.Context, string, string, string) error {
			t.Error("cancelled lookup still submitted")
			return nil
		},
	}
	f.service.StatusGateway = animeStatusReaderFunc(func(context.Context, string, string) (model.AnimeOfflineStatus, error) {
		t.Error("cancelled pass continued")
		return model.AnimeOfflineStatus{}, nil
	})
	r := &AnimeStatusRunner{Admin: f.service}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("recovery did not start")
	}
	if err := r.PollOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("recovery did not stop")
	}
	select {
	case <-stopped:
	default:
		t.Fatal("I/O outlived Run")
	}
	var token *string
	if err := db.QueryRow(`SELECT lease_token FROM anime_download_jobs WHERE id=?`, job.ID).Scan(&token); err != nil || token != nil {
		t.Fatal("lease not released", token, err)
	}
	got, _ := f.service.Store.GetAnimeDownloadJob(context.Background(), job.ID)
	if got.DownloadState != "queued" || got.NextRunAt == nil || got.BlockCode == nil || *got.BlockCode != "TASK_POLL_CANCELLED" {
		t.Fatal("cancellation not saved", got)
	}
}

func TestAnimeRecoveryRunnerExpiredLeaseMakesNoRequest(t *testing.T) {
	f, db, _, _ := animeSubmitFixture(t)
	f.service.FileGateway = &animeFileGatewayStub{}
	f.service.SubmitGateway = &animeSubmitGatewayStub{inspect: func(context.Context, string, string) (p115.AnimeTaskObservation, error) {
		t.Error("old lease reached network")
		return p115.AnimeTaskObservation{}, nil
	}}
	f.service.StatusGateway = animeStatusReaderFunc(func(context.Context, string, string) (model.AnimeOfflineStatus, error) {
		t.Error("old lease read status")
		return model.AnimeOfflineStatus{}, nil
	})
	work, found, err := f.service.Store.ReserveAnimeDownloadWork(context.Background(), time.Now().UnixMilli(), nil)
	if err != nil || !found {
		t.Fatal(found, err)
	}
	if _, err := db.Exec(`UPDATE anime_download_jobs SET lease_until=1`); err != nil {
		t.Fatal(err)
	}
	r := &AnimeStatusRunner{Admin: f.service}
	if err := r.execute(context.Background(), work); err != nil {
		t.Fatal(err)
	}
}

func TestAnimeRecoveryRunnerPreparedPauseStopsBeforeNetwork(t *testing.T) {
	f, _, session, job := animeSubmitFixture(t)
	if _, err := f.service.Store.PrepareAnimeSubmit(context.Background(), job.ID, job.Generation, session.Target()); err != nil {
		t.Fatal(err)
	}
	// Exercise the real control transaction, including lease fencing and the
	// retained queue marker, instead of changing only the parent SQL row.
	serviceControlAnime(t, f.service, job.Snapshot.Source.SubscriptionID, "pause")
	f.service.FileGateway = &animeFileGatewayStub{}
	f.service.SubmitGateway = &animeSubmitGatewayStub{inspect: func(context.Context, string, string) (p115.AnimeTaskObservation, error) {
		t.Error("paused prepared task still queried")
		return p115.AnimeTaskObservation{}, nil
	}}
	f.service.StatusGateway = animeStatusReaderFunc(func(context.Context, string, string) (model.AnimeOfflineStatus, error) {
		t.Error("paused prepared task read status")
		return model.AnimeOfflineStatus{}, nil
	})
	r := &AnimeStatusRunner{Admin: f.service}
	if err := r.PollOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, _ := f.service.Store.GetAnimeDownloadJob(context.Background(), job.ID)
	if got.NextRunAt != nil || got.BlockCode == nil || *got.BlockCode != "DOWNLOAD_PAUSED" || got.FileState != "pending" || got.Generation != job.Generation {
		t.Fatal("paused task kept polling", got)
	}
}
