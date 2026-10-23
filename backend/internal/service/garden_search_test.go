package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"testing"

	"strm115/backend/internal/client/animefeed"
	"strm115/backend/internal/client/garden"
)

type gardenSearchFake struct {
	calls int
	fail  bool
	paths []string
}

func (f *gardenSearchFake) Fetch(ctx context.Context, path string, _ int64) ([]byte, error) {
	f.calls++
	f.paths = append(f.paths, path)
	if f.fail {
		return nil, garden.ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	u, _ := url.Parse(path)
	q := u.Query()
	v := garden.Resources{Status: "OK", Rows: []garden.Resource{}}
	v.Pagination.Page, _ = strconv.Atoi(q.Get("page"))
	v.Pagination.Size = garden.SearchPageSize
	complete := true
	v.Pagination.Complete = &complete
	v.Filter.Search = []string{q.Get("search")}
	v.Filter.Provider = q.Get("provider")
	if q.Get("fansub") != "" {
		v.Filter.Fansubs = []string{q.Get("fansub")}
	}
	if q.Get("search") != "empty" {
		v.Rows = append(v.Rows, garden.Resource{ID: 1, Provider: "dmhy", ProviderID: "123", Title: "Old show 1080p", Href: "https://share.dmhy.org/topics/view/123", Size: 1234, Magnet: "magnet:?xt=urn:btih:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa&tr=https://tracker.test", CreatedAt: "2026-10-23T00:00:00Z", Publisher: garden.Person{ID: 1, Name: "uploader"}})
	}
	return json.Marshal(v)
}

func TestGardenResourceSearchCacheAndFallback(t *testing.T) {
	f := &gardenSearchFake{}
	s := NewGardenService(f)
	s.spacing = 0
	ctx := context.Background()
	in := GardenSearchInput{SearchOptions: garden.SearchOptions{Query: "empty"}, Fallback: "original"}
	first, err := s.SearchResources(ctx, in)
	if err != nil || first.Query != "original" || len(first.Items) != 1 || len(first.Groups) != 1 || first.HasMore || f.calls != 2 {
		t.Fatal(first, err, f.calls)
	}
	if first.Items[0].SubjectID != 0 || first.Items[0].Magnet != "magnet:?xt=urn:btih:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Fatal(first.Items)
	}
	again, err := s.SearchResources(ctx, in)
	if err != nil || !again.Cached || f.calls != 2 {
		t.Fatal(again, err, f.calls)
	}
	f.fail = true
	in.Refresh = true
	if _, err = s.SearchResources(ctx, in); !errors.Is(err, garden.ErrUnavailable) {
		t.Fatal(err)
	}
	in.Refresh = false
	if _, err = s.SearchResources(ctx, in); err != nil || f.calls != 3 {
		t.Fatal("failed refresh replaced cache", err, f.calls)
	}
	f.fail = false
	in.Page = 2
	page, err := s.SearchResources(ctx, in)
	if err != nil || page.Query != "empty" || f.calls != 4 {
		t.Fatal("later page triggered fallback", page, err)
	}
	in.GroupID, in.GroupName = "unknown_f1", "test"
	if _, err = s.SearchResources(ctx, in); !errors.Is(err, animefeed.ErrInput) || f.calls != 4 {
		t.Fatal("invalid input reached upstream", err)
	}
	for _, path := range f.paths {
		u, _ := url.Parse(path)
		if u.Path != "/resources" || u.Query().Get("subject") != "" || u.Query().Get("metadata") != "" {
			t.Fatal("unexpected metadata request", path)
		}
	}
}
