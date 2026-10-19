package service

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
)

type animeSubmitGatewayStub struct {
	inspect func(context.Context, string, string) (p115.AnimeTaskObservation, error)
	submit  func(context.Context, string, string, string) error
}

func (f *animeSubmitGatewayStub) InspectAnimeTask(ctx context.Context, cookie, hash string) (p115.AnimeTaskObservation, error) {
	return f.inspect(ctx, cookie, hash)
}
func (f *animeSubmitGatewayStub) SubmitAnimeTask(ctx context.Context, cookie, hash, cid string) error {
	return f.submit(ctx, cookie, hash, cid)
}

func animeSubmitFixture(t *testing.T) (*animeDownloadFixture, *sql.DB, *AnimeDownloadSession, model.AnimeDownloadJob) {
	t.Helper()
	f, db, session, job := animeCoordinationFixture(t)
	if _, err := db.Exec(`UPDATE anime_download_jobs SET directory_cid='200' WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	job.DirectoryCID = bindingPtr("200")
	dirs := animeDownloadDirectoryFunc(func(_ context.Context, cid, cookie string) (p115.DriveDirectory, error) {
		if cookie != animeDownloadTestCookie {
			return p115.DriveDirectory{}, errors.New("unexpected credential")
		}
		if cid == "100" {
			return p115.DriveDirectory{CID: cid, Path: "/动漫/100"}, nil
		}
		if cid == "200" {
			return p115.DriveDirectory{CID: cid, ParentCID: "100", Name: job.DirectoryName, Path: "/动漫/100/" + job.DirectoryName}, nil
		}
		if cid == "300" {
			return p115.DriveDirectory{CID: cid, ParentCID: "200", Name: "resource", Path: "/动漫/100/" + job.DirectoryName + "/resource"}, nil
		}
		return p115.DriveDirectory{}, errors.New("not found")
	})
	f.service.Directories, session.directories = dirs, dirs
	return f, db, session, job
}

func matchedAnimeTask(job model.AnimeDownloadJob) p115.AnimeTaskObservation {
	return p115.AnimeTaskObservation{Present: true, InfoHash: job.Snapshot.InfoHash, TargetCID: "200", ResultID: "300"}
}

func TestAnimeEnsureSubmitOnceUsesReceiptWithoutReadBack(t *testing.T) {
	for _, relation := range []string{"target", "result directory", "target result"} {
		t.Run(relation, func(t *testing.T) {
			f, db, session, job := animeSubmitFixture(t)
			reads, writes := 0, 0
			f.service.SubmitGateway = &animeSubmitGatewayStub{
				inspect: func(_ context.Context, cookie, hash string) (p115.AnimeTaskObservation, error) {
					reads++
					if cookie != animeDownloadTestCookie || hash != job.Snapshot.InfoHash {
						t.Fatal("wrong lookup identity")
					}
					if writes == 0 {
						return p115.AnimeTaskObservation{}, nil
					}
					out := matchedAnimeTask(job)
					if relation != "target" {
						out.TargetCID = ""
					}
					if relation == "target result" {
						out.ResultID = "200"
					}
					return out, nil
				},
				submit: func(_ context.Context, cookie, hash, cid string) error {
					writes++
					if cookie != animeDownloadTestCookie || hash != job.Snapshot.InfoHash || cid != "200" {
						t.Fatal("wrong pinned submit identity")
					}
					var state, download string
					if err := db.QueryRow(`SELECT o.state,j.download_state FROM anime_drive_operations o JOIN anime_download_jobs j ON j.id=o.download_job_id WHERE j.id=? AND o.authorized_at IS NOT NULL`, job.ID).Scan(&state, &download); err != nil || state != "dispatched" || download != "submitting" {
						t.Fatal("write occurred before dispatch commit", state, download, err)
					}
					return nil
				},
			}
			for i := 0; i < 3; i++ {
				out, err := f.service.EnsureDownload(context.Background(), session, job.ID)
				if err != nil || out.Operation.State != "confirmed" || out.Job.DownloadState != "accepted" || out.Job.FileState != "pending" || out.Job.WorkflowState != "active" {
					t.Fatal("wrong acceptance/completion result", out, err)
				}
			}
			if writes != 1 || reads != 1 {
				t.Fatal("repeated submit or needless query", reads, writes)
			}
			var raw string
			if err := db.QueryRow(`SELECT request_json||COALESCE(result_json,'') FROM anime_drive_operations`).Scan(&raw); err != nil || strings.Contains(raw, "UID=") || strings.Contains(raw, "magnet:") {
				t.Fatal("unsafe operation snapshot", raw, err)
			}
		})
	}
}

func TestAnimeEnsureUnknownOnlyReconciles(t *testing.T) {
	for _, mode := range []string{"timeout", "cancel", "rejected", "wrong directory", "unreadable", "missing relation", "account changed"} {
		t.Run(mode, func(t *testing.T) {
			f, _, session, job := animeSubmitFixture(t)
			writes, reads := 0, 0
			matched := false
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f.service.SubmitGateway = &animeSubmitGatewayStub{
				inspect: func(context.Context, string, string) (p115.AnimeTaskObservation, error) {
					reads++
					if matched {
						return matchedAnimeTask(job), nil
					}
					if writes > 0 {
						switch mode {
						case "wrong directory":
							o := matchedAnimeTask(job)
							o.TargetCID = "999"
							return o, nil
						case "unreadable":
							return p115.AnimeTaskObservation{}, errors.New("private " + animeDownloadTestCookie)
						case "missing relation":
							return p115.AnimeTaskObservation{Present: true, InfoHash: job.Snapshot.InfoHash}, nil
						}
					}
					return p115.AnimeTaskObservation{}, nil
				},
				submit: func(context.Context, string, string, string) error {
					writes++
					switch mode {
					case "cancel":
						cancel()
						return context.Canceled
					case "timeout", "wrong directory", "unreadable", "missing relation":
						return context.DeadlineExceeded
					case "rejected":
						return p115.ErrAnimeSubmitRejected
					case "account changed":
						a, err := f.accounts.Primary(job.AccountID)
						if err != nil {
							t.Fatal(err)
						}
						cookie := "UID=123_B; SEID=replaced"
						if err := f.accounts.Update(a.ID, a.Revision, a.Name, &cookie); err != nil {
							t.Fatal(err)
						}
					}
					return nil
				},
			}
			out, err := f.service.EnsureDownload(ctx, session, job.ID)
			want := "unknown"
			if mode == "rejected" {
				want = "failed"
			}
			if err != nil || out.Operation.State != want || out.Job.DownloadState != want || writes != 1 || reads != 1 {
				t.Fatal("uncertain request lost", out, err)
			}
			if mode == "account changed" {
				before := reads
				if _, err := f.service.EnsureDownload(context.Background(), session, job.ID); !errors.Is(err, storage.ErrAnimeAccountChanged) || reads != before || writes != 1 {
					t.Fatal("changed credential resumed", err)
				}
				return
			}
			for i := 0; i < 2; i++ {
				if _, err := f.service.EnsureDownload(context.Background(), session, job.ID); err != nil {
					t.Fatal(err)
				}
			}
			if writes != 1 {
				t.Fatal("unknown/failed request resubmitted")
			}
			matched = true
			out, err = f.service.EnsureDownload(context.Background(), session, job.ID)
			if err != nil || writes != 1 {
				t.Fatal("reconciliation resubmitted", err)
			}
			if want == "unknown" && out.Job.DownloadState != "accepted" {
				t.Fatal("eventual task not reconciled", out)
			}
		})
	}
}

func TestAnimeEnsurePreexistingTaskIsNeverAdopted(t *testing.T) {
	f, _, session, job := animeSubmitFixture(t)
	f.service.SubmitGateway = &animeSubmitGatewayStub{
		inspect: func(context.Context, string, string) (p115.AnimeTaskObservation, error) {
			return matchedAnimeTask(job), nil
		},
		submit: func(context.Context, string, string, string) error {
			t.Fatal("preexisting task resubmitted")
			return nil
		},
	}
	for i := 0; i < 2; i++ {
		out, err := f.service.EnsureDownload(context.Background(), session, job.ID)
		if err != nil || out.Job.DownloadState != "blocked" || out.Operation.State != "failed" || out.Operation.AuthorizedAt != nil {
			t.Fatal("external task was adopted", out, err)
		}
	}
}

func TestAnimeEnsureSubmissionFailureWindows(t *testing.T) {
	for _, mode := range []string{"prepare failure", "dispatch failure", "receipt failure", "lookup failure"} {
		t.Run(mode, func(t *testing.T) {
			f, db, session, job := animeSubmitFixture(t)
			writes := 0
			f.service.SubmitGateway = &animeSubmitGatewayStub{
				inspect: func(context.Context, string, string) (p115.AnimeTaskObservation, error) {
					if mode == "lookup failure" {
						return p115.AnimeTaskObservation{}, errors.New("private " + animeDownloadTestCookie)
					}
					if writes > 0 {
						return matchedAnimeTask(job), nil
					}
					return p115.AnimeTaskObservation{}, nil
				},
				submit: func(context.Context, string, string, string) error { writes++; return nil },
			}
			statement := ""
			switch mode {
			case "prepare failure":
				statement = `CREATE TRIGGER fail_submit BEFORE INSERT ON anime_drive_operations BEGIN SELECT RAISE(ABORT,'injected'); END`
			case "dispatch failure":
				statement = `CREATE TRIGGER fail_submit BEFORE UPDATE OF download_state ON anime_download_jobs WHEN NEW.download_state='submitting' BEGIN SELECT RAISE(ABORT,'injected'); END`
			case "receipt failure":
				statement = `CREATE TRIGGER fail_submit BEFORE UPDATE OF state ON anime_drive_operations WHEN NEW.state='confirmed' BEGIN SELECT RAISE(ABORT,'injected'); END`
			}
			if statement != "" {
				if _, err := db.Exec(statement); err != nil {
					t.Fatal(err)
				}
			}
			out, err := f.service.EnsureDownload(context.Background(), session, job.ID)
			if err == nil {
				t.Fatal("failure was hidden", out)
			}
			if mode == "receipt failure" {
				if writes != 1 || out.Operation.State != "dispatched" || out.Job.DownloadState != "submitting" {
					t.Fatal("receipt failure lost dispatched intent", out, writes)
				}
			} else if writes != 0 {
				t.Fatal("failed precondition still wrote")
			}
			if mode == "lookup failure" {
				if !errors.Is(err, ErrAnimeOfflineRead) || strings.Contains(err.Error(), "UID=") {
					t.Fatal("unsafe lookup error", err)
				}
				return
			}
			if _, err := db.Exec(`DROP TRIGGER fail_submit`); err != nil {
				t.Fatal(err)
			}
			out, err = f.service.EnsureDownload(context.Background(), session, job.ID)
			if err != nil || out.Job.DownloadState != "accepted" || writes != 1 {
				t.Fatal("recovery duplicated or lost submission", out, writes, err)
			}
		})
	}
}

func TestAnimeEnsureRevalidatesAuthorizationBeforeDispatch(t *testing.T) {
	for _, mode := range []string{"parent paused", "source paused", "parent generation", "source generation", "source revision", "withdrawn", "dismissed", "account revision", "target changed"} {
		t.Run(mode, func(t *testing.T) {
			f, db, session, job := animeSubmitFixture(t)
			f.service.SubmitGateway = &animeSubmitGatewayStub{
				inspect: func(context.Context, string, string) (p115.AnimeTaskObservation, error) {
					statement := ""
					switch mode {
					case "parent paused":
						statement = `UPDATE anime_subscriptions SET status='paused'`
					case "source paused":
						statement = `UPDATE anime_subscription_sources SET status='paused'`
					case "parent generation":
						statement = `UPDATE anime_subscriptions SET run_generation=run_generation+1`
					case "source generation":
						statement = `UPDATE anime_subscription_sources SET run_generation=run_generation+1`
					case "source revision":
						statement = `UPDATE anime_subscription_sources SET revision=revision+1`
					case "withdrawn":
						statement = `UPDATE anime_job_selections SET status='withdrawn'`
					case "dismissed":
						statement = `UPDATE anime_release_decisions SET disposition='dismissed'`
					case "account revision":
						a, err := f.accounts.Primary(job.AccountID)
						if err != nil {
							t.Fatal(err)
						}
						cookie := "UID=123_B"
						if err := f.accounts.Update(a.ID, a.Revision, a.Name, &cookie); err != nil {
							t.Fatal(err)
						}
					case "target changed":
						statement = `UPDATE anime_download_jobs SET directory_cid='999'`
					}
					if statement != "" {
						if _, err := db.Exec(statement); err != nil {
							t.Fatal(err)
						}
					}
					return p115.AnimeTaskObservation{}, nil
				},
				submit: func(context.Context, string, string, string) error {
					t.Fatal("withdrawn/stale intent dispatched")
					return nil
				},
			}
			if _, err := f.service.EnsureDownload(context.Background(), session, job.ID); err == nil {
				t.Fatal("stale authorization accepted")
			}
			op, err := f.service.Store.GetAnimeSubmitOperation(context.Background(), job.ID)
			if err != nil || op.State != "prepared" || op.AuthorizedAt != nil {
				t.Fatal("failed validation left dispatch permission", op, err)
			}
		})
	}
}

func TestAnimeEnsureResourceDirectoryRequired(t *testing.T) {
	for _, mode := range []string{"unprepared", "wrong parent", "wrong name"} {
		t.Run(mode, func(t *testing.T) {
			f, db, session, job := animeSubmitFixture(t)
			f.service.SubmitGateway = &animeSubmitGatewayStub{
				inspect: func(context.Context, string, string) (p115.AnimeTaskObservation, error) {
					if mode == "unprepared" {
						t.Fatal("unprepared job queried upstream")
					}
					return p115.AnimeTaskObservation{}, nil
				},
				submit: func(context.Context, string, string, string) error { t.Fatal("unverified directory used"); return nil },
			}
			if mode == "unprepared" {
				if _, err := db.Exec(`UPDATE anime_download_jobs SET directory_cid=NULL`); err != nil {
					t.Fatal(err)
				}
			} else {
				session.directories = animeDownloadDirectoryFunc(func(context.Context, string, string) (p115.DriveDirectory, error) {
					d := p115.DriveDirectory{CID: "200", ParentCID: "100", Name: job.DirectoryName}
					if mode == "wrong parent" {
						d.ParentCID = "999"
					} else {
						d.Name = "other"
					}
					return d, nil
				})
			}
			if _, err := f.service.EnsureDownload(context.Background(), session, job.ID); err == nil {
				t.Fatal("missing/unverified directory accepted")
			}
		})
	}
}

func TestAnimeEnsureRecordsFactsAfterPauseButFencesNewGeneration(t *testing.T) {
	for _, mode := range []string{"pause", "generation"} {
		t.Run(mode, func(t *testing.T) {
			f, db, session, job := animeSubmitFixture(t)
			writes := 0
			f.service.SubmitGateway = &animeSubmitGatewayStub{
				inspect: func(context.Context, string, string) (p115.AnimeTaskObservation, error) {
					if writes > 0 {
						return matchedAnimeTask(job), nil
					}
					return p115.AnimeTaskObservation{}, nil
				},
				submit: func(context.Context, string, string, string) error {
					writes++
					statement := `UPDATE anime_subscriptions SET status='paused'`
					if mode == "generation" {
						statement = `UPDATE anime_download_jobs SET generation=generation+1`
					}
					if _, err := db.Exec(statement); err != nil {
						t.Fatal(err)
					}
					return nil
				},
			}
			out, err := f.service.EnsureDownload(context.Background(), session, job.ID)
			if err != nil || writes != 1 {
				t.Fatal(out, err)
			}
			if mode == "pause" {
				if out.Job.DownloadState != "accepted" {
					t.Fatal("pause lost already dispatched fact", out)
				}
			} else if out.Job.DownloadState != "submitting" || out.Operation.State != "unknown" || out.Operation.ErrorCode == nil || *out.Operation.ErrorCode != "STALE_GENERATION" {
				t.Fatal("old executor advanced new generation", out)
			}
		})
	}
}

func TestAnimeEnsureLostReceiptStillConfirmsExactTask(t *testing.T) {
	f, _, session, job := animeSubmitFixture(t)
	writes := 0
	f.service.SubmitGateway = &animeSubmitGatewayStub{
		inspect: func(context.Context, string, string) (p115.AnimeTaskObservation, error) {
			if writes > 0 {
				return matchedAnimeTask(job), nil
			}
			return p115.AnimeTaskObservation{}, nil
		},
		submit: func(context.Context, string, string, string) error { writes++; return context.DeadlineExceeded },
	}
	out, err := f.service.EnsureDownload(context.Background(), session, job.ID)
	if err != nil || out.Operation.State != "unknown" || writes != 1 {
		t.Fatal("uncertain receipt not saved", out, err)
	}
	out, err = f.service.EnsureDownload(context.Background(), session, job.ID)
	if err != nil || out.Operation.State != "confirmed" || out.Job.DownloadState != "accepted" || writes != 1 {
		t.Fatal("lost receipt was replayed or acceptance lost", out, writes, err)
	}
}

func TestAnimeEnsurePersistentRestartBoundaries(t *testing.T) {
	for _, phase := range []string{"prepared", "dispatched", "unknown", "confirmed"} {
		t.Run(phase, func(t *testing.T) {
			f, db, session, job := animeSubmitFixture(t)
			ctx := context.Background()
			writes, reads := 0, 0
			visible := false
			f.service.SubmitGateway = &animeSubmitGatewayStub{
				inspect: func(context.Context, string, string) (p115.AnimeTaskObservation, error) {
					reads++
					if visible {
						return matchedAnimeTask(job), nil
					}
					return p115.AnimeTaskObservation{}, nil
				},
				submit: func(context.Context, string, string, string) error { writes++; visible = true; return nil },
			}
			if phase == "confirmed" {
				if _, err := f.service.EnsureDownload(ctx, session, job.ID); err != nil {
					t.Fatal(err)
				}
			} else {
				op, err := f.service.Store.PrepareAnimeSubmit(ctx, job.ID, job.Generation, session.Target())
				if err != nil {
					t.Fatal(err)
				}
				if phase != "prepared" {
					if err := f.service.Store.DispatchAnimeSubmit(ctx, op.ID); err != nil {
						t.Fatal(err)
					}
				}
				if phase == "unknown" {
					if err := f.service.Store.RecordAnimeSubmitOutcome(ctx, op.ID, "unknown", "TASK_MISSING", model.AnimeSubmitObservation{SchemaVersion: 1, Kind: "missing"}); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			if err := f.service.Store.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := storage.Open(ctx, f.path)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			accounts, err := OpenP115AccountsSQLite(ctx, reopened, f.dir, "test-secret")
			if err != nil {
				t.Fatal(err)
			}
			f.service.Store, f.service.Accounts, f.service.DownloadCoordinator = reopened, accounts, &CloudDownloadCoordinator{}
			session, err = f.service.ResumeDownloadTarget(ctx, job.Snapshot.Target)
			if err != nil {
				t.Fatal(err)
			}
			beforeReads := reads
			out, err := f.service.EnsureDownload(ctx, session, job.ID)
			if err != nil {
				t.Fatal(err)
			}
			if phase == "prepared" || phase == "confirmed" {
				if writes != 1 || out.Job.DownloadState != "accepted" {
					t.Fatal("known safe restart lost task", out, writes)
				}
				if phase == "confirmed" && reads != beforeReads {
					t.Fatal("confirmed task queried on ensure after restart")
				}
			} else {
				if writes != 0 || out.Job.DownloadState != "unknown" {
					t.Fatal("dispatched/unknown request replayed after restart", out, writes)
				}
				visible = true
				out, err = f.service.EnsureDownload(ctx, session, job.ID)
				if err != nil || writes != 0 || out.Job.DownloadState != "accepted" {
					t.Fatal("restart reconciliation did not preserve dispatch", out, writes, err)
				}
			}
		})
	}
}
