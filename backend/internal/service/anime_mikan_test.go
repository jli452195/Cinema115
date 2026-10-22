package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"strm115/backend/internal/client/mikan"
)

type animeMikanFake struct {
	service      *MikanService
	calls        atomic.Int32
	searchCalls  atomic.Int32
	read         func(context.Context, mikan.SourceRequest) (mikan.SourcePage, error)
	searchErr    error
	keepInterval bool
}

func (f *animeMikanFake) Search(context.Context, string, string) (mikan.Result, error) {
	f.searchCalls.Add(1)
	if !f.keepInterval {
		f.service.lastRequest = time.Time{}
	}
	return mikan.Result{Items: []mikan.Item{}}, f.searchErr
}

func (f *animeMikanFake) ReadSource(ctx context.Context, in mikan.SourceRequest) (mikan.SourcePage, error) {
	f.calls.Add(1)
	// Only the gate holder accesses lastRequest. Most tests bypass real pacing;
	// TestAnimeMikanSharedInterval separately verifies the actual site interval.
	if !f.keepInterval {
		f.service.lastRequest = time.Time{}
	}
	if f.read != nil {
		return f.read(ctx, in)
	}
	return animeMikanFakePage(in), nil
}

func animeMikanFakePage(in mikan.SourceRequest) mikan.SourcePage {
	page := mikan.SourcePage{Kind: in.Kind, Source: in.Source, ObservedAt: time.Now(), ReadBytes: 128, Releases: []mikan.SourceRelease{}, Completeness: "partial", StopReason: "more_available", RequestedTake: in.Take}
	if in.Kind == "entry" {
		page.Entry = &mikan.SourceEntry{ID: in.Source.ItemID, Title: "测试番剧", Groups: []mikan.Group{{ID: "45", Name: "字幕组甲"}, {ID: "28", Name: "字幕组乙"}}}
		return page
	}
	page.RawCount = 2
	if in.Kind == "history" {
		page.RawCount = min(in.Take, 2)
		if page.RawCount < in.Take {
			page.Completeness = "visible_end"
			page.StopReason = ""
		}
	}
	page.Releases = []mikan.SourceRelease{{PostKey: strings.Repeat("a", 40), Title: "资源"}}
	return page
}

func newAnimeMikanTestService() (*MikanService, *animeMikanFake) {
	f := &animeMikanFake{}
	s := NewMikanService(f, nil)
	f.service = s
	return s, f
}

