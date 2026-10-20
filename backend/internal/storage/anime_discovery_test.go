package storage

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"strm115/backend/internal/animeparse"
	"strm115/backend/internal/animepreview"
	"strm115/backend/internal/animeresource"
	"strm115/backend/internal/client/animefeed"
	"strm115/backend/internal/model"
)

func discoveryFixture(t *testing.T) (*downloadClaimFixture, int64) {
	t.Helper()
	f := newDownloadClaimFixture(t)
	now := time.Now().UnixMilli()
	baseline := model.AnimeExecutionBaseline{SchemaVersion: 1, Target: f.target, RuleVersion: animepreview.RuleVersion, ParserVersion: animeparse.ParserVersion, TimeVersion: animeparse.TimeVersion}
	raw, err := animeJSON(baseline)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.db.Exec(`UPDATE anime_source_baselines SET snapshot_json=?`, raw); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.db.Exec(`UPDATE anime_subscription_sources SET next_check_at=?`, now-1); err != nil {
		t.Fatal(err)
	}
	return f, now
}

func discoveryRow(key string, now int64) animefeed.SourceRelease {
	return animefeed.SourceRelease{PostKey: key, Title: "[字幕组] 动画 - 30 [1080p][HEVC]", PublishedRaw: time.UnixMilli(now).UTC().Format(time.RFC3339Nano), MagnetHint: "magnet:?xt=urn:btih:" + strings.Repeat("a", 40)}
}

func discoveryPoll(t *testing.T, s *Store, now int64) AnimeSourcePoll {
	t.Helper()
	p, found, err := s.ReserveAnimeSourcePoll(context.Background(), now)
	if err != nil || !found || p.IssueCode != "" {
		t.Fatal("poll not ready", found, p.IssueCode, err)
	}
	return p
}

func discoveryPage(p AnimeSourcePoll, now int64, rows ...animefeed.SourceRelease) animefeed.SourcePage {
	return animefeed.SourcePage{Kind: "rss", Source: animefeed.NewSource(p.Source.Provider, *p.Source.ItemID, *p.Source.GroupID), RawCount: len(rows), Releases: rows, ObservedAt: time.UnixMilli(now), Completeness: "partial", StopReason: "rss_window"}
}

func TestAnimeDiscoveryBaselineAndIndependentDecisions(t *testing.T) {
	f, now := discoveryFixture(t)
	p := discoveryPoll(t, f.store, now)
	rows := []animefeed.SourceRelease{discoveryRow("rel_current", now), discoveryRow("rel_ancient", 1), discoveryRow("rel_future", now+360001), discoveryRow("rel_missing", now), discoveryRow("rel_filter", now)}
	for i := range rows {
		rows[i].Title = fmt.Sprintf("[group] Anime - %d [1080p][HEVC]", 30+i)
	}
	rows[3].PublishedRaw = ""
	rows[4].Title = "[group] Anime - 34 [720p][AVC]"
	stats, err := f.store.CompleteAnimeSourcePoll(context.Background(), p, discoveryPage(p, now, rows...), now)
	if err != nil || stats.Observed != 5 || stats.Selected != 4 || stats.Review != 0 {
		t.Fatal(stats, err)
	}
	want := map[string]string{"rel_current": "selected", "rel_ancient": "selected", "rel_future": "selected", "rel_missing": "selected", "rel_filter": "filtered"}
	for key, disposition := range want {
		var id string
		if err := f.store.db.QueryRow(`SELECT id FROM anime_releases WHERE post_key=?`, key).Scan(&id); err != nil {
			t.Fatal(err)
		}
		d, err := f.store.GetAnimeReleaseDecision(context.Background(), p.Source.ID, id)
		if err != nil || d.Disposition != disposition {
			t.Fatal(key, d.Disposition, err)
		}
		if disposition == "selected" && (d.Snapshot.Selection == nil || d.Snapshot.Selection.Target != f.target || d.Snapshot.Selection.BaselineID != *p.Source.ActiveBaselineID) {
			t.Fatal("missing execution basis", d)
		}
	}
	// The other publisher has its own baseline and rules, even for the same
	// post. No primary success, history set or episode result is inherited.
	other := discoveryPoll(t, f.store, now)
	if other.Source.ID == p.Source.ID {
		t.Fatal("success feed remained due")
	}
	stats, err = f.store.CompleteAnimeSourcePoll(context.Background(), other, discoveryPage(other, now, rows[0]), now)
	if err != nil || stats.Selected != 1 {
		t.Fatal("independent standby skipped", stats, err)
	}
}

