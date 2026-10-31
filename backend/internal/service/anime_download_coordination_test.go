package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
)

type cloudTaskReaderFunc func(context.Context, string, p115.CloudResource) (p115.CloudTask, bool, error)

func (f cloudTaskReaderFunc) LookupTask(ctx context.Context, cookie string, r p115.CloudResource) (p115.CloudTask, bool, error) {
	return f(ctx, cookie, r)
}

func animeCoordinationFixture(t *testing.T) (*animeDownloadFixture, *sql.DB, *AnimeDownloadSession, model.AnimeDownloadJob) {
	t.Helper()
	f, db, session, source, release, row := animeClaimServiceFixture(t)
	f.service.Torrents = animeClaimTorrentFunc(func(context.Context, string, string, string) ([]byte, error) {
		return animeClaimTestTorrent(), nil
	})
	claim, err := f.service.ClaimDownload(context.Background(), session, source.ID, release.ID, 1, row)
	if err != nil {
		t.Fatal(err)
	}
	f.service.DownloadCoordinator = &CloudDownloadCoordinator{}
	return f, db, session, claim.Job
}

func TestAnimeDownloadLookupNeverDeletesOrSubmits(t *testing.T) {
	f, _, session, job := animeCoordinationFixture(t)
	for _, tc := range []struct {
		name, hash, display string
		found               bool
		readErr, wantErr    error
	}{
		{name: "completed", hash: job.Snapshot.InfoHash, display: "finished", found: true},
		{name: "failed", hash: job.Snapshot.InfoHash, display: "failed", found: true},
		{name: "missing"},
		{name: "wrong hash", hash: strings.Repeat("a", 40), found: true, wantErr: ErrCloudTaskUnclear},
		{name: "upstream error redacted", readErr: fmt.Errorf("private: %s", session.cookie), wantErr: ErrAnimeOfflineRead},
		{name: "deadline", readErr: context.DeadlineExceeded, wantErr: context.DeadlineExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &cloudWorkflowStub{
				lookup: func(ctx context.Context, cookie string, resource p115.CloudResource) (p115.CloudTask, bool, error) {
					if cookie != animeDownloadTestCookie || resource.Hash != job.Snapshot.InfoHash || resource.Protocol != "magnet" {
						t.Fatal("lookup changed credentials or resource")
					}
					if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 25*time.Second {
						t.Fatal("lookup is unbounded")
					}
					return p115.CloudTask{InfoHash: tc.hash, DisplayStatus: tc.display}, tc.found, tc.readErr
				},
				remove: func(context.Context, string, string) error { t.Fatal("automatic lookup deleted task"); return nil },
				add: func(context.Context, string, string, string) (json.RawMessage, error) {
					t.Fatal("automatic lookup submitted task")
					return nil, nil
				},
			}
			f.service.Offline = client
			_, found, err := f.service.LookupDownload(context.Background(), session, job.ID)
			if !errors.Is(err, tc.wantErr) || tc.wantErr == nil && found != tc.found {
				t.Fatal("wrong lookup result", found, err)
			}
			if err != nil && strings.Contains(err.Error(), session.cookie) {
				t.Fatal("lookup leaked credential")
			}
			saved, err := f.service.Store.GetAnimeDownloadJob(context.Background(), job.ID)
			if err != nil || saved.DownloadState != "queued" || saved.Revision != job.Revision {
				t.Fatal("lookup mutated persisted job", err)
			}
			waitCloudResources(t, f.service.DownloadCoordinator, job.UID, 0)
		})
	}
}

func TestAnimeAndManualDownloadUseTheSameResourceGate(t *testing.T) {
	for _, first := range []string{"automatic", "manual", "batch"} {
		t.Run(first, func(t *testing.T) {
			f, _, session, job := animeCoordinationFixture(t)
			entered, finish := make(chan struct{}), make(chan struct{})
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			var reads atomic.Int32
			client := &cloudWorkflowStub{
				lookup: func(ctx context.Context, _ string, _ p115.CloudResource) (p115.CloudTask, bool, error) {
					if reads.Add(1) != 1 {
						return p115.CloudTask{}, false, errors.New("second entry reached upstream")
					}
					close(entered)
					select {
					case <-finish:
						return p115.CloudTask{InfoHash: job.Snapshot.InfoHash, DisplayStatus: "running"}, true, nil
					case <-ctx.Done():
						return p115.CloudTask{}, false, ctx.Err()
					}
				},
			}
			f.service.Offline = client
			manual := &CloudDownloadService{Accounts: f.accounts, Client: client, Coordinator: f.service.DownloadCoordinator}
			config := CloudDownloadConfig{AccountID: job.AccountID, TargetCID: "999", TargetPath: "/手动目录"}
			link := "magnet:?xt=urn:btih:" + job.Snapshot.InfoHash
			done := make(chan error, 1)
			go func() {
				var err error
				switch first {
				case "automatic":
					_, _, err = f.service.LookupDownload(ctx, session, job.ID)
				case "manual":
					_, err = manual.Submit(ctx, config, link)
				case "batch":
					var batch *CloudDownloadBatch
					batch, err = manual.PrepareBatch(config, []string{link})
					if err == nil {
						err = batch.Run(ctx, func(CloudDownloadBatchEvent) error { return nil })
					}
				}
				done <- err
			}()
			defer func() { cancel(); <-done }()
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal("first entry did not reach upstream")
			}
			var err error
			if first == "automatic" {
				_, err = manual.Redo(ctx, config, link)
			} else {
				_, _, err = f.service.LookupDownload(ctx, session, job.ID)
			}
			if !errors.Is(err, ErrCloudTaskBusy) || reads.Load() != 1 {
				t.Fatal("manual/automatic mutual exclusion failed", err, reads.Load())
			}
			close(finish)
		})
	}
}

