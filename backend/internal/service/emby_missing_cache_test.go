package service

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"sync"
	"testing"

	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
)

func applyMissingCacheEvent(t *testing.T, f *embyFixture, generation, id string, items []model.EmbyItem, remove, metadata bool) {
	t.Helper()
	ctx, scope := context.Background(), embyScope(f.service.snapshot())
	if err := f.db.EmbyEnqueue(ctx, scope, id, "upsert"); err != nil {
		t.Fatal(err)
	}
	event, err := f.db.EmbyEventForItem(ctx, scope, id)
	if err != nil || event == nil {
		t.Fatal(event, err)
	}
	if metadata {
		err = f.db.EmbyApplyMetadataEvent(ctx, scope, generation, *event, items, "metadata pending")
	} else {
		err = f.db.EmbyApplyEvent(ctx, scope, generation, *event, items, remove)
	}
	if err != nil {
		t.Fatal(err)
	}
}

// The full calculation is an independent oracle for incremental grouping and
// accounting. Keeping fewer than one page of groups also checks the API totals.
func assertMissingCacheMatchesIndex(t *testing.T, f *embyFixture, generation string) EmbyMissingPage {
	t.Helper()
	ctx, config := context.Background(), f.service.snapshot()
	raw, err := f.db.EmbyMissingItems(ctx, embyScope(config), generation, nil)
	if err != nil {
		t.Fatal(err)
	}
	exclusions, err := f.db.EmbyMissingExclusions(ctx, embyMissingService(config))
	if err != nil {
		t.Fatal(err)
	}
	want, checked, err := buildEmbyMissing(ctx, filterEmbyMissingExclusions(raw, exclusions))
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(want, func(i, j int) bool {
		if want[i].Title != want[j].Title {
			return want[i].Title < want[j].Title
		}
		return want[i].Key < want[j].Key
	})
	got, err := f.service.MissingEpisodes(ctx, EmbyMissingQuery{})
	if err != nil {
		t.Fatal(err)
	}
	actual := append([]EmbyMissingSeries{}, got.Items...)
	var summary EmbyMissingSummary
	for i := range actual {
		actual[i].PosterURL = ""
	}
	for _, group := range want {
		summary.Series++
		summary.Seasons += len(group.Seasons)
		summary.Episodes += group.MissingCount
	}
	if !reflect.DeepEqual(actual, want) || got.Total != len(want) || got.CheckedSeries != checked || got.Summary != summary {
		t.Fatalf("incremental result differs from published index:\ngot=%+v\nwant=%+v checked=%d summary=%+v", got, want, checked, summary)
	}
	incomplete := 0
	for _, item := range raw {
		if item.Type == "Episode" && item.Season != 0 && (item.SeriesID == "" || item.Season < 0 || item.Episode < 0) {
			incomplete++
		}
	}
	count := 0
	for _, notice := range got.Notices {
		if notice.Code == "episodes_incomplete" {
			count = notice.Count
		}
	}
	if count != incomplete {
		t.Fatalf("incomplete episode notice=%d, indexed=%d", count, incomplete)
	}
	return got
}

