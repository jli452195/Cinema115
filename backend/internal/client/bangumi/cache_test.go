package bangumi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCacheFreshnessAccountsForCDNAgeAndDate(t *testing.T) {
	for _, tc := range []struct {
		name, control, age string
		dateAge            time.Duration
		want               time.Duration
	}{
		{"age", "max-age=3600", "1200", 10 * time.Minute, 40 * time.Minute},
		{"date", "max-age=3600", "60", 30 * time.Minute, 30 * time.Minute},
		{"local-cap", "max-age=7200", "0", 0, time.Hour},
		{"already-old", "max-age=60", "120", 0, -time.Minute},
		{"no-cache", "max-age=3600, no-cache", "0", 0, 0},
		{"large-age", "max-age=3600", "999999999999999999999999999", 0, time.Hour - 365*24*time.Hour},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clock := newClock()
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Date", clock.now().Add(-tc.dateAge).Format(http.TimeFormat))
				w.Header().Set("Cache-Control", tc.control)
				w.Header().Set("Age", tc.age)
				writeJSON(w, testSubject)
			})
			c.now = clock.now
			out, err := c.Subject(context.Background(), "278826")
			if err != nil || out.ExpiresAt-out.FetchedAt != tc.want.Milliseconds() || out.Stale != (tc.want <= 0) {
				t.Fatal("CDN freshness restarted", out.CacheMeta, err, tc.want)
			}
		})
	}
}

func Test304UsesPreviousPolicyWithoutRestartingCDNAge(t *testing.T) {
	clock := newClock()
	var calls atomic.Int32
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Date", clock.now().Format(http.TimeFormat))
		if calls.Add(1) == 1 {
			w.Header().Set("Cache-Control", "max-age=60")
			w.Header().Set("ETag", `"v1"`)
			writeJSON(w, testSubject)
			return
		}
		w.Header().Set("Age", "30")
		w.WriteHeader(http.StatusNotModified)
	})
	c.now = clock.now
	first, err := c.Subject(context.Background(), "278826")
	if err != nil {
		t.Fatal(err)
	}
	clock.add(time.Minute)
	second, err := c.Subject(context.Background(), "278826")
	if err != nil || second.ExpiresAt-second.FetchedAt != 30000 || second.DataVersion != first.DataVersion {
		t.Fatal(second, err)
	}
}

func TestNoStoreNeverRetainsSuccessfulBodyOrValidators(t *testing.T) {
	var calls atomic.Int32
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("If-None-Match") != "" {
			t.Error("no-store validator reused")
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("ETag", `"private"`)
		writeJSON(w, testSubject)
	})
	for i := 0; i < 2; i++ {
		if _, err := c.Subject(context.Background(), "278826"); err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 2 || len(c.cache) != 0 || c.cacheBytes != 0 {
		t.Fatal("no-store data was retained", calls.Load(), len(c.cache), c.cacheBytes)
	}
}

func TestRetryAfterIsSharedAcrossEndpointsAndRecheckedAfterQueue(t *testing.T) {
	for _, dateHeader := range []bool{false, true} {
		t.Run(fmt.Sprint(dateHeader), func(t *testing.T) {
			clock := newClock()
			var calls atomic.Int32
			started := make(chan struct{})
			release := make(chan struct{})
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if calls.Add(1) == 1 {
					close(started)
					<-release
					retry := "120"
					if dateHeader {
						retry = clock.now().Add(2 * time.Minute).Format(http.TimeFormat)
					}
					w.Header().Set("Retry-After", retry)
					w.WriteHeader(http.StatusTooManyRequests)
					return
				}
				writeJSON(w, testSubject)
			})
			c.now = clock.now
			var firstErr, queuedErr error
			var wg sync.WaitGroup
			wg.Add(1)
			go func() { defer wg.Done(); _, firstErr = c.Calendar(context.Background(), 0) }()
			<-started
			queued := make(chan struct{})
			wg.Add(1)
			go func() { defer wg.Done(); close(queued); _, queuedErr = c.Subject(context.Background(), "278826") }()
			<-queued
			close(release)
			wg.Wait()
			if !errors.Is(firstErr, ErrCooldown) || !errors.Is(queuedErr, ErrCooldown) || calls.Load() != 1 {
				t.Fatal("queued request bypassed cooldown", firstErr, queuedErr, calls.Load())
			}
			var detail *UpstreamError
			if !errors.As(firstErr, &detail) || !detail.RetryAfterAt.Equal(clock.now().Add(2*time.Minute)) {
				t.Fatal(firstErr)
			}
			if _, err := c.Relations(context.Background(), "278826"); !errors.Is(err, ErrCooldown) || calls.Load() != 1 {
				t.Fatal("relations bypassed cooldown", err)
			}
			if _, err := c.Search(context.Background(), SearchOptions{Keyword: "other"}); !errors.Is(err, ErrCooldown) || calls.Load() != 1 {
				t.Fatal("search bypassed cooldown", err)
			}
			clock.add(121 * time.Second)
			out, err := c.Subject(context.Background(), "278826")
			if err != nil || out.Subject.ID != "278826" || out.Issue != nil || calls.Load() != 2 {
				t.Fatal("cooldown did not recover", out, err)
			}
		})
	}
}

