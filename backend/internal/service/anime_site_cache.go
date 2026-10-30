package service

import (
	"context"
	"errors"
	"sync"
	"time"

	"strm115/backend/internal/client/animefeed"
	"strm115/backend/internal/client/mikan"
)

type animeSiteTransport interface {
	Fetch(context.Context, string, int64) ([]byte, error)
}
type animeSiteResponse struct {
	body              []byte
	observed, expires time.Time
}
type animeSiteFlight struct {
	done   chan struct{}
	result animeSiteResponse
	err    error
}
type animeSiteCache struct {
	Site                  string
	Client                animeSiteTransport
	mu                    sync.Mutex
	cache                 map[string]animeSiteResponse
	flights               map[string]*animeSiteFlight
	gate                  chan struct{}
	lastRequest, cooldown time.Time
	spacing               time.Duration
}

func newAnimeSiteCache(client animeSiteTransport, site string) *animeSiteCache {
	return &animeSiteCache{Site: site, Client: client, cache: make(map[string]animeSiteResponse, 32), flights: make(map[string]*animeSiteFlight, 8), gate: make(chan struct{}, 1), spacing: 2100 * time.Millisecond}
}

// All site JSON and RSS requests share one bounded cache, request gate and
// cooldown. Failed refreshes retain successes; a cancelled leader does not
// poison another caller. No background goroutines are started.
func (s *animeSiteCache) read(ctx context.Context, path string, ttl time.Duration, refresh bool, budget *AnimeSourceBudget, validate func([]byte) error) (animeSiteResponse, bool, error) {
	return s.readKey(ctx, path, path, ttl, refresh, budget, validate)
}

// readKey allows verified content to share a cache/flight across source URLs.
func (s *animeSiteCache) readKey(ctx context.Context, key, path string, ttl time.Duration, refresh bool, budget *AnimeSourceBudget, validate func([]byte) error) (animeSiteResponse, bool, error) {
	for {
		if err := ctx.Err(); err != nil {
			return animeSiteResponse{}, false, err
		}
		s.mu.Lock()
		if v, ok := s.cache[key]; ok && !refresh && time.Now().Before(v.expires) {
			s.mu.Unlock()
			return v, true, nil
		}
		if f, ok := s.flights[key]; ok {
			s.mu.Unlock()
			select {
			case <-ctx.Done():
				return animeSiteResponse{}, false, ctx.Err()
			case <-f.done:
				if errors.Is(f.err, context.Canceled) || errors.Is(f.err, context.DeadlineExceeded) || errors.Is(f.err, mikan.ErrLimit) {
					continue
				}
				return f.result, true, f.err
			}
		}
		if len(s.flights) >= 16 {
			s.mu.Unlock()
			return animeSiteResponse{}, false, &animefeed.HTTPError{Site: s.Site, Status: 429, RetryAt: time.Now().Add(2 * time.Second)}
		}
		f := &animeSiteFlight{done: make(chan struct{})}
		s.flights[key] = f
		s.mu.Unlock()
		v, err := s.fetch(ctx, path, ttl, budget, validate)
		s.mu.Lock()
		if err == nil {
			s.cache[key] = v
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
		delete(s.flights, key)
		close(f.done)
		s.mu.Unlock()
		return v, false, err
	}
}
func (s *animeSiteCache) fetch(ctx context.Context, path string, ttl time.Duration, budget *AnimeSourceBudget, validate func([]byte) error) (animeSiteResponse, error) {
	select {
	case s.gate <- struct{}{}:
	case <-ctx.Done():
		return animeSiteResponse{}, ctx.Err()
	}
	defer func() { <-s.gate }()
	s.mu.Lock()
	cooldown, last := s.cooldown, s.lastRequest
	s.mu.Unlock()
	if time.Now().Before(cooldown) {
		return animeSiteResponse{}, &animefeed.HTTPError{Site: s.Site, Status: 429, RetryAt: cooldown}
	}
	if delay := time.Until(last.Add(s.spacing)); delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return animeSiteResponse{}, ctx.Err()
		}
	}
	limit := int64(4 << 20)
	if budget != nil {
		var err error
		limit, err = budget.startRequest()
		if err != nil {
			return animeSiteResponse{}, err
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
		var e *animefeed.HTTPError
		if errors.As(err, &e) && (e.Status == 429 || e.Status == 503 && !e.RetryAt.IsZero()) {
			until := e.RetryAt
			if !until.After(time.Now()) {
				until = time.Now().Add(time.Minute)
			}
			s.mu.Lock()
			s.cooldown = until
			s.mu.Unlock()
		}
		return animeSiteResponse{}, err
	}
	if err := validate(body); err != nil {
		return animeSiteResponse{}, err
	}
	observed := time.Now()
	return animeSiteResponse{body: body, observed: observed, expires: observed.Add(ttl)}, nil
}
