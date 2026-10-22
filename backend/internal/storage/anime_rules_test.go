package storage

import (
	"context"
	"reflect"
	"testing"
	"time"

	"strm115/backend/internal/animeparse"
	"strm115/backend/internal/animepreview"
	"strm115/backend/internal/animeresource"
	"strm115/backend/internal/model"
)

func TestAnimeSourceCustomParseIsIndependentOfSharedPostCache(t *testing.T) {
	f, now := discoveryFixture(t)
	ctx := context.Background()
	rules := f.sub.Sources[0].Rules
	rules.EpisodePattern, rules.EpisodeGroup = `e=(\d+)`, 1
	raw, err := animeJSON(rules)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.db.Exec(`UPDATE anime_subscription_sources SET rules_json=? WHERE id=?`, raw, f.sub.Sources[0].ID); err != nil {
		t.Fatal(err)
	}
	row := discoveryRow("rel_shared_custom", now)
	row.Title += " e=31"
	verified, err := animeresource.Verify(ctx, "garden", row, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		poll := discoveryPoll(t, f.store, now)
		stats, err := f.store.CompleteAnimeSourcePoll(ctx, poll, discoveryPage(poll, now, row), now)
		if err != nil || stats.Selected != 1 {
			t.Fatal(stats, err)
		}
		r, err := scanAnimeRelease(f.store.db.QueryRow(`SELECT `+animeReleaseColumns+` FROM anime_releases WHERE post_key=?`, row.PostKey))
		if err != nil {
			t.Fatal(err)
		}
		d, err := f.store.GetAnimeReleaseDecision(ctx, poll.Source.ID, r.ID)
		if err != nil {
			t.Fatal(err)
		}
		want := 30
		if poll.Source.ID == f.sub.Sources[0].ID {
			want = 31
		}
		if d.Snapshot.Parse.Episode == nil || *d.Snapshot.Parse.Episode != want {
			t.Fatal("shared parse replaced source interpretation", d)
		}
		claim, err := f.store.ClaimAnimeDownload(ctx, poll.Source.ID, r.ID, d.Generation, verified, poll.Target)
		if err != nil {
			t.Fatal(err)
		}
		var snapshot model.AnimeDownloadSnapshot
		var selectedRaw string
		if err := f.store.db.QueryRow(`SELECT snapshot_json FROM anime_job_selections WHERE job_id=? AND source_id=?`, claim.Job.ID, poll.Source.ID).Scan(&selectedRaw); err != nil {
			t.Fatal(err)
		}
		if err := decodeAnimeJSON(selectedRaw, &snapshot); err != nil {
			t.Fatal(err)
		}
		if snapshot.Release.Parse.Episode == nil || *snapshot.Release.Parse.Episode != want {
			t.Fatal("claim used shared cache", snapshot.Release.Parse)
		}
	}
}