func TestEmbyMissingCacheIncrementalChangesMatchPublishedIndex(t *testing.T) {
	f := newEmbyFixture(t)
	const generation = "incremental"
	items := []model.EmbyItem{
		{ID: "a", Type: "Series", TMDB: "100", Name: "A", LibraryID: "tv"},
		{ID: "b", Type: "Series", TMDB: "100", Name: "B", LibraryID: "other"},
		{ID: "c", Type: "Series", TMDB: "200", Name: "C", LibraryID: "tv"},
		{ID: "local", Type: "Series", Name: "Local", LibraryID: "tv"},
		{ID: "a1", Type: "Episode", SeriesID: "a", Season: 1, Episode: 1, LibraryID: "tv"},
		{ID: "a3", Type: "Episode", SeriesID: "a", Season: 1, Episode: 3, LibraryID: "tv"},
		{ID: "b2", Type: "Episode", SeriesID: "b", Season: 1, Episode: 2, LibraryID: "other"},
		{ID: "c1", Type: "Episode", SeriesID: "c", ParentID: "season-c", Season: 1, Episode: 1, LibraryID: "tv"},
		{ID: "c3", Type: "Episode", SeriesID: "c", ParentID: "season-c", Season: 1, Episode: 3, LibraryID: "tv"},
		{ID: "local2", Type: "Episode", SeriesID: "local", Season: 1, Episode: 2, LibraryID: "tv"},
		{ID: "orphan", Type: "Episode", Season: 1, Episode: -1, LibraryID: "tv"},
	}
	publishMissingFixture(t, f, generation, items)
	page := assertMissingCacheMatchesIndex(t, f, generation)
	if page.CheckedSeries != 3 || page.Total != 2 {
		t.Fatal("cross-library episode versions must complete one merged show", page)
	}
	steps := []struct {
		name, id string
		rows     []model.EmbyItem
		remove   bool
		metadata bool
	}{
		{name: "rebind splits old group and merges new siblings", id: "b", rows: []model.EmbyItem{{ID: "b", Type: "Series", TMDB: "200", Name: "B", LibraryID: "other"}}},
		{name: "episode moves between series and seasons", id: "a3", rows: []model.EmbyItem{{ID: "a3", Type: "Episode", SeriesID: "c", ParentID: "season-c", Season: 2, Episode: 4, LibraryID: "tv"}}},
		{name: "local identity gains TMDB binding", id: "local", rows: []model.EmbyItem{{ID: "local", Type: "Series", TMDB: "100", Name: "Local", LibraryID: "tv"}}},
		{name: "metadata retry commits usable orphan correction", id: "orphan", rows: []model.EmbyItem{{ID: "orphan", Type: "Episode", SeriesID: "a", Season: 1, Episode: 2, LibraryID: "tv"}}, metadata: true},
		{name: "invalid episode returns after correction", id: "orphan", rows: []model.EmbyItem{{ID: "orphan", Type: "Episode", SeriesID: "a", Season: -1, Episode: 2, LibraryID: "tv"}}, metadata: true},
		{name: "season deletion removes every descendant", id: "season-c", remove: true},
		{name: "library deletion updates merged siblings", id: "other", remove: true},
		{name: "series deletion retains another source", id: "a", remove: true},
		{name: "noncanonical TMDB uses local identity", id: "local", rows: []model.EmbyItem{{ID: "local", Type: "Series", TMDB: "00100", Name: "Local", LibraryID: "tv"}}},
		{name: "last episode deletion removes checked show", id: "local2", remove: true},
	}
	for _, step := range steps {
		t.Run(step.name, func(t *testing.T) {
			applyMissingCacheEvent(t, f, generation, step.id, step.rows, step.remove, step.metadata)
			assertMissingCacheMatchesIndex(t, f, generation)
		})
	}
}

func TestEmbyMissingCacheConcurrentReadsReuseReportAndKeepPostersPrivate(t *testing.T) {
	f := newEmbyFixture(t)
	const generation = "concurrent"
	publishMissingFixture(t, f, generation, []model.EmbyItem{
		{ID: "a", Type: "Series", TMDB: "100", Name: "A"},
		{ID: "a2", Type: "Episode", SeriesID: "a", Season: 1, Episode: 2},
	})
	f.mu.Lock()
	before := f.detailCalls + f.pageCalls
	f.mu.Unlock()
	const readers = 16
	start := make(chan struct{})
	pages, failures := make(chan EmbyMissingPage, readers), make(chan error, readers)
	var wg sync.WaitGroup
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			page, err := f.service.MissingEpisodes(context.Background(), EmbyMissingQuery{})
			if err != nil {
				failures <- err
				return
			}
			pages <- page
		}()
	}
	close(start)
	wg.Wait()
	close(pages)
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	var season *EmbyMissingSeason
	for page := range pages {
		if page.Total != 1 || len(page.Items) != 1 || page.Summary.Episodes != 1 {
			t.Fatal(page)
		}
		if season == nil {
			season = &page.Items[0].Seasons[0]
		} else if season != &page.Items[0].Seasons[0] {
			t.Fatal("concurrent first requests rebuilt independent reports")
		}
		page.Items[0].PosterURL = "request-local-poster"
	}
	for _, query := range []EmbyMissingQuery{{}, {Search: "A"}, {Offset: 100}} {
		page, err := f.service.MissingEpisodes(context.Background(), query)
		if err != nil || page.Total != 1 || page.Items[0].PosterURL == "request-local-poster" || season != &page.Items[0].Seasons[0] {
			t.Fatal("pagination/search must reuse calculation without sharing poster mutations", page, err)
		}
	}
	applyMissingCacheEvent(t, f, generation, "a1", []model.EmbyItem{{ID: "a1", Type: "Episode", SeriesID: "a", Season: 1, Episode: 1}}, false, false)
	if page := assertMissingCacheMatchesIndex(t, f, generation); page.Total != 0 || page.CheckedSeries != 1 {
		t.Fatal("arrival must complete the cached show", page)
	}
	applyMissingCacheEvent(t, f, generation, "a1", nil, true, false)
	if page := assertMissingCacheMatchesIndex(t, f, generation); page.Total != 1 {
		t.Fatal("deletion must restore the gap", page)
	}
	f.mu.Lock()
	after := f.detailCalls + f.pageCalls
	f.mu.Unlock()
	if after != before {
		t.Fatalf("cache reads or incremental calculations contacted Emby: %d -> %d", before, after)
	}
}