func TestStaleRetentionAndRecoveryDoNotAdvanceFailureTimestamp(t *testing.T) {
	clock := newClock()
	var status atomic.Int32
	status.Store(200)
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Date", clock.now().Format(http.TimeFormat))
		w.Header().Set("Cache-Control", "max-age=1")
		if code := int(status.Load()); code != 200 {
			w.WriteHeader(code)
			return
		}
		writeJSON(w, testSubject)
	})
	c.now = clock.now
	ctx := context.Background()
	first, err := c.Subject(ctx, "278826")
	if err != nil {
		t.Fatal(err)
	}
	clock.add(2 * time.Second)
	status.Store(502)
	stale, err := c.Subject(ctx, "278826")
	if err != nil || !stale.Stale || stale.FetchedAt != first.FetchedAt {
		t.Fatal(stale, err)
	}
	clock.add(16 * time.Second)
	status.Store(200)
	recovered, err := c.Subject(ctx, "278826")
	if err != nil || recovered.Stale || recovered.Issue != nil || recovered.FetchedAt <= first.FetchedAt || recovered.DataVersion != first.DataVersion {
		t.Fatal("recovery did not refresh only the validation time", recovered, err)
	}
	clock.add(25 * time.Hour)
	status.Store(404)
	if _, err := c.Subject(ctx, "278826"); !errors.Is(err, ErrNotFound) {
		t.Fatal("snapshot exceeded retention", err)
	}
}

func TestCancellationDoesNotPoisonOtherReadersOrInstallBackoff(t *testing.T) {
	var calls atomic.Int32
	started := make(chan struct{})
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			close(started)
			<-r.Context().Done()
			return
		}
		writeJSON(w, testCalendar)
	})
	ctx, cancel := context.WithCancel(context.Background())
	var leaderErr, waiterErr error
	var waiter CalendarResult
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); _, leaderErr = c.Calendar(ctx, 3) }()
	<-started
	wg.Add(1)
	go func() { defer wg.Done(); waiter, waiterErr = c.Calendar(context.Background(), 3) }()
	cancel()
	wg.Wait()
	if !errors.Is(leaderErr, context.Canceled) || waiterErr != nil || len(waiter.Days) != 1 || len(waiter.Days[0].Items) != 1 || calls.Load() != 2 {
		t.Fatal("canceled leader poisoned live reader", leaderErr, waiterErr, calls.Load())
	}
	if len(c.flights) != 0 {
		t.Fatal("in-flight entry leaked")
	}
}

func TestCanceledWaiterAndQueuedRequestLeaveLeaderRunning(t *testing.T) {
	var calls atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		close(started)
		select {
		case <-release:
			writeJSON(w, testCalendar)
		case <-r.Context().Done():
		}
	})
	var wg sync.WaitGroup
	var leaderErr error
	wg.Add(1)
	go func() { defer wg.Done(); _, leaderErr = c.Calendar(context.Background(), 0) }()
	<-started
	for _, sameKey := range []bool{true, false} {
		ctx, cancel := context.WithCancel(context.Background())
		returned := make(chan struct{})
		var err error
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer close(returned)
			if sameKey {
				_, err = c.Calendar(ctx, 3)
			} else {
				_, err = c.Subject(ctx, "278826")
			}
		}()
		cancel()
		<-returned
		if !errors.Is(err, context.Canceled) {
			t.Fatal("waiter cancellation lost", err)
		}
	}
	close(release)
	wg.Wait()
	if leaderErr != nil || calls.Load() != 1 || len(c.flights) != 0 {
		t.Fatal("waiter canceled another reader or leaked flight", leaderErr, calls.Load())
	}
}

