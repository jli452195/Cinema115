package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"strm115/backend/internal/client/animefeed"
	"strm115/backend/internal/client/mikan"
	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
)

type animeRSSFunc func(context.Context, *AnimeSourceBudget, AnimeSourceInput) (AnimeSourceResult, error)

func (f animeRSSFunc) ReadSource(ctx context.Context, b *AnimeSourceBudget, in AnimeSourceInput) (AnimeSourceResult, error) {
	return f(ctx, b, in)
}

func discoveryServiceFixture(t *testing.T) (*animeDownloadFixture, *sql.DB, *AnimeDiscoveryRunner, model.AnimeSource, int64) {
	t.Helper()
	f, db, _, source, _, _ := animeClaimServiceFixture(t)
	now := time.Now().UnixMilli()
	if _, err := db.Exec(`UPDATE anime_settings SET organize_cid='900',organize_path='/organized'`); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{`DELETE FROM anime_release_decisions`, `DELETE FROM anime_releases`} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := db.Exec(`UPDATE anime_subscription_sources SET checked_at=1,next_check_at=?`, now-1); err != nil {
		t.Fatal(err)
	}
	r := &AnimeDiscoveryRunner{Admin: f.service, now: func() time.Time { return time.UnixMilli(now) }}
	r.Feeds = animeRSSFunc(func(_ context.Context, _ *AnimeSourceBudget, in AnimeSourceInput) (AnimeSourceResult, error) {
		return discoveryServicePage(in, now, 1), nil
	})
	f.service.Torrents = animeClaimTorrentFunc(func(context.Context, string, string, string) ([]byte, error) { return animeClaimTestTorrent(), nil })
	return f, db, r, source, now
}

func discoveryServicePage(in AnimeSourceInput, now int64, count int) AnimeSourceResult {
	page := animefeed.SourcePage{Kind: in.Kind, Source: in.Source, RawCount: count, ObservedAt: time.UnixMilli(now), Completeness: "partial", StopReason: "rss_window", Releases: make([]animefeed.SourceRelease, 0, count)}
	for i := 0; i < count; i++ {
		key := fmt.Sprintf("%040x", i+1)
		page.Releases = append(page.Releases, animefeed.SourceRelease{PostKey: key, Title: "[字幕组] 动画 - 01 [1080p]", PublishedRaw: time.UnixMilli(now).UTC().Format(time.RFC3339Nano), TorrentURL: "https://mikanani.me/Download/20261019/" + key + ".torrent"})
	}
	return AnimeSourceResult{SourcePage: page}
}

func TestAnimeDiscoveryRunnerQueuesAndClaimsWithoutDriveWrites(t *testing.T) {
	f, db, r, source, now := discoveryServiceFixture(t)
	var reads atomic.Int32
	r.Feeds = animeRSSFunc(func(ctx context.Context, b *AnimeSourceBudget, in AnimeSourceInput) (AnimeSourceResult, error) {
		reads.Add(1)
		if in.Kind != "rss" || in.Take != 0 || in.Refresh || in.Previous != nil {
			t.Error("automatic poll expanded history/forced refresh")
		}
		return discoveryServicePage(in, now, 1), nil
	})
	if err := r.PollOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := r.VerifyOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := r.PollOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := r.VerifyOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if reads.Load() != 1 {
		t.Fatal("success feed repeatedly polled", reads.Load())
	}
	for query, want := range map[string]int{`SELECT count(*) FROM anime_download_jobs WHERE download_state='queued' AND directory_cid IS NULL`: 1, `SELECT count(*) FROM anime_drive_operations`: 0, `SELECT count(*) FROM anime_job_selections`: 1} {
		var n int
		if err := db.QueryRow(query).Scan(&n); err != nil || n != want {
			t.Fatal(query, n, want, err)
		}
	}
	saved, err := f.service.Store.GetAnimeSource(context.Background(), source.ID)
	if err != nil || saved.Health != "ok" || saved.CheckedAt == nil || saved.NextCheckAt == nil || *saved.NextCheckAt < now+900000 || *saved.NextCheckAt > now+960000 {
		t.Fatal("invalid normal interval", saved, err)
	}
}

