package mikan

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func rssRow(key, guid string) string {
	return fmt.Sprintf(`<item><title>测试 [01][1080p]</title><guid isPermaLink="false">%s</guid><link>https://mikanani.me/Home/Episode/%s</link><description><![CDATA[<script>inert</script>]]></description><torrent xmlns="https://mikanani.me/0.1/"><link>https://mikanani.me/Home/Episode/%s</link><contentLength>1234</contentLength><pubDate>2020-11-04T13:19:24.888</pubDate></torrent><enclosure type="application/x-bittorrent" length="1234" url="https://mikanani.me/Download/20201104/%s.torrent"/></item>`, guid, key, key, key)
}

func rssPage(rows string) string {
	return `<?xml version="1.0"?><rss version="2.0"><channel><title>番组</title>` + rows + `</channel></rss>`
}

func sourceRequest(kind string, take int) SourceRequest {
	return SourceRequest{Kind: kind, Source: Source{ItemID: "2259", GroupID: "45"}, Take: take, MaxRows: AnimeMaxRows, BodyLimit: AnimeMaxBodyBytes}
}

func TestSourceRSSInput(t *testing.T) {
	source, err := ParseRSSSource(BaseURL + "/RSS/Bangumi?subgroupid=45&bangumiId=2259")
	if err != nil || source != (Source{ItemID:"2259", GroupID:"45"}) {
		t.Fatal(source, err)
	}
	canonical, err := source.RSSURL()
	if err != nil || canonical != BaseURL+"/RSS/Bangumi?bangumiId=2259&subgroupid=45" {
		t.Fatal(canonical, err)
	}
	for _, raw := range []string{
		"http://mikanani.me/RSS/Bangumi?bangumiId=2259&subgroupid=45",
		"https://mikanani.me:443/RSS/Bangumi?bangumiId=2259&subgroupid=45",
		"https://mikanani.me@evil.test/RSS/Bangumi?bangumiId=2259&subgroupid=45",
		BaseURL + "/RSS/Bangumi?bangumiId=2259&subgroupid=45&subgroupid=46",
		BaseURL + "/RSS/Bangumi?bangumiId=2259&subgroupid=45&token=secret",
		BaseURL + "/RSS/Bangumi?bangumiId=2259&subgroupid=45#fragment",
		BaseURL + "/RSS/Bangumi?bangumiId=2259&subgroupid=045",
		BaseURL + "/RSS/Bangumi?bangumiId=2259",
		BaseURL + "/RSS/%42angumi?bangumiId=2259&subgroupid=45",
	} {
		if _, err := ParseRSSSource(raw); !errors.Is(err, ErrInput) {
			t.Errorf("accepted %s", raw)
		}
	}
}

func TestSourceRSSIdentityAndRawMetadata(t *testing.T) {
	other := strings.Repeat("a", 40)
	page, err := parseSourceRSS([]byte(rssPage(rssRow(testHash, "same GUID")+rssRow(other, "same GUID"))), sourceRequest("rss", 0))
	if err != nil || page.RawCount != 2 || len(page.Releases) != 2 || page.Completeness != "partial" {
		t.Fatal(page, err)
	}
	first := page.Releases[0]
	if first.OriginalGUID != "same GUID" || first.PostKey != testHash || first.MagnetHint != "" || first.SizeBytes != 1234 || first.PublishedRaw != "2020-11-04T13:19:24.888" {
		t.Fatal(first)
	}
	// A missing date is retained for later review rather than replaced with now.
	body := strings.ReplaceAll(rssPage(rssRow(testHash, "")), "<pubDate>2020-11-04T13:19:24.888</pubDate>", "")
	page, err = parseSourceRSS([]byte(body), sourceRequest("rss", 0))
	if err != nil || page.Releases[0].PublishedRaw != "" {
		t.Fatal(page, err)
	}
}

func TestSourceRSSDuplicateRowsAndZero(t *testing.T) {
	row := rssRow(testHash, "guid")
	page, err := parseSourceRSS([]byte(rssPage(row+row)), sourceRequest("rss", 0))
	if err != nil || page.RawCount != 2 || len(page.Releases) != 1 {
		t.Fatal(page, err)
	}
	page, err = parseSourceRSS([]byte(rssPage("")), sourceRequest("rss", 0))
	if err != nil || page.RawCount != 0 || page.Releases == nil || page.Completeness != "partial" {
		t.Fatal(page, err)
	}
}

