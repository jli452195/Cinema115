package service

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/client/emby"
	"strm115/backend/internal/client/tmdb"
	"strm115/backend/internal/model"
)

func TestEmbyMissingSeasonRanges(t *testing.T) {
	for _, tc := range []struct {
		name        string
		owned       []EmbyEpisodeRange
		want        []EmbyEpisodeRange
		last, count int
	}{
		{"requested example", []EmbyEpisodeRange{{1, 4}, {6, 8}}, []EmbyEpisodeRange{{5, 5}}, 8, 1},
		{"missing beginning", []EmbyEpisodeRange{{4, 4}}, []EmbyEpisodeRange{{1, 3}}, 4, 3},
		{"overlapping versions", []EmbyEpisodeRange{{6, 8}, {1, 3}, {2, 4}, {3, 4}, {7, 7}}, []EmbyEpisodeRange{{5, 5}}, 8, 1},
		{"complete with no tail", []EmbyEpisodeRange{{1, 2}, {3, 4}}, []EmbyEpisodeRange{}, 4, 0},
		{"long series", []EmbyEpisodeRange{{1, 1}, {3001, 3001}}, []EmbyEpisodeRange{{2, 3000}}, 3001, 2999},
		{"huge number stays compact", []EmbyEpisodeRange{{1_000_000_000, 1_000_000_000}}, []EmbyEpisodeRange{{1, 999_999_999}}, 1_000_000_000, 999_999_999},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := embyMissingSeason(1, tc.owned)
			if !reflect.DeepEqual(got.Ranges, tc.want) || got.MissingCount != tc.count || got.LastEpisode != tc.last || got.OwnedCount != tc.last-tc.count {
				t.Fatalf("unexpected report: %+v", got)
			}
		})
	}
	// Compare interval merging with a simple enumeration oracle on bounded random data.
	rng := rand.New(rand.NewSource(115))
	for trial := 0; trial < 300; trial++ {
		owned := make([]EmbyEpisodeRange, 0, 20)
		present := make(map[int]bool)
		last := 0
		for n := 0; n < 20; n++ {
			first := 1 + rng.Intn(80)
			end := first + rng.Intn(8)
			owned = append(owned, EmbyEpisodeRange{first, end})
			last = max(last, end)
			for ep := first; ep <= end; ep++ {
				present[ep] = true
			}
		}
		got := embyMissingSeason(1, owned)
		missing := make(map[int]bool)
		for _, r := range got.Ranges {
			for ep := r.First; ep <= r.Last; ep++ {
				missing[ep] = true
			}
		}
		for ep := 1; ep <= last; ep++ {
			if missing[ep] == present[ep] {
				t.Fatalf("trial %d episode %d: %+v", trial, ep, got)
			}
		}
		if got.MissingCount != len(missing) || got.LastEpisode != last {
			t.Fatalf("incorrect counts: %+v", got)
		}
	}
}

func TestBuildEmbyMissingIdentityAndSeasons(t *testing.T) {
	items := []model.EmbyItem{
		{ID: "a", Type: "Series", TMDB: "100", Name: "测试剧"},
		{ID: "b", Type: "Series", TMDB: "100", Name: "另一版本", LibraryID: "another-library"},
		{ID: "local", Type: "Series", Name: "同名本地剧"},
		{ID: "local2", Type: "Series", Name: "同名本地剧"},
		{Type: "Episode", SeriesID: "a", TMDB: "episode-not-series", Season: 1, Episode: 1, EndEpisode: 4},
		{Type: "Episode", SeriesID: "b", Season: 1, Episode: 6, EndEpisode: 8},
		{Type: "Episode", SeriesID: "b", Season: 2, Episode: 1, EndEpisode: 2},
		{Type: "Episode", SeriesID: "a", Season: 0, Episode: 10},
		{Type: "Episode", SeriesID: "a", Season: -1, Episode: 10},
		{Type: "Episode", SeriesID: "a", Season: 1, Episode: 0},
		{Type: "Episode", Season: 1, Episode: 99},
		{Type: "Episode", SeriesID: "local", Season: 3, Episode: 2, EndEpisode: 1},
		{Type: "Episode", SeriesID: "local2", Season: 1, Episode: 3, EndEpisode: 9000},
	}
	groups, checked, err := buildEmbyMissing(context.Background(), items)
	if err != nil || checked != 3 || len(groups) != 3 {
		t.Fatal(groups, checked, err)
	}
	byKey := make(map[string]EmbyMissingSeries)
	for _, g := range groups {
		byKey[g.Key] = g
	}
	main := byKey["tmdb:100"]
	if main.Title != "测试剧" || main.MissingCount != 1 || len(main.Seasons) != 1 || main.Seasons[0].Season != 1 || !reflect.DeepEqual(main.Seasons[0].Ranges, []EmbyEpisodeRange{{5, 5}}) {
		t.Fatalf("cross-library versions or season boundaries incorrect: %+v", main)
	}
	if byKey["emby:local"].MissingCount != 1 || byKey["emby:local2"].MissingCount != 2 {
		t.Fatal("local identities merged or invalid range trusted", byKey)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := buildEmbyMissing(ctx, items); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation ignored", err)
	}
}

