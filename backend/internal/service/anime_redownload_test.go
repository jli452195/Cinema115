package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"strm115/backend/internal/client/animefeed"
	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
)

func animeRedownloadFixture(t *testing.T) (*animeDownloadFixture, *sql.DB, *animeOrganizationStageDrive, model.AnimeDownloadJob) {
	t.Helper()
	ctx := context.Background()
	f, _, job := animeEnabledStageWithConfig(t, func(_ *animeDownloadFixture, c *model.AnimeSubscriptionConfig, _ *animefeed.SourceRelease) {
		c.Naming = &model.AnimeNamingPlan{SchemaVersion: 1, Target: model.AnimeNamingTarget{TMDBID: "335790", Title: "测试动画", Year: "2026", Season: 1, Fingerprint: strings.Repeat("a", 64), Seasons: []model.AnimeNamingSeason{{Number: 1, Episodes: []model.AnimeNamingEpisode{{ID: "1001", Number: 1}}}}}, Sources: []model.AnimeSourceMapping{{SourceRef: "main", Segments: []model.AnimeMappingSegment{{First: 1, SourceBase: 1, TargetSeason: 1, TargetBase: 1}}}}}
	})
	db, err := sql.Open("sqlite", f.path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	d := &animeOrganizationStageDrive{animeOrganizeDrive: &animeOrganizeDrive{t: t, db: db, jobID: job.ID, entries: map[string][]model.AnimeDriveEntry{"900": {}, "200": {{ID: "400", ParentCID: "200", Kind: "file", Name: "01.mkv", Size: 1000}}}}}
	f.service.FileGateway, f.service.OrganizationGateway = d, d
	f.service.SubmitGateway = &animeSubmitGatewayStub{inspect: func(context.Context, string, string) (p115.AnimeTaskObservation, error) {
		return p115.AnimeTaskObservation{}, nil
	}, submit: func(context.Context, string, string, string) error { return nil }}
	f.service.StatusGateway = animeStatusReaderFunc(func(context.Context, string, string) (model.AnimeOfflineStatus, error) {
		v := serviceAnimeStatus(job, "finished", 100)
		v.TargetCID, v.ResultID = "200", "200"
		return v, nil
	})
	runner := &AnimeStatusRunner{Admin: f.service}
	if err := runner.PollOnce(ctx); err != nil {
		t.Fatal(err)
	}
	job, err = f.service.DownloadJob(ctx, job.ID)
	if err != nil || job.WorkflowState != "organized" {
		t.Fatal("initial RSS flow did not organize", job, err)
	}
	return f, db, d, job
}

func scheduleAnimeRedownload(t *testing.T, f *animeDownloadFixture, job model.AnimeDownloadJob) (storage.AnimeRedownloadInput, storage.AnimeRedownloadReceipt) {
	t.Helper()
	sub, err := f.service.Store.GetAnimeSubscription(context.Background(), job.Snapshot.Source.SubscriptionID, false)
	if err != nil {
		t.Fatal(err)
	}
	in := storage.AnimeRedownloadInput{ExpectedRevision: sub.Revision, SettingsRevision: f.settings.Revision, SourceID: job.Snapshot.Source.SourceID, Items: []storage.AnimeRedownloadItem{{ID: job.ID, Revision: job.Revision}, {ID: strings.Repeat("f", 32), Revision: 1}}}
	receipt, err := f.service.RedownloadAnime(context.Background(), "admin", "redownload_01", sub.ID, in)
	if err != nil || len(receipt.Results) != 2 || receipt.Results[0].JobID == "" || receipt.Results[1].Error == "" {
		t.Fatal(receipt, err)
	}
	return in, receipt
}

func TestAnimeRedownloadFullRSSFlowAndLatestResource(t *testing.T) {
	for _, status := range []string{"enabled", "paused", "archived"} {
		t.Run(status, func(t *testing.T) {
			ctx := context.Background()
			f, db, drive, old := animeRedownloadFixture(t)
			id := old.Snapshot.Source.SubscriptionID
			if status != "enabled" {
				sub, _ := f.service.Store.GetAnimeSubscription(ctx, id, false)
				action := map[string]string{"paused": "pause", "archived": "archive"}[status]
				if _, err := f.service.ControlSubscription(ctx, "admin", "end_subscription_01", id, action, AnimeControlInput{ExpectedRevision: sub.Revision}); err != nil {
					t.Fatal(err)
				}
			}
			lookups, deletes, adds := 0, 0, 0
			var newID string
			f.service.Offline = &cloudWorkflowStub{
				lookup: func(_ context.Context, cookie string, resource p115.CloudResource) (p115.CloudTask, bool, error) {
					lookups++
					if cookie != animeDownloadTestCookie || resource.Hash != old.Snapshot.InfoHash {
						t.Fatal("wrong resource or credential")
					}
					return p115.CloudTask{InfoHash: resource.Hash, DisplayStatus: "finished"}, true, nil
				},
				remove: func(context.Context, string, string) error {
					deletes++
					var state string
					if err := db.QueryRow(`SELECT state FROM anime_drive_operations WHERE download_job_id=? AND kind='submit' AND authorized_at IS NOT NULL`, newID).Scan(&state); err != nil || state != "dispatched" {
						t.Fatal("delete preceded durable submit intent", state, err)
					}
					return nil
				},
				add: func(_ context.Context, cookie, link, cid string) (json.RawMessage, error) {
					adds++
					if cookie != animeDownloadTestCookie || cid != "200" || link != "magnet:?xt=urn:btih:"+old.Snapshot.InfoHash {
						t.Fatal("changed download target")
					}
					return json.RawMessage(`{}`), nil
				},
			}
			in, receipt := scheduleAnimeRedownload(t, f, old)
			newID = receipt.Results[0].JobID
			if lookups+deletes+adds != 0 {
				t.Fatal("scheduling contacted 115")
			}
			again, err := f.service.RedownloadAnime(ctx, "admin", "redownload_01", id, in)
			if err != nil || !reflect.DeepEqual(again, receipt) {
				t.Fatal("replay created another attempt", again, err)
			}
			job, err := f.service.DownloadJob(ctx, newID)
			if err != nil || job.Attempt != old.Attempt+1 || !job.Snapshot.Redownload || job.DirectoryCID != nil || job.Files != nil || job.Tracking != nil || job.ResultObjectID != nil || job.DirectoryOperation != nil {
				t.Fatal("old progress leaked into new attempt", job, err)
			}
			states, local, err := f.service.Store.AnimePreviewStates(ctx, in.SourceID, []string{old.Snapshot.Release.ID})
			if err != nil || states[old.Snapshot.Release.ID] != "queued" || local[old.Snapshot.Release.ID].CanRedownload {
				t.Fatal(states, local, err)
			}
			// Simulate an owner deleting the organized file, then refill the new download.
			for cid, entries := range drive.entries {
				if cid != "200" {
					filtered := entries[:0]
					for _, e := range entries {
						if e.Kind == "directory" {
							filtered = append(filtered, e)
						}
					}
					drive.entries[cid] = filtered
				}
			}
			drive.entries["200"] = []model.AnimeDriveEntry{{ID: "500", ParentCID: "200", Kind: "file", Name: "01.mkv", Size: 1000}}
			drive.jobID = newID
			if err := (&AnimeStatusRunner{Admin: f.service}).PollOnce(ctx); err != nil {
				t.Fatal(err)
			}
			job, err = f.service.DownloadJob(ctx, newID)
			if err != nil || job.WorkflowState != "organized" || lookups != 1 || deletes != 1 || adds != 1 || drive.downloadCreates != 2 || drive.moves != 2 {
				t.Fatal("redownload did not rerun the full chain", job, lookups, deletes, adds, drive.downloadCreates, drive.moves, err)
			}
			page, err := f.service.SubscriptionJobs(ctx, id, 1, 20)
			if err != nil || page.Total != 1 || page.Items[0].ID != newID {
				t.Fatal("duplicate resource cards", page, err)
			}
			sub, _ := f.service.Store.GetAnimeSubscription(ctx, id, false)
			if sub.Status != status {
				t.Fatal("one-shot download changed RSS lifecycle", sub.Status)
			}
			if status == "enabled" {
				verified, found, err := f.service.Store.FindAnimeVerifiedRelease(ctx, old.Snapshot.Release.ID, old.Snapshot.Release.ContentFingerprint)
				if err != nil || !found {
					t.Fatal(found, err)
				}
				decision, _ := f.service.Store.GetAnimeReleaseDecision(ctx, in.SourceID, old.Snapshot.Release.ID)
				claim, err := f.service.Store.ClaimAnimeDownload(ctx, in.SourceID, old.Snapshot.Release.ID, decision.Generation, verified, job.Snapshot.Target)
				if err != nil || claim.Created || claim.Job.ID != newID {
					t.Fatal("RSS discovery duplicated manual attempt", claim, err)
				}
			}
		})
	}
}

func TestAnimeRedownloadRunningUnknownAndLostReplies(t *testing.T) {
	for _, mode := range []string{"running", "unclear", "delete_unknown", "submit_unknown"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			f, _, drive, old := animeRedownloadFixture(t)
			deletes, adds, reads := 0, 0, 0
			f.service.Offline = &cloudWorkflowStub{
				lookup: func(context.Context, string, p115.CloudResource) (p115.CloudTask, bool, error) {
					state := "finished"
					if mode == "running" {
						state = "downloading"
					}
					if mode == "unclear" {
						state = ""
					}
					return p115.CloudTask{InfoHash: old.Snapshot.InfoHash, DisplayStatus: state}, true, nil
				},
				remove: func(context.Context, string, string) error {
					deletes++
					if mode == "delete_unknown" {
						return context.DeadlineExceeded
					}
					return nil
				},
				add: func(context.Context, string, string, string) (json.RawMessage, error) {
					adds++
					return nil, context.DeadlineExceeded
				},
			}
			_, receipt := scheduleAnimeRedownload(t, f, old)
			job, _ := f.service.DownloadJob(ctx, receipt.Results[0].JobID)
			drive.jobID = job.ID
			session, err := f.service.ResumeDownloadTarget(ctx, job.Snapshot.Target)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.service.EnsureDownload(ctx, session, job.ID); err != nil {
				t.Fatal(err)
			}
			current, _ := f.service.DownloadJob(ctx, job.ID)
			if mode == "running" || mode == "unclear" {
				if deletes != 0 || adds != 0 || current.DownloadState != "blocked" {
					t.Fatal(current, deletes, adds)
				}
				return
			}
			if current.DownloadState != "unknown" || deletes != 1 {
				t.Fatal(current, deletes, adds)
			}
			f.service.SubmitGateway = &animeSubmitGatewayStub{inspect: func(context.Context, string, string) (p115.AnimeTaskObservation, error) {
				reads++
				return p115.AnimeTaskObservation{}, nil
			}, submit: func(context.Context, string, string, string) error { t.Fatal("uncertain write replayed"); return nil }}
			if _, err := f.service.EnsureDownload(ctx, session, job.ID); err != nil {
				t.Fatal(err)
			}
			if reads != 1 || deletes != 1 || adds != map[string]int{"delete_unknown": 0, "submit_unknown": 1}[mode] {
				t.Fatal(reads, deletes, adds)
			}
			if _, err := f.service.Store.GetAnimeOrganization(ctx, job.ID); !errors.Is(err, sql.ErrNoRows) {
				t.Fatal("used old organization checkpoint", err)
			}
		})
	}
}

