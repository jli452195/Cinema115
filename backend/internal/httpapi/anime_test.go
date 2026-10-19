package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/animepreview"
	"strm115/backend/internal/client/bangumi"
	"strm115/backend/internal/client/mikan"
	"strm115/backend/internal/model"
	"strm115/backend/internal/service"
	"strm115/backend/internal/storage"
)

type animeHTTPDiscovery struct {
	calls, relationCalls int
	episodeCalls         int
	err                  error
}

func (f *animeHTTPDiscovery) Episodes(ctx context.Context, id string, offset int) (bangumi.EpisodesResult, error) {
	f.episodeCalls++
	return bangumi.EpisodesResult{Items: []bangumi.Episode{{ID: "1388677", Number: 59, ChapterNumber: animeHTTPPtr(1.0), Title: "混战都市", Date: "2025-02-05"}}, Total: 1, Offset: offset, Limit: 100, MaxItems: 2000}, f.err
}

func (f *animeHTTPDiscovery) Calendar(ctx context.Context, day int) (bangumi.CalendarResult, error) {
	f.calls++
	return bangumi.CalendarResult{Days: []bangumi.Day{{Weekday: day, Items: []bangumi.Card{}}}}, f.err
}
func (f *animeHTTPDiscovery) Search(ctx context.Context, in bangumi.SearchOptions) (bangumi.SearchResult, error) {
	f.calls++
	return bangumi.SearchResult{Items: []bangumi.Card{}, Limit: in.Limit, Offset: in.Offset}, f.err
}
func (f *animeHTTPDiscovery) Subject(ctx context.Context, id string) (bangumi.SubjectResult, error) {
	f.calls++
	return bangumi.SubjectResult{Subject: bangumi.Subject{Card: bangumi.Card{ID: id, Title: "可信篇章", OriginalTitle: "Original"}}}, f.err
}
func (f *animeHTTPDiscovery) Relations(ctx context.Context, id string) (bangumi.RelationsResult, error) {
	f.relationCalls++
	return bangumi.RelationsResult{Items: []bangumi.Relation{}}, f.err
}

type animeHTTPMikan struct {
	reads int
	err   error
}

func (f *animeHTTPMikan) Search(context.Context, string, string) (mikan.Result, error) {
	return mikan.Result{Items: []mikan.Item{}, Groups: []mikan.Group{}, Bangumis: []mikan.Bangumi{}}, f.err
}
func (f *animeHTTPMikan) ReadSource(ctx context.Context, in mikan.SourceRequest) (mikan.SourcePage, error) {
	f.reads++
	if f.err != nil {
		return mikan.SourcePage{}, f.err
	}
	p := mikan.SourcePage{Kind: in.Kind, Source: in.Source, ObservedAt: time.Now().UTC().Truncate(time.Second), Completeness: "visible_end", ReadBytes: 100, Releases: []mikan.SourceRelease{}}
	if in.Kind == "entry" {
		p.Entry = &mikan.SourceEntry{ID: in.Source.ItemID, Title: "可信资源条目", Groups: []mikan.Group{{ID: "45", Name: "字幕组甲"}}}
		return p, nil
	}
	n := 2
	if in.Kind == "history" {
		n = min(in.Take, 2)
		p.RequestedTake = in.Take
	}
	p.RawCount = n
	for i := 1; i <= n; i++ {
		p.Releases = append(p.Releases, mikan.SourceRelease{PostKey: fmt.Sprintf("%040x", i), Title: fmt.Sprintf("[组] 番剧 [%02d][1080p]", i), PublishedRaw: p.ObservedAt.Add(-time.Minute).Format(time.RFC3339)})
	}
	return p, nil
}
func animeHTTPPtr[T any](v T) *T { return &v }
func animeHTTPSource() model.AnimeSourceConfig {
	return model.AnimeSourceConfig{Ref: "main", Role: "main", Provider: "mikan", Status: "paused", ItemID: animeHTTPPtr("2259"), GroupID: animeHTTPPtr("45"), GroupName: animeHTTPPtr("browser"), Binding: model.AnimeBinding{SchemaVersion: 1, Confirmed: true, Scope: &model.AnimeScope{Kind: "range", First: animeHTTPPtr(1), Last: animeHTTPPtr(100)}}, Rules: model.AnimeRules{SchemaVersion: 1, Resolution: animeHTTPPtr("1080p"), MinimumEpisode: animeHTTPPtr(1), InitialMode: "from_episode"}}
}

