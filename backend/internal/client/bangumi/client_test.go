package bangumi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const testSubject = `{"id":278826,"type":2,"name":"Re:Zero 2nd season","name_cn":"从零开始 第二季","date":"2020-07-08","summary":"篇章简介","platform":"TV","eps":13,"total_episodes":13,"images":{"medium":"http://lain.bgm.tv/pic/cover/m/06/8c/278826_test.jpg"}}`
const testCalendar = `[{"weekday":{"id":3},"items":[{"id":278826,"type":2,"name":"Re:Zero 2nd season","name_cn":"从零开始 第二季","air_date":"2020-07-08","air_weekday":3,"images":{"medium":"http://lain.bgm.tv/pic/cover/m/06/8c/278826_test.jpg"}},{"id":42,"type":1,"name":"原作小说"}]}]`

type testClock struct{ millis atomic.Int64 }

func newClock() *testClock {
	c := &testClock{}
	c.millis.Store(time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC).UnixMilli())
	return c
}
func (c *testClock) now() time.Time      { return time.UnixMilli(c.millis.Load()).UTC() }
func (c *testClock) add(d time.Duration) { c.millis.Add(d.Milliseconds()) }

func testClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	c := New()
	c.base = server.URL
	c.http = server.Client()
	c.http.Timeout = 2 * time.Second
	c.interval = 0
	return c
}

func writeJSON(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, body)
}

func TestCalendarUsesOneSnapshotAndCopiesResults(t *testing.T) {
	var calls atomic.Int32
	clock := newClock()
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/calendar" || r.Method != http.MethodGet || r.URL.RawQuery != "" || r.Header.Get("User-Agent") != userAgent || r.Header.Get("Accept") != "application/json" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Error("incorrect calendar request", r.URL, r.Header)
		}
		w.Header().Set("Date", clock.now().Format(http.TimeFormat))
		w.Header().Set("Cache-Control", "max-age=3600")
		writeJSON(w, testCalendar)
	})
	c.now = clock.now
	ctx := context.Background()
	first, err := c.Calendar(ctx, 3)
	if err != nil || len(first.Days) != 1 || len(first.Days[0].Items) != 1 {
		t.Fatal(first, err)
	}
	card := first.Days[0].Items[0]
	if card.ID != "278826" || card.Title != "从零开始 第二季" || card.OriginalTitle != "Re:Zero 2nd season" || card.Date != "2020-07-08" || card.CoverURL != "/api/bangumi/image/m-06-8c/278826_test.jpg" || card.Weekday == nil || *card.Weekday != 3 {
		t.Fatal(card)
	}
	version := first.DataVersion
	first.Days[0].Items[0].Title = "caller edit"
	*first.Days[0].Items[0].Weekday = 7
	clock.add(time.Minute)
	second, err := c.Calendar(ctx, 0)
	if err != nil || len(second.Days) != 7 || second.Days[2].Items[0].Title != card.Title || *second.Days[2].Items[0].Weekday != 3 || second.Days[0].Items == nil || second.DataVersion != version || second.FetchedAt != first.FetchedAt || second.ExpiresAt-first.FetchedAt != int64(time.Hour/time.Millisecond) || second.Stale {
		t.Fatal(second, err)
	}
	for day := 1; day <= 7; day++ {
		if _, err := c.Calendar(ctx, day); err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("weekday changes or cards triggered additional reads", calls.Load())
	}
}

