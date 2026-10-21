package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/client/animefeed"
	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
)

// This fixture uses real save/discovery/claim transactions, without SQL
// manufacturing an enabled subscription or execution selection.
func animeEnabledStage(t *testing.T) (*animeDownloadFixture, string, model.AnimeDownloadJob) {
	t.Helper()
	return animeEnabledStageWithConfig(t, nil)
}

func animeEnabledStageWithConfig(t *testing.T, configure func(*animeDownloadFixture, *model.AnimeSubscriptionConfig, *animefeed.SourceRelease)) (*animeDownloadFixture, string, model.AnimeDownloadJob) {
	t.Helper()
	ctx := context.Background()
	f := newAnimeDownloadFixture(t)
	e, err := f.service.Store.SaveAnimeEntry(ctx, model.AnimeEntry{EditionKey: "original", Title: "完整下载流程", Metadata: model.AnimeMetadata{SchemaVersion: 1}})
	if err != nil {
		t.Fatal(err)
	}
	c := model.AnimeSubscriptionConfig{EntryID: e.ID, Status: "paused", Form: model.AnimeForm{SchemaVersion: 1}, Sources: []model.AnimeSourceConfig{{Ref: "main", Role: "main", Provider: "garden", Status: "paused", RunWhenEnabled: true, ItemID: bindingPtr("123"), GroupID: bindingPtr("dmhy_f45"), GroupName: bindingPtr("字幕组"), Binding: model.AnimeBinding{SchemaVersion: 1, Confirmed: true, Scope: &model.AnimeScope{Kind: "range", First: bindingPtr(1), Last: bindingPtr(12)}}, Rules: model.AnimeRules{SchemaVersion: 1, MinimumEpisode: bindingPtr(1)}}}}
	row := animefeed.SourceRelease{PostKey: "rel_stage", Title: "[字幕组] 番剧 - 01 [1080p]", MagnetHint: "magnet:?xt=urn:btih:" + strings.Repeat("a", 40)}
	// Keep the generic download fixture valid for automatic initialization.
	account, err := f.accounts.Primary("")
	if err != nil {
		t.Fatal(err)
	}
	f.settings, err = f.service.SaveSettings(ctx, "admin", "stage_settings_01", AnimeSettingsInput{ExpectedRevision: f.settings.Revision, DownloadCID: *f.settings.DownloadCID, OrganizeCID: bindingPtr("900"), ExpectedPrimaryAccountID: account.ID, ExpectedPrimaryAccountRevision: int64(account.Revision)})
	if err != nil {
		t.Fatal(err)
	}
	if configure != nil {
		configure(f, &c, &row)
	}
	settings, err := f.service.Store.GetAnimeSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	c.Status = "enabled"
	for i := range c.Sources {
		c.Sources[i].Status = "enabled"
	}
	key, err := animeRequestKey("admin", "fixture", "stage_save_01", c)
	if err != nil {
		t.Fatal(err)
	}
	r, err := f.service.Store.SaveAnimeConfiguration(ctx, c, key, storage.AnimeConfigGuard{Entry: e, Settings: settings, RequireAccount: true, Runtime: true})
	if err != nil {
		t.Fatal(err)
	}
	sub, err := f.service.Store.GetAnimeSubscription(ctx, r.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Add(time.Second)
	discovery := &AnimeDiscoveryRunner{Admin: f.service, now: func() time.Time { return now }, Feeds: animeRSSFunc(func(_ context.Context, _ *AnimeSourceBudget, in AnimeSourceInput) (AnimeSourceResult, error) {
		return AnimeSourceResult{SourcePage: animefeed.SourcePage{Kind: in.Kind, Source: in.Source, ObservedAt: now, RawCount: 1, RequestedTake: in.Take, Completeness: "visible_end", Releases: []animefeed.SourceRelease{row}}}, nil
	})}
	if err := discovery.PollOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if err := discovery.VerifyOnce(ctx); err != nil {
		t.Fatal(err)
	}
	jobs, err := f.service.SubscriptionJobs(ctx, sub.ID, 1, 20)
	if err != nil || jobs.Total != 1 {
		t.Fatal(jobs, err)
	}
	f.service.DownloadCoordinator = &CloudDownloadCoordinator{}
	return f, sub.ID, jobs.Items[0]
}

func TestAnimeDownloadStageEndToEndMinimumRequests(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		t.Run(map[bool]string{false: "success_receipt", true: "lost_receipt"}[unknown], func(t *testing.T) {
			f, id, job := animeEnabledStage(t)
			ctx := context.Background()
			creates, inspects, submits, statuses, pages := 0, 0, 0, 0, 0
			f.service.Directories = animeDownloadDirectoryFunc(func(context.Context, string, string) (p115.DriveDirectory, error) {
				t.Error("normal flow read directory metadata")
				return p115.DriveDirectory{}, errors.New("unexpected")
			})
			f.service.FileGateway = &animeFileGatewayStub{create: func(context.Context, string, string, string) (string, error) { creates++; return "200", nil }, page: func(_ context.Context, cid string, offset int, cookie string) (p115.AnimeDrivePage, error) {
				pages++
				if cid != "200" || offset != 0 {
					t.Error("unscoped listing")
				}
				return p115.AnimeDrivePage{CID: cid, Count: 1, Entries: []model.AnimeDriveEntry{{ID: "400", ParentCID: cid, Kind: "file", Name: "01.mkv", Size: 9223372036854770000}}}, nil
			}}
			f.service.SubmitGateway = &animeSubmitGatewayStub{inspect: func(context.Context, string, string) (p115.AnimeTaskObservation, error) {
				inspects++
				if submits == 0 {
					return p115.AnimeTaskObservation{}, nil
				}
				return p115.AnimeTaskObservation{Present: true, InfoHash: job.Snapshot.InfoHash, TargetCID: "200", ResultID: "200"}, nil
			}, submit: func(context.Context, string, string, string) error {
				submits++
				if unknown {
					return context.DeadlineExceeded
				}
				return nil
			}}
			f.service.StatusGateway = animeStatusReaderFunc(func(context.Context, string, string) (model.AnimeOfflineStatus, error) {
				statuses++
				v := serviceAnimeStatus(job, "finished", 100)
				v.TargetCID, v.ResultID = "200", "200"
				return v, nil
			})
			now := time.Now().UnixMilli()
			runner := &AnimeStatusRunner{Admin: f.service, now: func() time.Time { return time.UnixMilli(now) }}
			if err := runner.PollOnce(ctx); err != nil {
				t.Fatal(err)
			}
			current, err := f.service.DownloadJob(ctx, job.ID)
			if err != nil {
				t.Fatal(err)
			}
			if unknown {
				if current.DownloadState != "unknown" {
					t.Fatal(current)
				}
				if _, err := f.service.JobAction(ctx, "admin", "unknown_no_retry", job.ID, "retry", AnimeControlInput{ExpectedRevision: current.Revision}); !errors.Is(err, storage.ErrAnimeTransition) {
					t.Fatal("unknown allowed resubmit", err)
				}
				serviceControlAnime(t, f.service, id, "pause")
				now = *current.NextRunAt + 1
				if err := runner.PollOnce(ctx); err != nil {
					t.Fatal(err)
				}
				current, err = f.service.DownloadJob(ctx, job.ID)
				if err != nil || current.DownloadState != "accepted" {
					t.Fatal(current, err)
				}
			}
			now = *current.NextRunAt + 1
			if err := runner.PollOnce(ctx); err != nil {
				t.Fatal(err)
			}
			view, err := f.service.JobView(ctx, job.ID)
			if err != nil || view.FileState != "verified" || view.WorkflowState != "original_saved" || view.PermittedActions.Reconcile || len(view.Files) != 2 || view.Files[1].Size != "9223372036854770000" {
				t.Fatal(view, err)
			}
			summary, err := f.service.Subscription(ctx, id)
			if err != nil || summary.ReleaseStats.PendingVerificationCount != 0 || summary.JobStats.TotalCount != 1 || summary.JobStats.RemoteCompletedCount != 1 || summary.FileStats.VerifiedCount != 1 || summary.CompletionStats.OriginalSavedCount != 1 {
				t.Fatal("mixed stage counts", summary, err)
			}
			now += int64(24 * time.Hour / time.Millisecond)
			if err := runner.PollOnce(ctx); err != nil {
				t.Fatal(err)
			}
			wantInspect := 1
			if unknown {
				wantInspect = 2
			}
			if creates != 1 || inspects != wantInspect || submits != 1 || statuses != 1 || pages != 1 {
				t.Fatal("extra 115 requests", creates, inspects, submits, statuses, pages)
			}
			if _, err := f.service.JobAction(ctx, "admin", "complete_read_no", job.ID, "reconcile", AnimeControlInput{ExpectedRevision: view.Revision}); !errors.Is(err, storage.ErrAnimeTransition) {
				t.Fatal("completed task rechecked", err)
			}
		})
	}
}
