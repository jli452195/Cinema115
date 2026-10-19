package garden

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/client/animefeed"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, e := os.ReadFile("testdata/" + name)
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func TestNativeCatalogAndSubject(t *testing.T) {
	c, e := ParseCatalog(fixture(t, "calendar.json"), "2026-10")
	if e != nil || len(c.Days) != 7 || c.Days[0].Day != 1 || c.Days[0].Items[0].ID != "634587" || c.Days[0].Items[0].Title == c.Days[0].Items[0].OriginalTitle || !strings.HasPrefix(c.Days[0].Items[0].CoverURL, "/api/garden/image/") {
		t.Fatal(c, e)
	}
	if _, e = ParseCatalog(fixture(t, "calendar.json"), "2026-07"); !errors.Is(e, ErrInvalid) {
		t.Fatal("wrong season", e)
	}
	if _, e = ParseCatalog([]byte(`{"ok":true,"data":{"calendar":[]}}`), "2026-10"); e == nil {
		t.Fatal("invalid calendar accepted")
	}
	s, e := ParseCatalog(fixture(t, "search.json"), "")
	if e != nil || len(s.Items) != 2 {
		t.Fatal(s, e)
	}
	i, e := ParseSubject(fixture(t, "subject.json"), "623249")
	if e != nil || i.BangumiID != "623249" {
		t.Fatal(i, e)
	}
	if _, e = ParseSubject(fixture(t, "subject.json"), "1"); e == nil {
		t.Fatal("wrong subject accepted")
	}
	for _, q := range []string{"", "\n", strings.Repeat("x", 129)} {
		if _, e = CatalogPath("", q); e == nil {
			t.Fatal("invalid query", q)
		}
	}
	for _, season := range []string{"2026-02", "1999-10", "2026-10&x=1"} {
		if _, e = CatalogPath(season, ""); e == nil {
			t.Fatal("invalid season", season)
		}
	}
}
func TestAttributedResourcesAndFeed(t *testing.T) {
	v, e := ParseResources(fixture(t, "resources.json"), "623249", 200, nil)
	if e != nil {
		t.Fatal(e)
	}
	i, e := ParseSubject(fixture(t, "subject.json"), "623249")
	if e != nil {
		t.Fatal(e)
	}
	entry := Entry(i, v)
	if !entry.GroupsPartial || len(entry.Groups) != 3 {
		t.Fatal(entry)
	}
	g := animefeed.Group{ID: "dmhy_f776", Name: v.Rows[0].Fansub.Name, Origin: "dmhy", Kind: "fansub"}
	source := animefeed.NewSource("garden", "623249", g.ID)
	feed, e := source.RSSURL(g.Name)
	if e != nil || !strings.Contains(feed, "provider=dmhy") || !strings.Contains(feed, "subject=623249") || !strings.Contains(feed, "fansub=") || !strings.Contains(feed, "duplicate=true") {
		t.Fatal(feed, e)
	}
	if _, e = source.RSSURL(); e == nil {
		t.Fatal("unscoped feed accepted")
	}
	filtered, e := ParseResources(fixture(t, "filtered.json"), "623249", 3, &g)
	if e != nil {
		t.Fatal(e)
	}
	p := SourcePage(source, filtered, 3, time.Now())
	if p.Completeness != "visible_end" || len(p.Releases) != 2 || p.Releases[0].Origin != "dmhy" || p.Releases[0].Publisher == "" || p.Releases[0].OriginalGUID != "https://animes.garden/detail/dmhy/729236" || strings.Contains(p.Releases[0].MagnetHint, "tr=") {
		t.Fatal(p)
	}
	for _, mutate := range []func(*Resources){
		func(v *Resources) { v.Rows[0].SubjectID++ }, func(v *Resources) { v.Rows[0].Fansub.ID++ }, func(v *Resources) { v.Filter.Provider = "mikan" }, func(v *Resources) { v.Rows[0].Href = "https://untrusted.test/" }, func(v *Resources) { v.Rows[0].Magnet = "magnet:?xt=urn:btih:invalid" }, func(v *Resources) { v.Rows[0].CreatedAt = "tomorrow" },
	} {
		var copy Resources
		_ = json.Unmarshal(fixture(t, "filtered.json"), &copy)
		mutate(&copy)
		raw, _ := json.Marshal(copy)
		if _, e = ParseResources(raw, "623249", 3, &g); e == nil {
			t.Fatal("attribution or metadata mismatch accepted")
		}
	}
	// The same publisher with no fansub gets its own publisher identity.
	r := v.Rows[0]
	r.Fansub = nil
	pg := groupFor(r)
	if pg.ID != "dmhy_p99" || pg.Kind != "publisher" {
		t.Fatal(pg)
	}
	path, e := ResourcesPath("623249", 100, &pg)
	if e != nil || !strings.Contains(path, "publisher=") || strings.Contains(path, "fansub=") {
		t.Fatal(path, e)
	}
	// A selected publisher includes posts with a fansub as well as its own
	// unattributed posts. Verify the publisher ID, not the discovery label.
	filtered.Filter.Fansubs = nil
	filtered.Filter.Publishers = []string{pg.Name}
	filtered.Rows[1].Fansub = nil
	raw, e := json.Marshal(filtered)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = ParseResources(raw, "623249", 3, &pg); e != nil {
		t.Fatal("publisher with mixed fansub attribution", e)
	}
	filtered.Rows[0].Publisher.ID++
	raw, e = json.Marshal(filtered)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = ParseResources(raw, "623249", 3, &pg); e == nil {
		t.Fatal("different publisher accepted")
	}
}
func TestHistoryBoundaryAndStablePostIdentity(t *testing.T) {
	v, e := ParseResources(fixture(t, "filtered.json"), "623249", 3, nil)
	if e != nil {
		t.Fatal(e)
	}
	s := animefeed.NewSource("garden", "623249", "dmhy_f776")
	complete := false
	v.Pagination.Complete = &complete
	p := SourcePage(s, v, 100, time.Now())
	if p.NextTake != 200 || p.Completeness != "partial" {
		t.Fatal(p)
	}
	p = SourcePage(s, v, 1000, time.Now())
	if p.StopReason != "source_limit" || p.NextTake != 0 {
		t.Fatal(p)
	}
	a := p.Releases[0].PostKey
	v.Rows[0].ID++
	if SourcePage(s, v, 1000, time.Now()).Releases[0].PostKey != a {
		t.Fatal("reimport changed identity")
	}
	complete = true
	v.Rows = []Resource{}
	p = SourcePage(s, v, 100, time.Now())
	if p.Completeness != "visible_end" || p.Total == nil || *p.Total != 0 {
		t.Fatal(p)
	}
}
func TestFixedHostsCancellationAndBounds(t *testing.T) {
	var redirected bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("mode") {
		case "redirect":
			http.Redirect(w, r, "/target", 302)
		case "limit":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(strings.Repeat("x", 100)))
		case "cooldown":
			w.Header().Set("Retry-After", "999999")
			w.WriteHeader(429)
		default:
			redirected = true
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("challenge"))
		}
	}))
	defer server.Close()
	c := New()
	c.MetadataURL, c.ResourcesURL = server.URL, server.URL
	if _, e := c.Fetch(context.Background(), "https://evil.test/", 100); !errors.Is(e, animefeed.ErrInput) {
		t.Fatal(e)
	}
	if _, e := c.Fetch(context.Background(), "/resources?mode=limit", 20); !errors.Is(e, ErrInvalid) {
		t.Fatal(e)
	}
	if _, e := c.Fetch(context.Background(), "/resources?mode=redirect", 100); e == nil {
		t.Fatal("redirect accepted")
	}
	if redirected {
		t.Fatal("followed redirect")
	}
	_, e := c.Fetch(context.Background(), "/resources?mode=cooldown", 100)
	var he *animefeed.HTTPError
	if !errors.As(e, &he) || he.Site != "garden" || time.Until(he.RetryAt) > time.Hour {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := c.Fetch(ctx, "/resources", 100); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if _, e := c.Fetch(context.Background(), "/resources", 100); !errors.Is(e, ErrInvalid) {
		t.Fatal("HTML accepted", e)
	}
}
