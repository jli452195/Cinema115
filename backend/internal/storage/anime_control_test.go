package storage

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"strm115/backend/internal/model"
)

func controlAnime(t *testing.T, s *Store, id, action string) model.AnimeControlReceipt {
	t.Helper()
	sub, err := s.GetAnimeSubscription(context.Background(), id, true)
	if err != nil {
		t.Fatal(err)
	}
	out, err := s.ControlAnimeSubscription(context.Background(), id, action, sub.Revision, nil)
	if err != nil {
		t.Fatal(action, err)
	}
	return out
}

func TestAnimeControlPauseResumePreservesConfirmedWork(t *testing.T) {
	f, now := discoveryFixture(t)
	r, v := f.selectRelease(t, 0, "rel_control", strings.Repeat("a", 40))
	job := f.claim(t, 0, r, v).Job
	// A prepared submit and the current worker must survive as intent, while
	// its permission token must not survive pause/resume.
	if _, err := f.store.db.Exec(`UPDATE anime_download_jobs SET directory_cid='200'`); err != nil {
		t.Fatal(err)
	}
	work := takeAnimeWork(t, f.store, now)
	op, err := f.store.PrepareAnimeSubmit(context.Background(), job.ID, job.Generation, f.target, work.Token)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := f.store.GetAnimeDownloadJob(context.Background(), job.ID)
	paused := controlAnime(t, f.store, f.sub.ID, "pause")
	got, _ := f.store.GetAnimeDownloadJob(context.Background(), job.ID)
	if paused.CancelledJobs != 0 || paused.PendingJobs != 1 || got.FileState != "pending" || got.BlockCode == nil || *got.BlockCode != "DOWNLOAD_PAUSED" || got.Generation != job.Generation {
		t.Fatal(paused, got)
	}
	if _, found, err := f.store.ReserveAnimeDownloadWork(context.Background(), now+1, nil); err != nil || found {
		t.Fatal("paused work claimed", found, err)
	}
	controlAnime(t, f.store, f.sub.ID, "resume")
	if err := f.store.DispatchAnimeSubmit(context.Background(), op.ID, work.Token); !errors.Is(err, ErrAnimeConflict) {
		t.Fatal("old worker dispatched after resume", err)
	}
	got, _ = f.store.GetAnimeDownloadJob(context.Background(), job.ID)
	if got.ID != before.ID || got.Generation != before.Generation || !reflect.DeepEqual(got.Snapshot, before.Snapshot) || got.BlockCode != nil || got.NextRunAt == nil {
		t.Fatal("resume rebuilt intent", got)
	}
	sub, _ := f.store.GetAnimeSubscription(context.Background(), f.sub.ID, true)
	if sub.RunGeneration != f.sub.RunGeneration {
		t.Fatal("changed confirmed execution generation")
	}
	for i, src := range sub.Sources {
		if src.RunGeneration != f.sub.Sources[i].RunGeneration {
			t.Fatal("baseline replaced", src)
		}
	}
	next := takeAnimeWork(t, f.store, time.Now().UnixMilli()+1)
	if err := f.store.DispatchAnimeSubmit(context.Background(), op.ID, next.Token); err != nil {
		t.Fatal(err)
	}
	assertDownloadCounts(t, f.store, 1, 1, 1, 1)
}

