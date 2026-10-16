package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
)

func publishMissingFixture(t *testing.T, f *embyFixture, generation string, items []model.EmbyItem) {
	t.Helper()
	ctx, scope := context.Background(), embyScope(f.service.snapshot())
	if err := f.db.EmbyEnsure(ctx, scope); err != nil {
		t.Fatal(err)
	}
	if err := f.db.EmbyStage(ctx, scope, generation, items); err != nil {
		t.Fatal(err)
	}
	if err := f.db.EmbyPublish(ctx, scope, generation, 0); err != nil {
		t.Fatal(err)
	}
}

func excludePreview(t *testing.T, f *embyFixture, key string) EmbyMissingExcludeInput {
	t.Helper()
	preview, err := f.service.MissingExclusionPreview(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	return EmbyMissingExcludeInput{Scope: preview.Scope, Key: preview.Key, Revision: preview.Revision}
}

func TestMissingExclusionsExactBindingPersistenceAndRestore(t *testing.T) {
	f := newEmbyFixture(t)
	ctx := context.Background()
	items := []model.EmbyItem{
		{ID: "correct", Type: "Series", TMDB: "106449", Name: "凡人修仙传", LibraryID: "tv"},
		{ID: "ghost", Type: "Series", TMDB: "285479", Name: "凡人修仙传", LibraryID: "tv"},
		{ID: "e1", Type: "Episode", SeriesID: "correct", Season: 1, Episode: 1, EndEpisode: 100},
		{ID: "e2", Type: "Episode", SeriesID: "correct", Season: 1, Episode: 101, EndEpisode: 182},
		{ID: "e3", Type: "Episode", SeriesID: "correct", Season: 1, Episode: 184},
		{ID: "e4", Type: "Episode", SeriesID: "correct", Season: 1, Episode: 186, EndEpisode: 195},
		{ID: "old", Type: "Episode", SeriesID: "ghost", Season: 1, Episode: 180, EndEpisode: 182},
	}
	publishMissingFixture(t, f, "one", items)
	preview, err := f.service.MissingExclusionPreview(ctx, "tmdb:285479")
	if err != nil || preview.MissingCount != 179 || len(preview.Sources) != 1 || preview.Sources[0].EpisodeCount != 1 || preview.Sources[0].SeriesID != "ghost" || preview.Sources[0].LibraryID != "tv" {
		t.Fatal(preview, err)
	}
	input := EmbyMissingExcludeInput{Scope: preview.Scope, Key: preview.Key, Revision: preview.Revision}
	if count, err := f.service.ExcludeMissing(ctx, input); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	assertCorrectOnly := func() {
		t.Helper()
		page, err := f.service.MissingEpisodes(ctx, EmbyMissingQuery{})
		if err != nil || page.Total != 1 || page.ExcludedCount != 1 || page.CheckedSeries != 1 || page.Summary.Episodes != 2 || page.Items[0].TMDBID != "106449" {
			t.Fatal(page, err)
		}
	}
	assertCorrectOnly()
	// Raw indexed media remains available to all other features.
	raw, err := f.db.EmbyMissingItems(ctx, input.Scope, "one")
	if err != nil || len(raw) != len(items) {
		t.Fatal(raw, err)
	}
	if _, err := f.service.ExcludeMissing(ctx, input); !errors.Is(err, ErrEmbyMissingChanged) {
		t.Fatal("replayed confirmation accepted", err)
	}
	// Incremental events and fresh complete snapshots cannot resurrect an exclusion.
	if err := f.db.EmbyEnqueue(ctx, input.Scope, "extra", "upsert"); err != nil {
		t.Fatal(err)
	}
	event, err := f.db.EmbyEventForItem(ctx, input.Scope, "extra")
	if err != nil || event == nil {
		t.Fatal(event, err)
	}
	if err := f.db.EmbyApplyEvent(ctx, input.Scope, "one", *event, []model.EmbyItem{{ID: "extra", Type: "Episode", SeriesID: "ghost", Season: 1, Episode: 183}}, false); err != nil {
		t.Fatal(err)
	}
	assertCorrectOnly()
	publishMissingFixture(t, f, "two", items)
	assertCorrectOnly()
	// The decision belongs to the service even after changing selected libraries.
	f.service.mu.Lock()
	f.service.config.LibraryIDs = []string{"tv"}
	f.service.mu.Unlock()
	publishMissingFixture(t, f, "three", items)
	assertCorrectOnly()
	// A corrected binding participates without clearing the old exclusion.
	items[1].TMDB = "106449"
	publishMissingFixture(t, f, "corrected", items)
	page, err := f.service.MissingEpisodes(ctx, EmbyMissingQuery{})
	if err != nil || page.Total != 1 || page.Summary.Episodes != 2 {
		t.Fatal(page, err)
	}
	preview, err = f.service.MissingExclusionPreview(ctx, "tmdb:106449")
	if err != nil || len(preview.Sources) != 2 {
		t.Fatal("corrected source was still suppressed", preview, err)
	}
	items[1].TMDB = "285479"
	publishMissingFixture(t, f, "old-binding", items)
	// Restart both SQLite and the service; no in-memory decision is required.
	if err := f.db.Close(); err != nil {
		t.Fatal(err)
	}
	f.db, err = storage.Open(ctx, f.path)
	if err != nil {
		t.Fatal(err)
	}
	secrets := f.service.secrets
	f.service, err = NewEmbyService(secrets, f.db)
	if err != nil {
		t.Fatal(err)
	}
	// New service reloads the original selection from its encrypted config.
	assertCorrectOnly()
	excluded, err := f.service.MissingExclusions(ctx, EmbyMissingQuery{Search: "285479"})
	if err != nil || excluded.Count != 1 || excluded.Total != 1 || excluded.Items[0].TMDBID != "285479" {
		t.Fatal(excluded, err)
	}
	wrong := EmbyMissingRestoreInput{Service: strings.Repeat("f", 64), ID: excluded.Items[0].ID}
	if err := f.service.RestoreMissing(ctx, wrong); !errors.Is(err, ErrEmbyMissingChanged) {
		t.Fatal(err)
	}
	if err := f.service.RestoreMissing(ctx, EmbyMissingRestoreInput{Service: excluded.Service, ID: excluded.Items[0].ID}); err != nil {
		t.Fatal(err)
	}
	page, err = f.service.MissingEpisodes(ctx, EmbyMissingQuery{})
	if err != nil || page.Total != 2 || page.ExcludedCount != 0 || page.Summary.Episodes != 181 {
		t.Fatal(page, err)
	}
	f.service.mu.Lock()
	f.service.config.ServerID = "different-server"
	f.service.mu.Unlock()
	other, err := f.service.MissingExclusions(ctx, EmbyMissingQuery{})
	if err != nil || other.Count != 0 {
		t.Fatal("service boundary leaked", other, err)
	}
}

func TestMissingExclusionsStalePreviewAndGroupedSources(t *testing.T) {
	f := newEmbyFixture(t)
	ctx := context.Background()
	items := []model.EmbyItem{
		{ID: "a", Type: "Series", TMDB: "100", Name: "合并剧", LibraryID: "tv"},
		{ID: "b", Type: "Series", TMDB: "100", Name: "合并剧另版", LibraryID: "movies"},
		{ID: "a1", Type: "Episode", SeriesID: "a", Season: 1, Episode: 1, EndEpisode: 4},
		{ID: "b1", Type: "Episode", SeriesID: "b", Season: 1, Episode: 6, EndEpisode: 8},
	}
	publishMissingFixture(t, f, "base", items)
	input := excludePreview(t, f, "tmdb:100")
	items[1].TMDB = "101"
	publishMissingFixture(t, f, "changed-binding", items)
	if _, err := f.service.ExcludeMissing(ctx, input); !errors.Is(err, ErrEmbyMissingChanged) {
		t.Fatal("changed binding accepted", err)
	}
	items[1].TMDB = "100"
	items[3].Episode = 7
	publishMissingFixture(t, f, "changed-episode", items)
	if _, err := f.service.ExcludeMissing(ctx, input); !errors.Is(err, ErrEmbyMissingChanged) {
		t.Fatal("changed episode evidence accepted", err)
	}
	items[3].Episode = 6
	publishMissingFixture(t, f, "base-again", items)
	input = excludePreview(t, f, "tmdb:100")
	// Two concurrent submissions of one confirmation commit at most once.
	var wg sync.WaitGroup
	results := make([]error, 2)
	for i := range results {
		wg.Add(1)
		go func(i int) { defer wg.Done(); _, results[i] = f.service.ExcludeMissing(ctx, input) }(i)
	}
	wg.Wait()
	if (results[0] == nil) == (results[1] == nil) {
		t.Fatal(results)
	}
	for _, err := range results {
		if err != nil && !errors.Is(err, ErrEmbyMissingChanged) {
			t.Fatal(err)
		}
	}
	page, err := f.service.MissingEpisodes(ctx, EmbyMissingQuery{})
	if err != nil || page.Total != 0 || page.CheckedSeries != 0 || page.ExcludedCount != 2 {
		t.Fatal(page, err)
	}
	excluded, err := f.service.MissingExclusions(ctx, EmbyMissingQuery{})
	if err != nil || excluded.Count != 2 {
		t.Fatal(excluded, err)
	}
	// Restoring one source must not restore another version sharing the same TMDB.
	if err := f.service.RestoreMissing(ctx, EmbyMissingRestoreInput{Service: excluded.Service, ID: excluded.Items[0].ID}); err != nil {
		t.Fatal(err)
	}
	page, err = f.service.MissingEpisodes(ctx, EmbyMissingQuery{})
	if err != nil || page.ExcludedCount != 1 {
		t.Fatal(page, err)
	}
	raw, err := f.db.EmbyMissingItems(ctx, embyScope(f.service.snapshot()), "base-again")
	if err != nil || len(raw) != 4 {
		t.Fatal(raw, err)
	}
}

func TestMissingExclusionsPaginationAndInput(t *testing.T) {
	f := newEmbyFixture(t)
	ctx := context.Background()
	service := embyMissingService(f.service.snapshot())
	items := make([]model.EmbyMissingExclusion, 0, 26)
	for i := 0; i < 26; i++ {
		items = append(items, model.EmbyMissingExclusion{ID: fmt.Sprintf("%032d", i), ExcludedAt: "2026-10-16T00:00:00Z", EmbyMissingSource: model.EmbyMissingSource{SeriesID: fmt.Sprint(i), TMDBID: fmt.Sprint(i + 100), Title: fmt.Sprintf("排除剧%02d", i)}})
	}
	if err := f.db.SaveEmbyMissingExclusions(ctx, service, items); err != nil {
		t.Fatal(err)
	}
	page, err := f.service.MissingExclusions(ctx, EmbyMissingQuery{})
	if err != nil || len(page.Items) != 24 || page.Count != 26 || !page.HasMore {
		t.Fatal(page, err)
	}
	page, err = f.service.MissingExclusions(ctx, EmbyMissingQuery{Offset: 999})
	if err != nil || page.Offset != 24 || len(page.Items) != 2 || page.HasMore {
		t.Fatal(page, err)
	}
	page, err = f.service.MissingExclusions(ctx, EmbyMissingQuery{Search: "排除剧01", Offset: 999})
	if err != nil || page.Count != 26 || page.Total != 1 || page.Offset != 0 {
		t.Fatal(page, err)
	}
	for _, q := range []EmbyMissingQuery{{Offset: -1}, {Offset: 1000001}, {Search: strings.Repeat("剧", 101)}} {
		if _, err := f.service.MissingExclusions(ctx, q); !errors.Is(err, ErrEmbyMissingInput) {
			t.Fatal(q, err)
		}
	}
	if _, err := f.service.ExcludeMissing(ctx, EmbyMissingExcludeInput{}); !errors.Is(err, ErrEmbyMissingInput) {
		t.Fatal(err)
	}
	if err := f.service.RestoreMissing(ctx, EmbyMissingRestoreInput{}); !errors.Is(err, ErrEmbyMissingInput) {
		t.Fatal(err)
	}
	if _, err := f.service.MissingExclusionPreview(ctx, ""); !errors.Is(err, ErrEmbyMissingInput) {
		t.Fatal(err)
	}
	if _, err := f.service.MissingExclusionPreview(ctx, "tmdb:missing"); !errors.Is(err, ErrEmbyMissingChanged) {
		t.Fatal(err)
	}
	// Excluded records remain manageable even when no current library snapshot exists.
	main, err := f.service.MissingEpisodes(ctx, EmbyMissingQuery{})
	if err != nil || main.Ready || main.ExcludedCount != 26 {
		t.Fatal(main, err)
	}
}
