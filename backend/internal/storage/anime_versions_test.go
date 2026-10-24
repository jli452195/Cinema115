package storage

import (
	"context"
	"testing"
	"time"

	"strm115/backend/internal/anime/preview"
	"strm115/backend/internal/client/animefeed"
	"strm115/backend/internal/model"
)

func TestAnimeDiscoveryCurrentHighestAndNoFutureUpgradeAfterRestart(t *testing.T) {
	ctx := context.Background()
	f, now := discoveryFixture(t)
	poll := discoveryPoll(t, f.store, now)
	if _, err := f.store.db.Exec(`UPDATE anime_subscription_sources SET status='paused' WHERE id<>?`, poll.Source.ID); err != nil {
		t.Fatal(err)
	}
	rows := make([]animefeed.SourceRelease, 0, 3)
	for _, version := range []string{"v2", "v3", ""} {
		row := discoveryRow("rel_initial_"+version, now)
		row.Title = "[字幕组] 动画 - 30" + version + " [1080p][HEVC]"
		rows = append(rows, row)
	}
	stats, err := f.store.CompleteAnimeSourcePoll(ctx, poll, discoveryPage(poll, now, rows...), now)
	if err != nil || stats.Selected != 1 {
		t.Fatal(stats, err)
	}
	var selectedTitle string
	if err := f.store.db.QueryRow(`SELECT r.title FROM anime_release_decisions d JOIN anime_releases r ON r.id=d.release_id WHERE d.source_id=? AND d.disposition='selected'`, poll.Source.ID).Scan(&selectedTitle); err != nil || selectedTitle != rows[1].Title {
		t.Fatal(selectedTitle, err)
	}
	var dbPath string
	if err := f.store.db.QueryRow(`SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&dbPath); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	f.store = reopened
	now += 2000
	if _, err := reopened.db.Exec(`UPDATE anime_subscription_sources SET next_check_at=? WHERE id=?`, now-1, poll.Source.ID); err != nil {
		t.Fatal(err)
	}
	poll = discoveryPoll(t, reopened, now)
	upgrade, otherSpec, nextEpisode := discoveryRow("rel_later_v4", now), discoveryRow("rel_other_spec", now), discoveryRow("rel_next_episode", now)
	upgrade.Title, otherSpec.Title, nextEpisode.Title = "[字幕组] 动画 - 30v4 [1080p][HEVC]", "[字幕组] 动画 - 30v2 [1080p][HEVC][简体内嵌]", "[字幕组] 动画 - 31v2 [1080p][HEVC]"
	stats, err = reopened.CompleteAnimeSourcePoll(ctx, poll, discoveryPage(poll, now, upgrade, otherSpec, nextEpisode), now)
	if err != nil || stats.Selected != 2 {
		t.Fatal(stats, err)
	}
	var reason, disposition string
	if err := reopened.db.QueryRow(`SELECT d.disposition,d.reason_code FROM anime_release_decisions d JOIN anime_releases r ON r.id=d.release_id WHERE r.post_key='rel_later_v4' AND d.source_id=?`, poll.Source.ID).Scan(&disposition, &reason); err != nil || disposition != "filtered" || reason != "RELEASE_EDITION_ALREADY_SELECTED" {
		t.Fatal(disposition, reason, err)
	}
	var jobs int
	if err := reopened.db.QueryRow(`SELECT count(*) FROM anime_download_jobs`).Scan(&jobs); err != nil || jobs != 0 {
		t.Fatal("poll must not create a drive task", jobs, err)
	}
	work, found, err := reopened.ReserveAnimeDiscovery(ctx, now)
	if err != nil || !found || work.Observation.Title != rows[1].Title {
		t.Fatal("pending v3 was replaced by v4", work.Observation.Title, found, err)
	}
	if err := reopened.CompleteAnimeDiscovery(ctx, work, "RESOURCE_REVIEW_REQUIRED", false, now); err != nil {
		t.Fatal(err)
	}
	// A verification failure cannot silently enable the later version either.
	now += int64(time.Second / time.Millisecond)
	if _, err := reopened.db.Exec(`UPDATE anime_subscription_sources SET next_check_at=? WHERE id=?`, now-1, poll.Source.ID); err != nil {
		t.Fatal(err)
	}
	poll = discoveryPoll(t, reopened, now)
	failedUpgrade := discoveryRow("rel_later_v5", now)
	failedUpgrade.Title = "[字幕组] 动画 - 30v5 [1080p][HEVC]"
	stats, err = reopened.CompleteAnimeSourcePoll(ctx, poll, discoveryPage(poll, now, failedUpgrade), now)
	if err != nil || stats.Selected != 0 {
		t.Fatal("failure unlocked edition", stats, err)
	}
}

func TestAnimeDiscoveryIneligibleHigherVersionCannotHideEligibleBase(t *testing.T) {
	for _, mode := range []string{"filtered", "already_dismissed"} {
		t.Run(mode, func(t *testing.T) {
			f, now := discoveryFixture(t)
			poll := discoveryPoll(t, f.store, now)
			base, higher := discoveryRow("rel_base", now), discoveryRow("rel_higher", now)
			higher.Title = "[字幕组] 动画 - 30v3 [1080p][HEVC]"
			switch mode {
			case "filtered":
				higher.Title += " unwanted"
				poll.Source.Rules.Exclude = []string{"unwanted"}
			case "already_dismissed":
				preview, err := preview.Build(animePollConfig(poll), "poll", discoveryPage(poll, now, higher), now)
				if err != nil {
					t.Fatal(err)
				}
				releases, err := f.store.SaveAnimeReleases(context.Background(), []model.AnimeRelease{preview.Items[0].Release})
				if err != nil {
					t.Fatal(err)
				}
				d := animeDecision(poll.Source, releases[0], "dismissed")
				raw, err := animeJSON(d.Snapshot)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := f.store.db.Exec(`INSERT INTO anime_release_decisions(source_id,release_id,disposition,source_revision,release_fingerprint,snapshot_json,updated_at) VALUES(?,?,'dismissed',?,?,?,?)`, poll.Source.ID, releases[0].ID, poll.Source.Revision, releases[0].ContentFingerprint, raw, now); err != nil {
					t.Fatal(err)
				}
			}
			stats, err := f.store.CompleteAnimeSourcePoll(context.Background(), poll, discoveryPage(poll, now, base, higher), now)
			if err != nil || stats.Selected != 1 {
				t.Fatal(stats, err)
			}
			var title string
			if err := f.store.db.QueryRow(`SELECT r.title FROM anime_release_decisions d JOIN anime_releases r ON r.id=d.release_id WHERE d.source_id=? AND d.disposition='selected'`, poll.Source.ID).Scan(&title); err != nil || title != base.Title {
				t.Fatal(title, err)
			}
		})
	}
}
