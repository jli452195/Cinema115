package service

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"strm115/backend/internal/client/p115"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type joiningCacheContext struct {
	context.Context
	joined chan struct{}
	once   sync.Once
}

func (c *joiningCacheContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.joined) })
	return c.Context.Done()
}
func TestDisabledDirectCacheStillMergesFlights(t *testing.T) {
	var c DirectLinkCache
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	fetch := func(context.Context) (DirectLinkValue, error) {
		calls.Add(1)
		close(started)
		<-release
		return DirectLinkValue{URL: "https://cdn.115.com/v"}, nil
	}
	go func() {
		defer close(done)
		_, _, _, err := c.Get(ctx, "key", 0, DirectLinkCacheEntry{}, fetch)
		if err != nil {
			t.Error(err)
		}
	}()
	<-started
	waiter := &joiningCacheContext{Context: ctx, joined: make(chan struct{})}
	waitDone := make(chan struct{})
	go func() {
		defer close(waitDone)
		_, hit, expires, err := c.Get(waiter, "key", 0, DirectLinkCacheEntry{}, fetch)
		if err != nil || !hit || !expires.IsZero() {
			t.Error("flight not shared", err)
		}
	}()
	select {
	case <-waiter.joined:
	case <-ctx.Done():
		t.Fatal("waiter did not join")
	}
	close(release)
	<-done
	<-waitDone
	if calls.Load() != 1 || len(c.Status().Entries) != 0 {
		t.Fatal("cache disabled flight behavior incorrect")
	}
	_, hit, expires, err := c.Get(ctx, "key", 0, DirectLinkCacheEntry{}, func(context.Context) (DirectLinkValue, error) {
		calls.Add(1)
		return DirectLinkValue{URL: "https://cdn.115.com/new"}, nil
	})
	if err != nil || hit || !expires.IsZero() || calls.Load() != 2 {
		t.Fatal("completed flight persisted")
	}
}

func TestDirectLinkCacheDefaultMigrationAndValidation(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want int
	}{
		{"", 9},
		{`{"enabled":false,"port":8097}`, 9},
		{`{"enabled":false,"port":8097,"directLinkCacheMinutes":0}`, 0},
		{`{"enabled":false,"port":8097,"directLinkCacheMinutes":12}`, 12},
	} {
		store, err := NewSessionStore(filepath.Join(t.TempDir(), "proxy.enc"), "secret")
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Save(tc.raw); err != nil {
			t.Fatal(err)
		}
		proxy, err := NewEmbyProxy(context.Background(), store, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if proxy.Status().Config.DirectLinkCacheMinutes != tc.want {
			t.Fatal("migration changed explicit setting")
		}
		proxy.Close()
	}
	for _, n := range []int{-1, 31} {
		if _, err := normalizeEmbyProxy(EmbyProxyConfig{Port: 8097, DirectLinkCacheMinutes: n}); err == nil {
			t.Fatal("invalid duration accepted")
		}
	}
}

func TestFriendLinkScopeAndFileRetention(t *testing.T) {
	s, _, selection := setupPlaybackCache(t)
	scope := selection.DirectLinkScope()
	other := *selection
	other.cookie = "UID=22_A; SEID=new"
	if other.DirectLinkScope() == scope {
		t.Fatal("cookie not isolated")
	}
	other = *selection
	other.binding.ParentCID = "100"
	if other.DirectLinkScope() == scope {
		t.Fatal("directory not isolated")
	}
	other = *selection
	other.binding.AccountID = "other"
	if other.DirectLinkScope() == scope {
		t.Fatal("account not isolated")
	}
	_, report, err := s.ResolveWithReport(context.Background(), selection, &p115.ShareInfo{Code: "private-share"}, "1", "source-private", "player-UA")
	if err != nil {
		t.Fatal(err)
	}
	if report.FileCacheKey == "" {
		t.Fatal("missing file cache reference")
	}
	entry := s.Status().Entries[0]
	entry.LastUsed = time.Now().Add(-73 * time.Hour)
	if err := s.put(entry); err != nil {
		t.Fatal(err)
	}
	if err := s.TouchDirectLink(context.Background(), report.FileCacheKey, report.FileCacheID); err != nil {
		t.Fatal(err)
	}
	if time.Since(s.Status().Entries[0].LastUsed) > time.Minute {
		t.Fatal("cache use did not refresh retention")
	}
	if err := s.Forget(context.Background(), report.FileCacheKey); err != nil {
		t.Fatal(err)
	}
	if err := s.TouchDirectLink(context.Background(), report.FileCacheKey, report.FileCacheID); err == nil {
		t.Fatal("forgotten file link accepted")
	}
}