func addDiscoveryServiceBackup(t *testing.T, db *sql.DB, source model.AnimeSource, now int64) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO anime_subscription_sources(id,subscription_id,role,position,status,run_when_enabled,provider,source_key,item_id,group_id,group_name,namespace_id,binding_json,rules_json,revision,run_generation,health,created_at,updated_at) SELECT 'standby-source',subscription_id,'standby',1,'paused',1,provider,'mikan:2259:46',item_id,'46',group_name,'standby-namespace',json_set(binding_json,'$.review.groupId','46'),rules_json,1,1,'unknown',created_at,updated_at FROM anime_subscription_sources WHERE id=?`, source.ID)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := db.Exec(`UPDATE anime_subscription_sources SET status='enabled',checked_at=1,next_check_at=? WHERE id='standby-source'`, now); err != nil {
		t.Fatal(err)
	}
}

func TestAnimeDiscoveryRunnerFailureIsolationAndHealthPolicy(t *testing.T) {
	for _, tc := range []struct {
		name         string
		err          error
		health, code string
		retry        int64
	}{
		{"network", errors.New("upstream Cookie=private"), "error", "SOURCE_READ_FAILED", 0},
		{"provider input", animefeed.ErrInput, "error", "SOURCE_RESPONSE_INVALID", 0},
		{"mikan input", mikan.ErrInput, "error", "SOURCE_RESPONSE_INVALID", 0},
		{"forbidden", &mikan.ResponseError{StatusCode: 403}, "unavailable", "SOURCE_UNAVAILABLE", 0},
		{"garden auth", &animefeed.HTTPError{Site: "garden", Status: 401}, "unavailable", "SOURCE_UNAVAILABLE", 0},
		{"captcha", mikan.ErrBlocked, "unavailable", "SOURCE_UNAVAILABLE", 0},
		{"budget", &mikan.LimitError{Kind: "requests"}, "error", "SOURCE_READ_BUDGET", 0},
		{"binding", mikan.ErrSourceBinding, "unavailable", "SOURCE_BINDING_CHANGED", 0},
		{"rate", &animefeed.HTTPError{Site: "anibt", Status: 429}, "cooldown", "SOURCE_RATE_LIMITED", 3600000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, db, r, src, now := discoveryServiceFixture(t)
			addDiscoveryServiceBackup(t, db, src, now)
			readErr := tc.err
			if tc.retry > 0 {
				response := *tc.err.(*animefeed.HTTPError)
				response.RetryAt = time.UnixMilli(now + tc.retry)
				readErr = &response
			}
			var reads atomic.Int32
			r.Feeds = animeRSSFunc(func(ctx context.Context, b *AnimeSourceBudget, in AnimeSourceInput) (AnimeSourceResult, error) {
				reads.Add(1)
				if in.Source.GroupID == "45" {
					return AnimeSourceResult{}, readErr
				}
				return discoveryServicePage(in, now, 1), nil
			})
			if err := r.PollOnce(context.Background()); err != nil {
				t.Fatal(err)
			}
			main, err := f.service.Store.GetAnimeSource(context.Background(), src.ID)
			if err != nil || main.Health != tc.health || main.ErrorCode == nil || *main.ErrorCode != tc.code || main.CheckedAt == nil || *main.CheckedAt != 1 {
				t.Fatal(main, err)
			}
			backup, err := f.service.Store.GetAnimeSource(context.Background(), "standby-source")
			if err != nil || backup.Health != "ok" || backup.CheckedAt == nil || reads.Load() != 2 {
				t.Fatal("failed main blocked standby", backup, reads.Load(), err)
			}
			if tc.retry > 0 && (main.NextCheckAt == nil || *main.NextCheckAt < now+tc.retry) {
				t.Fatal("Retry-After lost", main)
			}
		})
	}
}

func TestAnimeDiscoveryRunnerChecksBudgetAndPreservesSuccess(t *testing.T) {
	f, db, r, src, now := discoveryServiceFixture(t)
	if err := r.PollOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	var originalWindow string
	if err := db.QueryRow(`SELECT poll_snapshot_json FROM anime_subscription_sources WHERE id=?`, src.ID).Scan(&originalWindow); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE anime_subscription_sources SET next_check_at=?`, now); err != nil {
		t.Fatal(err)
	}
	r.Feeds = animeRSSFunc(func(ctx context.Context, b *AnimeSourceBudget, in AnimeSourceInput) (AnimeSourceResult, error) {
		if _, ok := ctx.Deadline(); !ok {
			t.Error("missing poll deadline")
		}
		for i := 0; i < 3; i++ {
			if _, err := b.startRequest(); err != nil {
				t.Error(err)
			}
			b.addBytes(1)
		}
		_, err := b.startRequest()
		return AnimeSourceResult{}, err
	})
	if err := r.PollOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	saved, err := f.service.Store.GetAnimeSource(context.Background(), src.ID)
	if err != nil || saved.Health != "error" || saved.CheckedAt == nil || *saved.CheckedAt != now || *saved.ErrorCode != "SOURCE_READ_BUDGET" {
		t.Fatal("failure discarded success", saved, err)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM anime_release_decisions WHERE disposition='selected'`).Scan(&n); err != nil || n != 1 {
		t.Fatal("failed refresh changed candidates", n, err)
	}
	var window string
	if err := db.QueryRow(`SELECT poll_snapshot_json FROM anime_subscription_sources WHERE id=?`, src.ID).Scan(&window); err != nil || window != originalWindow {
		t.Fatal("failure erased successful window", window, err)
	}
}

func TestAnimeDiscoveryRunnerBoundedVerificationAcrossEpisodes(t *testing.T) {
	f, db, r, _, now := discoveryServiceFixture(t)
	originalDirectories := f.service.Directories
	directoryReads := 0
	f.service.Directories = animeDownloadDirectoryFunc(func(ctx context.Context, cid, cookie string) (p115.DriveDirectory, error) {
		directoryReads++
		return originalDirectories.ReadDirectory(ctx, cid, cookie)
	})
	r.Feeds = animeRSSFunc(func(_ context.Context, _ *AnimeSourceBudget, in AnimeSourceInput) (AnimeSourceResult, error) {
		page := discoveryServicePage(in, now, 9)
		for i := range page.SourcePage.Releases {
			page.SourcePage.Releases[i].Title = fmt.Sprintf("[字幕组] 测试 - %02d [1080p]", 1+i)
		}
		return page, nil
	})
	var calls atomic.Int32
	f.service.Torrents = animeClaimTorrentFunc(func(_ context.Context, _, key, _ string) ([]byte, error) {
		calls.Add(1)
		return []byte(strings.Replace(string(animeClaimTestTorrent()), "name1:x", "name1:"+key[len(key)-1:], 1)), nil
	})
	if err := r.PollOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, want := range []int32{4, 8, 9} {
		if err := r.VerifyOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
		if calls.Load() != want {
			t.Fatal("unbounded or lost verification", calls.Load(), want)
		}
		if directoryReads != 0 {
			t.Fatal("same target reread within one bounded pass", directoryReads)
		}
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM anime_download_jobs`).Scan(&n); err != nil || n != int(want) {
			t.Fatal("independent episodes lost", n, want, err)
		}
	}
}

