package storage

import (
	"context"
	"fmt"
	"reflect"
	"testing"

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
