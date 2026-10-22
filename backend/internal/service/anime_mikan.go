package service

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"strm115/backend/internal/client/mikan"
)

type animeMikanReader interface {
	ReadSource(context.Context, mikan.SourceRequest) (mikan.SourcePage, error)
}

type AnimeSourceInput struct {
	Source             mikan.Source
	Kind               string // rss or history; entry verification is automatic
	Take               int
	Refresh            bool
	Previous           *AnimeSourceResult // Trusted prior snapshot, supplied by P1-06.
	confirmedGroupName string             // Only the runner supplies server-verified, persisted metadata.
}

type AnimeSourceResult struct {
	mikan.SourcePage
	Cached        bool      `json:"cached"`
	ExpiresAt     time.Time `json:"expiresAt"`
	WindowChanged bool      `json:"windowChanged"`
}

type animeMikanCache struct {
	page    mikan.SourcePage
	expires time.Time
	bytes   int
}

type animeMikanFlight struct {
	done  chan struct{}
	entry animeMikanCache
	err   error
}

func cloneAnimeMikanPage(page mikan.SourcePage) mikan.SourcePage {
	page.Releases = append([]mikan.SourceRelease{}, page.Releases...)
	if page.Entry != nil {
		copy := *page.Entry
		copy.Groups = append([]mikan.Group{}, copy.Groups...)
		copy.BangumiIDs = append([]string{}, copy.BangumiIDs...)
		page.Entry = &copy
	}
	return page
}

// ReadAnimeSource only reads one confirmed publisher's metadata. Cumulative
// history replaces that source's raw count; it is never appended as a new page.
// Failures do not clear cached successes or modify the caller's prior counts.
func (s *MikanService) ReadAnimeSource(ctx context.Context, budget *AnimeSourceBudget, in AnimeSourceInput) (AnimeSourceResult, error) {
	var out AnimeSourceResult
	if budget == nil || !in.Source.Valid() || (in.Kind != "rss" && in.Kind != "history") ||
		(in.Kind == "rss" && in.Take != 0) || (in.Kind == "history" && (in.Take < 1 || in.Take > mikan.AnimeMaxRows)) {
		return out, mikan.ErrInput
	}
	reader, ok := s.Client.(animeMikanReader)
	if !ok {
		return out, errors.New("蜜柑订阅读取功能不可用")
	}
	ctx, cancel := context.WithDeadline(ctx, budget.deadline)
	defer cancel()
	if err := budget.acquire(ctx); err != nil {
		return out, err
	}
	defer func() { <-budget.gate }()
	allowance := budget.allowance(in.Source)
	if allowance <= 0 || in.Take > allowance {
		return out, &mikan.LimitError{Kind: "rows"}
	}
	if in.Previous != nil && (in.Kind != "history" || in.Previous.Kind != "history" || in.Previous.Source != in.Source || in.Take <= in.Previous.RequestedTake) {
		return out, mikan.ErrInput
	}
	if err := budget.bindSource(in.Source); err != nil {
		return out, err
	}
	if in.confirmedGroupName == "" {
		entry, _, err := s.animeMikanRead(ctx, budget, reader, mikan.SourceRequest{Kind: "entry", Source: mikan.Source{ItemID: in.Source.ItemID}, MaxRows: mikan.AnimeMaxRows}, false)
		if err != nil {
			return out, err
		}
		if !validAnimeMikanEntry(entry.page, in.Source.ItemID) {
			return out, mikan.ErrParse
		}
		found := false
		for _, group := range entry.page.Entry.Groups {
			if group.ID == in.Source.GroupID {
				found = true
				break
			}
		}
		if !found {
			return out, mikan.ErrSourceBinding
		}
	}
	page, cached, err := s.animeMikanRead(ctx, budget, reader, mikan.SourceRequest{Kind: in.Kind, Source: in.Source, Take: in.Take, MaxRows: allowance}, in.Refresh)
	if err != nil {
		return out, err
	}
	if page.page.RawCount > allowance {
		return out, &mikan.LimitError{Kind: "rows"}
	}
	budget.replaceCount(in.Source, page.page.RawCount)
	out.SourcePage, out.Cached, out.ExpiresAt = cloneAnimeMikanPage(page.page), cached, page.expires
	if in.Previous != nil {
		out.WindowChanged = compareMikanHistory(&out.SourcePage, in.Previous.SourcePage)
	}
	if cached {
		out.ReadBytes = 0
	}
	return out, nil
}

