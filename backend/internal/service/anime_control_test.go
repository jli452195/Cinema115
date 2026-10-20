package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/animeparse"
	"strm115/backend/internal/animepreview"
	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
)

func serviceControlAnime(t *testing.T, s *AnimeAdminService, id, action string) model.AnimeControlReceipt {
	t.Helper()
	sub, err := s.Store.GetAnimeSubscription(context.Background(), id, true)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := s.ControlSubscription(context.Background(), "admin", action+"_request_"+strconv.FormatInt(sub.Revision, 10), id, action, AnimeControlInput{ExpectedRevision: sub.Revision})
	if err != nil {
		t.Fatal(action, err)
	}
	return receipt
}

func seedAnimeControlBaseline(t *testing.T, db *sql.DB, target model.AnimeDownloadTarget) {
	t.Helper()
	raw, err := json.Marshal(model.AnimeExecutionBaseline{SchemaVersion: 1, Target: target, RuleVersion: animepreview.RuleVersion, ParserVersion: animeparse.ParserVersion, TimeVersion: animeparse.TimeVersion})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE anime_source_baselines SET snapshot_json=?`, string(raw)); err != nil {
		t.Fatal(err)
	}
}

func TestAnimeControlRunnerPauseResumeWithoutNewJobOrSubmit(t *testing.T) {
	f, db, _, job := animeSubmitFixture(t)
	ctx := context.Background()
	seedAnimeControlBaseline(t, db, job.Snapshot.Target)
	lookups, submits := 0, 0
	f.service.SubmitGateway = &animeSubmitGatewayStub{
		inspect: func(context.Context, string, string) (p115.AnimeTaskObservation, error) {
			lookups++
			if lookups == 1 {
				serviceControlAnime(t, f.service, job.Snapshot.Source.SubscriptionID, "pause")
			}
			return p115.AnimeTaskObservation{}, nil
		},
		submit: func(context.Context, string, string, string) error { submits++; return nil },
	}
	now := time.Now().UnixMilli()
	runner := newAnimeControlRunner(t, f, &now)
	if err := runner.PollOnce(ctx); err != nil {
		t.Fatal(err)
	}
	paused, _ := f.service.Store.GetAnimeDownloadJob(ctx, job.ID)
	if submits != 0 || lookups != 1 || paused.FileState != "pending" || paused.BlockCode == nil || *paused.BlockCode != "DOWNLOAD_PAUSED" {
		t.Fatal("pause crossed write boundary", paused, lookups, submits)
	}
	if err := runner.PollOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if lookups != 1 {
		t.Fatal("paused task made upstream requests")
	}
	serviceControlAnime(t, f.service, job.Snapshot.Source.SubscriptionID, "resume")
	now = time.Now().UnixMilli() + 1
	if err := runner.PollOnce(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ := f.service.Store.GetAnimeDownloadJob(ctx, job.ID)
	if submits != 1 || lookups != 2 || got.DownloadState != "accepted" || got.Generation != job.Generation || !reflect.DeepEqual(got.Snapshot, job.Snapshot) {
		t.Fatal("resume duplicated intent", got, lookups, submits)
	}
	page, err := f.service.SubscriptionJobs(ctx, job.Snapshot.Source.SubscriptionID, 1, 20)
	if err != nil || page.Total != 1 {
		t.Fatal(page, err)
	}
}

func TestAnimeControlRunnerInFlightSubmitReceiptOrUnknown(t *testing.T) {
	for _, uncertain := range []bool{false, true} {
		t.Run(strconv.FormatBool(uncertain), func(t *testing.T) {
			f, _, _, job := animeSubmitFixture(t)
			ctx := context.Background()
			lookups, submits := 0, 0
			f.service.SubmitGateway = &animeSubmitGatewayStub{
				inspect: func(context.Context, string, string) (p115.AnimeTaskObservation, error) {
					lookups++
					if lookups == 1 {
						return p115.AnimeTaskObservation{}, nil
					}
					return matchedAnimeTask(job), nil
				},
				submit: func(context.Context, string, string, string) error {
					submits++
					serviceControlAnime(t, f.service, job.Snapshot.Source.SubscriptionID, "remove")
					if uncertain {
						return context.DeadlineExceeded
					}
					return nil
				},
			}
			now := time.Now().UnixMilli()
			runner := newAnimeControlRunner(t, f, &now)
			statuses := 0
			f.service.StatusGateway = animeStatusReaderFunc(func(context.Context, string, string) (model.AnimeOfflineStatus, error) {
				statuses++
				return serviceAnimeStatus(job, "pending", 0), nil
			})
			if err := runner.PollOnce(ctx); err != nil {
				t.Fatal(err)
			}
			got, _ := f.service.Store.GetAnimeDownloadJob(ctx, job.ID)
			want := "accepted"
			if uncertain {
				want = "unknown"
			}
			if got.DownloadState != want || lookups != 1 || submits != 1 || got.NextRunAt == nil {
				t.Fatal("lost receipt or immediate extra query", got, lookups, submits)
			}
			if uncertain {
				now = *got.NextRunAt
				if err := runner.PollOnce(ctx); err != nil {
					t.Fatal(err)
				}
				got, _ = f.service.Store.GetAnimeDownloadJob(ctx, job.ID)
				if got.DownloadState != "accepted" || lookups != 2 || submits != 1 {
					t.Fatal("unknown write replayed", got, lookups, submits)
				}
			}
		})
	}
}

func TestAnimeControlRunnerUnknownDirectoryReadThenCancel(t *testing.T) {
	f, db, _, job := animeSubmitFixture(t)
	ctx := context.Background()
	if _, err := db.Exec(`UPDATE anime_download_jobs SET directory_cid=NULL`); err != nil {
		t.Fatal(err)
	}
	creates, pages := 0, 0
	f.service.FileGateway = &animeFileGatewayStub{
		create: func(context.Context, string, string, string) (string, error) {
			creates++
			serviceControlAnime(t, f.service, job.Snapshot.Source.SubscriptionID, "remove")
			return "", context.DeadlineExceeded
		},
		page: func(_ context.Context, cid string, offset int, cookie string) (p115.AnimeDrivePage, error) {
			pages++
			if cid != job.ParentCID || offset != 0 {
				t.Fatal("unscoped recovery")
			}
			return p115.AnimeDrivePage{CID: cid, Offset: 0, Count: 1, Entries: []model.AnimeDriveEntry{animeFileRoot(job)}}, nil
		},
	}
	f.service.SubmitGateway = &animeSubmitGatewayStub{inspect: func(context.Context, string, string) (p115.AnimeTaskObservation, error) {
		t.Fatal("removed task inspected for a new submit")
		return p115.AnimeTaskObservation{}, nil
	}, submit: func(context.Context, string, string, string) error { t.Fatal("removed task submitted"); return nil }}
	now := time.Now().UnixMilli()
	runner := newAnimeControlRunner(t, f, &now)
	if err := runner.PollOnce(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ := f.service.Store.GetAnimeDownloadJob(ctx, job.ID)
	if got.NextRunAt == nil || got.WorkflowState != "active" || creates != 1 || pages != 0 {
		t.Fatal("unknown directory discarded", got, creates, pages)
	}
	now = *got.NextRunAt
	if err := runner.PollOnce(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ = f.service.Store.GetAnimeDownloadJob(ctx, job.ID)
	if got.WorkflowState != "cancelled" || got.DirectoryCID == nil || creates != 1 || pages != 1 {
		t.Fatal("directory not resolved once", got, creates, pages)
	}
	now += 86400000
	if err := runner.PollOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if creates != 1 || pages != 1 {
		t.Fatal("cancelled task kept polling")
	}
}

func TestAnimeControlRunnerStopsAfterReadingFilesForStoppedSubscription(t *testing.T) {
	for _, action := range []string{"pause", "archive", "remove"} {
		t.Run(action, func(t *testing.T) {
			f, _, job, runner, now := animeStatusFixture(t)
			ctx := context.Background()
			pages, statuses := 0, 0
			forbidAnimeRecoveryDirectoryRead(t, f)
			f.service.FileGateway = normalAnimeFileGateway(t, &pages)
			f.service.StatusGateway = animeStatusReaderFunc(func(context.Context, string, string) (model.AnimeOfflineStatus, error) {
				statuses++
				return serviceAnimeStatus(job, "finished", 100), nil
			})
			serviceControlAnime(t, f.service, job.Snapshot.Source.SubscriptionID, action)
			if err := runner.PollOnce(ctx); err != nil {
				t.Fatal(err)
			}
			got, _ := f.service.DownloadJob(ctx, job.ID)
			if got.WorkflowState != "original_saved" || got.FileState != "verified" || statuses != 1 || pages != 2 || got.NextRunAt != nil {
				t.Fatal("read tracking did not finish", got, statuses, pages)
			}
			*now += 86400000
			if err := runner.PollOnce(ctx); err != nil {
				t.Fatal(err)
			}
			if statuses != 1 || pages != 2 {
				t.Fatal("finished job still polled")
			}
			if action == "remove" {
				page, err := f.service.SubscriptionJobs(ctx, job.Snapshot.Source.SubscriptionID, 1, 20)
				if err != nil || page.Total != 1 || page.Items[0].WorkflowState != "original_saved" {
					t.Fatal("removed job hidden", page, err)
				}
			}
		})
	}
}

func TestAnimeControlServiceLocalOnlyAndIdempotency(t *testing.T) {
	f, db, _, job := animeSubmitFixture(t)
	ctx := context.Background()
	seedAnimeControlBaseline(t, db, job.Snapshot.Target)
	forbidAnimeRecoveryDirectoryRead(t, f)
	sub, _ := f.service.Store.GetAnimeSubscription(ctx, job.Snapshot.Source.SubscriptionID, true)
	in := AnimeControlInput{ExpectedRevision: sub.Revision}
	first, err := f.service.ControlSubscription(ctx, "admin", "pause_replay", sub.ID, "pause", in)
	if err != nil {
		t.Fatal(err)
	}
	reopenAnimeRecoveryFixture(t, f, db)
	again, err := f.service.ControlSubscription(ctx, "admin", "pause_replay", sub.ID, "pause", in)
	if err != nil || !reflect.DeepEqual(first, again) {
		t.Fatal(again, err)
	}
	in.ExpectedRevision++
	if _, err := f.service.ControlSubscription(ctx, "admin", "pause_replay", sub.ID, "pause", in); err == nil {
		t.Fatal("changed request reused key")
	}
	for _, action := range []string{"resume", "archive", "unarchive", "remove"} {
		serviceControlAnime(t, f.service, sub.ID, action)
	}
	got, err := f.service.DownloadJob(ctx, job.ID)
	if err != nil || got.WorkflowState != "cancelled" {
		t.Fatal(got, err)
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), animeDownloadTestCookie) {
		t.Fatal("job response leaked cookie")
	}
	page, err := f.service.SubscriptionJobs(ctx, sub.ID, 1, 20)
	if err != nil || page.Total != 1 {
		t.Fatal(page, err)
	}
	if _, err := f.service.ControlSubscription(ctx, "admin", "invalid_action", sub.ID, "enable", in); err == nil {
		t.Fatal("control bypassed first enable")
	}
}

func newAnimeControlRunner(t *testing.T, f *animeDownloadFixture, now *int64) *AnimeStatusRunner {
	t.Helper()
	if f.service.FileGateway == nil {
		f.service.FileGateway = &animeFileGatewayStub{create: func(context.Context, string, string, string) (string, error) {
			t.Error("unexpected directory create")
			return "", context.Canceled
		}, page: func(context.Context, string, int, string) (p115.AnimeDrivePage, error) {
			t.Error("unexpected directory listing")
			return p115.AnimeDrivePage{}, context.Canceled
		}}
	}
	f.service.StatusGateway = animeStatusReaderFunc(func(context.Context, string, string) (model.AnimeOfflineStatus, error) {
		t.Error("unexpected status request")
		return model.AnimeOfflineStatus{}, context.Canceled
	})
	return &AnimeStatusRunner{Admin: f.service, now: func() time.Time { return time.UnixMilli(*now) }}
}