type animeHTTPFixture struct {
	r           *gin.Engine
	s           *service.AnimeAdminService
	admin, user string
	bgm         *animeHTTPDiscovery
	mikan       *animeHTTPMikan
}

func newAnimeHTTPFixture(t *testing.T) animeHTTPFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "anime.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	auth := portalTestAuth(t, db, "admin", "admin-password", "secret")
	a, _, err := auth.LoginAdmin(ctx, "admin", "admin-password")
	if err != nil {
		t.Fatal(err)
	}
	u, _, err := auth.Login(ctx, "reader", "reader-password")
	if err != nil {
		t.Fatal(err)
	}
	b := &animeHTTPDiscovery{}
	m := &animeHTTPMikan{}
	bindings := &service.AnimeBindingService{Entries: db, Subjects: b, Mikan: service.NewMikanService(m, nil)}
	s := &service.AnimeAdminService{Store: db, Bindings: bindings, Discovery: b, Previews: &service.AnimePreviewService{Repository: db, Bindings: bindings}}
	r := gin.New()
	RegisterAnime(r.Group("/api/admin", PortalAuthenticated(auth), PortalAdminOnly()), s)
	return animeHTTPFixture{r, s, a, u, b, m}
}
func (f animeHTTPFixture) call(t *testing.T, method, path, token, key string, body any, want int) map[string]any {
	t.Helper()
	raw := ""
	if text, ok := body.(string); ok {
		raw = text
	} else if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		raw = string(b)
	}
	req := httptest.NewRequest(method, "/api/admin/anime"+path, strings.NewReader(raw))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", key)
	w := httptest.NewRecorder()
	f.r.ServeHTTP(w, req)
	if w.Code != want {
		t.Fatalf("%s %s got %d want %d: %s", method, path, w.Code, want, w.Body.String())
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("missing no-store")
	}
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if want >= 400 {
		if out["error"] == nil || out["code"] == nil {
			t.Fatal("missing stable error", out)
		}
	}
	return out
}

func TestAnimeHTTPAuthBeforeAnyBusiness(t *testing.T) {
	f := newAnimeHTTPFixture(t)
	for _, path := range []string{"/capabilities", "/settings", "/discovery/calendar", "/subscriptions", "/subscriptions/not-found", "/previews/not-found/releases"} {
		if v := f.call(t, "GET", path, "", "", nil, 401); v["code"] != "UNAUTHENTICATED" {
			t.Fatal(v)
		}
		if v := f.call(t, "GET", path, f.user, "", nil, 403); v["code"] != "ADMIN_REQUIRED" {
			t.Fatal(v)
		}
	}
	for _, path := range []string{"/subscriptions", "/subscriptions/preview", "/source-bindings/preview", "/discovery/search", "/previews/not-found/load", "/jobs/not-found/retry"} {
		f.call(t, "POST", path, "", "", `broken`, 401)
		f.call(t, "POST", path, f.user, "", `broken`, 403)
	}
	if f.bgm.calls != 0 || f.mikan.reads != 0 {
		t.Fatal("unauthorized read upstream")
	}
	v := f.call(t, "GET", "/capabilities", f.admin, "", nil, 200)
	caps := v["capabilities"].(map[string]any)
	if caps["configure"] != true || caps["download"] != false || caps["organize"] != false {
		t.Fatal(v)
	}
}

