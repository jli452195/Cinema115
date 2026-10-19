package httpapi

import (
	"context"
	"testing"

	"strm115/backend/internal/storage"
)

func TestAnimeChapterEpisodesReadOnlyAdminRoute(t *testing.T) {
	f := newAnimeHTTPFixture(t)
	f.call(t, "GET", "/discovery/subjects/510728/episodes", "", "", nil, 401)
	f.call(t, "GET", "/discovery/subjects/510728/episodes", f.user, "", nil, 403)
	for _, path := range []string{"bad/episodes", "510728/episodes?offset=1", "510728/episodes?offset=-100", "510728/episodes?offset=2000", "510728/episodes?offset=0100"} {
		f.call(t, "GET", "/discovery/subjects/"+path, f.admin, "", nil, 400)
	}
	if f.bgm.episodeCalls != 0 {
		t.Fatal("invalid or unauthorized read reached discovery")
	}
	v := f.call(t, "GET", "/discovery/subjects/510728/episodes?offset=0", f.admin, "", nil, 200)
	item := v["items"].([]any)[0].(map[string]any)
	if item["number"] != float64(59) || item["chapterNumber"] != float64(1) || f.bgm.episodeCalls != 1 {
		t.Fatal(v)
	}
	list, err := f.s.Store.ListAnimeSubscriptions(context.Background(), storage.AnimeSubscriptionFilter{})
	if err != nil || list.Total != 0 {
		t.Fatal("episode read persisted configuration", list, err)
	}
}
