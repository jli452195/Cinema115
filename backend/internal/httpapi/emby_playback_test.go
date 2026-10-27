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

const proxyPlaybackTestUser = "8be403a10974486c94a987b8036c8484"

func proxyPlaybackFixture(t *testing.T, denied *atomic.Bool) *embyProxyHandler {
	t.Helper()
	accounts, id := proxyTestAccounts(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/emby/Users/" + proxyPlaybackTestUser:
			if denied.Load() {
				w.WriteHeader(403)
				return
			}
			fmt.Fprintf(w, `{"Id":%q,"Policy":{"EnableMediaPlayback":true}}`, proxyPlaybackTestUser)
		case "/emby/Users/" + proxyPlaybackTestUser + "/Items/movie":
			fmt.Fprint(w, `{"Id":"movie","PlayAccess":"Full"}`)
		case "/emby/Items/movie/PlaybackInfo":
			fmt.Fprint(w, `{"MediaSources":[{"Id":"src","SupportsDirectPlay":true,"Path":"http://nas/api/redirect115/abc123"}]}`)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(upstream.Close)
	h := NewEmbyProxyHandler(service.EmbyProxyConfig{URL: upstream.URL, AccountID: id, DirectLinkCacheMinutes: 9}, accounts, NewShareAPI(func() string { return "" }), service.NewEmbyProxyLogs(&service.GenerationLogs{})).(*embyProxyHandler)
	t.Cleanup(h.Close)
	if err := h.identities.Remember(upstream.URL, "token", proxyPlaybackTestUser); err != nil {
		t.Fatal(err)
	}
	return h
}

func proxyPlaybackCall(h *embyProxyHandler, path, ua string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", path, nil)
	r.Header.Set("User-Agent", ua)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestPlaybackPinsCredentialsDuringRequest(t *testing.T) {
	var denied atomic.Bool
	h := proxyPlaybackFixture(t, &denied)
	old, err := h.accounts.Get(h.config.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
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
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- proxyPlaybackCall(h, "/cinema115/play?ItemId=movie&MediaSourceId=src&api_key=token", "page")
	}()
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
	select {
	case w := <-done:
		if w.Code != 302 {
			t.Fatal(w.Code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("playback did not finish")
	}
	if got := <-seen; got != old.Cookie {
		t.Fatal("task credentials changed")
	}
	if w := proxyPlaybackCall(h, "/cinema115/play?ItemId=movie&MediaSourceId=src&api_key=token", "page"); w.Code != 302 {
		t.Fatal(w.Code)
	}
	if calls.Load() != 2 {
		t.Fatal("stale credential cache reused")
	}
	if got := <-seen; got != updated {
		t.Fatal("new request did not use current credentials")
	}
}

func TestPlaybackTimingFollowsDebugSwitch(t *testing.T) {
	for _, debug := range []bool{false, true} {
		t.Run(fmt.Sprint(debug), func(t *testing.T) {
			var denied atomic.Bool
			h := proxyPlaybackFixture(t, &denied)
			h.config.Debug = debug
			h.pickDownload = func(context.Context, string, string, string) (string, error) { return "https://cdn.115.com/file", nil }
			play := "/cinema115/play?ItemId=movie&MediaSourceId=src&api_key=token"
			if w := proxyPlaybackCall(h, play, "player"); w.Code != 302 {
				t.Fatal(w.Code)
			}
			steps := h.logs.Snapshot()[0].Details.Steps
			if !strings.Contains(steps, "播放取链结果") || !strings.Contains(steps, "取链账号") {
				t.Fatal("missing playback summary", steps)
			}
			for _, marker := range []string{"[+", "Emby 请求计时", "播放入口分段耗时"} {
				if strings.Contains(steps, marker) != debug {
					t.Fatal("wrong debug timing", marker, steps)
				}
			}
			denied.Store(true)
			if w := proxyPlaybackCall(h, play, "player"); w.Code != 403 {
				t.Fatal("authorization bypassed")
			}
			rows := h.logs.Snapshot()
			if rows[0].Level != "ERROR" || !strings.Contains(rows[0].Details.Steps, "HTTP 403") {
				t.Fatal("error hidden by debug switch")
			}
		})
	}
}

func TestPlaybackTimingIncludesSlowEmbyPhase(t *testing.T) {
	var denied atomic.Bool
	h := proxyPlaybackFixture(t, &denied)
	h.config.Debug = true
	transport := h.http.Transport
	entered, release := make(chan struct{}), make(chan struct{})
	h.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/PlaybackInfo") {
			close(entered)
			select {
			case <-release:
			case <-r.Context().Done():
				return nil, r.Context().Err()
			}
		}
		return transport.RoundTrip(r)
	})
	h.pickDownload = func(context.Context, string, string, string) (string, error) {
		return "https://cdn.115.com/file", nil
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- proxyPlaybackCall(h, "/cinema115/play?ItemId=movie&MediaSourceId=src&api_key=token", "player")
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("media query not reached")
	}
	// Simulate a slow upstream separately from the fast 115 callback.
	time.Sleep(30 * time.Millisecond)
	close(release)
	select {
	case w := <-done:
		if w.Code != 302 {
			t.Fatal(w.Code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("playback stuck")
	}
	steps := h.logs.Snapshot()[0].Details.Steps
	pos := strings.Index(steps, "Emby 媒体源确定=")
	var duration int64
	if pos < 0 {
		t.Fatal("missing media timing", steps)
	}
	if _, err := fmt.Sscanf(steps[pos:], "Emby 媒体源确定=%d ms", &duration); err != nil || duration < 30 {
		t.Fatal("upstream wait was not measured", duration, err)
	}
	if !strings.Contains(steps, "直链缓存等待及 115 取链=") || !strings.Contains(steps, "首字节=") {
		t.Fatal("incomplete timing")
	}
}
