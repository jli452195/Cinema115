package service

import (
	"context"
	"fmt"
	"time"

	"strm115/backend/internal/client/anibt"
	"strm115/backend/internal/client/animefeed"
	"strm115/backend/internal/client/imagecache"
	"strm115/backend/internal/client/mikan"
	"strm115/backend/internal/storage"
)

type AnibtService struct {
	*animeSiteCache
	CatalogLinks AnimeCatalogLinks
}

func NewAnibtService(client animeSiteTransport) *AnibtService {
	return &AnibtService{animeSiteCache: newAnimeSiteCache(client, "anibt")}
}

type AnimeAnibtCatalog struct {
	anibt.Catalog
	FetchedAt int64 `json:"fetchedAt"`
	ExpiresAt int64 `json:"expiresAt"`
}

func (s *AnibtService) Catalog(ctx context.Context, season, keyword string, refresh bool) (AnimeAnibtCatalog, error) {
	var out AnimeAnibtCatalog
	path, err := anibt.CatalogPath(season, keyword)
	if err != nil {
		return out, err
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	read, _, err := s.read(ctx, path, 10*time.Minute, refresh, nil, func(body []byte) error { _, err := anibt.ParseCatalog(body, season); return err })
	if err != nil {
		return out, err
	}
	catalog, err := anibt.ParseCatalog(read.body, season)
	if err != nil {
		return out, err
	}
	for i := range catalog.Days {
		for j := range catalog.Days[i].Items {
			catalog.Days[i].Items[j].CoverURL = imagecache.AnibtCoverURL(catalog.Days[i].Items[j].CoverURL)
		}
	}
	for i := range catalog.Items {
		catalog.Items[i].CoverURL = imagecache.AnibtCoverURL(catalog.Items[i].CoverURL)
	}
	if s.CatalogLinks != nil {
		links := make([]storage.AnimeCatalogLink, 0, len(catalog.Items)+100)
		add := func(item anibt.Item) {
			b := item.BangumiID
			links = append(links, storage.AnimeCatalogLink{ItemID: item.ID, BangumiID: &b, ObservedAt: read.observed.UnixMilli()})
		}
		for _, day := range catalog.Days {
			for _, item := range day.Items {
				add(item)
			}
		}
		for _, item := range catalog.Items {
			add(item)
		}
		if err := s.CatalogLinks.CacheAnimeCatalogLinks(ctx, "anibt", links); err != nil {
			return out, err
		}
	}
	return AnimeAnibtCatalog{Catalog: catalog, FetchedAt: read.observed.UnixMilli(), ExpiresAt: read.expires.UnixMilli()}, nil
}
func (s *AnibtService) ReadAnimeSourceEntry(ctx context.Context, budget *AnimeSourceBudget, id string, refresh bool) (AnimeSourceEntryResult, error) {
	var out AnimeSourceEntryResult
	if budget == nil {
		return out, animefeed.ErrInput
	}
	path, err := anibt.GroupsPath(id)
	if err != nil {
		return out, err
	}
	ctx, cancel := context.WithDeadline(ctx, budget.deadline)
	defer cancel()
	if err := budget.acquire(ctx); err != nil {
		return out, err
	}
	defer func() { <-budget.gate }()
	read, cached, err := s.read(ctx, path, time.Minute, refresh, budget, func(body []byte) error { _, err := anibt.ParseEntry(body, id); return err })
	if err != nil {
		return out, err
	}
	entry, err := anibt.ParseEntry(read.body, id)
	if err != nil {
		return out, err
	}
	out = AnimeSourceEntryResult{Entry: entry, ObservedAt: read.observed, ExpiresAt: read.expires, Cached: cached}
	if err := cacheAnimeEntryLink(ctx, s.CatalogLinks, "anibt", out); err != nil {
		return AnimeSourceEntryResult{}, err
	}
	return out, nil
}
func (s *AnibtService) ReadAnimeSource(ctx context.Context, budget *AnimeSourceBudget, in AnimeSourceInput) (AnimeSourceResult, error) {
	var out AnimeSourceResult
	if budget == nil || in.Source.Site() != "anibt" || !in.Source.Valid() || (in.Kind != "rss" && in.Kind != "history") || in.Kind == "rss" && in.Take != 0 || in.Kind == "history" && (in.Take < 1 || in.Take > 100) {
		return out, animefeed.ErrInput
	}
	if err := budget.bindSource(in.Source); err != nil {
		return out, err
	}
	if in.Previous != nil && (in.Kind != "history" || in.Previous.Source != in.Source || in.Take <= in.Previous.RequestedTake) {
		return out, animefeed.ErrInput
	}
	ctx, cancel := context.WithDeadline(ctx, budget.deadline)
	defer cancel()
	entry, err := s.ReadAnimeSourceEntry(ctx, budget, in.Source.ItemID, false)
	if err != nil {
		return out, err
	}
	total := -1
	found := false
	for _, group := range entry.Entry.Groups {
		if group.ID == in.Source.GroupID {
			found = true
			if group.TotalReleases != nil {
				total = *group.TotalReleases
			}
			break
		}
	}
	if !found {
		return out, mikan.ErrSourceBinding
	}
	take := in.Take
	if in.Kind == "rss" {
		take = 100
	}
	if take > budget.allowance(in.Source) {
		return out, &mikan.LimitError{Kind: "rows"}
	}
	path, err := anibt.RSSPath(in.Source, take)
	if err != nil {
		return out, err
	}
	if err := budget.acquire(ctx); err != nil {
		return out, err
	}
	defer func() { <-budget.gate }()
	read, cached, err := s.read(ctx, path, time.Minute, in.Refresh, budget, func(body []byte) error {
		rows, err := anibt.ParseRSS(body, in.Source)
		if err == nil && len(rows) > take {
			return anibt.ErrInvalid
		}
		return err
	})
	if err != nil {
		return out, fmt.Errorf("read AniBT publisher feed: %w", err)
	}
	rows, err := anibt.ParseRSS(read.body, in.Source)
	if err != nil {
		return out, err
	}
	page := anibt.HistoryPage(in.Source, rows, take, total, read.observed)
	if in.Kind == "rss" {
		page.Kind = "rss"
		page.Completeness = "partial"
		page.StopReason = "rss_window"
		page.RequestedTake = 0
		page.NextTake = 0
	}
	if in.Previous != nil {
		out.WindowChanged = compareMikanHistory(&page, in.Previous.SourcePage)
		if take == 100 && page.Completeness == "partial" {
			page.StopReason, page.NextTake = "source_limit", 0
		}
	}
	budget.replaceCount(in.Source, page.RawCount)
	out.SourcePage, out.ExpiresAt, out.Cached = page, read.expires, cached
	return out, nil
}

type AnimeSourceReader interface {
	ReadAnimeSource(context.Context, *AnimeSourceBudget, AnimeSourceInput) (AnimeSourceResult, error)
	ReadAnimeSourceEntry(context.Context, *AnimeSourceBudget, string, bool) (AnimeSourceEntryResult, error)
}

func (s *AnimeBindingService) sourceReader(provider string) (AnimeSourceReader, error) {
	switch provider {
	case "mikan":
		if s.Mikan != nil {
			return s.Mikan, nil
		}
	case "garden":
		if s.Garden != nil {
			return s.Garden, nil
		}
	case "anibt":
		if s.Anibt != nil {
			return s.Anibt, nil
		}
	}
	return nil, ErrAnimeBindingInput
}
func (s *AnimeBindingService) ReadSource(ctx context.Context, budget *AnimeSourceBudget, in AnimeSourceInput) (AnimeSourceResult, error) {
	in.Source = animefeed.NewSource(in.Source.Site(), in.Source.ItemID, in.Source.GroupID)
	reader, err := s.sourceReader(in.Source.Site())
	if err != nil {
		return AnimeSourceResult{}, err
	}
	return reader.ReadAnimeSource(ctx, budget, in)
}
func (s *AnimeBindingService) ReadEntry(ctx context.Context, budget *AnimeSourceBudget, provider, id string, refresh bool) (AnimeSourceEntryResult, error) {
	reader, err := s.sourceReader(provider)
	if err != nil {
		return AnimeSourceEntryResult{}, err
	}
	return reader.ReadAnimeSourceEntry(ctx, budget, id, refresh)
}
