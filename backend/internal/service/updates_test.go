package service

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"strm115/backend/internal/client/tmdb"
	"strm115/backend/internal/model"
)

type recentUpdatesSourceFake struct {
	groups []model.MediaUpdateGroup
	ready  bool
}

func (f recentUpdatesSourceFake) RecentUpdateGroups(context.Context, time.Time, time.Time, int, int) ([]model.MediaUpdateGroup, string, error) {
	status := "waiting"
	if f.ready {
		status = "ready"
	}
	return f.groups, status, nil
}

type recentUpdatesMetadataFake struct{}

func (recentUpdatesMetadataFake) Details(_ context.Context, kind string, id int) (*tmdb.Detail, error) {
	if kind == "movie" {
		return &tmdb.Detail{Item: tmdb.Item{ID: id, Kind: kind, Title: "电影", PosterURL: "movie.jpg", Date: "2026-01-01", Rating: 8.1, Votes: 10}}, nil
	}
	return &tmdb.Detail{Item: tmdb.Item{ID: id, Kind: kind, Title: "剧集", PosterURL: "series.jpg", Date: "2020-01-01", Rating: 8.8, Votes: 20}, Seasons: []tmdb.SeasonSummary{{Number: 2, PosterURL: "season.jpg"}}}, nil
}

func TestRecentUpdatesListUsesSeasonCardsAndStableLabels(t *testing.T) {
	now := time.Date(2026, 9, 17, 4, 0, 0, 0, time.UTC)
	updates := &RecentUpdatesService{
		Source: recentUpdatesSourceFake{ready: true, groups: []model.MediaUpdateGroup{
			{Kind: "tv", TMDBID: 20, FallbackTitle: "旧剧名", Season: 2, Date: "2026-09-17", UpdatedAt: now.Unix(), Episodes: []model.MediaUpdateEpisode{{First: 1, Last: 3}}},
			{Kind: "movie", TMDBID: 10, FallbackTitle: "旧电影名", Season: -1, Date: "2026-09-17", UpdatedAt: now.Unix()},
		}},
		Metadata: recentUpdatesMetadataFake{}, Now: func() time.Time { return now },
	}
	page, err := updates.List(context.Background(), RecentUpdatesQuery{Range: "today"})
	if err != nil || !page.Ready || len(page.Items) != 2 {
		t.Fatal(page, err)
	}
	season := page.Items[0]
	if season.Title != "剧集（第 2 季）" || season.PosterURL != "season.jpg" || season.EpisodeLabel != "S02 E01-E03" || season.EpisodeCount != 3 {
		t.Fatalf("季卡片错误: %+v", season)
	}
	if page.Items[1].Title != "电影" || page.From != "2026-09-17" || page.To != "2026-09-17" {
		t.Fatalf("电影或日期错误: %+v", page)
	}
	if _, err := updates.List(context.Background(), RecentUpdatesQuery{Date: "2026-02-30"}); !errors.Is(err, ErrRecentUpdatesInput) {
		t.Fatal("未拒绝无效日期", err)
	}
}

type recentUpdatesBudgetMetadata struct {
	cachedCalls  atomic.Int32
	networkCalls atomic.Int32
	t            *testing.T
}

func (f *recentUpdatesBudgetMetadata) CachedDetails(_ context.Context, kind string, id int) (*tmdb.Detail, error) {
	f.cachedCalls.Add(1)
	if id <= 2 {
		return nil, nil
	}
	return &tmdb.Detail{Item: tmdb.Item{ID: id, Kind: kind, Title: "cached"}}, nil
}

func (f *recentUpdatesBudgetMetadata) Details(ctx context.Context, _ string, _ int) (*tmdb.Detail, error) {
	f.networkCalls.Add(1)
	if f.cachedCalls.Load() != recentUpdatesPageSize {
		f.t.Error("optional network requests started before cached cards were read")
	}
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) > 2*time.Second {
		f.t.Error("optional metadata inherited the full page timeout")
	}
	return nil, errors.New("metadata unavailable")
}

func TestRecentUpdatesReadsCacheBeforeBoundedOptionalMetadata(t *testing.T) {
	groups := make([]model.MediaUpdateGroup, 0, recentUpdatesPageSize)
	for id := 1; id <= recentUpdatesPageSize; id++ {
		groups = append(groups, model.MediaUpdateGroup{Kind: "movie", TMDBID: id, FallbackTitle: fmt.Sprint(id), UpdatedAt: 1})
	}
	metadata := &recentUpdatesBudgetMetadata{t: t}
	s := &RecentUpdatesService{Source: recentUpdatesSourceFake{ready: true, groups: groups}, Metadata: metadata}
	page, err := s.List(context.Background(), RecentUpdatesQuery{})
	if err != nil || len(page.Items) != recentUpdatesPageSize || metadata.networkCalls.Load() != 2 {
		t.Fatal(len(page.Items), metadata.networkCalls.Load(), err)
	}
	for _, item := range page.Items {
		if item.TMDBID <= 2 {
			if !item.MetadataMissing || item.Title != fmt.Sprint(item.TMDBID) {
				t.Fatal("local timeline was lost after optional metadata failed", item)
			}
		} else if item.MetadataMissing || item.Title != "cached" {
			t.Fatal("cached metadata was blocked by uncached cards", item)
		}
	}
}
