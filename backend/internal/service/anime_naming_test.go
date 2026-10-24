package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"strm115/backend/internal/client/tmdb"
	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
)

type animeNamingFake struct {
	offline bool
	calls   int
	seasons []int
}

func (f *animeNamingFake) Browse(_ context.Context, o tmdb.Options) (*tmdb.Page, error) {
	f.calls++
	return &tmdb.Page{Items: []tmdb.Item{{ID: 117061, Kind: "tv", Title: "侦探已死", OriginalTitle: "The Detective Is Already Dead", Date: "2021-07-04"}}}, nil
}
func (f *animeNamingFake) AnimeDetails(context.Context, int) (*tmdb.Detail, error) {
	f.calls++
	if f.offline {
		return nil, errors.New("offline")
	}
	return &tmdb.Detail{Item: tmdb.Item{ID: 117061, Kind: "tv", Title: "侦探已死", Date: "2021-07-04"}, Seasons: []tmdb.SeasonSummary{{Number: 1, Date: "2021-07-04", EpisodeCount: 12}, {Number: 2, Date: "2026-10-07", EpisodeCount: 13}}}, nil
}
func (f *animeNamingFake) AnimeSeason(_ context.Context, _ int, n int) (*tmdb.SeasonDetail, error) {
	f.calls++
	f.seasons = append(f.seasons, n)
	ep := make([]tmdb.Episode, 13)
	for i := range ep {
		ep[i] = tmdb.Episode{ID: n*100 + i + 1, Number: i + 1, Name: fmt.Sprint("第", i+1, "集"), Date: fmt.Sprintf("2026-10-%02d", i+1), SourceSeason: n, SourceNumber: i + 1}
	}
	return &tmdb.SeasonDetail{Number: n, Episodes: ep}, nil
}
func (f *animeNamingFake) AnimeGroups(context.Context, int) ([]tmdb.EpisodeGroupSummary, error) {
	return []tmdb.EpisodeGroupSummary{}, nil
}
func (f *animeNamingFake) AnimeGroup(context.Context, int, string) (*tmdb.EpisodeGroup, error) {
	return nil, tmdb.ErrNotFound
}

func TestAnimeNamingRecommendationUsesSeasonDateAndKeepsSeriesYear(t *testing.T) {
	s := &AnimeNamingService{Metadata: &animeNamingFake{}}
	ctx := context.Background()
	search, err := s.Search(ctx, "侦探已经死了。 第二季")
	if err != nil || search.RecommendedID != "117061" {
		t.Fatal(search, err)
	}
	target, err := s.Target(ctx, AnimeNamingTargetInput{TMDBID: "117061"}, "2026-10-07")
	if err != nil || target.Season != 2 || target.Year != "2021" || target.Seasons[1].Episodes[0].ID != "201" || target.Seasons[1].Episodes[0].Date != "2026-10-01" {
		t.Fatal(target, err)
	}
	input := model.AnimeNamingInput{TMDBID: target.TMDBID, Season: 2, Fingerprint: strings.Repeat("a", 64)}
	if _, err := s.Resolve(ctx, input, nil); !errors.Is(err, ErrAnimeNamingChanged) {
		t.Fatal("stale metadata accepted", err)
	}
}
func TestAnimeNamingAtomicSaveReplayAndOfflineLocalEdit(t *testing.T) {
	s, _, _, in := animeAdminFixture(t)
	in.SettingsRevision = animeSaveReadySettings(t, s).Revision
	f := &animeNamingFake{}
	s.Naming = &AnimeNamingService{Metadata: f}
	ctx := context.Background()
	season := 2
	target, err := s.Naming.Target(ctx, AnimeNamingTargetInput{TMDBID: "117061", Season: &season}, "")
	if err != nil {
		t.Fatal(err)
	}
	ref := in.Config.Sources[0].Ref
	in.Config.Naming = &model.AnimeNamingInput{TMDBID: target.TMDBID, Season: season, Fingerprint: target.Fingerprint, Sources: []model.AnimeSourceMapping{{SourceRef: ref, Segments: []model.AnimeMappingSegment{{First: 25, SourceBase: 25, TargetSeason: 2, TargetBase: 1}}}}}
	r, err := s.SaveDefinition(ctx, "admin", "naming_create_001", "", in)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := s.Store.GetAnimeSubscription(ctx, r.ID, false)
	if err != nil || saved.Naming == nil {
		t.Fatal(saved, err)
	}
	if saved.Naming.Sources[0].SourceRef != r.SourceIDs[ref] || in.Config.Naming.Sources[0].SourceRef != ref {
		t.Fatal("unstable reference or mutated input", saved.Naming)
	}
	f.offline = true
	before := f.calls
	if again, err := s.SaveDefinition(ctx, "admin", "naming_create_001", "", in); err != nil || again.ID != r.ID || f.calls != before {
		t.Fatal("replay re-read metadata", again, err)
	}
	in.Config.Entry = AnimeEntryRef{ID: saved.EntryID}
	in.ExpectedRevision = saved.Revision
	in.Config.Sources = []model.AnimeSourceConfig{animeHTTPFreeCompleteSource(saved.Sources[0])}
	in.Config.Naming.Sources[0].SourceRef = saved.Sources[0].ID
	in.Config.Naming.Sources[0].Segments[0].TargetBase = 2
	if _, err := s.SaveDefinition(ctx, "admin", "naming_edit_001", r.ID, in); err != nil {
		t.Fatal("verified snapshot unavailable offline", err)
	}
	updated, _ := s.Store.GetAnimeSubscription(ctx, r.ID, false)
	n := 30
	projection := updated.Naming.Project(saved.Sources[0].ID, model.AnimeReleaseParse{Kind: "episode", Episode: &n})
	if projection.Status != "mapped" || *projection.Episode != 7 || f.calls != before {
		t.Fatal(projection, f.calls, before)
	}
}
func TestAnimeNamingDoesNotReadOrChangeGlobalNumbering(t *testing.T) {
	s, _, _ := numberingFixture(t)
	ctx := context.Background()
	info := saveGroup(t, s, 80462, 0)
	global, err := s.Details(ctx, "tv", 80462)
	if err != nil || global.EpisodeCount != 12 {
		t.Fatal(global, err)
	}
	raw, err := s.AnimeDetails(ctx, 80462)
	if err != nil || raw.EpisodeCount != 29 || raw.Numbering != nil {
		t.Fatal("anime inherited global numbering", raw, err)
	}
	season, err := s.AnimeSeason(ctx, 80462, 1)
	if err != nil || len(season.Episodes) != 29 {
		t.Fatal(season, err)
	}
	if _, err := s.AnimeGroup(ctx, 80462, "foreigngroup"); !errors.Is(err, tmdb.ErrNotFound) {
		t.Fatal("foreign group accepted", err)
	}
	after, err := s.NumberingRevision(ctx, 80462)
	if err != nil || after != info.Revision {
		t.Fatal("global revision changed", after, err)
	}
}