func TestAnimeControlFencesOldRSSAndDiscoveryAfterFastResume(t *testing.T) {
	f, now := discoveryFixture(t)
	p := discoveryPoll(t, f.store, now)
	if _, err := f.store.CompleteAnimeSourcePoll(context.Background(), p, discoveryPage(p, now, discoveryRow("rel_control_pending", now)), now); err != nil {
		t.Fatal(err)
	}
	verification, found, err := f.store.ReserveAnimeDiscovery(context.Background(), now)
	if err != nil || !found {
		t.Fatal(found, err)
	}
	oldPoll := discoveryPoll(t, f.store, now+1)
	controlAnime(t, f.store, f.sub.ID, "pause")
	controlAnime(t, f.store, f.sub.ID, "resume")
	if _, err := f.store.CompleteAnimeSourcePoll(context.Background(), oldPoll, discoveryPage(oldPoll, now+2), now+2); !errors.Is(err, ErrAnimeConflict) {
		t.Fatal("old RSS committed", err)
	}
	d, err := f.store.GetAnimeReleaseDecision(context.Background(), verification.Decision.SourceID, verification.Decision.ReleaseID)
	if err != nil || d.Generation <= verification.Decision.Generation || d.Disposition != "selected" || !reflect.DeepEqual(d.Snapshot, verification.Decision.Snapshot) {
		t.Fatal("pending choice replaced", d, err)
	}
	if err := f.store.CompleteAnimeDiscovery(context.Background(), verification, "DISCOVERY_CANCELLED", false, now+3); !errors.Is(err, ErrAnimeConflict) {
		t.Fatal("old verifier committed", err)
	}
	current, found, err := f.store.ReserveAnimeDiscovery(context.Background(), now+4)
	if err != nil || !found || current.Decision.Generation <= verification.Decision.Generation {
		t.Fatal(current, found, err)
	}
}

func TestAnimeControlDispatchedSubmitKeepsReceiptAcrossStop(t *testing.T) {
	for _, action := range []string{"pause", "archive"} {
		t.Run(action, func(t *testing.T) {
			f, job := animeSubmitStoreFixture(t)
			ctx := context.Background()
			now := time.Now().UnixMilli()
			work := takeAnimeWork(t, f.store, now)
			op, err := f.store.PrepareAnimeSubmit(ctx, job.ID, job.Generation, f.target, work.Token)
			if err != nil {
				t.Fatal(err)
			}
			if err := f.store.DispatchAnimeSubmit(ctx, op.ID, work.Token); err != nil {
				t.Fatal(err)
			}
			receipt := controlAnime(t, f.store, f.sub.ID, action)
			if receipt.CancelledJobs != 0 || receipt.InFlightOperationCount != 1 {
				t.Fatal("external intent cancelled", receipt)
			}
			if err := f.store.RecordAnimeSubmitOutcome(ctx, op.ID, "confirmed", "", model.AnimeSubmitObservation{SchemaVersion: 1, Kind: "accepted", TargetCID: "200"}, work.Token); err != nil {
				t.Fatal("lost in-flight result", err)
			}
			if err := f.store.CompleteAnimeDownloadWork(ctx, work, "", now+1); err != nil {
				t.Fatal(err)
			}
			got, _ := f.store.GetAnimeDownloadJob(ctx, job.ID)
			if got.DownloadState != "accepted" || got.Generation != job.Generation {
				t.Fatal(got)
			}
			if _, found, err := f.store.ReserveAnimeStatusPoll(ctx, *got.NextRunAt, nil); err != nil || !found {
				t.Fatal("stopped subscription lost read tracking", found, err)
			}
		})
	}
}