func TestSearchPreservesQueryPaginationAndZeroResults(t *testing.T) {
	var calls atomic.Int32
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/v0/search/subjects" || r.Method != http.MethodPost || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") || r.Header.Get("If-None-Match") != "" {
			t.Error("incorrect search request")
		}
		var body struct {
			Keyword, Sort string
			Filter        struct{ Type []int }
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.Sort != "match" || !reflect.DeepEqual(body.Filter.Type, []int{2}) {
			t.Error("missing anime filter", body)
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		total, data := 69, `[`+testSubject+`]`
		if body.Keyword == "nothing" {
			total, data = 0, `[]`
		} else if offset >= 69 {
			data = `[]`
		}
		writeJSON(w, fmt.Sprintf(`{"total":%d,"limit":%d,"offset":%d,"data":%s}`, total, limit, offset, data))
	})
	ctx := context.Background()
	input := SearchOptions{Keyword: "  Re:ゼロ 2nd!  ", Limit: 2}
	first, err := c.Search(ctx, input)
	if err != nil || first.Total != 69 || len(first.Items) != 1 || first.Limit != 2 {
		t.Fatal(first, err)
	}
	input.Keyword = "Re:ゼロ 2nd!"
	if _, err := c.Search(ctx, input); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("trimmed identical query was not shared")
	}
	for _, changed := range []SearchOptions{{Keyword: "Reゼロ 2nd!", Limit: 2}, {Keyword: "Re:ゼロ 2nd!", Limit: 3}, {Keyword: "Re:ゼロ 2nd!", Limit: 2, Offset: 2}} {
		if _, err := c.Search(ctx, changed); err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 4 {
		t.Fatal("distinct words or paging collapsed", calls.Load())
	}
	empty, err := c.Search(ctx, SearchOptions{Keyword: "nothing"})
	if err != nil || empty.Total != 0 || empty.Items == nil || len(empty.Items) != 0 || empty.Limit != 20 {
		t.Fatal(empty, err)
	}
	if _, err := c.Search(ctx, SearchOptions{Keyword: "nothing"}); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 5 {
		t.Fatal("valid zero results were not cached")
	}
	end, err := c.Search(ctx, SearchOptions{Keyword: input.Keyword, Limit: 2, Offset: 71})
	if err != nil || end.Total != 69 || len(end.Items) != 0 {
		t.Fatal(end, err)
	}
}

func TestLocalInputValidationMakesNoRequests(t *testing.T) {
	var calls atomic.Int32
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1); writeJSON(w, `[]`) })
	ctx := context.Background()
	for _, input := range []SearchOptions{{}, {Keyword: " \t "}, {Keyword: strings.Repeat("字", 129)}, {Keyword: "valid", Limit: 51}, {Keyword: "valid", Limit: -1}, {Keyword: "valid", Offset: -1}, {Keyword: "valid", Offset: 1001}, {Keyword: "valid", Sort: "heat"}, {Keyword: "a\nb"}, {Keyword: string([]byte{0xff})}} {
		if _, err := c.Search(ctx, input); !errors.Is(err, ErrInput) {
			t.Fatal(input, err)
		}
	}
	for _, id := range []string{"", "0", "01", "-1", "1.2", "1e3", "../123", "123?x=1"} {
		if _, err := c.Subject(ctx, id); !errors.Is(err, ErrInput) {
			t.Fatal(id, err)
		}
		if _, err := c.Relations(ctx, id); !errors.Is(err, ErrInput) {
			t.Fatal(id, err)
		}
	}
	if _, err := c.Calendar(ctx, 8); !errors.Is(err, ErrInput) {
		t.Fatal(err)
	}
	if _, err := c.Calendar(ctx, -1); !errors.Is(err, ErrInput) {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatal("invalid input reached upstream")
	}
}

func TestSubjectAndRelationsAreLazyAndIndependentlyValidated(t *testing.T) {
	clock := newClock()
	var detailCalls, relationCalls atomic.Int32
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Date", clock.now().Format(http.TimeFormat))
		w.Header().Set("Cache-Control", "max-age=60")
		switch r.URL.Path {
		case "/v0/subjects/278826":
			if detailCalls.Add(1) == 1 {
				if r.Header.Get("If-None-Match") != "" {
					t.Error("cold conditional read")
				}
				w.Header().Set("ETag", `"detail-v1"`)
				writeJSON(w, testSubject)
			} else {
				if r.Header.Get("If-None-Match") != `"detail-v1"` || r.Header.Get("If-Modified-Since") != "" {
					t.Error("wrong detail validator", r.Header)
				}
				w.WriteHeader(http.StatusNotModified)
			}
		case "/v0/subjects/278826/subjects":
			if relationCalls.Add(1) == 1 {
				w.Header().Set("Last-Modified", "Sun, 18 Oct 2026 08:00:00 GMT")
				writeJSON(w, `[{"id":140001,"type":2,"name":"前篇","name_cn":"","relation":"前传"},{"id":123,"type":1,"name":"原作小说","relation":"原作"}]`)
			} else {
				if r.Header.Get("If-Modified-Since") != "Sun, 18 Oct 2026 08:00:00 GMT" || r.Header.Get("If-None-Match") != "" {
					t.Error("wrong relation validator", r.Header)
				}
				w.WriteHeader(http.StatusNotModified)
			}
		default:
			t.Error("unexpected recursive/per-card request", r.URL.Path)
		}
	})
	c.now = clock.now
	ctx := context.Background()
	detail, err := c.Subject(ctx, "278826")
	if err != nil || detail.Subject.Synopsis != "篇章简介" || detail.Subject.TotalEpisodes == nil || *detail.Subject.TotalEpisodes != 13 || detail.Subject.Date != "2020-07-08" {
		t.Fatal(detail, err)
	}
	if relationCalls.Load() != 0 {
		t.Fatal("details fetched relations eagerly")
	}
	relations, err := c.Relations(ctx, "278826")
	if err != nil || len(relations.Items) != 1 || relations.Items[0].ID != "140001" || relations.Items[0].Relation != "前传" {
		t.Fatal(relations, err)
	}
	clock.add(61 * time.Second)
	detail2, err := c.Subject(ctx, "278826")
	if err != nil || detail2.DataVersion != detail.DataVersion || detail2.FetchedAt <= detail.FetchedAt || detail2.Stale {
		t.Fatal("304 lost detail/version", detail2, err)
	}
	relations2, err := c.Relations(ctx, "278826")
	if err != nil || relations2.DataVersion != relations.DataVersion || !reflect.DeepEqual(relations.Items, relations2.Items) {
		t.Fatal("304 lost relations", relations2, err)
	}
	if detailCalls.Load() != 2 || relationCalls.Load() != 2 {
		t.Fatal(detailCalls.Load(), relationCalls.Load())
	}
}