func TestAnimeHTTPStrictBoundaryAndSafeErrors(t *testing.T) {
	f := newAnimeHTTPFixture(t)
	for _, body := range []string{`{"keyword":"x","unknown":1}`, `{"keyword":"x"} {}`, `{"keyword":" "}`, `{"keyword":"x","sort":"rank"}`, `{"keyword":"x","limit":51}`, `{"keyword":"x","offset":1001}`, `{"keyword":"x","offset":1.5}`} {
		f.call(t, "POST", "/discovery/search", f.admin, "", body, 400)
	}
	f.call(t, "POST", "/discovery/search", f.admin, "", strings.Repeat(" ", 65<<10), 413)
	f.call(t, "POST", "/subscriptions", f.admin, "key_0001", strings.Repeat(" ", (1<<20)+1), 413)
	f.call(t, "PATCH", "/settings", f.admin, "key_0001", `{"downloadPath":"browser lie"}`, 400)
	for _, path := range []string{"/subscriptions?page=0", "/subscriptions?pageSize=101", "/subscriptions?page=1&page=2", "/discovery/calendar?weekday=9", "/discovery/subjects/not-decimal", "/sources/mikan/not-decimal"} {
		f.call(t, "GET", path, f.admin, "", nil, 400)
	}
	if f.bgm.calls != 0 || f.mikan.reads != 0 {
		t.Fatal("invalid input reached upstream")
	}
	f.call(t, "POST", "/discovery/search", f.admin, "", `{"keyword":"  番剧  "}`, 200)
	f.call(t, "GET", "/discovery/subjects/123", f.admin, "", nil, 200)
	if f.bgm.relationCalls != 0 {
		t.Fatal("relations loaded implicitly")
	}
	f.call(t, "GET", "/discovery/subjects/123?includeRelations=true", f.admin, "", nil, 200)
	if f.bgm.relationCalls != 1 {
		t.Fatal("explicit relation read missing")
	}
	f.bgm.err = fmt.Errorf("Cookie=do-not-leak upstream <html>: %w", bangumi.ErrUnavailable)
	v := f.call(t, "GET", "/discovery/subjects/123", f.admin, "", nil, 502)
	raw, _ := json.Marshal(v)
	if strings.Contains(string(raw), "do-not-leak") || strings.Contains(string(raw), "html") {
		t.Fatal("unsafe error", v)
	}
	f.bgm.err = &bangumi.UpstreamError{Kind: bangumi.ErrCooldown, RetryAfterAt: time.Now().Add(time.Minute)}
	if v := f.call(t, "GET", "/discovery/subjects/123", f.admin, "", nil, 429); v["code"] != "SOURCE_COOLDOWN" {
		t.Fatal(v)
	}
}

func TestAnimeHTTPConfigReceiptAndLocalReads(t *testing.T) {
	f := newAnimeHTTPFixture(t)
	in := service.AnimeSaveInput{SaveMode: "draft", SettingsRevision: 1, Config: service.AnimeSaveConfig{Entry: service.AnimeEntryRef{BangumiID: animeHTTPPtr("123"), EditionKey: "original"}, Sources: []model.AnimeSourceConfig{animeHTTPSource()}}}
	f.call(t, "POST", "/subscriptions", f.admin, "", in, 400)
	first := f.call(t, "POST", "/subscriptions", f.admin, "create_001", in, 201)
	bgmCalls, mikanReads := f.bgm.calls, f.mikan.reads
	f.bgm.err = bangumi.ErrNotFound
	f.mikan.err = mikan.ErrNetwork
	again := f.call(t, "POST", "/subscriptions", f.admin, "create_001", in, 201)
	if first["id"] != again["id"] || first["revision"] != again["revision"] || f.bgm.calls != bgmCalls || f.mikan.reads != mikanReads {
		t.Fatal("replay consulted upstream", again)
	}
	id := first["id"].(string)
	f.call(t, "GET", "/subscriptions/"+id, f.admin, "", nil, 200)
	f.call(t, "GET", "/subscriptions?pageSize=1", f.admin, "", nil, 200)
	if f.bgm.calls != bgmCalls || f.mikan.reads != mikanReads {
		t.Fatal("local list read upstream")
	}
	if v := f.call(t, "POST", "/subscriptions", f.admin, "duplicate_new", in, 409); v["code"] != "SUBSCRIPTION_EXISTS" || v["details"].(map[string]any)["existingId"] != id {
		t.Fatal(v)
	}
	in.SaveMode = "paused"
	if v := f.call(t, "POST", "/subscriptions", f.admin, "create_001", in, 409); v["code"] != "IDEMPOTENCY_KEY_REUSED" {
		t.Fatal(v)
	}
	in.SaveMode = "enabled"
	if v := f.call(t, "POST", "/subscriptions", f.admin, "enabled_01", in, 409); v["code"] != "CAPABILITY_UNAVAILABLE" {
		t.Fatal(v)
	}
	for _, path := range []string{"/subscriptions/" + id + "/enable", "/jobs/a/retry", "/subscriptions/" + id + "/organize/apply"} {
		if v := f.call(t, "POST", path, f.admin, "", nil, 409); v["code"] != "CAPABILITY_UNAVAILABLE" {
			t.Fatal(v)
		}
	}
}

