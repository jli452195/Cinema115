package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"strm115/backend/internal/animeparse"
	"strm115/backend/internal/animepreview"
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
	f, db, session, source, _, _ := animeClaimServiceFixture(t)
	now := time.Now().UnixMilli()
	baseline := model.AnimeExecutionBaseline{SchemaVersion: 1, Target: session.Target(), RuleVersion: animepreview.RuleVersion, ParserVersion: animeparse.ParserVersion, TimeVersion: animeparse.TimeVersion}
	raw, err := json.Marshal(baseline)
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{`DELETE FROM anime_release_decisions`, `DELETE FROM anime_releases`} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`UPDATE anime_source_baselines SET t0=?,snapshot_json=?`, now-3600000, string(raw)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE anime_subscription_sources SET next_check_at=?`, now-1); err != nil {
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
	_, err = db.Exec(`INSERT INTO anime_source_baselines(id,source_id,source_revision,t0,confirmed_at,fingerprint,snapshot_json) SELECT 'standby-baseline','standby-source',1,t0,confirmed_at,fingerprint,snapshot_json FROM anime_source_baselines WHERE source_id=?`, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE anime_subscription_sources SET status='enabled',active_baseline_id='standby-baseline',next_check_at=? WHERE id='standby-source'`, now); err != nil {
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
		{"forbidden", &mikan.ResponseError{StatusCode: 403}, "unavailable", "SOURCE_UNAVAILABLE", 0},
		{"garden auth", &animefeed.HTTPError{Site: "garden", Status: 401}, "unavailable", "SOURCE_UNAVAILABLE", 0},
		{"captcha", mikan.ErrBlocked, "unavailable", "SOURCE_UNAVAILABLE", 0},
		{"budget", &mikan.LimitError{Kind: "requests"}, "error", "SOURCE_READ_BUDGET", 0},
		{"binding", mikan.ErrSourceBinding, "unavailable", "SOURCE_BINDING_CHANGED", 0},
		{"rate", &animefeed.HTTPError{Site: "anibt", Status: 429, RetryAt: time.Now().Add(time.Hour)}, "cooldown", "SOURCE_RATE_LIMITED", 3600000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, db, r, src, now := discoveryServiceFixture(t)
			addDiscoveryServiceBackup(t, db, src, now)
			var reads atomic.Int32
			r.Feeds = animeRSSFunc(func(ctx context.Context, b *AnimeSourceBudget, in AnimeSourceInput) (AnimeSourceResult, error) {
				reads.Add(1)
				if in.Source.GroupID == "45" {
					return AnimeSourceResult{}, tc.err
				}
				return discoveryServicePage(in, now, 1), nil
			})
			if err := r.PollOnce(context.Background()); err != nil {
				t.Fatal(err)
			}
			main, err := f.service.Store.GetAnimeSource(context.Background(), src.ID)
			if err != nil || main.Health != tc.health || main.ErrorCode == nil || *main.ErrorCode != tc.code || main.CheckedAt != nil {
				t.Fatal(main, err)
			}
			backup, err := f.service.Store.GetAnimeSource(context.Background(), "standby-source")
			if err != nil || backup.Health != "ok" || backup.CheckedAt == nil || reads.Load() != 2 {
				t.Fatal("failed main blocked standby", backup, reads.Load(), err)
			}
			if tc.retry > 0 && (main.NextCheckAt == nil || *main.NextCheckAt < now+tc.retry-1000) {
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

func TestAnimeDiscoveryRunnerBoundedVerificationAndSameEpisodeVersions(t *testing.T) {
	f, db, r, _, now := discoveryServiceFixture(t)
	originalDirectories := f.service.Directories
	directoryReads := 0
	f.service.Directories = animeDownloadDirectoryFunc(func(ctx context.Context, cid, cookie string) (p115.DriveDirectory, error) {
		directoryReads++
		return originalDirectories.ReadDirectory(ctx, cid, cookie)
	})
	r.Feeds = animeRSSFunc(func(_ context.Context, _ *AnimeSourceBudget, in AnimeSourceInput) (AnimeSourceResult, error) {
		return discoveryServicePage(in, now, 9), nil
	})
	var calls atomic.Int32
	f.service.Torrents = animeClaimTorrentFunc(func(_ context.Context, _, key, _ string) ([]byte, error) {
		calls.Add(1)
		return []byte(strings.Replace(string(animeClaimTestTorrent()), "name1:x", "name1:"+key[len(key)-1:], 1)), nil
	})
	if err := r.PollOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	for round, want := range []int32{4, 8, 9} {
		if err := r.VerifyOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
		if calls.Load() != want {
			t.Fatal("unbounded or lost verification", calls.Load(), want)
		}
		if directoryReads != round+1 {
			t.Fatal("same target reread within one bounded pass", directoryReads)
		}
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM anime_download_jobs`).Scan(&n); err != nil || n != int(want) {
			t.Fatal("same-episode variants collapsed", n, want, err)
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
		if _, err := db.Exec(`INSERT INTO anime_source_baselines(id,source_id,source_revision,t0,confirmed_at,fingerprint,snapshot_json) SELECT ?,?,1,t0,confirmed_at,fingerprint,snapshot_json FROM anime_source_baselines WHERE source_id=?`, id, id, src.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`UPDATE anime_subscription_sources SET status='enabled',active_baseline_id=?,next_check_at=? WHERE id=?`, id, now, id); err != nil {
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
			var calls int
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
	for _, mode := range []string{"paused", "generation", "target", "invalid page", "invalid baseline"} {
		t.Run(mode, func(t *testing.T) {
			_, db, r, _, now := discoveryServiceFixture(t)
			var calls int
			if mode == "invalid baseline" {
				if _, err := db.Exec(`UPDATE anime_source_baselines SET snapshot_json='{}'`); err != nil {
					t.Fatal(err)
				}
			}
			r.Feeds = animeRSSFunc(func(_ context.Context, _ *AnimeSourceBudget, in AnimeSourceInput) (AnimeSourceResult, error) {
				calls++
				page := discoveryServicePage(in, now, 1)
				query := map[string]string{"paused": `UPDATE anime_subscriptions SET status='paused'`, "generation": `UPDATE anime_subscription_sources SET run_generation=run_generation+1`, "target": `UPDATE anime_settings SET revision=revision+1`}[mode]
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
			if mode == "invalid baseline" && calls != 0 {
				t.Fatal("unconfirmed baseline fetched", calls)
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
	f.service.Directories = animeDownloadDirectoryFunc(func(ctx context.Context, _, _ string) (p115.DriveDirectory, error) {
		verifyOnce.Do(func() { close(verifyStarted) })
		<-ctx.Done()
		exited.Add(1)
		return p115.DriveDirectory{}, ctx.Err()
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
