package service

import (
	"context"
	"time"

	"strm115/backend/internal/client/animefeed"
	"strm115/backend/internal/client/garden"
	"strm115/backend/internal/client/mikan"
	"strm115/backend/internal/storage"
)

type GardenService struct {
	*animeSiteCache
	CatalogLinks AnimeCatalogLinks
}

func NewGardenService(client animeSiteTransport) *GardenService {
	return &GardenService{animeSiteCache: newAnimeSiteCache(client, "garden")}
}

type AnimeGardenCatalog struct {
	garden.Catalog
	FetchedAt int64 `json:"fetchedAt"`
	ExpiresAt int64 `json:"expiresAt"`
}

func (s *GardenService) Catalog(ctx context.Context, season, keyword string, refresh bool) (AnimeGardenCatalog, error) {
	var out AnimeGardenCatalog
	path, err := garden.CatalogPath(season, keyword)
	if err != nil {
		return out, err
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	read, _, err := s.read(ctx, path, animeCatalogTTL(season, keyword, time.Now()), refresh, nil, func(body []byte) error { _, err := garden.ParseCatalog(body, season); return err })
	if err != nil {
		return out, err
	}
	catalog, err := garden.ParseCatalog(read.body, season)
	if err != nil {
		return out, err
	}
	if s.CatalogLinks != nil {
		links := make([]storage.AnimeCatalogLink, 0, len(catalog.Items)+100)
		add := func(item garden.Item) {
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
		if err := s.CatalogLinks.CacheAnimeCatalogLinks(ctx, "garden", links); err != nil {
			return out, err
		}
	}
	return AnimeGardenCatalog{Catalog: catalog, FetchedAt: read.observed.UnixMilli(), ExpiresAt: read.expires.UnixMilli()}, nil
}
func (s *GardenService) ReadAnimeSourceEntry(ctx context.Context, budget *AnimeSourceBudget, id string, refresh bool) (AnimeSourceEntryResult, error) {
	var out AnimeSourceEntryResult
	if budget == nil {
		return out, animefeed.ErrInput
	}
	path, err := garden.SubjectPath(id)
	if err != nil {
		return out, err
	}
	ctx, cancel := context.WithDeadline(ctx, budget.deadline)
	defer cancel()
	if err := budget.acquire(ctx); err != nil {
		return out, err
	}
	defer func() { <-budget.gate }()
	subject, cachedSubject, err := s.read(ctx, path, 10*time.Minute, refresh, budget, func(body []byte) error { _, err := garden.ParseSubject(body, id); return err })
	if err != nil {
		return out, err
	}
	item, err := garden.ParseSubject(subject.body, id)
	if err != nil {
		return out, err
	}
	path, err = garden.ResourcesPath(id, garden.GroupScanLimit, nil)
	if err != nil {
		return out, err
	}
	read, cached, err := s.read(ctx, path, time.Minute, refresh, budget, func(body []byte) error {
		_, err := garden.ParseResources(body, id, garden.GroupScanLimit, nil)
		return err
	})
	if err != nil {
		return out, err
	}
	resources, err := garden.ParseResources(read.body, id, garden.GroupScanLimit, nil)
	if err != nil {
		return out, err
	}
	out = AnimeSourceEntryResult{Entry: garden.Entry(item, resources), ObservedAt: read.observed, ExpiresAt: read.expires, Cached: cached && cachedSubject}
	if err := cacheAnimeEntryLink(ctx, s.CatalogLinks, "garden", out); err != nil {
		return AnimeSourceEntryResult{}, err
	}
	return out, nil
}
func (s *GardenService) ReadAnimeSource(ctx context.Context, budget *AnimeSourceBudget, in AnimeSourceInput) (AnimeSourceResult, error) {
	var out AnimeSourceResult
	if budget == nil || in.Source.Site() != "garden" || !in.Source.Valid() || (in.Kind != "rss" && in.Kind != "history") || in.Kind == "rss" && in.Take != 0 || in.Kind == "history" && (in.Take < 1 || in.Take > garden.HistoryLimit) {
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
	selected, err := in.Source.Group(in.confirmedGroupName)
	if in.confirmedGroupName == "" {
		entry, readErr := s.ReadAnimeSourceEntry(ctx, budget, in.Source.ItemID, false)
		if readErr != nil {
			return out, readErr
		}
		err = mikan.ErrSourceBinding
		for _, group := range entry.Entry.Groups {
			if group.ID == in.Source.GroupID {
				selected, err = group, nil
				break
			}
		}
	}
	if err != nil {
		return out, err
	}
	take := in.Take
	if in.Kind == "rss" {
		take = 100
	}
	if take > budget.allowance(in.Source) {
		return out, &mikan.LimitError{Kind: "rows"}
	}
	path, err := garden.ResourcesPath(in.Source.ItemID, take, &selected)
	if err != nil {
		return out, err
	}
	if err := budget.acquire(ctx); err != nil {
		return out, err
	}
	defer func() { <-budget.gate }()
	read, cached, err := s.read(ctx, path, time.Minute, in.Refresh, budget, func(body []byte) error {
		_, err := garden.ParseResources(body, in.Source.ItemID, take, &selected)
		return err
	})
	if err != nil {
		return out, err
	}
	resources, err := garden.ParseResources(read.body, in.Source.ItemID, take, &selected)
	if err != nil {
		return out, err
	}
	page := garden.SourcePage(in.Source, resources, take, read.observed)
	if in.Kind == "rss" {
		page.Kind = "rss"
		page.Completeness = "partial"
		page.StopReason = "rss_window"
		page.RequestedTake = 0
		page.NextTake = 0
		page.Total = nil
	}
	if in.Previous != nil {
		out.WindowChanged = compareMikanHistory(&page, in.Previous.SourcePage)
	}
	budget.replaceCount(in.Source, page.RawCount)
	out.SourcePage, out.ExpiresAt, out.Cached = page, read.expires, cached
	return out, nil
}
