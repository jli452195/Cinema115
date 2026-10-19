package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"strm115/backend/internal/client/anibt"
	"strm115/backend/internal/client/animefeed"
	"strm115/backend/internal/client/imagecache"
	"strm115/backend/internal/client/mikan"
)

type anibtTransport interface {
	Fetch(context.Context, string, int64) ([]byte, error)
}
type anibtCache struct {
	body              []byte
	observed, expires time.Time
}
type anibtFlight struct {
	done   chan struct{}
	result anibtCache
	err    error
}
type AnibtService struct {
	Client                anibtTransport
	mu                    sync.Mutex
	cache                 map[string]anibtCache
	flights               map[string]*anibtFlight
	gate                  chan struct{}
	lastRequest, cooldown time.Time
	spacing               time.Duration
}

func NewAnibtService(client anibtTransport) *AnibtService {
	return &AnibtService{Client: client, cache: make(map[string]anibtCache, 32), flights: make(map[string]*anibtFlight, 8), gate: make(chan struct{}, 1), spacing: 2100 * time.Millisecond}
}

// All AniBT JSON and RSS requests share one bounded cache, request gate and
// cooldown. Failed refreshes retain successes; a cancelled leader does not
// poison another caller. No background goroutines are started.
func (s *AnibtService) read(ctx context.Context, path string, ttl time.Duration, refresh bool, budget *AnimeSourceBudget, validate func([]byte) error) (anibtCache, bool, error) {
	for {
		if err := ctx.Err(); err != nil {
			return anibtCache{}, false, err
		}
		s.mu.Lock()
		if v, ok := s.cache[path]; ok && !refresh && time.Now().Before(v.expires) {
			s.mu.Unlock()
			return v, true, nil
		}
		if f, ok := s.flights[path]; ok {
			s.mu.Unlock()
			select {
			case <-ctx.Done():
				return anibtCache{}, false, ctx.Err()
			case <-f.done:
				if errors.Is(f.err, context.Canceled) || errors.Is(f.err, context.DeadlineExceeded) || errors.Is(f.err, mikan.ErrLimit) {
					continue
				}
				return f.result, true, f.err
			}
		}
		if len(s.flights) >= 16 {
			s.mu.Unlock()
			return anibtCache{}, false, &anibt.HTTPError{Status: 429, RetryAt: time.Now().Add(2 * time.Second)}
		}
		f := &anibtFlight{done: make(chan struct{})}
		s.flights[path] = f
		s.mu.Unlock()
		v, err := s.fetch(ctx, path, ttl, budget, validate)
		s.mu.Lock()
		if err == nil {
			s.cache[path] = v
			bytes := 0
			for key, entry := range s.cache {
				if !time.Now().Before(entry.expires) {
					delete(s.cache, key)
				} else {
					bytes += len(entry.body)
				}
			}
			for len(s.cache) > 32 || bytes > 8<<20 {
				oldestKey := ""
				var oldest time.Time
				for key, entry := range s.cache {
					if oldestKey == "" || entry.observed.Before(oldest) {
						oldestKey, oldest = key, entry.observed
					}
				}
				bytes -= len(s.cache[oldestKey].body)
				delete(s.cache, oldestKey)
			}
		}
		f.result, f.err = v, err
		delete(s.flights, path)
		close(f.done)
		s.mu.Unlock()
		return v, false, err
	}
}
func (s *AnibtService) fetch(ctx context.Context, path string, ttl time.Duration, budget *AnimeSourceBudget, validate func([]byte) error) (anibtCache, error) {
	select {
	case s.gate <- struct{}{}:
	case <-ctx.Done():
		return anibtCache{}, ctx.Err()
	}
	defer func() { <-s.gate }()
	s.mu.Lock()
	cooldown, last := s.cooldown, s.lastRequest
	s.mu.Unlock()
	if time.Now().Before(cooldown) {
		return anibtCache{}, &anibt.HTTPError{Status: 429, RetryAt: cooldown}
	}
	if delay := time.Until(last.Add(s.spacing)); delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return anibtCache{}, ctx.Err()
		}
	}
	limit := int64(4 << 20)
	if budget != nil {
		var err error
		limit, err = budget.startRequest()
		if err != nil {
			return anibtCache{}, err
		}
	}
	s.mu.Lock()
	s.lastRequest = time.Now()
	s.mu.Unlock()
	body, err := s.Client.Fetch(ctx, path, limit)
	if budget != nil {
		budget.addBytes(int64(len(body)))
	}
	if err != nil {
		var e *anibt.HTTPError
		if errors.As(err, &e) && (e.Status == 429 || e.Status == 503 && !e.RetryAt.IsZero()) {
			until := e.RetryAt
			if !until.After(time.Now()) {
				until = time.Now().Add(time.Minute)
			}
			s.mu.Lock()
			s.cooldown = until
			s.mu.Unlock()
		}
		return anibtCache{}, err
	}
	if err := validate(body); err != nil {
		return anibtCache{}, err
	}
	observed := time.Now()
	return anibtCache{body: body, observed: observed, expires: observed.Add(ttl)}, nil
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
	return AnimeSourceEntryResult{Entry: entry, ObservedAt: read.observed, ExpiresAt: read.expires, Cached: cached}, nil
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