func TestAnimeControlDeleteWaitsForActiveWorker(t *testing.T) {
	f, job := animeDirectoryStoreFixture(t)
	ctx := context.Background()
	work := takeAnimeWork(t, f.store, time.Now().UnixMilli())
	started, create, err := f.store.StartAnimeDirectory(ctx, job.ID, job.Generation, f.target, work.Token)
	if err != nil || !create {
		t.Fatal(create, err)
	}
	if _, err := f.store.ControlAnimeSubscription(ctx, f.sub.ID, "remove", f.sub.Revision, nil); !errors.Is(err, ErrAnimeDeleteBusy) {
		t.Fatal(err)
	}
	root := model.AnimeDriveEntry{ID: "200", ParentCID: job.ParentCID, Kind: "directory", Name: job.DirectoryName}
	if err := f.store.FinishAnimeDirectory(ctx, started, &root, "", work.Token); err != nil {
		t.Fatal(err)
	}
	if err := f.store.CompleteAnimeDownloadWork(ctx, work, "", time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	controlAnime(t, f.store, f.sub.ID, "remove")
	if _, err := f.store.GetAnimeDownloadJob(ctx, job.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("deleted worker left a job", err)
	}
	if err := f.store.DispatchAnimeSubmit(ctx, "missing_operation", work.Token); err == nil {
		t.Fatal("stale worker dispatched")
	}
}

func TestAnimeControlSharedResourceKeepsOtherSubscription(t *testing.T) {
	for _, otherState := range []string{"enabled", "paused"} {
		t.Run(otherState, func(t *testing.T) {
			f, _ := discoveryFixture(t)
			ctx := context.Background()
			e := createAnimeEntry(t, f.store, nil)
			config := animeConfig(e.ID)
			config.Status = "draft"
			config.Sources = nil
			second := saveAnimeConfig(t, f.store, config)
			if _, err := f.store.db.Exec(`UPDATE anime_subscriptions SET status='enabled',first_enabled_at=1 WHERE id=?`, second.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := f.store.db.Exec(`UPDATE anime_subscription_sources SET subscription_id=? WHERE id=?`, second.ID, f.sub.Sources[1].ID); err != nil {
				t.Fatal(err)
			}
			r, v := f.selectRelease(t, 0, "rel_shared_1", strings.Repeat("a", 40))
			job := f.claim(t, 0, r, v).Job
			r, v = f.selectRelease(t, 1, "rel_shared_2", strings.Repeat("a", 40))
			other := f.claim(t, 1, r, v).Job
			if other.ID != job.ID {
				t.Fatal("resource duplicated")
			}
			if otherState == "paused" {
				controlAnime(t, f.store, second.ID, "pause")
			}
			if receipt := controlAnime(t, f.store, f.sub.ID, "remove"); receipt.CancelledJobs != 0 {
				t.Fatal(receipt)
			}
			got, _ := f.store.GetAnimeDownloadJob(ctx, job.ID)
			if got.WorkflowState != "active" || got.Generation != job.Generation {
				t.Fatal("other subscription lost shared job", got)
			}
			if otherState == "paused" {
				controlAnime(t, f.store, second.ID, "resume")
			}
			if _, err := f.store.ListAnimeDownloadJobs(ctx, f.sub.ID, 1, 20); !errors.Is(err, sql.ErrNoRows) {
				t.Fatal("deleted history visible", err)
			}
			page, err := f.store.ListAnimeDownloadJobs(ctx, second.ID, 1, 20)
			if err != nil || page.Total != 1 || len(page.Items) != 1 {
				t.Fatal("other subscription lost history", page, err)
			}
		})
	}
}

func TestAnimeControlResumeKeepsSourceIntentAndCooldown(t *testing.T) {
	for _, status := range []string{"draft", "paused", "enabled"} {
		t.Run(status, func(t *testing.T) {
			f, now := discoveryFixture(t)
			r, v := f.selectRelease(t, 1, "rel_paused_source", strings.Repeat("b", 40))
			job := f.claim(t, 1, r, v).Job
			controlAnime(t, f.store, f.sub.ID, "pause")
			if _, err := f.store.db.Exec(`UPDATE anime_subscription_sources SET status=?,run_when_enabled=0,health='unavailable',next_check_at=?,retry_after=? WHERE id=?`, status, now+600000, now+900000, f.sub.Sources[1].ID); err != nil {
				t.Fatal(err)
			}
			before, _ := f.store.GetAnimeSource(context.Background(), f.sub.Sources[1].ID)
			receipt := controlAnime(t, f.store, f.sub.ID, "resume")
			after, _ := f.store.GetAnimeSource(context.Background(), before.ID)
			if !reflect.DeepEqual(before, after) || len(receipt.ResumedSourceIDs) != 1 || len(receipt.UnchangedSources) != 1 {
				t.Fatal("resume changed source", receipt, before, after)
			}
			got, _ := f.store.GetAnimeDownloadJob(context.Background(), job.ID)
			if got.BlockCode == nil || *got.BlockCode != "DOWNLOAD_PAUSED" {
				t.Fatal("separately stopped source resumed", got)
			}
		})
	}
}

func TestAnimeControlResumeRejectsInvalidRulesOrTarget(t *testing.T) {
	for _, query := range []string{`UPDATE anime_settings SET organize_cid=NULL,organize_path=NULL`, `UPDATE anime_subscription_sources SET rules_json=json_set(rules_json,'$.match',json('["["]'))`, `UPDATE p115_accounts SET cookie_enc=x''`} {
		t.Run(query, func(t *testing.T) {
			f, _ := discoveryFixture(t)
			if _, err := f.store.db.Exec(`UPDATE anime_subscription_sources SET checked_at=1`); err != nil {
				t.Fatal(err)
			}
			paused := controlAnime(t, f.store, f.sub.ID, "pause")
			if _, err := f.store.db.Exec(query); err != nil {
				t.Fatal(err)
			}
			if _, err := f.store.ControlAnimeSubscription(context.Background(), f.sub.ID, "resume", paused.Revision, nil); err == nil {
				t.Fatal("changed execution basis accepted")
			}
			sub, _ := f.store.GetAnimeSubscription(context.Background(), f.sub.ID, true)
			if sub.Status != "paused" || sub.Revision != paused.Revision {
				t.Fatal("failed resume committed", sub)
			}
		})
	}
}

func TestAnimeControlCASIdempotencyAndReopen(t *testing.T) {
	f, _ := discoveryFixture(t)
	ctx := context.Background()
	key := animeAdminKey("control_retry")
	key.Operation = "POST /pause"
	var wins atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := f.store.ControlAnimeSubscription(ctx, f.sub.ID, "pause", f.sub.Revision, nil)
			if err == nil {
				wins.Add(1)
			} else if !errors.Is(err, ErrAnimeConflict) {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatal("CAS winners", wins.Load())
	}
	sub, _ := f.store.GetAnimeSubscription(ctx, f.sub.ID, true)
	first, err := f.store.ControlAnimeSubscription(ctx, sub.ID, "archive", sub.Revision, &key)
	if err != nil {
		t.Fatal(err)
	}
	var path string
	if err := f.store.db.QueryRow(`SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&path); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	again, err := reopened.ControlAnimeSubscription(ctx, sub.ID, "archive", sub.Revision, &key)
	if err != nil || !reflect.DeepEqual(first, again) {
		t.Fatal("receipt lost on reopen", again, err)
	}
	key.Digest = strings.Repeat("b", 64)
	if _, err := reopened.ControlAnimeSubscription(ctx, sub.ID, "archive", sub.Revision, &key); !errors.Is(err, ErrAnimeIdempotency) {
		t.Fatal(err)
	}
	controlAnime(t, reopened, sub.ID, "unarchive")
	got, _ := reopened.GetAnimeSubscription(ctx, sub.ID, true)
	if got.Status != "paused" {
		t.Fatal("unarchive enabled execution", got)
	}
}

func TestAnimeControlTransactionRollsBackAllEffects(t *testing.T) {
	f, job := animeSubmitStoreFixture(t)
	ctx := context.Background()
	if _, err := f.store.db.Exec(`CREATE TRIGGER anime_control_fail BEFORE INSERT ON anime_request_keys BEGIN SELECT RAISE(ABORT,'injected control failure'); END`); err != nil {
		t.Fatal(err)
	}
	key := animeAdminKey("failed_control")
	if _, err := f.store.ControlAnimeSubscription(ctx, f.sub.ID, "remove", f.sub.Revision, &key); err == nil {
		t.Fatal("failure accepted")
	}
	sub, _ := f.store.GetAnimeSubscription(ctx, f.sub.ID, true)
	if sub.Status != "enabled" || sub.Revision != f.sub.Revision {
		t.Fatal("parent partially committed", sub)
	}
	got, _ := f.store.GetAnimeDownloadJob(ctx, job.ID)
	if !reflect.DeepEqual(got, job) {
		t.Fatal("job partially committed", got, job)
	}
	var n int
	if err := f.store.db.QueryRow(`SELECT count(*) FROM anime_job_selections WHERE status='authorized'`).Scan(&n); err != nil || n != 1 {
		t.Fatal(n, err)
	}
}

func TestAnimeControlCompletedJobNeverReopens(t *testing.T) {
	f, job := animeSubmitStoreFixture(t)
	if _, err := f.store.db.Exec(`UPDATE anime_download_jobs SET download_state='completed',file_state='verified',workflow_state='original_saved' WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	before, _ := f.store.GetAnimeDownloadJob(context.Background(), job.ID)
	controlAnime(t, f.store, f.sub.ID, "archive")
	controlAnime(t, f.store, f.sub.ID, "unarchive")
	after, _ := f.store.GetAnimeDownloadJob(context.Background(), job.ID)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("completed history changed", before, after)
	}
}

func TestAnimeControlStateBoundariesAndNoOpRevision(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	for _, tc := range []struct {
		state, action, want string
		wantErr             error
	}{
		{"draft", "pause", "", ErrAnimeTransition},
		{"draft", "resume", "", ErrAnimeTransition},
		{"paused", "resume", "", ErrAnimeConfigurationRequired},
		{"archived", "resume", "", ErrAnimeConfigurationRequired},
		{"removed", "unarchive", "", ErrAnimeTransition},
		{"removed", "archive", "", ErrAnimeTransition},
		{"paused", "pause", "paused", nil},
		{"archived", "archive", "archived", nil},
		{"draft", "archive", "archived", nil},
	} {
		t.Run(tc.state+"_"+tc.action, func(t *testing.T) {
			e := createAnimeEntry(t, s, nil)
			c := animeConfig(e.ID)
			c.Status = "draft"
			c.Sources = nil
			sub := saveAnimeConfig(t, s, c)
			if _, err := s.db.Exec(`UPDATE anime_subscriptions SET status=? WHERE id=?`, tc.state, sub.ID); err != nil {
				t.Fatal(err)
			}
			out, err := s.ControlAnimeSubscription(ctx, sub.ID, tc.action, sub.Revision, nil)
			if !errors.Is(err, tc.wantErr) {
				t.Fatal(out, err)
			}
			got, _ := s.GetAnimeSubscription(ctx, sub.ID, true)
			if err != nil {
				if got.Status != tc.state || got.Revision != sub.Revision {
					t.Fatal("rejected action mutated state", got)
				}
				return
			}
			wantRevision := sub.Revision
			if tc.want != tc.state {
				wantRevision++
			}
			if out.Status != tc.want || out.Revision != wantRevision || got.Status != tc.want {
				t.Fatal(out, got)
			}
		})
	}
}

func TestAnimeControlResumeDoesNotClearOtherFailure(t *testing.T) {
	for _, tc := range []struct{ code, state string }{{"DIRECTORY_CONFLICT", "needs_review"}, {"TASK_READ_FAILED", "pending"}} {
		t.Run(tc.code, func(t *testing.T) {
			f, _ := discoveryFixture(t)
			r, v := f.selectRelease(t, 0, "rel_blocked_control", strings.Repeat("c", 40))
			job := f.claim(t, 0, r, v).Job
			if _, err := f.store.db.Exec(`UPDATE anime_download_jobs SET block_code=?,file_state=? WHERE id=?`, tc.code, tc.state, job.ID); err != nil {
				t.Fatal(err)
			}
			controlAnime(t, f.store, f.sub.ID, "pause")
			if _, found, err := f.store.ReserveAnimeDownloadWork(context.Background(), time.Now().Add(time.Hour).UnixMilli(), nil); err != nil || found {
				t.Fatal("paused failed job kept taking a lease", found, err)
			}
			controlAnime(t, f.store, f.sub.ID, "resume")
			got, _ := f.store.GetAnimeDownloadJob(context.Background(), job.ID)
			if got.FileState != tc.state || got.BlockCode == nil || *got.BlockCode != tc.code {
				t.Fatal("resume cleared independent failure", got)
			}
			_, found, err := f.store.ReserveAnimeDownloadWork(context.Background(), time.Now().Add(time.Hour).UnixMilli(), nil)
			if err != nil || found != (tc.state == "pending") {
				t.Fatal("restored retry state lost", found, err)
			}
		})
	}
}