func TestAnimeDiscoveryRunnerOnlyHighestVersionAndSkipsLaterWithoutTorrentReads(t *testing.T) {
	f, db, runner, source, now := discoveryServiceFixture(t)
	var reads atomic.Int32
	f.service.Torrents = animeClaimTorrentFunc(func(context.Context, string, string, string) ([]byte, error) {
		reads.Add(1)
		return animeClaimTestTorrent(), nil
	})
	runner.Feeds = animeRSSFunc(func(_ context.Context, _ *AnimeSourceBudget, in AnimeSourceInput) (AnimeSourceResult, error) {
		page := discoveryServicePage(in, now, 3)
		for i, label := range []string{"", "v3", "v2"} {
			page.SourcePage.Releases[i].Title = "[字幕组] 动画 - 01" + label + " [1080p]"
		}
		return page, nil
	})
	if err := runner.PollOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := runner.VerifyOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if reads.Load() != 1 {
		t.Fatal("read lower versions' torrents", reads.Load())
	}
	var title string
	if err := db.QueryRow(`SELECT json_extract(snapshot_json,'$.release.title') FROM anime_download_jobs`).Scan(&title); err != nil || !strings.Contains(title, "01v3") {
		t.Fatal(title, err)
	}
	// The first job is only locally queued, yet its chosen version is final.
	now += 1000
	runner.now = func() time.Time { return time.UnixMilli(now) }
	if _, err := db.Exec(`UPDATE anime_subscription_sources SET next_check_at=? WHERE id=?`, now, source.ID); err != nil {
		t.Fatal(err)
	}
	runner.Feeds = animeRSSFunc(func(_ context.Context, _ *AnimeSourceBudget, in AnimeSourceInput) (AnimeSourceResult, error) {
		page := discoveryServicePage(in, now, 1)
		page.SourcePage.Releases[0].PostKey = strings.Repeat("4", 40)
		page.SourcePage.Releases[0].Title = "[字幕组] 动画 - 01v4 [1080p]"
		page.SourcePage.Releases[0].TorrentURL = "https://mikanani.me/Download/20261019/" + page.SourcePage.Releases[0].PostKey + ".torrent"
		return page, nil
	})
	if err := runner.PollOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := runner.VerifyOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if reads.Load() != 1 {
		t.Fatal("later version fetched torrent", reads.Load())
	}
	for query, want := range map[string]int{`SELECT count(*) FROM anime_download_jobs`: 1, `SELECT count(*) FROM anime_drive_operations`: 0} {
		var count int
		if err := db.QueryRow(query).Scan(&count); err != nil || count != want {
			t.Fatal(query, count, err)
		}
	}
}