func TestAnimeDownloadRechecksAfterUIDQueue(t *testing.T) {
	for _, mutation := range []string{"account", "generation", "cancel"} {
		t.Run(mutation, func(t *testing.T) {
			f, db, session, job := animeCoordinationFixture(t)
			var reads atomic.Int32
			f.service.Offline = cloudTaskReaderFunc(func(context.Context, string, p115.CloudResource) (p115.CloudTask, bool, error) {
				reads.Add(1)
				return p115.CloudTask{}, false, nil
			})
			release, err := f.service.DownloadCoordinator.Acquire(context.Background(), job.UID, "bt_v1", strings.Repeat("a", 40))
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			done := make(chan struct{})
			var runErr error
			go func() { defer close(done); _, _, runErr = f.service.LookupDownload(ctx, session, job.ID) }()
			defer func() { cancel(); <-done }()
			waitCloudResources(t, f.service.DownloadCoordinator, job.UID, 2)
			want := storage.ErrAnimeAccountChanged
			switch mutation {
			case "account":
				a, err := f.accounts.Primary(job.AccountID)
				if err != nil {
					t.Fatal(err)
				}
				cookie := "UID=999_B; SEID=changed"
				if err := f.accounts.Update(a.ID, a.Revision, a.Name, &cookie); err != nil {
					t.Fatal(err)
				}
			case "generation":
				if _, err := db.Exec(`UPDATE anime_download_jobs SET generation=generation+1 WHERE id=?`, job.ID); err != nil {
					t.Fatal(err)
				}
				want = storage.ErrAnimeConflict
			case "cancel":
				cancel()
				want = context.Canceled
			}
			release()
			<-done
			if !errors.Is(runErr, want) || reads.Load() != 0 {
				t.Fatal("stale waiting operation reached upstream", runErr, reads.Load())
			}
			waitCloudResources(t, f.service.DownloadCoordinator, job.UID, 0)
		})
	}
}

func TestAnimeDownloadLookupRejectsUnboundGatewayOrTarget(t *testing.T) {
	f, db, session, job := animeCoordinationFixture(t)
	if _, _, err := f.service.LookupDownload(context.Background(), session, job.ID); !errors.Is(err, ErrAnimeOfflineUnavailable) {
		t.Fatal("unbound gateway accepted", err)
	}
	f.service.Offline = cloudTaskReaderFunc(func(context.Context, string, p115.CloudResource) (p115.CloudTask, bool, error) {
		t.Fatal("invalid target reached upstream")
		return p115.CloudTask{}, false, nil
	})
	savedCoordinator := f.service.DownloadCoordinator
	f.service.DownloadCoordinator = nil
	if _, _, err := f.service.LookupDownload(context.Background(), session, job.ID); !errors.Is(err, ErrAnimeOfflineUnavailable) {
		t.Fatal("automatic service created its own fallback gate", err)
	}
	f.service.DownloadCoordinator = savedCoordinator
	if _, _, err := f.service.LookupDownload(context.Background(), nil, job.ID); !errors.Is(err, storage.ErrAnimeInvalid) {
		t.Fatal("nil credential session accepted", err)
	}
	if _, err := db.Exec(`UPDATE anime_download_jobs SET parent_cid='999' WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.service.LookupDownload(context.Background(), session, job.ID); !errors.Is(err, storage.ErrAnimeResourceTarget) {
		t.Fatal("conflicting job target accepted", err)
	}
}

func TestAnimeDownloadLookupPinsTargetAndRechecksCredentialAfterRead(t *testing.T) {
	for _, mode := range []string{"settings changed", "credential changed during read"} {
		t.Run(mode, func(t *testing.T) {
			f, _, session, job := animeCoordinationFixture(t)
			if mode == "settings changed" {
				f.saveSettings(t, "200")
			}
			f.service.Offline = cloudTaskReaderFunc(func(_ context.Context, cookie string, r p115.CloudResource) (p115.CloudTask, bool, error) {
				if cookie != animeDownloadTestCookie || r.Hash != job.Snapshot.InfoHash || session.Target().ParentCID != "100" {
					t.Fatal("lookup followed current defaults instead of saved target")
				}
				if mode == "credential changed during read" {
					a, err := f.accounts.Primary(job.AccountID)
					if err != nil {
						t.Fatal(err)
					}
					replacement := "UID=789_B; SEID=replaced-account"
					if err := f.accounts.Update(a.ID, a.Revision, a.Name, &replacement); err != nil {
						t.Fatal(err)
					}
				}
				return p115.CloudTask{InfoHash: r.Hash, DisplayStatus: "finished"}, true, nil
			})
			_, found, err := f.service.LookupDownload(context.Background(), session, job.ID)
			if mode == "settings changed" {
				if err != nil || !found {
					t.Fatal("old target was retargeted or rejected after defaults changed", err)
				}
			} else if !errors.Is(err, storage.ErrAnimeAccountChanged) || found {
				t.Fatal("stale credential result accepted", found, err)
			}
			waitCloudResources(t, f.service.DownloadCoordinator, job.UID, 0)
		})
	}
}