func TestAnimeNamingReadsOnlySelectedSeasonAndRejectsRemovedOptions(t *testing.T) {
	f := &animeNamingFake{}
	s := &AnimeNamingService{Metadata: f}
	season := 2
	target, err := s.Target(context.Background(), AnimeNamingTargetInput{TMDBID: "117061", Season: &season}, "")
	if err != nil {
		t.Fatal(err)
	}
	if f.calls != 2 || len(f.seasons) != 1 || f.seasons[0] != season || target.Seasons[0].Episodes != nil {
		t.Fatal("loaded an unused season", f.calls, f.seasons, target)
	}
	in := model.AnimeNamingInput{TMDBID: target.TMDBID, Season: 2, Fingerprint: target.Fingerprint}
	if _, err := s.Resolve(context.Background(), in, nil); err != nil {
		t.Fatal("selected season snapshot changed", err)
	}
	for _, mapping := range []model.AnimeSourceMapping{
		{SourceRef: "main"},
		{SourceRef: "main", Segments: []model.AnimeMappingSegment{{TargetSeason: 1}}},
		{SourceRef: "main", Segments: []model.AnimeMappingSegment{{TargetSeason: 2, Last: bindingPtr(12)}}},
		{SourceRef: "main", Segments: []model.AnimeMappingSegment{{TargetSeason: 2}}, Overrides: []model.AnimeMappingOverride{{RawKind: "special", RawNumber: "SP1", Action: "ignore"}}},
	} {
		in.Sources = []model.AnimeSourceMapping{mapping}
		before := f.calls
		if _, err := s.Resolve(context.Background(), in, nil); !errors.Is(err, storage.ErrAnimeInvalid) || f.calls != before {
			t.Fatal("removed options made metadata requests", mapping, err, f.calls, before)
		}
	}
}

func TestAnimeNamingSplitCourSearchAndSeasonFallback(t *testing.T) {
	for _, tc := range []struct {
		name, query string
		season      int
	}{
		{"Re：从零开始的异世界生活 第三季 反击篇", "Re：从零开始的异世界生活", 3},
		{"Re:ゼロから始める異世界生活 3rd season 反擊編", "Re:ゼロから始める異世界生活", 3},
		{"Example Season 12 Part 2", "Example", 12},
		{"作品 第二季", "作品", 2},
	} {
		if query, hint := animeSearchName(tc.name), animeSeasonHint([]string{tc.name}); query != tc.query || hint != tc.season {
			t.Fatal(tc, query, hint)
		}
	}
	if animeSeasonHint([]string{"作品 第二季", "Example 3rd season"}) != 0 {
		t.Fatal("conflicting title hints must not force a season")
	}
	s := &AnimeNamingService{Metadata: &animeNamingFake{}}
	target, err := s.Target(context.Background(), AnimeNamingTargetInput{TMDBID: "117061"}, "2027-02-05", "作品 第二季 反击篇")
	if err != nil || target.Season != 2 {
		t.Fatal("split cour fell back to first season", target, err)
	}
}