func TestAnimeDiscoveryRunnerBoundsPollPassAndSkipsInactiveSources(t *testing.T) {
	_, db, r, src, now := discoveryServiceFixture(t)
	addDiscoveryServiceBackup(t, db, src, now)
	for i := 2; i <= 4; i++ {
		id := fmt.Sprintf("standby-%d", i)
		_, err := db.Exec(`INSERT INTO anime_subscription_sources(id,subscription_id,role,position,status,run_when_enabled,provider,source_key,item_id,group_id,group_name,namespace_id,binding_json,rules_json,revision,run_generation,health,created_at,updated_at) SELECT ?,subscription_id,'standby',?,'paused',1,provider,?,item_id,?,group_name,?,json_set(binding_json,'$.review.groupId',?),rules_json,1,1,'unknown',created_at,updated_at FROM anime_subscription_sources WHERE id=?`, id, i, "mikan:2259:"+fmt.Sprint(46+i), fmt.Sprint(46+i), id, fmt.Sprint(46+i), src.ID)
		if err != nil {
			t.Fatal(err)
		}

		if _, err := db.Exec(`UPDATE anime_subscription_sources SET status='enabled',next_check_at=? WHERE id=?`, now, id); err != nil {
			t.Fatal(err)
		}
	}
	var calls int
	r.Feeds = animeRSSFunc(func(_ context.Context, _ *AnimeSourceBudget, in AnimeSourceInput) (AnimeSourceResult, error) {
		calls++
		return discoveryServicePage(in, now, 0), nil
	})
	if err := r.PollOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 4 {
		t.Fatal("unbounded feed pass", calls)
	}
	if err := r.PollOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 5 {
		t.Fatal("later feed starved", calls)
	}
	if _, err := db.Exec(`UPDATE anime_subscription_sources SET next_check_at=?,run_when_enabled=0`, now); err != nil {
		t.Fatal(err)
	}
	if err := r.PollOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := r.VerifyOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 5 {
		t.Fatal("disabled feed fetched", calls)
	}
}

