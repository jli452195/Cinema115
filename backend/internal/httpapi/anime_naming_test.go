package httpapi

import (
	"context"
	"testing"

	"strm115/backend/internal/client/tmdb"
	"strm115/backend/internal/model"
	"strm115/backend/internal/service"
	"strm115/backend/internal/storage"
)

type animeHTTPNaming struct{}

func (animeHTTPNaming) Browse(context.Context, tmdb.Options) (*tmdb.Page, error) {
	return &tmdb.Page{Items: []tmdb.Item{{ID: 117061, Kind: "tv", Title: "可信篇章", Date: "2021-07-04"}}}, nil
}
func (animeHTTPNaming) AnimeDetails(context.Context, int) (*tmdb.Detail, error) {
	return &tmdb.Detail{Item: tmdb.Item{ID: 117061, Title: "可信篇章", Date: "2021-07-04"}, Seasons: []tmdb.SeasonSummary{{Number: 2, Name: "第二季", EpisodeCount: 1}}}, nil
}
func (animeHTTPNaming) AnimeSeason(context.Context, int, int) (*tmdb.SeasonDetail, error) {
	return &tmdb.SeasonDetail{Number: 2, Episodes: []tmdb.Episode{{ID: 201, Number: 1, Name: "开始", SourceSeason: 2, SourceNumber: 1}}}, nil
}
func (animeHTTPNaming) AnimeGroups(context.Context, int) ([]tmdb.EpisodeGroupSummary, error) {
	return []tmdb.EpisodeGroupSummary{}, nil
}
func (animeHTTPNaming) AnimeGroup(context.Context, int, string) (*tmdb.EpisodeGroup, error) {
	return nil, tmdb.ErrNotFound
}

func TestAnimeNamingReadOnlyRoutesAndAuthorization(t *testing.T) {
	f := newAnimeHTTPFixture(t)
	f.s.Naming = &service.AnimeNamingService{Metadata: animeHTTPNaming{}}
	for _, token := range []string{"", f.user} {
		status := 401
		if token != "" {
			status = 403
		}
		f.call(t, "POST", "/naming/search", token, "", map[string]any{"bangumiId": "123"}, status)
	}
	r := f.call(t, "POST", "/naming/search", f.admin, "", map[string]any{"bangumiId": "123"}, 200)
	if r["recommendedId"] != "117061" {
		t.Fatal(r)
	}
	target := f.call(t, "POST", "/naming/target", f.admin, "", map[string]any{"tmdbId": "117061", "bangumiId": "123"}, 200)
	fingerprint, _ := target["fingerprint"].(string)
	if len(fingerprint) != 64 || target["year"] != "2021" {
		t.Fatal(target)
	}
	mapping := model.AnimeSourceMapping{SourceRef: "main", Segments: []model.AnimeMappingSegment{{First: 0, SourceBase: 0, TargetSeason: 2, TargetBase: 0}}}
	body := map[string]any{"target": target, "mapping": mapping, "group": "字幕组", "items": []map[string]any{{"id": "release", "title": "动画 - 13 [1080p][SC_TC]", "parse": map[string]any{"kind": "episode", "episode": 13, "rawEpisode": "13"}}}}
	for _, token := range []string{"", f.user} {
		status := 401
		if token != "" {
			status = 403
		}
		f.call(t, "POST", "/naming/preview", token, "", body, status)
	}
	// This route needs no live TMDB provider and cannot save preview metadata.
	f.s.Naming.Metadata = nil
	preview := f.call(t, "POST", "/naming/preview", f.admin, "", body, 200)
	if len(preview["items"].([]any)) != 1 {
		t.Fatal(preview)
	}
	f.s.Naming.Metadata = animeHTTPNaming{}
	f.call(t, "POST", "/naming/preview", f.admin, "", nil, 400)
	f.call(t, "POST", "/naming/target", f.admin, "", map[string]any{"tmdbId": "117061", "extraSeasons": []int{1}}, 400)
	f.call(t, "POST", "/naming/target", f.admin, "", map[string]any{"tmdbId": "117061", "season": 99}, 404)
	f.call(t, "POST", "/naming/search", f.admin, "", map[string]any{"keyword": ""}, 400)
	f.call(t, "GET", "/naming/groups/117061", f.admin, "", nil, 200)
	list, err := f.s.Store.ListAnimeSubscriptions(context.Background(), storage.AnimeSubscriptionFilter{})
	if err != nil || list.Total != 0 {
		t.Fatal("read-only naming routes persisted data", list, err)
	}
	f.s.Naming.Metadata = nil
	f.call(t, "GET", "/naming/groups/117061", f.admin, "", nil, 409)
}