func TestDirectLinkCacheTTLIsolationClearAndPrivacy(t *testing.T) {
	var c DirectLinkCache
	calls := 0
	fetch := func(context.Context) (DirectLinkValue, error) {
		calls++
		return DirectLinkValue{URL: "https://cdn.115.com/private?secret=token", Report: PlaybackResolution{Mode: "朋友分享直取", AccountLabel: "friend"}}, nil
	}
	key := DirectLinkKey("cookie-secret", "share-password", "UA")
	before := time.Now()
	_, hit, expiry, err := c.Get(context.Background(), key, 9*time.Minute, DirectLinkCacheEntry{User: "viewer"}, fetch)
	if err != nil || hit || expiry.Sub(before) < 9*time.Minute || expiry.Sub(before) > 9*time.Minute+time.Second {
		t.Fatal("default TTL not 9 minutes")
	}
	_, hit, expiry2, err := c.Get(context.Background(), key, 9*time.Minute, DirectLinkCacheEntry{}, fetch)
	if err != nil || !hit || calls != 1 || !expiry2.Equal(expiry) {
		t.Fatal("hit extended expiry or refetched")
	}
	raw, _ := json.Marshal(c.Status())
	for _, secret := range []string{"cookie-secret", "share-password"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("status leaked secrets")
		}
	}
	if c.Status().Entries[0].URL != "https://cdn.115.com/private?secret=token" || c.Status().Entries[0].CreatedAt.IsZero() {
		t.Fatal("admin cache missing complete link or creation time")
	}
	for _, other := range []string{DirectLinkKey("new-cookie", "share-password", "UA"), DirectLinkKey("cookie-secret", "share-password", "other-UA")} {
		if _, hit, _, err := c.Get(context.Background(), other, time.Minute, DirectLinkCacheEntry{}, fetch); err != nil || hit {
			t.Fatal("cache isolation failed")
		}
	}
	c.mu.Lock()
	e := c.entries[key]
	e.view.ExpiresAt = time.Now().Add(-time.Second)
	c.entries[key] = e
	c.mu.Unlock()
	if _, hit, _, _ := c.Get(context.Background(), key, time.Minute, DirectLinkCacheEntry{}, fetch); hit {
		t.Fatal("expired entry used")
	}
	c.Remove(key)
	if _, hit, _, _ := c.Get(context.Background(), key, time.Minute, DirectLinkCacheEntry{}, fetch); hit {
		t.Fatal("removed entry used")
	}
	c.Clear()
	if len(c.Status().Entries) != 0 {
		t.Fatal("clear failed")
	}
	for i := 0; i < 2; i++ {
		_, hit, _, _ := c.Get(context.Background(), key, 0, DirectLinkCacheEntry{}, fetch)
		if hit {
			t.Fatal("disabled cache hit")
		}
	}
	if len(c.Status().Entries) != 0 {
		t.Fatal("disabled cache stored link")
	}
}

func TestDirectLinkCacheConcurrentFetchAndCancelledWaiter(t *testing.T) {
	var c DirectLinkCache
	var calls atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	fetch := func(ctx context.Context) (DirectLinkValue, error) {
		calls.Add(1)
		close(started)
		select {
		case <-release:
			return DirectLinkValue{URL: "https://cdn.115.com/v"}, nil
		case <-ctx.Done():
			return DirectLinkValue{}, ctx.Err()
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if _, _, _, err := c.Get(ctx, "key", time.Minute, DirectLinkCacheEntry{}, fetch); err != nil {
			t.Error(err)
		}
	}()
	<-started
	cancelled, stop := context.WithCancel(ctx)
	stop()
	if _, _, _, err := c.Get(cancelled, "key", time.Minute, DirectLinkCacheEntry{}, fetch); !errors.Is(err, context.Canceled) {
		t.Fatal("cancel ignored")
	}
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, _, err := c.Get(ctx, "key", time.Minute, DirectLinkCacheEntry{}, fetch); err != nil {
				t.Error(err)
			}
		}()
	}
	close(release)
	wg.Wait()
	if calls.Load() != 1 || c.Status().Hits != 12 {
		t.Fatal("duplicate fetch", calls.Load(), c.Status().Hits)
	}
}

func TestDirectLinkClearDuringFetchAndFailures(t *testing.T) {
	var c DirectLinkCache
	fetch := func(context.Context) (DirectLinkValue, error) {
		c.Clear()
		return DirectLinkValue{URL: "https://cdn.115.com/v"}, nil
	}
	if _, _, _, err := c.Get(context.Background(), "key", time.Minute, DirectLinkCacheEntry{}, fetch); err != nil {
		t.Fatal(err)
	}
	if len(c.Status().Entries) != 0 {
		t.Fatal("inflight write survived clear")
	}
	calls := 0
	failure := func(context.Context) (DirectLinkValue, error) {
		calls++
		return DirectLinkValue{}, errors.New("failed")
	}
	for i := 0; i < 2; i++ {
		if _, _, _, err := c.Get(context.Background(), "key", time.Minute, DirectLinkCacheEntry{}, failure); err == nil {
			t.Fatal("failure lost")
		}
	}
	if calls != 2 || len(c.Status().Entries) != 0 {
		t.Fatal("failure cached")
	}
}