func TestEmbyMissingCacheCancellationKeepsPendingChanges(t *testing.T) {
	f := newEmbyFixture(t)
	const generation = "cancellation"
	publishMissingFixture(t, f, generation, []model.EmbyItem{
		{ID: "a", Type: "Series", Name: "A"},
		{ID: "a2", Type: "Episode", SeriesID: "a", Season: 1, Episode: 2},
	})
	assertMissingCacheMatchesIndex(t, f, generation)
	applyMissingCacheEvent(t, f, generation, "a1", []model.EmbyItem{{ID: "a1", Type: "Episode", SeriesID: "a", Season: 1, Episode: 1}}, false, false)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.service.MissingEpisodes(ctx, EmbyMissingQuery{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled report request returned %v", err)
	}
	if page := assertMissingCacheMatchesIndex(t, f, generation); page.Total != 0 || page.CheckedSeries != 1 {
		t.Fatal("cancellation lost a committed episode change", page)
	}
}

func BenchmarkEmbyMissingReport(b *testing.B) {
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(b.TempDir(), "media.db"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { db.Close() })
	secrets, err := NewSessionStore(filepath.Join(b.TempDir(), "emby.enc"), "benchmark-secret")
	if err != nil {
		b.Fatal(err)
	}
	s, err := NewEmbyService(secrets, db)
	if err != nil {
		b.Fatal(err)
	}
	s.config = EmbyConfig{URL: "http://unused.invalid", APIKey: "benchmark", ServerID: testEmbyServerID, LibraryIDs: []string{"tv"}}
	scope := embyScope(s.config)
	rows := make([]model.EmbyItem, 0, 3000*40)
	for show := 1; show <= 3000; show++ {
		id := fmt.Sprintf("series-%04d", show)
		rows = append(rows, model.EmbyItem{ID: id, Type: "Series", TMDB: fmt.Sprint(show), Name: id, LibraryID: "tv"})
		for episode := 1; episode <= 40; episode++ {
			if episode == 7 {
				continue
			}
			rows = append(rows, model.EmbyItem{ID: fmt.Sprintf("%s-%02d", id, episode), Type: "Episode", SeriesID: id, Season: 1, Episode: episode, LibraryID: "tv"})
		}
	}
	if err := db.EmbyEnsure(ctx, scope); err != nil {
		b.Fatal(err)
	}
	if err := db.EmbyStage(ctx, scope, "benchmark", rows); err != nil {
		b.Fatal(err)
	}
	if err := db.EmbyPublish(ctx, scope, "benchmark", 0); err != nil {
		b.Fatal(err)
	}
	b.Run("full_index_and_calculation", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			items, err := db.EmbyMissingItems(ctx, scope, "benchmark", nil)
			if err != nil {
				b.Fatal(err)
			}
			groups, checked, err := buildEmbyMissing(ctx, items)
			if err != nil || checked != 3000 || len(groups) != 3000 {
				b.Fatal(checked, len(groups), err)
			}
		}
	})
	if _, err := s.MissingEpisodes(ctx, EmbyMissingQuery{}); err != nil {
		b.Fatal(err)
	}
	b.Run("cached_page", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			page, err := s.MissingEpisodes(ctx, EmbyMissingQuery{})
			if err != nil || page.Total != 3000 || len(page.Items) != 24 {
				b.Fatal(page.Total, len(page.Items), err)
			}
		}
	})
}
