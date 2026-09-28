package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"strm115/backend/internal/service"
)

const prewarmTestUser = "8be403a10974486c94a987b8036c8484"

func prewarmFixture(t *testing.T, enabled, multiple bool, denied *atomic.Bool) *embyProxyHandler {
	t.Helper()
	accounts, id := proxyTestAccounts(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/emby/Users/" + prewarmTestUser:
			if denied.Load() {
				w.WriteHeader(403)
				return
			}
			fmt.Fprintf(w, `{"Id":%q,"Policy":{"EnableMediaPlayback":true}}`, prewarmTestUser)
		case "/emby/Users/" + prewarmTestUser + "/Items/movie":
			fmt.Fprint(w, `{"Id":"movie","PlayAccess":"Full"}`)
		case "/emby/Items/movie/PlaybackInfo":
			sources := `{"Id":"src","SupportsDirectPlay":true,"Path":"http://nas/api/redirect115/abc123"}`
			if multiple {
				sources += `,{"Id":"src2","SupportsDirectPlay":true,"Path":"http://nas/api/redirect115/xyz123"}`
			}
			fmt.Fprintf(w, `{"MediaSources":[%s]}`, sources)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(upstream.Close)
	h := NewEmbyProxyHandler(service.EmbyProxyConfig{URL: upstream.URL, AccountID: id, Prewarm: enabled, DirectLinkCacheMinutes: 9}, accounts, NewShareAPI(func() string { return "" }), service.NewEmbyProxyLogs(&service.GenerationLogs{})).(*embyProxyHandler)
	t.Cleanup(h.Close)
	if err := h.identities.Remember(upstream.URL, "token", prewarmTestUser); err != nil {
		t.Fatal(err)
	}
	return h
}

func prewarmCall(h *embyProxyHandler, path, ua string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", path, nil)
	r.Header.Set("User-Agent", ua)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func awaitPrewarm(t *testing.T, h *embyProxyHandler) {
	t.Helper()
	done := make(chan struct{})
	go func() { h.prewarm.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("prewarm did not finish")
	}
}

func TestPrewarmAsyncReuseUAAndAuthorization(t *testing.T) {
	var denied atomic.Bool
	h := prewarmFixture(t, true, false, &denied)
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	h.pickDownload = func(ctx context.Context, pc, cookie, ua string) (string, error) {
		if calls.Add(1) == 1 {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
		return "https://cdn.115.com/" + ua, nil
	}
	info := "/emby/Items/movie/PlaybackInfo?api_key=token"
	play := "/cinema115/play?ItemId=movie&MediaSourceId=src&api_key=token"
	if w := prewarmCall(h, info, "page"); w.Code != 200 {
		t.Fatal(w.Code)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("not started")
	}
	// Repeated page reads do not create another task while the cloud call blocks.
	prewarmCall(h, info, "page")
	if calls.Load() != 1 {
		t.Fatal("duplicate task")
	}
	result := make(chan *httptest.ResponseRecorder, 1)
	go func() { result <- prewarmCall(h, play, "page") }()
	select {
	case <-result:
		t.Fatal("playback did not wait")
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	select {
	case w := <-result:
		if w.Code != 302 || w.Header().Get("Location") != "https://cdn.115.com/page" {
			t.Fatal(w.Code, w.Body.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("playback stuck")
	}
	awaitPrewarm(t, h)
	if calls.Load() != 1 {
		t.Fatal("same UA fetched twice")
	}
	if w := prewarmCall(h, play, "player"); w.Code != 302 || w.Header().Get("Location") != "https://cdn.115.com/player" {
		t.Fatal("UA cache crossed")
	}
	if calls.Load() != 2 {
		t.Fatal("UA change not fetched")
	}
	denied.Store(true)
	if w := prewarmCall(h, play, "page"); w.Code != 403 {
		t.Fatal("cached link bypassed permission", w.Code)
	}
}

func TestPrewarmAdmissionAndPermission(t *testing.T) {
	for _, tc := range []struct {
		name                                  string
		enabled, multiple, denied, foreground bool
		query                                 string
		want                                  int32
	}{
		{"disabled", false, false, false, false, "", 0},
		{"ambiguous", true, true, false, false, "", 0},
		{"selected", true, true, false, false, "&MediaSourceId=src2", 1},
		{"denied", true, false, true, false, "", 0},
		{"foreground", true, false, false, true, "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var denied atomic.Bool
			denied.Store(tc.denied)
			h := prewarmFixture(t, tc.enabled, tc.multiple, &denied)
			var calls atomic.Int32
			h.pickDownload = func(ctx context.Context, pc, cookie, ua string) (string, error) {
				calls.Add(1)
				return "https://cdn.115.com/file", nil
			}
			if tc.foreground {
				finish := h.foregroundPlayback()
				defer finish()
			}
			prewarmCall(h, "/emby/Items/movie/PlaybackInfo?api_key=token"+tc.query, "page")
			awaitPrewarm(t, h)
			if calls.Load() != tc.want {
				t.Fatal("unexpected cloud access", calls.Load())
			}
		})
	}
}

func TestPrewarmCloseCancelsTask(t *testing.T) {
	var denied atomic.Bool
	h := prewarmFixture(t, true, false, &denied)
	started := make(chan struct{})
	h.pickDownload = func(ctx context.Context, pc, cookie, ua string) (string, error) {
		close(started)
		<-ctx.Done()
		return "", ctx.Err()
	}
	prewarmCall(h, "/emby/Items/movie/PlaybackInfo?api_key=token", "page")
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("not started")
	}
	h.Close()
	prewarmCall(h, "/emby/Items/movie/PlaybackInfo?api_key=token", "other")
	awaitPrewarm(t, h)
}

func TestPrewarmPinsCredentialsAndDoesNotReuseAfterChange(t *testing.T) {
	var denied atomic.Bool
	h := prewarmFixture(t, true, false, &denied)
	old, err := h.accounts.Get(h.config.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	seen := make(chan string, 2)
	h.pickDownload = func(ctx context.Context, pc, cookie, ua string) (string, error) {
		if calls.Add(1) == 1 {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
		seen <- cookie
		return "https://cdn.115.com/file", nil
	}
	prewarmCall(h, "/emby/Items/movie/PlaybackInfo?api_key=token", "page")
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("not started")
	}
	updated := "UID=123_A; SEID=replacement-secret"
	if err := h.accounts.Update(old.ID, old.Revision, old.Name, &updated); err != nil {
		t.Fatal(err)
	}
	close(release)
	awaitPrewarm(t, h)
	if got := <-seen; got != old.Cookie {
		t.Fatal("task credentials changed")
	}
	if w := prewarmCall(h, "/cinema115/play?ItemId=movie&MediaSourceId=src&api_key=token", "page"); w.Code != 302 {
		t.Fatal(w.Code)
	}
	if calls.Load() != 2 {
		t.Fatal("stale credential cache reused")
	}
	if got := <-seen; got != updated {
		t.Fatal("new request did not use current credentials")
	}
}

func TestPrewarmPOSTSelectedVersion(t *testing.T) {
	var denied atomic.Bool
	h := prewarmFixture(t, true, true, &denied)
	var selected atomic.Value
	h.pickDownload = func(ctx context.Context, pc, cookie, ua string) (string, error) {
		selected.Store(pc)
		return "https://cdn.115.com/file", nil
	}
	r := httptest.NewRequest("POST", "/emby/Items/movie/PlaybackInfo?api_key=token", strings.NewReader(`{"MediaSourceId":"src2"}`))
	r.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(httptest.NewRecorder(), r)
	awaitPrewarm(t, h)
	if selected.Load() != "xyz123" {
		t.Fatal("wrong selected version", selected.Load())
	}
}