func TestMalformedUpstreamNeverBecomesSuccessfulEmptyData(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		search     bool
	}{
		{"null-calendar", `null`, false},
		{"object-calendar", `{"error":"bad"}`, false},
		{"missing-weekday", `[{"items":[]}]`, false},
		{"null-items", `[{"weekday":{"id":1},"items":null}]`, false},
		{"duplicate-weekday", `[{"weekday":{"id":1},"items":[]},{"weekday":{"id":1},"items":[]}]`, false},
		{"missing-id", `[{"weekday":{"id":1},"items":[{"type":2,"name":"bad"}]}]`, false},
		{"null-search", `null`, true},
		{"missing-search-data", `{"total":0,"limit":20,"offset":0}`, true},
		{"null-search-data", `{"total":0,"limit":20,"offset":0,"data":null}`, true},
		{"wrong-offset", `{"total":0,"limit":20,"offset":10,"data":[]}`, true},
		{"ignored-type-filter", `{"total":1,"limit":20,"offset":0,"data":[{"id":1,"type":1,"name":"小说"}]}`, true},
		{"trailing-json", `[] []`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) { writeJSON(w, tc.body) })
			var err error
			if tc.search {
				_, err = c.Search(context.Background(), SearchOptions{Keyword: "valid"})
			} else {
				_, err = c.Calendar(context.Background(), 0)
			}
			if !errors.Is(err, ErrInvalid) {
				t.Fatal("malformed response accepted", err)
			}
			c.mu.Lock()
			defer c.mu.Unlock()
			for _, entry := range c.cache {
				if len(entry.data) != 0 {
					t.Fatal("malformed response cached as success")
				}
			}
		})
	}
}

func TestHTTPFailuresKeepSuccessfulSnapshotWithIssue(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   error
		code   string
	}{
		{"removed", 404, "removed-private-body", ErrNotFound, "UPSTREAM_NOT_FOUND"},
		{"access", 403, "private-body", ErrAccess, "UPSTREAM_UNAVAILABLE"},
		{"limited", 429, "private-body", ErrCooldown, "SOURCE_COOLDOWN"},
		{"outage", 502, "<html>private-body</html>", ErrUnavailable, "UPSTREAM_UNAVAILABLE"},
		{"invalid", 200, `{"id":0}`, ErrInvalid, "UPSTREAM_INVALID"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clock := newClock()
			var calls atomic.Int32
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Date", clock.now().Format(http.TimeFormat))
				if calls.Add(1) == 1 {
					w.Header().Set("Cache-Control", "max-age=1")
					writeJSON(w, testSubject)
					return
				}
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			})
			c.now = clock.now
			ctx := context.Background()
			first, err := c.Subject(ctx, "278826")
			if err != nil {
				t.Fatal(err)
			}
			clock.add(2 * time.Second)
			stale, err := c.Subject(ctx, "278826")
			if err != nil || !stale.Stale || stale.Issue == nil || stale.Issue.Code != tc.code || stale.Issue.RetryAfterAt == nil || stale.DataVersion != first.DataVersion || stale.FetchedAt != first.FetchedAt || !reflect.DeepEqual(stale.Subject, first.Subject) {
				t.Fatal("snapshot or issue lost", stale, err)
			}
			stale.Subject.Title = "edit"
			stale.Issue.Code = "edit"
			*stale.Issue.RetryAfterAt = 0
			cached, err := c.Subject(ctx, "278826")
			if err != nil || cached.Issue == nil || cached.Issue.Code == "edit" || *cached.Issue.RetryAfterAt == 0 || cached.Subject.Title == "edit" || calls.Load() != 2 {
				t.Fatal("backoff bypass or shared mutable result", cached, err, calls.Load())
			}
			// A cold client must return the failure, not a fake empty object.
			cold := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			})
			if _, err := cold.Subject(ctx, "278826"); !errors.Is(err, tc.want) || strings.Contains(err.Error(), "private-body") {
				t.Fatal("cold failure misclassified/leaked", err)
			}
		})
	}
}

