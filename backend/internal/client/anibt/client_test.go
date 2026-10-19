package anibt

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"strm115/backend/internal/client/animefeed"
	"testing"
	"time"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, e := os.ReadFile("testdata/" + name)
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func TestPublicFixtures(t *testing.T) {
	c, e := ParseCatalog(fixture(t, "calendar.json"), "2026-10")
	if e != nil || c.Provider != "anibt" || len(c.Items) != c.Total || len(c.Days) == 0 {
		t.Fatalf("catalog: %+v %v", c, e)
	}
	entry, e := ParseEntry(fixture(t, "groups.json"), "630160")
	if e != nil || len(entry.Groups) != 3 || entry.Groups[0].ID != "lolihouse" {
		t.Fatalf("groups: %+v %v", entry, e)
	}
	s := animefeed.NewSource("anibt", "630160", "lolihouse")
	rows, e := ParseRSS(fixture(t, "rss.xml"), s)
	if e != nil || len(rows) != 3 || !strings.HasPrefix(rows[0].PostKey, "rel_") {
		t.Fatalf("RSS: %+v %v", rows, e)
	}
	for _, row := range rows {
		if row.Title == "" || row.TorrentURL == "" || row.OriginalGUID == "" {
			t.Fatal(row)
		}
	}
}
func TestRejectMalformedAndCrossSource(t *testing.T) {
	s := animefeed.NewSource("anibt", "630160", "lolihouse")
	for _, body := range []string{`<rss/>`, `<html/>`, `<rss><channel><item/></channel></rss>`} {
		if _, e := ParseRSS([]byte(body), s); !errors.Is(e, ErrInvalid) {
			t.Fatal(body, e)
		}
	}
	s.GroupID = "other"
	if _, e := ParseRSS(fixture(t, "rss.xml"), s); !errors.Is(e, ErrInvalid) {
		t.Fatal("cross group", e)
	}
	if _, e := ParseEntry([]byte(`{"ok":true,"data":{"bgmId":630160,"title":{"primary":"Title"}}}`), "630160"); !errors.Is(e, ErrInvalid) {
		t.Fatal("absent groups", e)
	}
	if _, e := ParseCatalog([]byte(`{"ok":true,"data":{"items":[]}}`), "2026-10"); !errors.Is(e, ErrInvalid) {
		t.Fatal("changed envelope", e)
	}
	for _, season := range []string{"2026-02", "2026/10", "1999-10"} {
		if _, e := CatalogPath(season, ""); !errors.Is(e, animefeed.ErrInput) {
			t.Fatal(season, e)
		}
	}
	if _, e := CatalogPath("", strings.Repeat("x", 121)); !errors.Is(e, animefeed.ErrInput) {
		t.Fatal(e)
	}
}
func TestHistoryWindowEvidence(t *testing.T) {
	s := animefeed.NewSource("anibt", "630160", "lolihouse")
	for _, tc := range []struct {
		take, rows, total, next int
		complete, stop          string
	}{{50, 3, 3, 0, "visible_end", ""}, {50, 50, 100, 100, "partial", "more_available"}, {100, 100, 150, 0, "partial", "source_limit"}, {100, 100, 100, 0, "visible_end", ""}, {100, 0, -1, 0, "partial", "source_limit"}} {
		p := HistoryPage(s, make([]animefeed.SourceRelease, tc.rows), tc.take, tc.total, time.Now())
		if p.Completeness != tc.complete || p.StopReason != tc.stop || p.NextTake != tc.next {
			t.Fatalf("%+v: %+v", tc, p)
		}
	}
	p, e := RSSPath(s, 100)
	if e != nil || strings.Contains(p, "limit=") {
		t.Fatal("canonical default", p, e)
	}
}
func TestFetchBoundsRedirectsAndStatus(t *testing.T) {
	for _, tc := range []struct {
		name, location string
		status         int
		want           error
	}{
		{"canonical", "/rss/anime.xml?bgmId=630160&groupSlug=lolihouse", 308, nil},
		{"merged", "/rss/anime.xml?bgmId=9&groupSlug=lolihouse", 308, ErrMoved},
		{"foreign", "https://example.com/rss/anime.xml", 308, ErrInvalid},
		{"unknown query", "/rss/anime.xml?bgmId=630160&groupSlug=lolihouse&secret=1", 308, ErrInvalid},
		{"limited", "", 429, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("limit") != "" {
					if tc.location != "" {
						w.Header().Set("Location", tc.location)
					}
					w.Header().Set("Retry-After", "10")
					w.WriteHeader(tc.status)
					return
				}
				w.Header().Set("Content-Type", "application/xml")
				w.Write([]byte(`<rss><channel/></rss>`))
			}))
			defer srv.Close()
			c := &Client{HTTP: srv.Client(), BaseURL: srv.URL}
			_, e := c.Fetch(context.Background(), "/rss/anime.xml?bgmId=630160&groupSlug=lolihouse&limit=50", 1024)
			if tc.want != nil {
				if !errors.Is(e, tc.want) {
					t.Fatal(e)
				}
				return
			}
			if tc.status == 429 {
				var h *HTTPError
				if !errors.As(e, &h) || !h.RetryAt.After(time.Now()) {
					t.Fatal(e)
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
		})
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(strings.Repeat("x", 20)))
	}))
	defer srv.Close()
	c := &Client{HTTP: srv.Client(), BaseURL: srv.URL}
	body, e := c.Fetch(context.Background(), "/api/seasons/anime?season=2026-FALL", 10)
	if !errors.Is(e, ErrInvalid) || len(body) != 11 {
		t.Fatal("read accounting", len(body), e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := c.Fetch(ctx, "/api/seasons/anime?season=2026-FALL", 10); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}