func TestAnimeRedownloadRollbackAndConcurrentClicks(t *testing.T) {
	ctx := context.Background()
	f, db, _, old := animeRedownloadFixture(t)
	f.service.Offline = &cloudWorkflowStub{} // Scheduling must never call it.
	sub, err := f.service.Store.GetAnimeSubscription(ctx, old.Snapshot.Source.SubscriptionID, false)
	if err != nil {
		t.Fatal(err)
	}
	in := storage.AnimeRedownloadInput{ExpectedRevision: sub.Revision, SettingsRevision: f.settings.Revision, SourceID: old.Snapshot.Source.SourceID, Items: []storage.AnimeRedownloadItem{{ID: old.ID, Revision: old.Revision}}}
	if _, err := db.Exec(`CREATE TRIGGER reject_redownload_receipt BEFORE INSERT ON anime_request_keys BEGIN SELECT RAISE(ABORT,'receipt failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.RedownloadAnime(ctx, "admin", "rollback_redownload", sub.ID, in); err == nil {
		t.Fatal("ignored receipt failure")
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM anime_download_jobs`).Scan(&count); err != nil || count != 1 {
		t.Fatal("partial scheduling escaped rollback", count, err)
	}
	if _, err := db.Exec(`DROP TRIGGER reject_redownload_receipt`); err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	var receipts [2]storage.AnimeRedownloadReceipt
	var errs [2]error
	for i := range receipts {
		group.Add(1)
		go func(i int) {
			defer group.Done()
			receipts[i], errs[i] = f.service.RedownloadAnime(ctx, "admin", "parallel_redownload", sub.ID, in)
		}(i)
	}
	group.Wait()
	replayed, err := f.service.RedownloadAnime(ctx, "admin", "parallel_redownload", sub.ID, in)
	if err != nil || len(replayed.Results) != 1 || replayed.Results[0].JobID == "" {
		t.Fatal(replayed, err, errs)
	}
	for i, receipt := range receipts {
		if errs[i] == nil && !reflect.DeepEqual(receipt, replayed) {
			t.Fatal("same key returned conflicting outcomes", receipts)
		}
	}
	if err := db.QueryRow(`SELECT count(*) FROM anime_download_jobs`).Scan(&count); err != nil || count != 2 {
		t.Fatal("concurrent clicks created duplicate attempts", count, err)
	}
	latest, err := f.service.DownloadJob(ctx, replayed.Results[0].JobID)
	if err != nil {
		t.Fatal(err)
	}
	states, jobs, err := f.service.Store.AnimePreviewStates(ctx, in.SourceID, []string{old.Snapshot.Release.ID})
	if err != nil || states[old.Snapshot.Release.ID] != "queued" || jobs[old.Snapshot.Release.ID].CanRedownload {
		t.Fatal("in-flight attempt remained selectable", states, jobs, err)
	}
	in.Items = []storage.AnimeRedownloadItem{{ID: latest.ID, Revision: latest.Revision}}
	rejected, err := f.service.RedownloadAnime(ctx, "admin", "reject_running_again", sub.ID, in)
	if err != nil || len(rejected.Results) != 1 || rejected.Results[0].JobID != "" || rejected.Results[0].Error == "" {
		t.Fatal("allowed another active attempt", rejected, err)
	}
}