func TestSourceRSSRejectsBrokenOrAmbiguousRows(t *testing.T) {
	row := rssRow(testHash, "guid")
	for name, body := range map[string]string{
		"html":                  "<html>Just a moment...</html>",
		"no_channel":            `<rss version="2.0"></rss>`,
		"two_channels":          `<rss version="2.0"><channel/><channel/></rss>`,
		"wrong_version":         `<rss version="1.0"><channel/></rss>`,
		"second_document":       rssPage("") + rssPage(""),
		"doctype":               `<!DOCTYPE rss SYSTEM "https://evil.test/entity">` + rssPage(row),
		"bad_row":               rssPage("<item><title>no link</title></item>"),
		"external_link":         rssPage(strings.ReplaceAll(row, "https://mikanani.me/Home/Episode/", "https://evil.test/Home/Episode/")),
		"external_torrent":      rssPage(strings.ReplaceAll(row, "https://mikanani.me/Download/", "https://evil.test/Download/")),
		"size_conflict":         rssPage(strings.ReplaceAll(row, `length="1234"`, `length="1235"`)),
		"link_conflict":         rssPage(strings.Replace(row, "<link>https://mikanani.me/Home/Episode/"+testHash+"</link><contentLength>", "<link>https://mikanani.me/Home/Episode/"+strings.Repeat("a", 40)+"</link><contentLength>", 1)),
		"same_post_changed":     rssPage(row + strings.Replace(row, "测试 [01]", "改名 [01]", 1)),
		"duplicate_link":        rssPage(strings.Replace(row, "</guid>", "</guid><link>https://mikanani.me/Home/Episode/"+testHash+"</link>", 1)),
		"deep_xml":              rssPage(strings.Repeat("<x>", 35) + strings.Repeat("</x>", 35)),
		"wrong_field_namespace": rssPage(strings.Replace(row, "<title>", `<title xmlns="https://evil.test/">`, 1)),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseSourceRSS([]byte(body), sourceRequest("rss", 0)); !errors.Is(err, ErrParse) {
				t.Fatal(err)
			}
		})
	}
}

func TestSourceRSSRawLimitBeforeDeduplication(t *testing.T) {
	in := sourceRequest("rss", 0)
	in.MaxRows = 1
	if _, err := parseSourceRSS([]byte(rssPage(strings.Repeat(rssRow(testHash, "guid"), 2))), in); !errors.Is(err, ErrLimit) {
		t.Fatal(err)
	}
	if _, err := parseSourceRSS([]byte(rssPage(strings.Repeat("<item/>", AnimeMaxRows+1))), sourceRequest("rss", 0)); !errors.Is(err, ErrLimit) {
		t.Fatal(err)
	}
}

func TestSourceHistoryRawCountAndVisibleEnd(t *testing.T) {
	row := testRow(testHash, "资源", "2026/10/18 12:00")
	page, err := parseSourceHTML([]byte(testPage(row+row)), sourceRequest("history", 2))
	if err != nil || page.RawCount != 2 || len(page.Releases) != 1 || page.Completeness != "partial" || page.NextTake != 52 {
		t.Fatal(page, err)
	}
	page, err = parseSourceHTML([]byte(testPage(row+row)), sourceRequest("history", 3))
	if err != nil || page.Completeness != "visible_end" || page.NextTake != 0 {
		t.Fatal(page, err)
	}
	page, err = parseSourceHTML([]byte(testPage("")), sourceRequest("history", 1))
	if err != nil || page.Completeness != "visible_end" || page.RawCount != 0 {
		t.Fatal(page, err)
	}
	if _, err = parseSourceHTML([]byte(testPage(row+strings.Replace(row, "资源", "changed", 1))), sourceRequest("history", 2)); !errors.Is(err, ErrParse) {
		t.Fatal(err)
	}
	if _, err = parseSourceHTML([]byte(testPage(row+row)), sourceRequest("history", 1)); !errors.Is(err, ErrParse) {
		t.Fatal("ignored take accepted", err)
	}
}