func TestAnimeDiscoveryReservationsConcurrentAndFenced(t *testing.T) {
	f, now := discoveryFixture(t)
	if _, err := f.store.db.Exec(`UPDATE anime_subscription_sources SET status='paused' WHERE id=?`, f.sub.Sources[1].ID); err != nil {
		t.Fatal(err)
	}
	var count atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, found, err := f.store.ReserveAnimeSourcePoll(context.Background(), now)
			if err != nil {
				t.Error(err)
			}
			if found {
				count.Add(1)
			}
		}()
	}
	wg.Wait()
	if count.Load() != 1 {
		t.Fatal("duplicate poll", count.Load())
	}
	oldSource, err := f.store.GetAnimeSource(context.Background(), f.sub.Sources[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	// An expired poll can be replaced; its late result cannot advance or
	// release the newer owner's reservation.
	newPoll := discoveryPoll(t, f.store, now+animeDiscoveryLease+1)
	old := newPoll
	old.Token = *oldSource.CheckToken
	if _, err := f.store.CompleteAnimeSourcePoll(context.Background(), old, discoveryPage(old, now, discoveryRow("rel_late", now)), now+animeDiscoveryLease+1); !errors.Is(err, ErrAnimeConflict) {
		t.Fatal("old token accepted", err)
	}
	if err := f.store.FailAnimeSourcePoll(context.Background(), old, "SOURCE_READ_FAILED", 0, false, now+animeDiscoveryLease+1); !errors.Is(err, ErrAnimeConflict) {
		t.Fatal("old token released new owner", err)
	}
	if _, err := f.store.CompleteAnimeSourcePoll(context.Background(), newPoll, discoveryPage(newPoll, now), now+animeDiscoveryLease+1); err != nil {
		t.Fatal(err)
	}
}

func TestAnimeDiscoveryMutationAndTransactionRollback(t *testing.T) {
	for _, mode := range []string{"pause", "source generation", "parent generation", "revision", "target", "write failure"} {
		t.Run(mode, func(t *testing.T) {
			f, now := discoveryFixture(t)
			p := discoveryPoll(t, f.store, now)
			query := map[string]string{"pause": `UPDATE anime_subscriptions SET status='paused'`, "source generation": `UPDATE anime_subscription_sources SET run_generation=run_generation+1`, "parent generation": `UPDATE anime_subscriptions SET run_generation=run_generation+1`, "revision": `UPDATE anime_subscription_sources SET revision=revision+1`, "target": `UPDATE anime_settings SET revision=revision+1,download_cid='999'`, "write failure": `CREATE TRIGGER fail_poll BEFORE INSERT ON anime_release_decisions BEGIN SELECT RAISE(ABORT,'injected'); END`}[mode]
			if _, err := f.store.db.Exec(query); err != nil {
				t.Fatal(err)
			}
			if _, err := f.store.CompleteAnimeSourcePoll(context.Background(), p, discoveryPage(p, now, discoveryRow("rel_new", now)), now); err == nil {
				t.Fatal("stale/failing poll committed")
			}
			var n int
			if err := f.store.db.QueryRow(`SELECT count(*) FROM anime_releases`).Scan(&n); err != nil || n != 0 {
				t.Fatal("partial observations escaped", n, err)
			}
			source, err := f.store.GetAnimeSource(context.Background(), p.Source.ID)
			if err != nil || source.CheckedAt != nil || source.CheckToken == nil || *source.CheckToken != p.Token {
				t.Fatal("failed transaction changed success/ownership", source, err)
			}
		})
	}
}

func TestAnimeDiscoveryBackoffStaggerUnavailableAndCancellation(t *testing.T) {
	f, now := discoveryFixture(t)
	if _, err := f.store.db.Exec(`UPDATE anime_subscription_sources SET next_check_at=NULL`); err != nil {
		t.Fatal(err)
	}
	_, found, err := f.store.ReserveAnimeSourcePoll(context.Background(), now)
	if err != nil || found {
		t.Fatal(found, err)
	}
	for _, src := range f.sub.Sources {
		current, err := f.store.GetAnimeSource(context.Background(), src.ID)
		if err != nil || current.NextCheckAt == nil || *current.NextCheckAt < now || *current.NextCheckAt > now+60000 {
			t.Fatal("invalid initial spread", current, err)
		}
	}
	if _, err := f.store.db.Exec(`UPDATE anime_subscription_sources SET status='paused' WHERE id=?`, f.sub.Sources[1].ID); err != nil {
		t.Fatal(err)
	}
	now += 60001
	for i := 1; i <= 5; i++ {
		p := discoveryPoll(t, f.store, now)
		retryAt := int64(0)
		if i == 1 {
			retryAt = now + 3600000
		}
		if err := f.store.FailAnimeSourcePoll(context.Background(), p, "SOURCE_RATE_LIMITED", retryAt, false, now); err != nil {
			t.Fatal(err)
		}
		src, err := f.store.GetAnimeSource(context.Background(), p.Source.ID)
		minimum := now + animeDiscoveryBackoff(i)
		if err != nil || src.NextCheckAt == nil || *src.NextCheckAt < max(minimum, retryAt) {
			t.Fatal("backoff/Retry-After ignored", src, err)
		}
		if _, found, err := f.store.ReserveAnimeSourcePoll(context.Background(), *src.NextCheckAt-1); err != nil || found {
			t.Fatal("early retry", found, err)
		}
		now = *src.NextCheckAt
	}
	p := discoveryPoll(t, f.store, now)
	if err := f.store.FailAnimeSourcePoll(context.Background(), p, "POLL_CANCELLED", 0, false, now); err != nil {
		t.Fatal(err)
	}
	p = discoveryPoll(t, f.store, now+5000)
	if err := f.store.FailAnimeSourcePoll(context.Background(), p, "SOURCE_UNAVAILABLE", 0, true, now+5000); err != nil {
		t.Fatal(err)
	}
	if _, found, err := f.store.ReserveAnimeSourcePoll(context.Background(), now+86400000); err != nil || found {
		t.Fatal("unavailable feed automatically probed", found, err)
	}
}

func TestAnimeDiscoveryFrozenChoicesAndPersistentVerification(t *testing.T) {
	f, now := discoveryFixture(t)
	p := discoveryPoll(t, f.store, now)
	if _, err := f.store.db.Exec(`UPDATE anime_subscription_sources SET status='paused' WHERE id<>?`, p.Source.ID); err != nil {
		t.Fatal(err)
	}
	page := discoveryPage(p, now, discoveryRow("rel_pending", now))
	if _, err := f.store.CompleteAnimeSourcePoll(context.Background(), p, page, now); err != nil {
		t.Fatal(err)
	}
	work, found, err := f.store.ReserveAnimeDiscovery(context.Background(), now)
	if err != nil || !found {
		t.Fatal(found, err)
	}
	// A fresh process resumes the persisted exact observation, including after
	// the release leaves the RSS window. It does not need to fetch the feed.
	var path string
	if err := f.store.db.QueryRow(`SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&path); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	f.store = reopened
	if _, found, err := reopened.ReserveAnimeDiscovery(context.Background(), now+1); err != nil || found {
		t.Fatal("live verifier duplicated", found, err)
	}
	newWork, found, err := reopened.ReserveAnimeDiscovery(context.Background(), now+animeDiscoveryLease+1)
	if err != nil || !found || !reflect.DeepEqual(newWork.Observation, work.Observation) || newWork.Decision.Generation <= work.Decision.Generation {
		t.Fatal("pending work lost", newWork, found, err)
	}
	v, err := animeresource.Verify(context.Background(), "garden", newWork.Observation, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.ClaimAnimeDownload(context.Background(), work.Decision.SourceID, work.Decision.ReleaseID, work.Decision.Generation, v, f.target); !errors.Is(err, ErrAnimeConflict) {
		t.Fatal("old verifier created intent", err)
	}
	if _, err := reopened.ClaimAnimeDownload(context.Background(), newWork.Decision.SourceID, newWork.Decision.ReleaseID, newWork.Decision.Generation, v, f.target); err != nil {
		t.Fatal(err)
	}
	if err := reopened.CompleteAnimeDiscovery(context.Background(), newWork, "", false, now+animeDiscoveryLease+2); err != nil {
		t.Fatal(err)
	}
	assertDownloadCounts(t, reopened, 1, 1, 1, 1)
	if _, found, err := reopened.ReserveAnimeDiscovery(context.Background(), now+86400000); err != nil || found {
		t.Fatal("claimed work scheduled again", found, err)
	}
	// Polling the same post again retains its original authorization snapshot.
	before, err := reopened.GetAnimeReleaseDecision(context.Background(), newWork.Decision.SourceID, newWork.Decision.ReleaseID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.db.Exec(`UPDATE anime_subscription_sources SET next_check_at=? WHERE id=?`, now+1, p.Source.ID); err != nil {
		t.Fatal(err)
	}
	p2 := discoveryPoll(t, reopened, now+2)
	if p2.Source.ID != p.Source.ID {
		t.Fatal("unexpected source")
	}
	page.ObservedAt = time.UnixMilli(now + 2)
	if _, err := reopened.CompleteAnimeSourcePoll(context.Background(), p2, page, now+2); err != nil {
		t.Fatal(err)
	}
	after, err := reopened.GetAnimeReleaseDecision(context.Background(), before.SourceID, before.ReleaseID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("repeat poll replaced frozen choice", err)
	}
}

func TestAnimeDiscoveryPendingRetryAndExplicitDecisions(t *testing.T) {
	for _, mode := range []string{"retry", "cancel", "review", "dismissed", "filtered"} {
		t.Run(mode, func(t *testing.T) {
			f, now := discoveryFixture(t)
			p := discoveryPoll(t, f.store, now)
			if _, err := f.store.CompleteAnimeSourcePoll(context.Background(), p, discoveryPage(p, now, discoveryRow("rel_pending", now)), now); err != nil {
				t.Fatal(err)
			}
			work, found, err := f.store.ReserveAnimeDiscovery(context.Background(), now)
			if err != nil || !found {
				t.Fatal(found, err)
			}
			if mode == "retry" || mode == "cancel" || mode == "review" {
				code, retry := "RESOURCE_REVIEW_REQUIRED", false
				if mode == "retry" {
					code, retry = "RESOURCE_READ_FAILED", true
				}
				if mode == "cancel" {
					code = "DISCOVERY_CANCELLED"
				}
				if err := f.store.CompleteAnimeDiscovery(context.Background(), work, code, retry, now); err != nil {
					t.Fatal(err)
				}
				_, due, err := f.store.ReserveAnimeDiscovery(context.Background(), now+1)
				if err != nil || due {
					t.Fatal("immediate retry", due, err)
				}
				_, due, err = f.store.ReserveAnimeDiscovery(context.Background(), now+60001)
				if err != nil || due != (mode != "review") {
					t.Fatal("wrong retry policy", due, err)
				}
			} else {
				if _, err := f.store.db.Exec(`UPDATE anime_release_decisions SET disposition=?,lease_token=NULL,lease_until=NULL,generation=generation+1 WHERE source_id=?`, mode, p.Source.ID); err != nil {
					t.Fatal(err)
				}
				if err := f.store.CompleteAnimeDiscovery(context.Background(), work, "RESOURCE_READ_FAILED", true, now); !errors.Is(err, ErrAnimeConflict) {
					t.Fatal("old verifier restored explicit decision", err)
				}
				if _, err := f.store.db.Exec(`UPDATE anime_subscription_sources SET next_check_at=? WHERE id=?`, now, p.Source.ID); err != nil {
					t.Fatal(err)
				}
				p2 := discoveryPoll(t, f.store, now)
				if _, err := f.store.CompleteAnimeSourcePoll(context.Background(), p2, discoveryPage(p2, now, discoveryRow("rel_pending", now)), now); err != nil {
					t.Fatal(err)
				}
				d, err := f.store.GetAnimeReleaseDecision(context.Background(), work.Decision.SourceID, work.Decision.ReleaseID)
				if err != nil || d.Disposition != mode {
					t.Fatal("poll restored excluded choice", d, err)
				}
			}
		})
	}
}

func TestAnimeDiscoveryRejectsInvalidBaselineWithoutRequest(t *testing.T) {
	for _, mode := range []string{"legacy", "parser", "target"} {
		t.Run(mode, func(t *testing.T) {
			f, now := discoveryFixture(t)
			query := map[string]string{"legacy": `UPDATE anime_source_baselines SET snapshot_json='{}'`, "parser": `UPDATE anime_source_baselines SET snapshot_json=json_set(snapshot_json,'$.parserVersion',1)`, "target": `UPDATE anime_settings SET revision=revision+1,download_cid='999'`}[mode]
			if _, err := f.store.db.Exec(query); err != nil {
				t.Fatal(err)
			}
			p, found, err := f.store.ReserveAnimeSourcePoll(context.Background(), now)
			if err != nil || !found || p.IssueCode == "" {
				t.Fatal("invalid baseline authorized", p.IssueCode, found, err)
			}
			if _, err := f.store.CompleteAnimeSourcePoll(context.Background(), p, discoveryPage(p, now), now); !errors.Is(err, ErrAnimeInvalid) {
				t.Fatal(err)
			}
		})
	}
}

func TestAnimeDiscoveryCommittedClaimReceiptRecovery(t *testing.T) {
	f, now := discoveryFixture(t)
	p := discoveryPoll(t, f.store, now)
	if _, err := f.store.CompleteAnimeSourcePoll(context.Background(), p, discoveryPage(p, now, discoveryRow("rel_pending", now)), now); err != nil {
		t.Fatal(err)
	}
	work, found, err := f.store.ReserveAnimeDiscovery(context.Background(), now)
	if err != nil || !found {
		t.Fatal(found, err)
	}
	v, err := animeresource.Verify(context.Background(), "garden", work.Observation, nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := f.store.ClaimAnimeDownload(context.Background(), work.Decision.SourceID, work.Decision.ReleaseID, work.Decision.Generation, v, f.target)
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
	reopened, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if _, found, err := reopened.ReserveAnimeDiscovery(context.Background(), now+86400000); err != nil || found {
		t.Fatal("lost receipt repeated claim", found, err)
	}
	// Even a late local error cannot demote a committed resource/job.
	if err := reopened.CompleteAnimeDiscovery(context.Background(), work, "RESOURCE_READ_FAILED", true, now+1000); err != nil {
		t.Fatal(err)
	}
	saved, err := reopened.GetAnimeDownloadJob(context.Background(), result.Job.ID)
	if err != nil || !reflect.DeepEqual(saved, result.Job) {
		t.Fatal("late failure changed job", err)
	}
	assertDownloadCounts(t, reopened, 1, 1, 1, 1)
}
