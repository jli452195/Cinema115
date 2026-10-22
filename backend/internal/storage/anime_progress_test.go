package storage

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"strm115/backend/internal/model"
)

func progressFixture(t *testing.T) *downloadClaimFixture {
	t.Helper()
	f := newDownloadClaimFixture(t)
	for _, source := range f.sub.Sources {
		source.Binding.Scope.First, source.Binding.Scope.Last = animePtr(1), animePtr(12)
		source.Binding.ChapterCorrespondence = &model.AnimeChapterCorrespondence{SourceEpisode: 1, ChapterEpisode: 1}
		source.Rules.MinimumEpisode = animePtr(1)
		binding, _ := animeJSON(source.Binding)
		rules, _ := animeJSON(source.Rules)
		if _, err := f.store.db.Exec(`UPDATE anime_subscription_sources SET binding_json=?,rules_json=?,checked_at=1 WHERE id=?`, binding, rules, source.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.store.db.Exec(`UPDATE anime_entries SET metadata_json=json_set(metadata_json,'$.totalEpisodes',12)`); err != nil {
		t.Fatal(err)
	}
	var err error
	f.sub, err = f.store.GetAnimeSubscription(context.Background(), f.sub.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func progressEpisode(t *testing.T, f *downloadClaimFixture, group, episode int, organized bool) string {
	t.Helper()
	r, v := f.selectTitle(t, group, fmt.Sprintf("rel_progress_%d_%d", group, episode), fmt.Sprintf("%040x", 1000+group*100+episode), fmt.Sprintf("[group] Anime - %02d [1080p][HEVC]", episode))
	if !organized {
		return ""
	}
	job := f.claim(t, group, r, v).Job
	if _, err := f.store.db.Exec(`UPDATE anime_download_jobs SET download_state='completed',file_state='verified',workflow_state='organized' WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	return job.ID
}

func refreshProgress(t *testing.T, f *downloadClaimFixture) map[string]model.AnimeSourceProgress {
	t.Helper()
	for i := 0; ; i++ {
		found, err := f.store.RefreshAnimeSourceProgress(context.Background())
		if err != nil || i > model.AnimeMaxSources {
			t.Fatal("bounded local refresh", found, err)
		}
		if !found {
			break
		}
	}
	ids := make([]string, 0, len(f.sub.Sources))
	for _, source := range f.sub.Sources {
		ids = append(ids, source.ID)
	}
	result, err := f.store.AnimeSourceProgresses(context.Background(), ids, true)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestAnimeSourceProgressMissingEpisodeIsPerRSS(t *testing.T) {
	f := progressFixture(t)
	for group := 0; group < 2; group++ {
		for n := 1; n <= 10; n++ {
			progressEpisode(t, f, group, n, group != 0 || n != 7)
		}
	}
	main, other := f.sub.Sources[0].ID, f.sub.Sources[1].ID
	progress := refreshProgress(t, f)
	if got := progress[main]; got.State != "missing" || got.MissingCount != 1 || got.OrganizedCount != 9 || !reflect.DeepEqual(got.Missing, []model.AnimeEpisodeRange{{First: 7, Last: 7}}) {
		t.Fatal("published 1-10, missing organized episode 7", got)
	}
	if got := progress[other]; got.State != "ongoing" || got.MissingCount != 0 || got.PublishedThrough != 10 {
		t.Fatal("another group's success or future episodes changed coverage", got)
	}
	// The list returns just the materialized summary, not missing ranges.
	for i := 0; i < 3; i++ {
		cards, err := f.store.AnimeSourceProgresses(context.Background(), []string{main, other}, false)
		if err != nil || cards[main].Missing != nil || cards[main].MissingCount != 1 {
			t.Fatal(cards, err)
		}
	}
	if found, err := f.store.RefreshAnimeSourceProgress(context.Background()); err != nil || found {
		t.Fatal("reading a list scheduled a new check", found, err)
	}
	// Filling 7 clears only this source; complete still requires 11 and 12.
	progressEpisode(t, f, 0, 7, true)
	if got := refreshProgress(t, f)[main]; got.State != "ongoing" || got.MissingCount != 0 {
		t.Fatal(got)
	}
	progressEpisode(t, f, 0, 11, true)
	progressEpisode(t, f, 0, 12, true)
	if got := refreshProgress(t, f)[main]; got.State != "complete" || got.OrganizedCount != 12 {
		t.Fatal(got)
	}
}

func TestAnimeProgressZeroDoesNotExtendRegularTotal(t *testing.T) {
	f := progressFixture(t)
	source := &f.sub.Sources[0]
	source.Binding.Scope.First, source.Rules.MinimumEpisode = animePtr(0), animePtr(0)
	binding, _ := animeJSON(source.Binding)
	rules, _ := animeJSON(source.Rules)
	if _, err := f.store.db.Exec(`UPDATE anime_subscription_sources SET binding_json=?,rules_json=? WHERE id=?`, binding, rules, source.ID); err != nil {
		t.Fatal(err)
	}
	progressEpisode(t, f, 0, 0, true)
	for n := 1; n <= 12; n++ {
		progressEpisode(t, f, 0, n, true)
	}
	if got := refreshProgress(t, f)[source.ID]; got.State != "complete" || got.FirstEpisode != 1 || got.OrganizedCount != 12 || got.PublishedThrough != 12 || got.MissingCount != 0 {
		t.Fatal("zero shifted regular chapter coverage", got)
	}
}

func TestAnimeSourceProgressInvalidationAndMinimum(t *testing.T) {
	f := progressFixture(t)
	id := f.sub.Sources[0].ID
	jobID := progressEpisode(t, f, 0, 10, true)
	refreshProgress(t, f)
	// Ordinary status/progress polling doesn't dirty an unchanged result.
	if _, err := f.store.db.Exec(`UPDATE anime_download_jobs SET revision=revision+1,raw_status_json='{}' WHERE id=?`, jobID); err != nil {
		t.Fatal(err)
	}
	if found, err := f.store.RefreshAnimeSourceProgress(context.Background()); err != nil || found {
		t.Fatal("unchanged receipts recomputed coverage", found, err)
	}
	// A new starting point cannot display the old rules' red badge.
	if _, err := f.store.db.Exec(`UPDATE anime_subscription_sources SET rules_json=json_set(rules_json,'$.minimumEpisode',10) WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	stale, err := f.store.AnimeSourceProgresses(context.Background(), []string{id}, false)
	if err != nil || stale[id].State != "unknown" {
		t.Fatal("old rule result leaked", stale, err)
	}
	if got := refreshProgress(t, f)[id]; got.State != "ongoing" || got.FirstEpisode != 10 || got.MissingCount != 0 {
		t.Fatal("intentionally skipped episodes counted missing", got)
	}
	if _, err := f.store.db.Exec(`UPDATE anime_subscriptions SET form_json=json_set(form_json,'$.totalEpisodes',10) WHERE id=?`, f.sub.ID); err != nil {
		t.Fatal(err)
	}
	if got := refreshProgress(t, f)[id]; got.State != "complete" {
		t.Fatal("manual total did not refresh completion", got)
	}
	if _, err := f.store.db.Exec(`UPDATE anime_subscription_sources SET rules_json=json_set(rules_json,'$.episodePattern','([0-9]{2})','$.episodeGroup',1) WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	if got := refreshProgress(t, f)[id]; got.State != "ongoing" || got.PublishedThrough != 0 || got.OrganizedCount != 0 {
		t.Fatal("old numbering interpretation reused after regex edit", got)
	}
	if _, err := f.store.db.Exec(`UPDATE anime_subscription_sources SET checked_at=NULL WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	if got := refreshProgress(t, f)[id]; got.State != "unknown" {
		t.Fatal("uninitialized source reported completeness", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.store.RefreshAnimeSourceProgress(ctx); err == nil {
		t.Fatal("cancelled worker kept running")
	}
}

func TestAnimeSourceProgressContinuousNumbersAndRestart(t *testing.T) {
	f := progressFixture(t)
	id := f.sub.Sources[0].ID
	source := &f.sub.Sources[0]
	source.Binding.Scope.First, source.Binding.Scope.Last = animePtr(25), animePtr(36)
	source.Binding.ChapterCorrespondence.SourceEpisode = 25
	source.Rules.MinimumEpisode = animePtr(27)
	binding, _ := animeJSON(source.Binding)
	rules, _ := animeJSON(source.Rules)
	if _, err := f.store.db.Exec(`UPDATE anime_subscription_sources SET binding_json=?,rules_json=? WHERE id=?`, binding, rules, id); err != nil {
		t.Fatal(err)
	}
	progressEpisode(t, f, 0, 28, true)
	want := refreshProgress(t, f)[id]
	if want.FirstEpisode != 27 || want.PublishedThrough != 28 || want.MissingCount != 1 || !reflect.DeepEqual(want.Missing, []model.AnimeEpisodeRange{{First: 27, Last: 27}}) {
		t.Fatal("continuous numbering counted skipped/future episodes", want)
	}
	// An unknown final total still allows gaps within the observed range.
	if _, err := f.store.db.Exec(`UPDATE anime_entries SET metadata_json=json_remove(metadata_json,'$.totalEpisodes')`); err != nil {
		t.Fatal(err)
	}
	if got := refreshProgress(t, f)[id]; !reflect.DeepEqual(got, want) {
		t.Fatal("unknown final total lost local gaps", got)
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
	f.store = reopened
	if found, err := reopened.RefreshAnimeSourceProgress(context.Background()); err != nil || found {
		t.Fatal("restart recomputed clean persisted summaries", found, err)
	}
	if got := refreshProgress(t, f)[id]; !reflect.DeepEqual(got, want) {
		t.Fatal("restart lost persisted progress", got)
	}
}

func TestAnimeSourceProgressChapterUpgradeInvalidatesOnce(t *testing.T) {
	f := progressFixture(t)
	id := f.sub.Sources[0].ID
	progressEpisode(t, f, 0, 1, true)
	want := refreshProgress(t, f)[id]
	var path string
	if err := f.store.db.QueryRow(`SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&path); err != nil {
		t.Fatal(err)
	}
	// Reproduce v57's trigger, which watched totals but not premiere dates.
	if _, err := f.store.db.Exec(`DROP TRIGGER anime_progress_entry_update;
CREATE TRIGGER anime_progress_entry_update AFTER UPDATE OF metadata_json ON anime_entries
WHEN json_extract(OLD.metadata_json,'$.totalEpisodes') IS NOT json_extract(NEW.metadata_json,'$.totalEpisodes') BEGIN
 UPDATE anime_source_progress SET generation=generation+1 WHERE source_id IN (SELECT s.id FROM anime_subscription_sources s JOIN anime_subscriptions p ON p.id=s.subscription_id WHERE p.entry_id=NEW.id);
END; PRAGMA user_version=57;`); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		var err error
		f.store, err = Open(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		defer f.store.Close()
		progress, err := f.store.AnimeSourceProgresses(ctx, []string{id}, true)
		if err != nil || i == 0 && progress[id].State != "unknown" || i > 0 && !reflect.DeepEqual(progress[id], want) {
			t.Fatal("upgrade invalidation or clean restart", i, progress, err)
		}
		if got := refreshProgress(t, f)[id]; !reflect.DeepEqual(got, want) {
			t.Fatal("upgrade changed saved receipts or subscription", got)
		}
		for _, change := range []struct {
			path, value string
			invalidates bool
		}{{"$.airDate", "2026-10-02", i == 0}, {"$.airDate", "2026-10-02", false}, {"$.synopsis", "description", false}} {
			if _, err := f.store.db.Exec(`UPDATE anime_entries SET metadata_json=json_set(metadata_json,?,?)`, change.path, change.value); err != nil {
				t.Fatal(err)
			}
			if dirty, err := f.store.RefreshAnimeSourceProgress(ctx); err != nil || dirty != change.invalidates {
				t.Fatal("metadata invalidation", change, dirty, err)
			}
			refreshProgress(t, f)
		}
		if err := f.store.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAnimeCompletedSourceStopsPollingIndependently(t *testing.T) {
	f := progressFixture(t)
	ctx, now := context.Background(), time.Now().UnixMilli()
	main, other := f.sub.Sources[0].ID, f.sub.Sources[1].ID
	for n := 1; n <= 12; n++ {
		progressEpisode(t, f, 0, n, true)
	}
	progressEpisode(t, f, 1, 12, false)
	refreshProgress(t, f)
	if _, err := f.store.db.Exec(`UPDATE anime_subscription_sources SET health='ok',next_check_at=?`, now); err != nil {
		t.Fatal(err)
	}
	for _, source := range f.sub.Sources {
		if _, err := f.store.db.Exec(`INSERT INTO anime_source_activity VALUES(?,?,?,?)`, source.ID, source.Revision, now, now); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		poll := discoveryPoll(t, f.store, now)
		if poll.Source.ID == main {
			if !poll.LocalOnly {
				t.Fatal("completed main source requested RSS")
			}
		} else if poll.Source.ID == other && !poll.LocalOnly {
			if _, err := f.store.CompleteAnimeSourcePoll(ctx, poll, discoveryPage(poll, now), now); err != nil {
				t.Fatal(err)
			}
		} else {
			t.Fatal("unfinished standby stopped polling", poll.Source.ID, poll.LocalOnly)
		}
	}
	assertStopped := func() {
		t.Helper()
		if poll, found, err := f.store.ReserveAnimeSourcePoll(ctx, now); err != nil || found {
			t.Fatal("completed source still polled", poll.Source.ID, found, err)
		}
	}
	assertStopped()
	var path string
	if err := f.store.db.QueryRow(`SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&path); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	var err error
	f.store, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.store.Close()
	assertStopped()
	if _, err := f.store.db.Exec(`UPDATE anime_subscription_sources SET next_check_at=? WHERE id=?`, now, main); err != nil {
		t.Fatal(err)
	}
	if poll := discoveryPoll(t, f.store, now); !poll.LocalOnly || poll.Source.ID != main {
		t.Fatal("restart lost completed coverage", poll.Source.ID, poll.LocalOnly)
	}
	// Resume clears activity. Check once to restore health instead of leaving
	// the completed source unable to participate in eventual auto-archive.
	if _, err := f.store.db.Exec(`DELETE FROM anime_source_activity WHERE source_id=?`, main); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.db.Exec(`UPDATE anime_subscription_sources SET next_check_at=? WHERE id=?`, now, main); err != nil {
		t.Fatal(err)
	}
	poll := discoveryPoll(t, f.store, now)
	if poll.Source.ID != main || poll.LocalOnly {
		t.Fatal("missing activity suppressed recovery", poll.Source.ID)
	}
	if _, err := f.store.CompleteAnimeSourcePoll(ctx, poll, discoveryPage(poll, now), now); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.db.Exec(`UPDATE anime_subscription_sources SET rules_json=json_set(rules_json,'$.minimumEpisode',2),next_check_at=? WHERE id=?`, now, main); err != nil {
		t.Fatal(err)
	}
	poll = discoveryPoll(t, f.store, now)
	if poll.Source.ID != main || poll.LocalOnly {
		t.Fatal("dirty old completion suppressed new configuration", poll.Source.ID)
	}
	// Coverage can become complete while this poll is in flight. Its receipt
	// must remain authorized, and clear the lease normally.
	if got := refreshProgress(t, f)[main]; got.State != "complete" {
		t.Fatal(got)
	}
	if _, err := f.store.CompleteAnimeSourcePoll(ctx, poll, discoveryPage(poll, now), now); err != nil {
		t.Fatal("in-flight poll lost authorization on completion", err)
	}
	assertStopped()
}