func TestAnimeDiscoveryReevaluatesOldInterpretationsAndFreezesSelections(t *testing.T) {
	f, now := discoveryFixture(t)
	ctx := context.Background()
	poll := discoveryPoll(t, f.store, now)
	if _, err := f.store.db.Exec(`UPDATE anime_subscription_sources SET run_when_enabled=0 WHERE id<>?`, poll.Source.ID); err != nil {
		t.Fatal(err)
	}
	row := discoveryRow("rel_upgrade", now)
	row.Title += "[无修正]"
	if _, err := f.store.CompleteAnimeSourcePoll(ctx, poll, discoveryPage(poll, now, row), now); err != nil {
		t.Fatal(err)
	}
	r, err := scanAnimeRelease(f.store.db.QueryRow(`SELECT `+animeReleaseColumns+` FROM anime_releases WHERE post_key=?`, row.PostKey))
	if err != nil {
		t.Fatal(err)
	}
	d, err := f.store.GetAnimeReleaseDecision(ctx, poll.Source.ID, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	d.Snapshot.Selection = nil
	d.Snapshot.ParserVersion = 6
	d.Snapshot.Parse.VersionLabel = "修正"
	raw, _ := animeJSON(d.Snapshot)
	if _, err := f.store.db.Exec(`UPDATE anime_release_decisions SET disposition='needs_confirmation',snapshot_json=? WHERE source_id=?`, raw, poll.Source.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.db.Exec(`UPDATE anime_subscription_sources SET next_check_at=? WHERE id=?`, now+1, poll.Source.ID); err != nil {
		t.Fatal(err)
	}
	poll = discoveryPoll(t, f.store, now+1)
	stats, err := f.store.CompleteAnimeSourcePoll(ctx, poll, discoveryPage(poll, now+1, row), now+1)
	if err != nil || stats.Selected != 1 {
		t.Fatal("old parser interpretation never reconsidered", stats, err)
	}
	d, err = f.store.GetAnimeReleaseDecision(ctx, poll.Source.ID, r.ID)
	if err != nil || d.Snapshot.RuleVersion != animepreview.RuleVersion || d.Snapshot.ParserVersion != animeparse.ParserVersion || d.Snapshot.Parse.VersionLabel != "" {
		t.Fatal(d, err)
	}
	// Simulate a previously selected snapshot from an older engine. Upgrading
	// can reinterpret unselected rows, but never changes an authorized choice.
	d.Snapshot.ParserVersion, d.Snapshot.RuleVersion = 1, 1
	raw, _ = animeJSON(d.Snapshot)
	if _, err := f.store.db.Exec(`UPDATE anime_release_decisions SET snapshot_json=? WHERE source_id=?`, raw, poll.Source.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.db.Exec(`UPDATE anime_subscription_sources SET next_check_at=? WHERE id=?`, now+2, poll.Source.ID); err != nil {
		t.Fatal(err)
	}
	poll = discoveryPoll(t, f.store, now+2)
	if _, err := f.store.CompleteAnimeSourcePoll(ctx, poll, discoveryPage(poll, now+2, row), now+2); err != nil {
		t.Fatal(err)
	}
	after, err := f.store.GetAnimeReleaseDecision(ctx, poll.Source.ID, r.ID)
	if err != nil || !reflect.DeepEqual(after, d) {
		t.Fatal("selected snapshot changed on engine upgrade", after, err)
	}
	source, err := f.store.GetAnimeSource(ctx, poll.Source.ID)
	if err != nil || source.Health != "ok" || source.NextCheckAt == nil {
		t.Fatal("engine upgrade stopped RSS", source, err)
	}
}

func TestAnimeClaimAndQueuedSubmitUseFrozenParseAfterEngineUpgrade(t *testing.T) {
	f := newDownloadClaimFixture(t)
	ctx := context.Background()
	r, verified := f.selectRelease(t, 0, "rel_old_engine", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	source := f.sub.Sources[0]
	d, err := f.store.GetAnimeReleaseDecision(ctx, source.ID, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	d.Snapshot.ParserVersion, d.Snapshot.RuleVersion = 1, 1
	raw, _ := animeJSON(d.Snapshot)
	if _, err := f.store.db.Exec(`UPDATE anime_release_decisions SET snapshot_json=? WHERE source_id=?`, raw, source.ID); err != nil {
		t.Fatal(err)
	}
	unknown, _ := animeJSON(model.AnimeReleaseParse{SchemaVersion: 1, Kind: "unknown"})
	if _, err := f.store.db.Exec(`UPDATE anime_releases SET parser_version=99,parse_json=? WHERE id=?`, unknown, r.ID); err != nil {
		t.Fatal(err)
	}
	claim, err := f.store.ClaimAnimeDownload(ctx, source.ID, r.ID, d.Generation, verified, f.target)
	if err != nil || claim.Job.Snapshot.Release.Parse.Episode == nil || *claim.Job.Snapshot.Release.Parse.Episode != 30 {
		t.Fatal("engine upgrade rejected frozen selection", claim, err)
	}
	if _, err := f.store.db.Exec(`UPDATE anime_download_jobs SET directory_cid='12345' WHERE id=?`, claim.Job.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.PrepareAnimeSubmit(ctx, claim.Job.ID, claim.Job.Generation, claim.Job.Snapshot.Target); err != nil {
		t.Fatal("shared parse blocked queued task", err)
	}
}

func TestAnimeSavingDirectorySettingsRestartsTargetBlockedSource(t *testing.T) {
	f, now := discoveryFixture(t)
	ctx := context.Background()
	source := f.sub.Sources[0]
	if _, err := f.store.db.Exec(`UPDATE anime_subscription_sources SET health='unavailable',error_code='DOWNLOAD_TARGET_CHANGED',next_check_at=NULL WHERE id=?`, source.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.db.Exec(`UPDATE anime_subscription_sources SET run_when_enabled=0 WHERE id<>?`, source.ID); err != nil {
		t.Fatal(err)
	}
	settings, err := f.store.GetAnimeSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.SaveAnimeSettings(ctx, settings, animeAdminKey("fixed_directory_settings")); err != nil {
		t.Fatal(err)
	}
	_, found, err := f.store.ReserveAnimeSourcePoll(ctx, max(now, time.Now().UnixMilli()))
	if err != nil || !found {
		t.Fatal("corrected directory did not restart RSS", found, err)
	}
}