func Test304RequiresSuccessfulBodyAndSentValidator(t *testing.T) {
	var calls atomic.Int32
	clock := newClock()
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Date", clock.now().Format(http.TimeFormat))
		if calls.Add(1) == 1 {
			w.Header().Set("Cache-Control", "max-age=0")
			writeJSON(w, testSubject)
			return
		}
		w.WriteHeader(http.StatusNotModified)
	})
	c.now = clock.now
	first, err := c.Subject(context.Background(), "278826")
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.Subject(context.Background(), "278826")
	if err != nil || !second.Stale || second.Issue == nil || second.Issue.Code != "UPSTREAM_INVALID" || second.FetchedAt != first.FetchedAt {
		t.Fatal("unsolicited 304 accepted", second, err)
	}
	cold := testClient(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNotModified) })
	if _, err := cold.Subject(context.Background(), "278826"); !errors.Is(err, ErrInvalid) {
		t.Fatal("cold 304 accepted", err)
	}
}

func TestResponseLimitRedirectAndRequestTimeout(t *testing.T) {
	t.Run("large-response", func(t *testing.T) {
		c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, strings.Repeat(" ", maxResponseBytes+1))
		})
		if _, err := c.Calendar(context.Background(), 0); !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
	})
	t.Run("redirect", func(t *testing.T) {
		var targetCalls atomic.Int32
		target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { targetCalls.Add(1); writeJSON(w, `[]`) }))
		defer target.Close()
		c := testClient(t, func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) })
		c.http.CheckRedirect = newHTTPClient().CheckRedirect
		if _, err := c.Calendar(context.Background(), 0); !errors.Is(err, ErrUnavailable) {
			t.Fatal(err)
		}
		if targetCalls.Load() != 0 {
			t.Fatal("followed untrusted redirect")
		}
	})
	t.Run("timeout", func(t *testing.T) {
		var calls atomic.Int32
		c := testClient(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1); <-r.Context().Done() })
		c.http.Timeout = 25 * time.Millisecond
		if _, err := c.Calendar(context.Background(), 0); !errors.Is(err, ErrTimeout) {
			t.Fatal("timeout not classified", err)
		}
		if _, err := c.Calendar(context.Background(), 0); !errors.Is(err, ErrTimeout) || calls.Load() != 1 {
			t.Fatal("timeout retried without backoff", err, calls.Load())
		}
	})
}

func TestConcurrentReadersShareRequestAndOwnTheirReturnedData(t *testing.T) {
	var calls atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			close(started)
		}
		select {
		case <-release:
			writeJSON(w, testCalendar)
		case <-r.Context().Done():
		}
	})
	const readers = 12
	results := make([]CalendarResult, readers)
	errs := make([]error, readers)
	var wg sync.WaitGroup
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); results[i], errs[i] = c.Calendar(context.Background(), 3) }(i)
	}
	<-started
	close(release)
	wg.Wait()
	for i, err := range errs {
		if err != nil || len(results[i].Days) != 1 {
			t.Fatal(i, err)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("duplicate in-flight reads", calls.Load())
	}
	results[0].Days[0].Items[0].Title = "changed"
	if results[1].Days[0].Items[0].Title == "changed" {
		t.Fatal("flight readers share mutable payload")
	}
}
