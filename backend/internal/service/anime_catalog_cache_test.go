package service

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestAnimeCatalogFreshnessAtQuarterBoundary(t *testing.T) {
	// Shanghai is already in April while the host clock is still in March.
	now := time.Date(2026, 3, 31, 16, 1, 0, 0, time.UTC)
	if season := animeCatalogSeason(now); season != "2026-04" {
		t.Fatal("quarter must follow Shanghai date", season)
	}
	for _, tc := range []struct {
		season, keyword string
		want            time.Duration
	}{
		{"2026-01", "", 24 * time.Hour},
		{"2025-10", "", 24 * time.Hour},
		{"2026-04", "", time.Hour},
		{"2026-07", "", time.Hour},
		{"", "search", 10 * time.Minute},
	} {
		if got := animeCatalogTTL(tc.season, tc.keyword, now); got != tc.want {
			t.Errorf("%s %q: %v, want %v", tc.season, tc.keyword, got, tc.want)
		}
	}
}

func TestAnimeCatalogFreshnessAcrossProviders(t *testing.T) {
	now := time.Now().In(animeCatalogTimeZone)
	current := animeCatalogSeason(now)
	history := animeCatalogSeason(now.AddDate(0, -3, 0))
	for _, provider := range []string{"mikan", "anibt", "garden"} {
		for _, tc := range []struct {
			name, season, keyword string
			ttl, age              time.Duration
		}{
			{"current", current, "", time.Hour, 20 * time.Minute},
			{"history", history, "", 24 * time.Hour, 2 * time.Hour},
			{"search", "", "test", 10 * time.Minute, 5 * time.Minute},
		} {
			t.Run(provider+"/"+tc.name, func(t *testing.T) {
				ctx := context.Background()
				var read func(bool) (int64, int64, error)
				var age func(time.Duration)
				var calls func() int
				if provider == "mikan" {
					f := &catalogReader{}
					s := NewMikanService(f, nil)
					read = func(refresh bool) (int64, int64, error) {
						v, err := s.AnimeCatalog(ctx, tc.season, tc.keyword, refresh)
						return v.FetchedAt, v.ExpiresAt, err
					}
					age = func(elapsed time.Duration) {
						s.mu.Lock()
						defer s.mu.Unlock()
						for key, entry := range s.cache {
							entry.fetched, entry.expires = entry.fetched.Add(-elapsed), entry.expires.Add(-elapsed)
							s.cache[key] = entry
						}
						s.lastRequest = time.Time{}
					}
					calls = func() int { return f.calls }
				} else {
					var cache *animeSiteCache
					f := &archiveTransport{}
					if provider == "anibt" {
						label := ""
						if tc.season != "" {
							label = tc.season[:4] + map[string]string{"01": "冬", "04": "春", "07": "夏", "10": "秋"}[tc.season[5:]]
						}
						f.body = []byte(fmt.Sprintf(`{"ok":true,"data":{"byWeekday":[],"total":0,"requestedSeason":%q,"scope":{"kind":"season","season":%q}}}`, label, label))
						s := NewAnibtService(f)
						cache = s.animeSiteCache
						read = func(refresh bool) (int64, int64, error) {
							v, err := s.Catalog(ctx, tc.season, tc.keyword, refresh)
							return v.FetchedAt, v.ExpiresAt, err
						}
					} else {
						f.body = []byte(fmt.Sprintf(`{"ok":true,"data":{"seasons":[%q],"calendar":[[],[],[],[],[],[],[]]}}`, tc.season))
						if tc.keyword != "" {
							f.body = []byte(`{"ok":true,"data":[],"next_cursor":0}`)
						}
						s := NewGardenService(f)
						cache = s.animeSiteCache
						read = func(refresh bool) (int64, int64, error) {
							v, err := s.Catalog(ctx, tc.season, tc.keyword, refresh)
							return v.FetchedAt, v.ExpiresAt, err
						}
					}
					cache.spacing = 0
					age = func(elapsed time.Duration) {
						cache.mu.Lock()
						defer cache.mu.Unlock()
						for key, entry := range cache.cache {
							entry.observed, entry.expires = entry.observed.Add(-elapsed), entry.expires.Add(-elapsed)
							cache.cache[key] = entry
						}
					}
					calls = func() int { return int(f.calls.Load()) }
				}

				fetched, expires, err := read(false)
				if err != nil || expires-fetched != tc.ttl.Milliseconds() {
					t.Fatalf("freshness: %d ms, want %v: %v", expires-fetched, tc.ttl, err)
				}
				age(tc.age)
				if _, _, err := read(false); err != nil || calls() != 1 {
					t.Fatalf("fresh cache must avoid upstream request: %d calls, %v", calls(), err)
				}
				if provider == "mikan" {
					age(0) // Remove the existing transport spacing without waiting.
				}
				if _, _, err := read(true); err != nil || calls() != 2 {
					t.Fatalf("manual refresh must bypass fresh cache: %d calls, %v", calls(), err)
				}
				age(tc.ttl + time.Second)
				if _, _, err := read(false); err != nil || calls() != 3 {
					t.Fatalf("expired cache must fetch on demand: %d calls, %v", calls(), err)
				}
			})
		}
	}
}