func TestAnimeHTTPPreviewProjectionOwnershipAndSelectionCAS(t *testing.T) {
	f := newAnimeHTTPFixture(t)
	in := service.AnimePreviewInput{SourceRef: "main", Config: animepreview.Config{Chapter: animepreview.Chapter{BangumiID: animeHTTPPtr("123"), EditionKey: "original", DraftKey: "draft-one"}, Settings: model.AnimeSettings{Revision: 1}, Sources: []model.AnimeSourceConfig{animeHTTPSource()}}}
	v := f.call(t, "POST", "/subscriptions/preview", f.admin, "", in, 200)
	if _, ok := v["batch"]; ok {
		t.Fatal("internal complete batch leaked")
	}
	batch := v["batchId"].(string)
	summary := v["sources"].([]any)[0].(map[string]any)
	preview := summary["sourcePreviewId"].(string)
	page := v["page"].(map[string]any)
	if page["total"] != float64(2) {
		t.Fatal(page)
	}
	f.call(t, "GET", "/previews/"+batch+"/releases?sourceRef=main", f.admin, "", nil, 200)
	selectBody := gin.H{"sourceRef": "main", "sourcePreviewId": preview, "expectedLoadRevision": 1, "selectedReleaseIds": []string{}, "dismissedReleaseIds": []string{}}
	f.call(t, "POST", "/previews/"+batch+"/selections", f.admin, "", selectBody, 200)
	if v := f.call(t, "POST", "/previews/"+batch+"/selections", f.admin, "", selectBody, 409); v["code"] != "REVISION_CONFLICT" {
		t.Fatal(v)
	}
	load := gin.H{"sourceRef": "main", "sourcePreviewId": preview, "expectedLoadRevision": 2, "nextTake": 100}
	loaded := f.call(t, "POST", "/previews/"+batch+"/load", f.admin, "", load, 200)
	summary = loaded["sources"].([]any)[0].(map[string]any)
	if summary["loadRevision"] != float64(3) || summary["selectedCount"] != float64(0) {
		t.Fatal(loaded)
	}
	// Saved release rows from a different subscription cannot be read by
	// passing their source ID to this subscription's endpoint.
	create := service.AnimeSaveInput{SaveMode: "draft", SettingsRevision: 1, Config: service.AnimeSaveConfig{Entry: service.AnimeEntryRef{BangumiID: animeHTTPPtr("123"), EditionKey: "original"}, Sources: in.Config.Sources}}
	sub := f.call(t, "POST", "/subscriptions", f.admin, "source_scope_01", create, 201)
	f.call(t, "GET", "/subscriptions/"+sub["id"].(string)+"/releases?sourceId=foreign-source", f.admin, "", nil, 400)
	if _, err := f.s.Previews.Releases(context.Background(), "other-admin", batch, "main", 1, 50); err == nil {
		t.Fatal("preview crossed administrator boundary")
	}
	// Source ownership is checked before querying another subscription's rows.
	f.call(t, "GET", "/subscriptions/not-existing/releases?sourceId=other", f.admin, "", nil, 404)
}