// A changing cumulative window cannot establish an end boundary. This only
// describes the new snapshot; it never drops or rewrites prior user selections.
func compareMikanHistory(current *mikan.SourcePage, previous mikan.SourcePage) bool {
	old := make(map[string]bool, len(previous.Releases))
	for _, release := range previous.Releases {
		old[release.PostKey] = true
	}
	changed := len(current.Releases) < len(previous.Releases)
	progress := false
	for i, release := range current.Releases {
		if !old[release.PostKey] {
			progress = true
		}
		if i < len(previous.Releases) && release.PostKey != previous.Releases[i].PostKey {
			changed = true
		}
	}
	if changed {
		current.Completeness, current.StopReason = "partial", "more_available"
	}
	if !progress && len(previous.Releases) > 0 {
		current.Completeness, current.StopReason, current.NextTake = "partial", "no_progress", 0
	}
	return changed
}

func (s *MikanService) animeMikanRead(ctx context.Context, budget *AnimeSourceBudget, reader animeMikanReader, in mikan.SourceRequest, refresh bool) (animeMikanCache, bool, error) {
	key := in.Kind + ":" + in.Source.ItemID + ":" + in.Source.GroupID + ":" + strconv.Itoa(in.Take)
	for {
		if err := ctx.Err(); err != nil {
			return animeMikanCache{}, false, err
		}
		s.mu.Lock()
		if entry, ok := s.animeCache[key]; ok && !refresh && time.Now().Before(entry.expires) {
			s.mu.Unlock()
			return entry, true, nil
		}
		if flight, ok := s.animeFlights[key]; ok {
			s.mu.Unlock()
			select {
			case <-ctx.Done():
				return animeMikanCache{}, false, ctx.Err()
			case <-flight.done:
				if errors.Is(flight.err, context.Canceled) || errors.Is(flight.err, context.DeadlineExceeded) || errors.Is(flight.err, mikan.ErrLimit) {
					continue
				}
				return flight.entry, true, flight.err
			}
		}
		if len(s.flights)+len(s.animeFlights) >= 32 {
			s.mu.Unlock()
			return animeMikanCache{}, false, errors.New("蜜柑读取繁忙，请稍后重试")
		}
		flight := &animeMikanFlight{done: make(chan struct{})}
		s.animeFlights[key] = flight
		s.mu.Unlock()
		var page mikan.SourcePage
		err := s.withMikanAccess(ctx, func() error {
			limit, err := budget.startRequest()
			if err != nil {
				return err
			}
			in.BodyLimit = limit
			readCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
			defer cancel()
			page, err = reader.ReadSource(readCtx, in)
			budget.addBytes(page.ReadBytes)
			return err
		})
		entry := animeMikanCache{page: cloneAnimeMikanPage(page)}
		if err == nil && in.Kind == "entry" && !validAnimeMikanEntry(page, in.Source.ItemID) {
			err = mikan.ErrParse
		}
		ttl := time.Minute
		if in.Kind == "entry" {
			ttl = 10 * time.Minute
		}
		entry.expires = page.ObservedAt.Add(ttl)
		if err == nil {
			data, marshalErr := json.Marshal(entry.page)
			if marshalErr != nil {
				err = mikan.ErrParse
			} else {
				entry.bytes = len(data)
			}
		}
		s.mu.Lock()
		if err == nil {
			s.saveAnimeMikanCache(key, entry)
		}
		flight.entry, flight.err = entry, err
		delete(s.animeFlights, key)
		close(flight.done)
		s.mu.Unlock()
		return entry, false, err
	}
}

// Caller holds s.mu. Storage is bounded by both entries and normalized bytes.
func (s *MikanService) saveAnimeMikanCache(key string, entry animeMikanCache) {
	for k, old := range s.animeCache {
		if k == key || !time.Now().Before(old.expires) {
			s.animeCacheBytes -= old.bytes
			delete(s.animeCache, k)
		}
	}
	if entry.bytes > 8<<20 {
		return
	}
	for len(s.animeCache) >= 32 || s.animeCacheBytes+entry.bytes > 8<<20 {
		oldestKey := ""
		var oldest time.Time
		for k, old := range s.animeCache {
			if oldestKey == "" || old.page.ObservedAt.Before(oldest) {
				oldestKey, oldest = k, old.page.ObservedAt
			}
		}
		s.animeCacheBytes -= s.animeCache[oldestKey].bytes
		delete(s.animeCache, oldestKey)
	}
	s.animeCache[key] = entry
	s.animeCacheBytes += entry.bytes
}