func TestSourceHistoryLimitAndInvalidRows(t *testing.T) {
	body := []byte(testPage(strings.Repeat(testRow(testHash, "资源", "2026/10/18"), AnimeMaxRows)))
	page, err := parseSourceHTML(body, sourceRequest("history", AnimeMaxRows))
	if err != nil || page.RawCount != AnimeMaxRows || page.StopReason != "limit" || page.NextTake != 0 || page.Completeness != "partial" {
		t.Fatal(page, err)
	}
	bad := testPage(testRow(testHash, "资源", "2026/10/18") + "<tr><td>broken</td></tr>")
	if _, err := parseSourceHTML([]byte(bad), sourceRequest("history", 10)); !errors.Is(err, ErrParse) {
		t.Fatal(err)
	}
}

func TestSourceHTTPPathsAndNoDownload(t *testing.T) {
	calls := make([]string, 0, 3)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.URL.Path)
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			t.Error("credentials sent")
		}
		if r.URL.Query().Get("bangumiId") != "2259" {
			t.Error(r.URL)
		}
		switch r.URL.Path {
		case "/RSS/Bangumi":
			if r.URL.Query().Get("subgroupid") != "45" || r.URL.Query().Get("subtitleGroupId") != "" {
				t.Error(r.URL)
			}
			fmt.Fprint(w, rssPage(rssRow(testHash, "guid")))
		case "/Home/ExpandEpisodeTable":
			if r.URL.Query().Get("subtitleGroupId") != "45" || r.URL.Query().Get("take") != "65" {
				t.Error(r.URL)
			}
			fmt.Fprint(w, testPage(testRow(testHash, "资源", "2026/10/18")))
		default:
			t.Errorf("unexpected request %s", r.URL)
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	client := New()
	client.BaseURL = server.URL
	for _, in := range []SourceRequest{sourceRequest("rss", 0), sourceRequest("history", 65)} {
		page, err := client.ReadSource(context.Background(), in)
		if err != nil || page.RawCount != 1 || page.ReadBytes <= 0 || page.ObservedAt.IsZero() {
			t.Fatal(page, err)
		}
	}
	if len(calls) != 2 {
		t.Fatal(calls)
	}
}

func TestSourceHTTPBoundsAndCancellation(t *testing.T) {
	body := rssPage(rssRow(testHash, "guid"))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
	defer server.Close()
	client := New()
	client.BaseURL = server.URL
	in := sourceRequest("rss", 0)
	in.BodyLimit = int64(len(body) - 1)
	page, err := client.ReadSource(context.Background(), in)
	if !errors.Is(err, ErrLimit) || page.ReadBytes != int64(len(body)) {
		t.Fatal(page, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.ReadSource(ctx, sourceRequest("rss", 0)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for _, in := range []SourceRequest{{Kind: "rss"}, sourceRequest("history", 2001), sourceRequest("rss", 1)} {
		if _, err := client.ReadSource(context.Background(), in); !errors.Is(err, ErrInput) {
			t.Fatal(err)
		}
	}
}

func TestSourceHTTPRedirectAndCooldown(t *testing.T) {
	redirectCalls := 0
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirectCalls++ }))
	defer destination.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", destination.URL)
		w.WriteHeader(http.StatusFound)
	}))
	defer server.Close()
	client := New()
	client.BaseURL = server.URL
	client.HTTP = server.Client()
	if _, err := client.ReadSource(context.Background(), sourceRequest("rss", 0)); err == nil || redirectCalls != 0 {
		t.Fatal(err, redirectCalls)
	}
	now := time.Now()
	for _, raw := range []string{"120", now.Add(2 * time.Minute).UTC().Format(http.TimeFormat), "9999999999999999999999999999999"} {
		resp := &http.Response{StatusCode: 429, Header: http.Header{"Retry-After": {raw}}}
		err := responseError(resp, now)
		var status *ResponseError
		if !errors.Is(err, ErrBlocked) || !errors.As(err, &status) || status.RetryAt.Before(now.Add(time.Minute)) {
			t.Fatal(err)
		}
	}
}