func TestAnimeDiscoveryRunnerFatalStoreErrorCancelsAndJoins(t *testing.T) {
	f, _, r, _, _ := discoveryServiceFixture(t)
	if err := f.service.Store.Close(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { done <- r.Run(ctx) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("database failure hidden")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runner did not join after fatal failure")
	}
}

func TestAnimeDiscoveryRunnerVerificationFailurePolicy(t *testing.T) {
	for _, mode := range []string{"temporary", "invalid", "claim write failure"} {
		t.Run(mode, func(t *testing.T) {
			f, db, r, _, now := discoveryServiceFixture(t)
			if err := r.PollOnce(context.Background()); err != nil {
				t.Fatal(err)
			}
			calls := 0
			f.service.Torrents = animeClaimTorrentFunc(func(context.Context, string, string, string) ([]byte, error) {
				calls++
				if mode == "temporary" {
					return nil, errors.New("Cookie=private")
				}
				if mode == "invalid" {
					return []byte("invalid torrent"), nil
				}
				return animeClaimTestTorrent(), nil
			})
			if mode == "claim write failure" {
				if _, err := db.Exec(`CREATE TRIGGER fail_claim BEFORE INSERT ON anime_download_jobs BEGIN SELECT RAISE(ABORT,'injected'); END`); err != nil {
					t.Fatal(err)
				}
			}
			err := r.VerifyOnce(context.Background())
			if mode == "claim write failure" {
				if err == nil {
					t.Fatal("database failure hidden")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := r.VerifyOnce(context.Background()); err != nil {
				t.Fatal(err)
			}
			if calls != 1 {
				t.Fatal("failed metadata immediately retried", calls)
			}
			r.now = func() time.Time { return time.UnixMilli(now + 60001) }
			if err := r.VerifyOnce(context.Background()); err != nil {
				t.Fatal(err)
			}
			want := 1
			if mode == "temporary" {
				want = 2
			}
			if calls != want {
				t.Fatal("wrong metadata retry policy", calls, want)
			}
			var code string
			if err := db.QueryRow(`SELECT reason_code FROM anime_release_decisions LIMIT 1`).Scan(&code); err != nil || strings.Contains(code, "private") {
				t.Fatal("unsafe reason", code, err)
			}
		})
	}
}

func TestAnimeDiscoveryRunnerStalePollAndInvalidResponseIsolation(t *testing.T) {
	for _, mode := range []string{"paused", "generation", "target", "invalid page"} {
		t.Run(mode, func(t *testing.T) {
			_, db, r, _, now := discoveryServiceFixture(t)
			r.Feeds = animeRSSFunc(func(_ context.Context, _ *AnimeSourceBudget, in AnimeSourceInput) (AnimeSourceResult, error) {
				page := discoveryServicePage(in, now, 1)
				query := map[string]string{"paused": `UPDATE anime_subscriptions SET status='paused'`, "generation": `UPDATE anime_subscription_sources SET run_generation=run_generation+1`, "target": `UPDATE anime_settings SET revision=revision+1,download_cid='999'`}[mode]
				if query != "" {
					if _, err := db.Exec(query); err != nil {
						t.Error(err)
					}
				}
				if mode == "invalid page" {
					page.RawCount = 2001
				}
				return page, nil
			})
			if err := r.PollOnce(context.Background()); err != nil {
				t.Fatal(err)
			}
			var n int
			if err := db.QueryRow(`SELECT count(*) FROM anime_releases`).Scan(&n); err != nil || n != 0 {
				t.Fatal("stale/invalid observation committed", n, err)
			}
		})
	}
}

func TestAnimeDiscoveryRunnerRunCancellationWaitsForBothLanes(t *testing.T) {
	f, db, r, _, now := discoveryServiceFixture(t)
	if err := r.PollOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE anime_subscription_sources SET next_check_at=?`, now); err != nil {
		t.Fatal(err)
	}
	pollStarted, verifyStarted := make(chan struct{}), make(chan struct{})
	var pollOnce, verifyOnce sync.Once
	var exited atomic.Int32
	r.Feeds = animeRSSFunc(func(ctx context.Context, _ *AnimeSourceBudget, _ AnimeSourceInput) (AnimeSourceResult, error) {
		pollOnce.Do(func() { close(pollStarted) })
		<-ctx.Done()
		exited.Add(1)
		return AnimeSourceResult{}, ctx.Err()
	})
	f.service.Torrents = animeClaimTorrentFunc(func(ctx context.Context, _, _, _ string) ([]byte, error) {
		verifyOnce.Do(func() { close(verifyStarted) })
		<-ctx.Done()
		exited.Add(1)
		return nil, ctx.Err()
	})
	r.now = nil
	r.interval = time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()
	for _, started := range []chan struct{}{pollStarted, verifyStarted} {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("lane did not start")
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runner did not wait/exit")
	}
	if exited.Load() != 2 {
		t.Fatal("unjoined work", exited.Load())
	}
	for _, query := range []string{`SELECT count(*) FROM anime_subscription_sources WHERE check_token IS NOT NULL`, `SELECT count(*) FROM anime_release_decisions WHERE lease_token IS NOT NULL`} {
		var n int
		if err := db.QueryRow(query).Scan(&n); err != nil || n != 0 {
			t.Fatal("cancel leaked reservation", query, n, err)
		}
	}
}

func TestAnimeDiscoveryCommitWakesVerifierWithoutAnotherRSSRead(t *testing.T) {
	f, db, r, _, now := discoveryServiceFixture(t)
	r.interval = time.Hour // The test cannot succeed through the fallback tick.
	feedStarted, allowFeed, verified := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var feedCalls atomic.Int32
	var verifiedOnce sync.Once
	r.Feeds = animeRSSFunc(func(ctx context.Context, _ *AnimeSourceBudget, in AnimeSourceInput) (AnimeSourceResult, error) {
		if feedCalls.Add(1) == 1 {
			close(feedStarted)
		}
		select {
		case <-ctx.Done():
			return AnimeSourceResult{}, ctx.Err()
		case <-allowFeed:
			return discoveryServicePage(in, now, 1), nil
		}
	})
	f.service.Torrents = animeClaimTorrentFunc(func(context.Context, string, string, string) ([]byte, error) {
		verifiedOnce.Do(func() { close(verified) })
		return animeClaimTestTorrent(), nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(2 * time.Second):
			t.Error("discovery lanes did not stop")
		}
	})
	select {
	case <-feedStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("RSS lane did not start")
	}
	// Let the empty verifier finish its initial pass while RSS is blocked.
	time.Sleep(50 * time.Millisecond)
	close(allowFeed)
	select {
	case <-verified:
	case <-time.After(2 * time.Second):
		t.Fatal("committed RSS selections waited for the verifier timer")
	}
	for deadline := time.Now().Add(2 * time.Second); ; {
		var jobs int
		if err := db.QueryRow(`SELECT count(*) FROM anime_download_jobs`).Scan(&jobs); err != nil {
			t.Fatal(err)
		}
		if jobs == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("woken verification did not commit the download intent")
		}
		time.Sleep(5 * time.Millisecond)
	}
	for i := 0; i < 100; i++ {
		f.service.wakeAnimeVerification()
	}
	if feedCalls.Load() != 1 || len(f.service.animeVerificationWakeSignal()) > 1 {
		t.Fatal("verification notifications caused extra RSS reads or unbounded queuing")
	}
}

func TestAnimeDiscoveryDrainsDueSourcesWithoutSchedulerSleep(t *testing.T) {
	f, db, r, source, now := discoveryServiceFixture(t)
	for i := 1; i <= 4; i++ {
		id := fmt.Sprintf("%032x", i+100)
		group := fmt.Sprint(45 + i)
		_, err := db.Exec(`INSERT INTO anime_subscription_sources(id,subscription_id,role,position,status,run_when_enabled,provider,source_key,item_id,group_id,group_name,namespace_id,binding_json,rules_json,revision,run_generation,health,checked_at,next_check_at,created_at,updated_at) SELECT ?,subscription_id,'standby',?,'enabled',1,provider,?,item_id,?,group_name,?,json_set(binding_json,'$.review.groupId',?),rules_json,1,1,'unknown',1,?,created_at,updated_at FROM anime_subscription_sources WHERE id=?`, id, i, "mikan:2259:"+group, group, id, group, now, source.ID)
		if err != nil {
			t.Fatal(err)
		}

	}
	// Consume any fixture notification: only the bounded batch's own wakeup
	// can process the fifth source while the fallback timer is one hour long.
	select {
	case <-f.service.animeDiscoveryWakeSignal():
	default:
	}
	r.interval = time.Hour
	var calls atomic.Int32
	lastRead := make(chan struct{})
	r.Feeds = animeRSSFunc(func(_ context.Context, _ *AnimeSourceBudget, in AnimeSourceInput) (AnimeSourceResult, error) {
		if calls.Add(1) == 5 {
			close(lastRead)
		}
		return discoveryServicePage(in, now, 0), nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(2 * time.Second):
			t.Error("discovery did not stop")
		}
	})
	select {
	case <-lastRead:
	case <-time.After(2 * time.Second):
		t.Fatal("fifth due source waited for the scheduler timer")
	}
}

func TestAnimeDiscoveryRunnerReusesInteractiveMikanCache(t *testing.T) {
	_, _, r, _, now := discoveryServiceFixture(t)
	m, transport := newAnimeMikanTestService()
	bindings := &AnimeBindingService{Mikan: m}
	transport.read = func(_ context.Context, in mikan.SourceRequest) (mikan.SourcePage, error) {
		if in.Kind == "entry" {
			return animeMikanFakePage(in), nil
		}
		if in.Kind != "rss" || in.Take != 0 {
			t.Error("scheduler loaded history")
		}
		return discoveryServicePage(AnimeSourceInput{Source: in.Source, Kind: "rss"}, now, 1).SourcePage, nil
	}
	r.Feeds = bindings
	// An interactive read and subsequent scheduler pass share the actual
	// application reader, not a second polling cache or a new site gate.
	in := AnimeSourceInput{Source: animefeed.NewSource("mikan", "2259", "45"), Kind: "rss"}
	if _, err := bindings.ReadSource(context.Background(), animeMikanTestBudget(t, nil), in); err != nil {
		t.Fatal(err)
	}
	if err := r.PollOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if transport.calls.Load() != 2 {
		t.Fatal("UI and worker repeated upstream reads", transport.calls.Load())
	}
	if err := r.VerifyOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
}