func animeMikanTestBudget(t *testing.T, loaded map[mikan.Source]int) *AnimeSourceBudget {
	t.Helper()
	b, err := NewAnimeSourceBudget(loaded)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestAnimeMikanReadCacheAndCumulativeCounts(t *testing.T) {
	s, f := newAnimeMikanTestService()
	ctx := context.Background()
	source := mikan.Source{ItemID: "2259", GroupID: "45"}
	budget := animeMikanTestBudget(t, nil)
	in := AnimeSourceInput{Source: source, Kind: "history", Take: 1}
	first, err := s.ReadAnimeSource(ctx, budget, in)
	if err != nil || first.RawCount != 1 || first.Cached || budget.Stats().Requests != 2 {
		t.Fatal(first, budget.Stats(), err)
	}
	in.Take = 3
	second, err := s.ReadAnimeSource(ctx, budget, in)
	if err != nil || second.RawCount != 2 || second.Completeness != "visible_end" || budget.Stats().TotalRawCount != 2 || budget.Stats().Requests != 3 {
		t.Fatal(second, budget.Stats(), err)
	}
	second.Releases[0].Title = "modified"
	newBudget := animeMikanTestBudget(t, nil)
	cached, err := s.ReadAnimeSource(ctx, newBudget, in)
	if err != nil || !cached.Cached || cached.Releases[0].Title != "资源" || !cached.ObservedAt.Equal(second.ObservedAt) || !cached.ExpiresAt.Equal(second.ExpiresAt) || cached.ReadBytes != 0 || newBudget.Stats().Requests != 0 || f.calls.Load() != 3 {
		t.Fatal(cached, newBudget.Stats(), err)
	}
	in.Take = 4
	if _, err := s.ReadAnimeSource(ctx, budget, in); !errors.Is(err, mikan.ErrLimit) || f.calls.Load() != 3 || budget.Stats().TotalRawCount != 2 {
		t.Fatal(budget.Stats(), err)
	}
}

func TestAnimeMikanValidZeroAndInvalidBinding(t *testing.T) {
	s, f := newAnimeMikanTestService()
	ctx := context.Background()
	in := AnimeSourceInput{Source: mikan.Source{ItemID: "2259", GroupID: "999"}, Kind: "rss"}
	if _, err := s.ReadAnimeSource(ctx, animeMikanTestBudget(t, nil), in); !errors.Is(err, mikan.ErrSourceBinding) || f.calls.Load() != 1 {
		t.Fatal(err, f.calls.Load())
	}
	f.read = func(_ context.Context, in mikan.SourceRequest) (mikan.SourcePage, error) {
		page := animeMikanFakePage(in)
		page.RawCount = 0
		page.Releases = []mikan.SourceRelease{}
		return page, nil
	}
	in.Source.GroupID = "45"
	zero, err := s.ReadAnimeSource(ctx, animeMikanTestBudget(t, nil), in)
	if err != nil || zero.Releases == nil || zero.RawCount != 0 || zero.Completeness != "partial" {
		t.Fatal(zero, err)
	}
	in.Source.GroupID = "../45"
	if _, err := s.ReadAnimeSource(ctx, animeMikanTestBudget(t, nil), in); !errors.Is(err, mikan.ErrInput) || f.calls.Load() != 2 {
		t.Fatal(err, f.calls.Load())
	}
}

func TestAnimeMikanConfirmedSourceSkipsEntry(t *testing.T) {
	s, f := newAnimeMikanTestService()
	f.read = func(_ context.Context, in mikan.SourceRequest) (mikan.SourcePage, error) {
		if in.Kind == "entry" {
			t.Fatal("background read fetched the group list")
		}
		return animeMikanFakePage(in), nil
	}
	in := AnimeSourceInput{Source: mikan.Source{ItemID: "2259", GroupID: "45"}, Kind: "rss", confirmedGroupName: "字幕组甲"}
	for i := 0; i < 2; i++ {
		budget := animeMikanTestBudget(t, nil)
		page, err := s.ReadAnimeSource(context.Background(), budget, in)
		if err != nil || page.RawCount != 2 || budget.Stats().Requests != 1-i || page.Cached != (i == 1) {
			t.Fatal(page, budget.Stats(), err)
		}
	}
	f.read = func(context.Context, mikan.SourceRequest) (mikan.SourcePage, error) {
		return mikan.SourcePage{}, mikan.ErrParse
	}
	in.Refresh = true
	if _, err := s.ReadAnimeSource(context.Background(), animeMikanTestBudget(t, nil), in); !errors.Is(err, mikan.ErrParse) || f.calls.Load() != 2 {
		t.Fatal("failed feed triggered metadata fallback", err, f.calls.Load())
	}
}

func TestAnimeMikanBatchRawBudgetCheckedBeforeRead(t *testing.T) {
	s, f := newAnimeMikanTestService()
	ctx := context.Background()
	a, b := mikan.Source{ItemID: "2259", GroupID: "45"}, mikan.Source{ItemID: "2259", GroupID: "28"}
	loaded := map[mikan.Source]int{a: 1500, b: 400}
	budget := animeMikanTestBudget(t, loaded)
	loaded[a] = 1 // Constructor must copy its seed, not borrow mutable caller state.
	if _, err := s.ReadAnimeSource(ctx, budget, AnimeSourceInput{Source: a, Kind: "history", Take: 1601}); !errors.Is(err, mikan.ErrLimit) || f.calls.Load() != 0 {
		t.Fatal(err, f.calls.Load())
	}
	if _, err := s.ReadAnimeSource(ctx, budget, AnimeSourceInput{Source: b, Kind: "history", Take: 501}); !errors.Is(err, mikan.ErrLimit) || f.calls.Load() != 0 {
		t.Fatal(err, f.calls.Load())
	}
	if _, err := NewAnimeSourceBudget(map[mikan.Source]int{a: 1500, b: 501}); !errors.Is(err, mikan.ErrLimit) {
		t.Fatal(err)
	}
	if _, err := s.ReadAnimeSource(ctx, budget, AnimeSourceInput{Source: a, Kind: "history", Take: 1600}); err != nil || budget.Stats().TotalRawCount != 402 {
		t.Fatal(budget.Stats(), err)
	}
}

func TestAnimeMikanByteBudgetAndExpiredOperation(t *testing.T) {
	s, f := newAnimeMikanTestService()
	ctx := context.Background()
	f.read = func(_ context.Context, in mikan.SourceRequest) (mikan.SourcePage, error) {
		page := animeMikanFakePage(in)
		if in.Take == 3 {
			if in.BodyLimit != 2<<20 {
				t.Errorf("remaining body budget %d", in.BodyLimit)
			}
			return mikan.SourcePage{ReadBytes: in.BodyLimit + 1}, mikan.ErrLimit
		}
		page.ReadBytes = 3 << 20
		return page, nil
	}
	source := mikan.Source{ItemID: "2259", GroupID: "45"}
	budget := animeMikanTestBudget(t, nil)
	if _, err := s.ReadAnimeSource(ctx, budget, AnimeSourceInput{Source: source, Kind: "history", Take: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReadAnimeSource(ctx, budget, AnimeSourceInput{Source: source, Kind: "history", Take: 3}); !errors.Is(err, mikan.ErrLimit) || budget.Stats().ReadBytes != (8<<20)+1 || budget.Stats().TotalRawCount != 1 {
		t.Fatal(budget.Stats(), err)
	}
	expired := animeMikanTestBudget(t, nil)
	expired.deadline = time.Now().Add(-time.Second)
	if _, err := s.ReadAnimeSource(ctx, expired, AnimeSourceInput{Source: source, Kind: "history", Take: 1}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}

func TestAnimeMikanFailurePreservesLastSuccess(t *testing.T) {
	s, f := newAnimeMikanTestService()
	ctx := context.Background()
	in := AnimeSourceInput{Source: mikan.Source{ItemID: "2259", GroupID: "45"}, Kind: "rss"}
	first, err := s.ReadAnimeSource(ctx, animeMikanTestBudget(t, nil), in)
	if err != nil {
		t.Fatal(err)
	}
	f.read = func(context.Context, mikan.SourceRequest) (mikan.SourcePage, error) {
		return mikan.SourcePage{ReadBytes: 17}, mikan.ErrParse
	}
	in.Refresh = true
	budget := animeMikanTestBudget(t, map[mikan.Source]int{in.Source: 2})
	if _, err := s.ReadAnimeSource(ctx, budget, in); !errors.Is(err, mikan.ErrParse) || budget.Stats().TotalRawCount != 2 {
		t.Fatal(err, budget.Stats())
	}
	in.Refresh = false
	cached, err := s.ReadAnimeSource(ctx, animeMikanTestBudget(t, nil), in)
	if err != nil || !cached.Cached || !first.ObservedAt.Equal(cached.ObservedAt) || cached.RawCount != 2 {
		t.Fatal(cached, err)
	}
}

func TestAnimeMikanConcurrentMergeAndWaiterCancellation(t *testing.T) {
	s, f := newAnimeMikanTestService()
	started, release := make(chan struct{}), make(chan struct{})
	f.read = func(ctx context.Context, in mikan.SourceRequest) (mikan.SourcePage, error) {
		if in.Kind == "entry" {
			return animeMikanFakePage(in), nil
		}
		close(started)
		select {
		case <-release:
			return animeMikanFakePage(in), nil
		case <-ctx.Done():
			return mikan.SourcePage{}, ctx.Err()
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	in := AnimeSourceInput{Source: mikan.Source{ItemID: "2259", GroupID: "45"}, Kind: "rss"}
	results := make(chan error, 2)
	firstBudget := animeMikanTestBudget(t, nil)
	go func() { _, err := s.ReadAnimeSource(ctx, firstBudget, in); results <- err }()
	<-started
	waitCtx, stop := context.WithCancel(ctx)
	stop()
	if _, err := s.ReadAnimeSource(waitCtx, animeMikanTestBudget(t, nil), in); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	secondBudget := animeMikanTestBudget(t, nil)
	go func() { _, err := s.ReadAnimeSource(ctx, secondBudget, in); results <- err }()
	close(release)
	for i := 0; i < 2; i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if f.calls.Load() != 2 || firstBudget.Stats().Requests != 2 || secondBudget.Stats().Requests != 0 {
		t.Fatal(f.calls.Load(), firstBudget.Stats(), secondBudget.Stats())
	}
}

func TestAnimeMikanLeaderCancellationAllowsNewRead(t *testing.T) {
	s, f := newAnimeMikanTestService()
	started := make(chan struct{})
	var attempts atomic.Int32
	f.read = func(ctx context.Context, in mikan.SourceRequest) (mikan.SourcePage, error) {
		if in.Kind == "entry" {
			return animeMikanFakePage(in), nil
		}
		if attempts.Add(1) == 1 {
			close(started)
			<-ctx.Done()
			return mikan.SourcePage{}, ctx.Err()
		}
		return animeMikanFakePage(in), nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	leaderCtx, stop := context.WithCancel(ctx)
	in := AnimeSourceInput{Source: mikan.Source{ItemID: "2259", GroupID: "45"}, Kind: "rss"}
	result := make(chan error, 1)
	leaderBudget := animeMikanTestBudget(t, nil)
	go func() { _, err := s.ReadAnimeSource(leaderCtx, leaderBudget, in); result <- err }()
	<-started
	stop()
	if _, err := s.ReadAnimeSource(ctx, animeMikanTestBudget(t, nil), in); err != nil {
		t.Fatal(err)
	}
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if attempts.Load() != 2 {
		t.Fatal(attempts.Load())
	}
}

func TestAnimeMikanCooldownSharedWithLegacySearch(t *testing.T) {
	s, f := newAnimeMikanTestService()
	ctx := context.Background()
	until := time.Now().Add(2 * time.Minute)
	f.read = func(context.Context, mikan.SourceRequest) (mikan.SourcePage, error) {
		return mikan.SourcePage{}, &mikan.ResponseError{StatusCode: 429, RetryAt: until}
	}
	in := AnimeSourceInput{Source: mikan.Source{ItemID: "2259", GroupID: "45"}, Kind: "rss", Refresh: true}
	if _, err := s.ReadAnimeSource(ctx, animeMikanTestBudget(t, nil), in); !errors.Is(err, mikan.ErrBlocked) {
		t.Fatal(err)
	}
	var cooldown *MikanCooldownError
	if _, err := s.Search(ctx, MikanInput{Query: "测试", Refresh: true}); !errors.As(err, &cooldown) || !cooldown.Until.Equal(until) || f.searchCalls.Load() != 0 {
		t.Fatal(err, f.searchCalls.Load())
	}
	// A legacy endpoint's server cooldown also blocks new anime reads.
	s2, f2 := newAnimeMikanTestService()
	f2.searchErr = &mikan.ResponseError{StatusCode: 503, RetryAt: until}
	if _, err := s2.Search(ctx, MikanInput{Query: "测试"}); !errors.Is(err, mikan.ErrBlocked) {
		t.Fatal(err)
	}
	if _, err := s2.ReadAnimeSource(ctx, animeMikanTestBudget(t, nil), in); !errors.As(err, &cooldown) || f2.calls.Load() != 0 {
		t.Fatal(err, f2.calls.Load())
	}
}

func TestAnimeMikanSharedInterval(t *testing.T) {
	s, f := newAnimeMikanTestService()
	f.keepInterval = true
	ctx := context.Background()
	if _, err := s.Search(ctx, MikanInput{Query: "测试"}); err != nil {
		t.Fatal(err)
	}
	last := s.lastRequest
	f.read = func(_ context.Context, in mikan.SourceRequest) (mikan.SourcePage, error) {
		if elapsed := time.Since(last); elapsed < 950*time.Millisecond {
			t.Errorf("site interval bypassed: %s", elapsed)
		}
		last = time.Now()
		return animeMikanFakePage(in), nil
	}
	if _, err := s.ReadAnimeSource(ctx, animeMikanTestBudget(t, nil), AnimeSourceInput{Source: mikan.Source{ItemID: "2259", GroupID: "45"}, Kind: "rss"}); err != nil {
		t.Fatal(err)
	}
}

func TestAnimeMikanContinuationBoundaries(t *testing.T) {
	for _, mode := range []string{"progress", "window_changed", "no_progress"} {
		t.Run(mode, func(t *testing.T) {
			s, f := newAnimeMikanTestService()
			f.read = func(_ context.Context, in mikan.SourceRequest) (mikan.SourcePage, error) {
				page := animeMikanFakePage(in)
				if in.Kind == "history" && in.Take == 3 {
					if mode != "no_progress" {
						page.Releases = append(page.Releases, mikan.SourceRelease{PostKey: strings.Repeat("b", 40), Title: "第二条"})
					}
					if mode == "window_changed" {
						page.Releases[0], page.Releases[1] = page.Releases[1], page.Releases[0]
					}
				}
				return page, nil
			}
			source := mikan.Source{ItemID: "2259", GroupID: "45"}
			first, err := s.ReadAnimeSource(context.Background(), animeMikanTestBudget(t, nil), AnimeSourceInput{Source: source, Kind: "history", Take: 1})
			if err != nil {
				t.Fatal(err)
			}
			budget := animeMikanTestBudget(t, map[mikan.Source]int{source: first.RawCount})
			continued, err := s.ReadAnimeSource(context.Background(), budget, AnimeSourceInput{Source: source, Kind: "history", Take: 3, Previous: &first})
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "progress":
				if continued.Completeness != "visible_end" || continued.WindowChanged {
					t.Fatal(continued)
				}
			case "window_changed":
				if continued.Completeness != "partial" || !continued.WindowChanged {
					t.Fatal(continued)
				}
			case "no_progress":
				if continued.Completeness != "partial" || continued.StopReason != "no_progress" || continued.NextTake != 0 {
					t.Fatal(continued)
				}
			}
			if first.RawCount != 1 || len(first.Releases) != 1 || first.Releases[0].PostKey != strings.Repeat("a", 40) {
				t.Fatal("previous snapshot changed", first)
			}
		})
	}
}

func TestAnimeMikanOperationKeepsOneSource(t *testing.T) {
	s, f := newAnimeMikanTestService()
	budget := animeMikanTestBudget(t, nil)
	in := AnimeSourceInput{Source: mikan.Source{ItemID: "2259", GroupID: "45"}, Kind: "rss"}
	if _, err := s.ReadAnimeSource(context.Background(), budget, in); err != nil {
		t.Fatal(err)
	}
	in.Source.GroupID = "28"
	if _, err := s.ReadAnimeSource(context.Background(), budget, in); !errors.Is(err, mikan.ErrInput) || f.calls.Load() != 2 {
		t.Fatal(err, f.calls.Load())
	}
}

func TestAnimeMikanHTTPIntegration(t *testing.T) {
	var calls atomic.Int32
	key := strings.Repeat("a", 40)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		switch r.URL.Path {
		case "/Home/Bangumi/2259":
			fmt.Fprint(w, `<p class="bangumi-title">测试番剧</p><a class="subgroup-name" data-anchor="#45">字幕组</a><div class="episode-table"><table><tbody></tbody></table></div>`)
		case "/RSS/Bangumi":
			if r.URL.Query().Get("bangumiId") != "2259" || r.URL.Query().Get("subgroupid") != "45" {
				t.Error(r.URL)
			}
			fmt.Fprintf(w, `<rss version="2.0"><channel><item><title>资源</title><guid>same</guid><link>https://mikanani.me/Home/Episode/%s</link><enclosure type="application/x-bittorrent" length="123" url="https://mikanani.me/Download/20261018/%s.torrent"/></item></channel></rss>`, key, key)
		default:
			t.Errorf("unexpected URL %s", r.URL)
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	client := mikan.New()
	client.BaseURL = server.URL
	s := NewMikanService(client, nil)
	budget := animeMikanTestBudget(t, nil)
	result, err := s.ReadAnimeSource(context.Background(), budget, AnimeSourceInput{Source: mikan.Source{ItemID: "2259", GroupID: "45"}, Kind: "rss"})
	if err != nil || calls.Load() != 2 || result.RawCount != 1 || result.Completeness != "partial" || result.Releases[0].PostKey != key || budget.Stats().Requests != 2 || budget.Stats().ReadBytes <= 0 {
		t.Fatal(result, budget.Stats(), err)
	}
}

func TestAnimeMikanCacheCapacity(t *testing.T) {
	for _, size := range []int{1, 1 << 20} {
		s, _ := newAnimeMikanTestService()
		s.mu.Lock()
		for i := 0; i < 40; i++ {
			s.saveAnimeMikanCache(strings.Repeat("x", i+1), animeMikanCache{page: mikan.SourcePage{ObservedAt: time.Now()}, expires: time.Now().Add(time.Minute), bytes: size})
		}
		if len(s.animeCache) > 32 || s.animeCacheBytes > 8<<20 {
			t.Error("unbounded cache", len(s.animeCache), s.animeCacheBytes)
		}
		s.mu.Unlock()
	}
}