func TestMissingEpisodesPublishedScopeEventsAndPaging(t *testing.T) {
	f := newEmbyFixture(t)
	ctx := context.Background()
	before, err := f.service.MissingEpisodes(ctx, EmbyMissingQuery{})
	if err != nil || before.Ready {
		t.Fatal("unsynced scope must not report no gaps", before, err)
	}
	scope := embyScope(f.service.snapshot())
	items := make([]model.EmbyItem, 0, 60)
	for i := 1; i <= 30; i++ {
		id := fmt.Sprintf("series-%02d", i)
		items = append(items, model.EmbyItem{ID: id, Type: "Series", TMDB: fmt.Sprint(i), Name: fmt.Sprintf("剧集%02d", i)},
			model.EmbyItem{ID: "ep-" + id, Type: "Episode", SeriesID: id, Season: 1, Episode: 2, EndEpisode: -1})
	}
	if err := f.db.EmbyStage(ctx, scope, "published", items); err != nil {
		t.Fatal(err)
	}
	if err := f.db.EmbyPublish(ctx, scope, "published", 0); err != nil {
		t.Fatal(err)
	}
	cache, err := encodeTMDB(&tmdb.Detail{Item: tmdb.Item{PosterURL: "/cached-poster.jpg"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.db.SaveTMDBCache(ctx, tmdbDetailCachePrefix+"tv:1", cache, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	if err := f.db.EmbyStage(ctx, scope, "staging", []model.EmbyItem{{ID: "staged", Type: "Episode", SeriesID: "series-01", Season: 1, Episode: 1}}); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	calls := f.detailCalls + f.pageCalls
	f.mu.Unlock()
	page, err := f.service.MissingEpisodes(ctx, EmbyMissingQuery{})
	if err != nil || !page.Ready || page.Total != 30 || len(page.Items) != 24 || !page.HasMore || page.Summary.Episodes != 30 || page.CheckedSeries != 30 || page.Items[0].PosterURL != "/cached-poster.jpg" {
		t.Fatal(page, err)
	}
	if page.Items[1].PosterURL != "/api/admin/emby/posters/"+scope+"/series-02" {
		t.Fatal("uncached TMDB metadata must use the known Emby series ID", page.Items[1])
	}
	next, err := f.service.MissingEpisodes(ctx, EmbyMissingQuery{Offset: page.NextOffset})
	if err != nil || len(next.Items) != 6 || next.HasMore || next.Items[0].TMDBID != "25" {
		t.Fatal(next, err)
	}
	filtered, err := f.service.MissingEpisodes(ctx, EmbyMissingQuery{Search: "剧集01"})
	if err != nil || filtered.Total != 1 || filtered.Items[0].TMDBID != "1" {
		t.Fatal(filtered, err)
	}
	filtered, err = f.service.MissingEpisodes(ctx, EmbyMissingQuery{Search: "30", Offset: 999})
	if err != nil || filtered.Total != 1 || filtered.Offset != 0 || filtered.Items[0].TMDBID != "30" {
		t.Fatal(filtered, err)
	}
	f.mu.Lock()
	afterCalls := f.detailCalls + f.pageCalls
	f.mu.Unlock()
	if afterCalls != calls {
		t.Fatal("detection contacted Emby")
	}
	if err := f.db.EmbyEnqueue(ctx, scope, "new-episode", "upsert"); err != nil {
		t.Fatal(err)
	}
	event, err := f.db.EmbyEventForItem(ctx, scope, "new-episode")
	if err != nil || event == nil {
		t.Fatal(event, err)
	}
	if err := f.db.EmbyApplyEvent(ctx, scope, "published", *event, []model.EmbyItem{{ID: "new-episode", Type: "Episode", SeriesID: "series-01", Season: 1, Episode: 1}}, false); err != nil {
		t.Fatal(err)
	}
	filtered, err = f.service.MissingEpisodes(ctx, EmbyMissingQuery{Search: "剧集01"})
	if err != nil || filtered.Total != 0 {
		t.Fatal("incremental arrival must remove the gap", filtered, err)
	}
	if err := f.db.EmbyQueueSync(ctx, scope); err != nil {
		t.Fatal(err)
	}
	if err := f.db.EmbyEnqueue(ctx, scope, "pending", "upsert"); err != nil {
		t.Fatal(err)
	}
	page, err = f.service.MissingEpisodes(ctx, EmbyMissingQuery{})
	if err != nil || len(page.Notices) < 2 || !page.Ready {
		t.Fatal("old snapshot must remain visible with warnings", page, err)
	}
	f.service.mu.Lock()
	f.service.config.LibraryIDs = []string{"different-library"}
	f.service.mu.Unlock()
	page, err = f.service.MissingEpisodes(ctx, EmbyMissingQuery{})
	if err != nil || page.Ready || len(page.Items) != 0 {
		t.Fatal("scope change reused an old snapshot", page, err)
	}
	for _, q := range []EmbyMissingQuery{{Offset: -1}, {Offset: 1_000_001}, {Search: strings.Repeat("剧", 101)}} {
		if _, err := f.service.MissingEpisodes(ctx, q); !errors.Is(err, ErrEmbyMissingInput) {
			t.Fatal("bad query accepted", q, err)
		}
	}
}

func TestEmbyMissingNoticesDistinguishMetadataAndIndexFailures(t *testing.T) {
	for _, tt := range []struct {
		name   string
		status EmbyStatus
		broken int
		want   map[string]EmbyMissingNotice
	}{
		{name: "metadata alone is neutral", status: EmbyStatus{Connected: true, Failed: 8, MetadataFailed: 8}, want: map[string]EmbyMissingNotice{"metadata_incomplete": {Level: "info", Count: 8}}},
		{name: "real event failures remain visible", status: EmbyStatus{Connected: true, Pending: 5, MetadataPending: 3, Failed: 10, MetadataFailed: 8}, want: map[string]EmbyMissingNotice{"events_pending": {Level: "info", Count: 2}, "events_failed": {Level: "warning", Count: 2}, "metadata_incomplete": {Level: "info", Count: 11}}},
		{name: "unconfirmed is not disconnected", status: EmbyStatus{}, want: map[string]EmbyMissingNotice{"connection_unconfirmed": {Level: "info"}}},
		{name: "connection error", status: EmbyStatus{ConnectionError: "timeout"}, want: map[string]EmbyMissingNotice{"connection_failed": {Level: "warning"}}},
		{name: "episode information is required", status: EmbyStatus{Connected: true}, broken: 2, want: map[string]EmbyMissingNotice{"episodes_incomplete": {Level: "warning", Count: 2}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			notices := embyMissingNotices(tt.status, tt.broken)
			if len(notices) != len(tt.want) {
				t.Fatal(notices)
			}
			for _, n := range notices {
				want, ok := tt.want[n.Code]
				if !ok || n.Level != want.Level || n.Count != want.Count || n.Message == "" {
					t.Fatal(n)
				}
			}
		})
	}
}

func TestEmbyMissingIncrementalWithoutTMDB(t *testing.T) {
	f := newEmbyFixture(t)
	f.sync(t)
	ctx := context.Background()
	f.items = append(f.items,
		emby.Item{ID: "local", Type: "Series", ParentID: "tv", Name: "本地资源"},
		emby.Item{ID: "local-1", Type: "Episode", SeriesID: "local", ParentIndexNumber: number(1), IndexNumber: number(1)},
		emby.Item{ID: "local-3", Type: "Episode", SeriesID: "local", ParentIndexNumber: number(1), IndexNumber: number(3)},
	)
	series := f.event(t, "local", "upsert")
	f.apply(t, series)
	page, err := f.service.MissingEpisodes(ctx, EmbyMissingQuery{Search: "本地资源"})
	if err != nil || len(page.Items) != 1 || page.Items[0].Key != "emby:local" || page.Items[0].MissingCount != 1 {
		t.Fatal("TMDB metadata blocked a complete series notification", page, err)
	}
	f.items = append(f.items, emby.Item{ID: "local-2", Type: "Episode", SeriesID: "local", ParentIndexNumber: number(1), IndexNumber: number(2)})
	episode := f.event(t, "local-2", "upsert")
	f.apply(t, episode)
	page, err = f.service.MissingEpisodes(ctx, EmbyMissingQuery{Search: "本地资源"})
	if err != nil || len(page.Items) != 0 || page.CheckedSeries != 2 {
		t.Fatal("TMDB metadata blocked the newly arrived episode", page, err)
	}
	state, err := f.service.Status(ctx)
	if err != nil || state.MetadataPending != 2 {
		t.Fatal("local updates consumed metadata retries", state, err)
	}
	// A stale metadata result must not apply records or consume its replacement event.
	f.items[len(f.items)-1].IndexNumber = number(4)
	latest := f.event(t, "local-2", "upsert")
	f.apply(t, episode)
	page, err = f.service.MissingEpisodes(ctx, EmbyMissingQuery{Search: "本地资源"})
	if err != nil || len(page.Items) != 0 {
		t.Fatal("stale metadata result changed the local index", page, err)
	}
	f.apply(t, latest)
	page, err = f.service.MissingEpisodes(ctx, EmbyMissingQuery{Search: "本地资源"})
	if err != nil || len(page.Items) != 1 || page.Items[0].MissingCount != 1 {
		t.Fatal("replacement notification did not update the local index", page, err)
	}
	// Invalid episode fields stay out of the algorithm and produce an explicit notice.
	f.items = append(f.items, emby.Item{ID: "local-incomplete", Type: "Episode", SeriesID: "local", ParentIndexNumber: number(1)})
	f.apply(t, f.event(t, "local-incomplete", "upsert"))
	page, err = f.service.MissingEpisodes(ctx, EmbyMissingQuery{Search: "本地资源"})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, notice := range page.Notices {
		if notice.Code == "episodes_incomplete" && notice.Count == 1 && notice.Level == "warning" {
			found = true
		}
	}
	if !found || page.Items[0].MissingCount != 1 {
		t.Fatal("incomplete episode fields were hidden or counted as gaps", page)
	}
	for i := range f.items {
		if f.items[i].ID == "local" {
			f.items[i].ProviderIDs = map[string]string{"Tmdb": "9999"}
		}
	}
	f.apply(t, f.event(t, "local", "upsert"))
	page, err = f.service.MissingEpisodes(ctx, EmbyMissingQuery{Search: "本地资源"})
	if err != nil || len(page.Items) != 1 || page.Items[0].Key != "tmdb:9999" || f.match(t, "tv", "9999").State != "present" {
		t.Fatal("corrected TMDB binding did not update detection and matching", page, err)
	}
}

func TestEmbyMetadataIndexDoesNotPublishPartialReads(t *testing.T) {
	f := newEmbyFixture(t)
	f.sync(t)
	f.items = append(f.items, emby.Item{ID: "new-local", Type: "Series", ParentID: "tv", Name: "分页失败本地剧"})
	for i := 1; i <= 201; i++ {
		f.items = append(f.items, emby.Item{ID: fmt.Sprintf("new-local-%d", i), Type: "Episode", SeriesID: "new-local", ParentIndexNumber: number(1), IndexNumber: number(i * 2)})
	}
	f.failPage = true
	f.apply(t, f.event(t, "new-local", "upsert"))
	page, err := f.service.MissingEpisodes(context.Background(), EmbyMissingQuery{Search: "分页失败本地剧"})
	if err != nil || len(page.Items) != 0 {
		t.Fatal("partial series pages entered the published index", page, err)
	}
	state, err := f.service.Status(context.Background())
	if err != nil || state.Pending != 1 || state.MetadataPending != 0 {
		t.Fatal("page failure was incorrectly downgraded to missing metadata", state, err)
	}
}
