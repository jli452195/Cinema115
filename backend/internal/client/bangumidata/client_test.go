package bangumidata

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return &Client{HTTP: server.Client(), URL: server.URL}
}

func TestLookupExactSubjectAndTVReferences(t *testing.T) {
	data := `{"items":[
		{"title":"目标作品","begin":"2025-10-01T00:00:00Z","sites":[{"site":"bangumi","id":"584847"},{"site":"tmdb","id":"tv/261343/season/1"}]},
		{"title":"同篇章定位到一集","begin":"2025-10-01T00:00:00Z","sites":[{"site":"bangumi","id":"584847"},{"site":"tmdb","id":"tv/261343/season/1/episode/14"}]},
		{"title":"同作品无季","begin":"2025-01-01","sites":[{"site":"bangumi","id":"584847"},{"site":"tmdb","id":"tv/261343"}]},
		{"title":"不应误命中","sites":[{"site":"bangumi","id":"1584847"},{"site":"tmdb","id":"tv/999/season/1"}]},
		{"title":"电影过滤","sites":[{"site":"bangumi","id":"584847"},{"site":"tmdb","id":"movie/261343"}]},
		{"title":"畸形链接过滤","sites":[{"site":"bangumi","id":"584847"},{"site":"tmdb","id":"tv/0/season/x"}]}
	]}`
	client := testClient(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(data)) })
	got, err := client.Lookup(context.Background(), "584847")
	if err != nil {
		t.Fatal(err)
	}
	if got.Stale || len(got.Candidates) != 3 || got.Candidates[0].TMDBID != "261343" || got.Candidates[0].Season == nil || *got.Candidates[0].Season != 1 || got.Candidates[0].Year != "2025" || got.Candidates[1].Episode == nil || *got.Candidates[1].Episode != 14 || got.Candidates[2].Season != nil {
		t.Fatalf("unexpected exact lookup result: %+v", got)
	}
	*got.Candidates[0].Season = 99
	*got.Candidates[1].Episode = 99
	again, err := client.Lookup(context.Background(), "584847")
	if err != nil || *again.Candidates[0].Season != 1 || *again.Candidates[1].Episode != 14 {
		t.Fatalf("caller mutated shared snapshot: %+v, %v", again, err)
	}
	other, err := client.Lookup(context.Background(), "1584847")
	if err != nil || len(other.Candidates) != 1 || other.Candidates[0].TMDBID != "999" {
		t.Fatalf("exact subject lookup failed: %+v, %v", other, err)
	}
	missing, err := client.Lookup(context.Background(), "42")
	if err != nil || len(missing.Candidates) != 0 {
		t.Fatalf("valid no-match should be distinguishable from an error: %+v, %v", missing, err)
	}
}

func TestLookupEpisodeLinkAndAmbiguousSubject(t *testing.T) {
	data := `{"items":[
		{"title":"千歳くんはラムネ瓶のなか(第2クール)","begin":"2026-10-13T15:30:00.000Z","sites":[{"site":"bangumi","id":"584847"},{"site":"tmdb","id":"tv/261343/season/1/episode/14"}]},
		{"title":"ブラッククローバー 2nd Season","sites":[{"site":"bangumi","id":"567896"},{"site":"tmdb","id":"tv/73223/season/2"}]},
		{"title":"不明确的条目","sites":[{"site":"bangumi","id":"10"},{"site":"bangumi","id":"11"},{"site":"tmdb","id":"tv/50/season/1"}]}
	]}`
	client := testClient(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(data)) })
	for _, test := range []struct {
		id, tmdb string
		season   int
	}{{"584847", "261343", 1}, {"567896", "73223", 2}} {
		got, err := client.Lookup(context.Background(), test.id)
		if err != nil || len(got.Candidates) != 1 || got.Candidates[0].TMDBID != test.tmdb || got.Candidates[0].Season == nil || *got.Candidates[0].Season != test.season {
			t.Fatalf("real link format %s was not parsed: %+v, %v", test.id, got, err)
		}
	}
	for _, id := range []string{"10", "11"} {
		got, err := client.Lookup(context.Background(), id)
		if err != nil || len(got.Candidates) != 0 {
			t.Fatalf("ambiguous multi-subject association must not be promoted: %+v, %v", got, err)
		}
	}
}

func TestLookupRefreshFailureReturnsExplicitStaleSnapshot(t *testing.T) {
	var calls atomic.Int32
	client := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			_, _ = w.Write([]byte(`{"items":[{"title":"作品","sites":[{"site":"bangumi","id":"584847"},{"site":"tmdb","id":"tv/261343/season/1"}]}]}`))
			return
		}
		http.Error(w, "upstream unavailable", http.StatusBadGateway)
	})
	first, err := client.Lookup(context.Background(), "584847")
	if err != nil || first.Stale {
		t.Fatalf("initial lookup: %+v, %v", first, err)
	}
	client.mu.Lock()
	client.data.expiresAt = time.Now().Add(-time.Second)
	client.mu.Unlock()
	stale, err := client.Lookup(context.Background(), "584847")
	if err == nil || !errors.Is(err, ErrUnavailable) || !stale.Stale || len(stale.Candidates) != 1 || stale.Candidates[0].TMDBID != "261343" {
		t.Fatalf("failed refresh must return an explicitly stale suggestion and error: %+v, %v", stale, err)
	}
	if _, err := client.Lookup(context.Background(), "584847"); !errors.Is(err, ErrUnavailable) || calls.Load() != 2 {
		t.Fatalf("failure cooldown should avoid repetitive upstream downloads: calls=%d err=%v", calls.Load(), err)
	}
}

func TestLookupRejectsBadIDsAndInvalidDataset(t *testing.T) {
	var calls atomic.Int32
	client := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`{"items":[]}`))
	})
	if _, err := client.Lookup(context.Background(), "584847/../1"); err == nil || calls.Load() != 0 {
		t.Fatal("invalid subject ID should not make a request")
	}
	if _, err := client.Lookup(context.Background(), "584847"); err == nil || !strings.Contains(err.Error(), "no valid") {
		t.Fatalf("empty/broken upstream data must not be treated as a valid no-match: %v", err)
	}
}

func TestParseTMDBID(t *testing.T) {
	for _, value := range []string{"1", "261343", "9999999999"} {
		if !ParseTMDBID(value) {
			t.Fatalf("expected valid TMDB ID %q", value)
		}
	}
	for _, value := range []string{"", "0", "01", " 123", "123/season/1", "12345678901"} {
		if ParseTMDBID(value) {
			t.Fatalf("unexpected valid TMDB ID %q", value)
		}
	}
}

func TestFetchRejectsOversizedBody(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", maxBodySize+1)))
	})
	if _, err := client.Lookup(context.Background(), "584847"); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized body accepted: %v", err)
	}
}
