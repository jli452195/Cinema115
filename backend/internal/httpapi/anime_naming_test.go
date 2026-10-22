package httpapi

import (
	"context"
	"strings"
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
	in := model.AnimeNamingInput{TMDBID: "117061", Season: 2, Fingerprint: fingerprint, Sources: []model.AnimeSourceMapping{{SourceRef: "main", Segments: []model.AnimeMappingSegment{{First: 25, SourceBase: 25, TargetSeason: 2, TargetBase: 1}}}}}
	body := map[string]any{"naming": in, "items": []map[string]string{{"sourceRef": "main", "title": "[组] 番剧 [25][1080p]", "groupName": "测试组"}, {"sourceRef": "main", "title": "[组] 番剧 [26][1080p]", "groupName": "测试组"}, {"sourceRef": "main", "title": "[组] 番剧 [12.5][1080p]", "groupName": "测试组"}}}
	preview := f.call(t, "POST", "/naming/preview", f.admin, "", body, 200)
	items := preview["items"].([]any)
	first := items[0].(map[string]any)
	if first["status"] != "mapped" || !strings.Contains(first["pathStem"].(string), ".S02E01.测试组") || items[1].(map[string]any)["status"] != "mapped" || items[2].(map[string]any)["status"] != "unmapped" {
		t.Fatal(preview)
	}
	in.Fingerprint = strings.Repeat("b", 64)
	body["naming"] = in
	f.call(t, "POST", "/naming/preview", f.admin, "", body, 409)
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