func TestCacheIsBoundedByEntriesAndNormalizedBytes(t *testing.T) {
	t.Run("entries", func(t *testing.T) {
		clock := newClock()
		c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
			id := strings.TrimPrefix(r.URL.Path, "/v0/subjects/")
			writeJSON(w, strings.ReplaceAll(testSubject, "278826", id))
		})
		c.now = clock.now
		for i := 1; i <= maxCacheEntries+4; i++ {
			if _, err := c.Subject(context.Background(), fmt.Sprint(i)); err != nil {
				t.Fatal(err)
			}
			clock.add(time.Millisecond)
		}
		if len(c.cache) != maxCacheEntries {
			t.Fatal("cache entry bound failed", len(c.cache))
		}
		if _, ok := c.cache["subject:1"]; ok {
			t.Fatal("oldest entry was not evicted")
		}
	})
	t.Run("bytes", func(t *testing.T) {
		var body strings.Builder
		body.WriteByte('[')
		for i := 1; i <= 1000; i++ {
			if i > 1 {
				body.WriteByte(',')
			}
			fmt.Fprintf(&body, `{"id":%d,"type":2,"name":"%s","relation":"续集"}`, i, strings.Repeat("x", 512))
		}
		body.WriteByte(']')
		response := body.String()
		c := testClient(t, func(w http.ResponseWriter, r *http.Request) { writeJSON(w, response) })
		for i := 1; i <= 10; i++ {
			if _, err := c.Relations(context.Background(), fmt.Sprint(i)); err != nil {
				t.Fatal(err)
			}
		}
		if c.cacheBytes > maxCacheBytes || len(c.cache) >= 10 {
			t.Fatal("normalized byte bound failed", c.cacheBytes, len(c.cache))
		}
		actual := 0
		for _, entry := range c.cache {
			actual += len(entry.data)
		}
		if actual != c.cacheBytes {
			t.Fatal("cache byte accounting drifted", actual, c.cacheBytes)
		}
	})
}

func TestControlledCoversAndInvalidSubjectIdentity(t *testing.T) {
	for _, raw := range []string{"http://127.0.0.1/pic/cover/m/06/8c/test.jpg", "https://lain.bgm.tv:443/pic/cover/m/06/8c/test.jpg", "https://user:pass@lain.bgm.tv/pic/cover/m/06/8c/test.jpg", "https://lain.bgm.tv/pic/cover/m/06/8c/test.jpg?secret=x", "https://lain.bgm.tv/pic/cover/m/06/8c/test.jpg#fragment", "https://lain.bgm.tv/pic/cover/%6d/06/8c/test.jpg", "https://lain.bgm.tv/other/test.jpg"} {
		if normalizeCover(raw) != "" {
			t.Fatal("uncontrolled image accepted", raw)
		}
	}
	for _, body := range []string{strings.Replace(testSubject, `"id":278826`, `"id":278827`, 1), strings.Replace(testSubject, `"type":2`, `"type":1`, 1), `null`, `{"id":278826,"type":2}`} {
		c := testClient(t, func(w http.ResponseWriter, r *http.Request) { writeJSON(w, body) })
		if _, err := c.Subject(context.Background(), "278826"); !errors.Is(err, ErrInvalid) {
			t.Fatal("wrong/deleted chapter accepted", err)
		}
	}
}

func TestQueuedReadsStayBoundedAndRespectCancellation(t *testing.T) {
	var calls atomic.Int32
	started := make(chan struct{})
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-r.Context().Done()
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	for i := 1; i <= maxFlights; i++ {
		wg.Add(1)
		go func(id int) { defer wg.Done(); _, _ = c.Subject(ctx, fmt.Sprint(id)) }(i)
	}
	<-started
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	full := false
	for !full {
		select {
		case <-ticker.C:
			c.mu.Lock()
			full = len(c.flights) == maxFlights
			c.mu.Unlock()
		case <-deadline.C:
			cancel()
			wg.Wait()
			t.Fatal("readers did not enter bounded queue")
		}
	}
	if _, err := c.Subject(context.Background(), "999"); !errors.Is(err, ErrBusy) {
		cancel()
		wg.Wait()
		t.Fatal("queue cap not enforced", err)
	}
	cancel()
	wg.Wait()
	if len(c.flights) != 0 || calls.Load() != 1 {
		t.Fatal("canceled queued requests reached upstream/leaked", len(c.flights), calls.Load())
	}
}

func TestSiteIntervalWaitCanBeCanceledBeforeNetwork(t *testing.T) {
	var calls atomic.Int32
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1); writeJSON(w, testSubject) })
	c.interval = time.Second
	if _, err := c.Subject(context.Background(), "278826"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if _, err := c.Relations(ctx, "278826"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("interval wait lost cancellation", err)
	}
	if calls.Load() != 1 || len(c.flights) != 0 {
		t.Fatal("canceled interval wait sent request/leaked", calls.Load())
	}
}
