package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"strm115/backend/internal/service"
)

func TestThirdPartyPlaybackAuthorizationUAAndCache(t *testing.T) {
	accounts, id := proxyTestAccounts(t)
	calls := 0
	fail := false
	third := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.UserAgent() == "" || r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" || r.Header.Get("X-Emby-Token") != "" {
			t.Error("missing UA or leaked credentials")
		}
		if r.Header.Get("Range") != "bytes=0-0" {
			t.Error("unbounded request")
		}
		if fail {
			w.WriteHeader(500)
			return
		}
		w.Header().Set("Location", "https://cdn.115cdn.net/video?sign=test")
		w.WriteHeader(302)
	}))
	defer third.Close()
	source := third.URL + "/api/redirect115/abc123/%E7%94%B5%E5%BD%B1.mkv"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/emby/Users/AuthenticateByName":
			fmt.Fprint(w, `{"AccessToken":"viewer-token","User":{"Id":"user1"},"SessionInfo":{"UserId":"user1"}}`)
		case "/emby/Users/user1":
			fmt.Fprint(w, `{"Id":"user1","Name":"viewer","Policy":{"EnableMediaPlayback":true}}`)
		case "/emby/Users/user1/Items/denied":
			w.WriteHeader(403)
		case "/emby/Users/user1/Items/movie":
			fmt.Fprint(w, `{"Id":"movie","PlayAccess":"Full"}`)
		case "/emby/Items/movie/PlaybackInfo":
			json.NewEncoder(w).Encode(map[string]any{"MediaSources": []any{map[string]any{"Id": "source1", "Path": source, "SupportsDirectPlay": true, "SupportsDirectStream": true}}})
		default:
			w.WriteHeader(418)
		}
	}))
	defer upstream.Close()
	logs := service.NewEmbyProxyLogs(&service.GenerationLogs{})
	h := NewEmbyProxyHandler(service.EmbyProxyConfig{URL: upstream.URL, AccountID: id, ThirdPartyURL: third.URL, DirectLinkCacheMinutes: 9}, accounts, NewShareAPI(func() string { return "" }), logs).(*embyProxyHandler)
	call := func(method, path, ua string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, nil)
		r.Header.Set("User-Agent", ua)
		r.Header.Set("Cookie", "player-private")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	call("POST", "/emby/Users/AuthenticateByName", "player")
	if w := call("GET", "/emby/Items/movie/PlaybackInfo?api_key=viewer-token", "player"); w.Code != 200 || !strings.Contains(w.Body.String(), "/cinema115/play?") {
		t.Fatal("not rewritten", w.Code, w.Body.String())
	}
	if calls != 0 {
		t.Fatal("prefetched before authorization")
	}
	for _, p := range []string{"/cinema115/play?ItemId=denied&api_key=viewer-token", "/cinema115/play?ItemId=movie&api_key=invalid"} {
		if w := call("GET", p, "player"); w.Code < 400 {
			t.Fatal("unauthorized request accepted")
		}
	}
	if calls != 0 {
		t.Fatal("unauthorized request reached third party")
	}
	path := "/emby/videos/movie/original.mkv?MediaSourceId=source1&api_key=viewer-token"
	for _, method := range []string{"GET", "HEAD"} {
		w := call(method, path, "player")
		if w.Code != 302 || w.Body.Len() != 0 || w.Header().Get("Location") == "" {
			t.Fatal("original route failed", w.Code, w.Body.String())
		}
	}
	if calls != 1 {
		t.Fatal("cache not reused", calls)
	}
	call("GET", path, "different-player")
	if calls != 2 {
		t.Fatal("UA cache not isolated")
	}
	if w := call("GET", path, ""); w.Code != 502 || calls != 2 {
		t.Fatal("empty UA reached upstream")
	}
	entries := h.DirectLinkStatus().Entries
	if len(entries) != 2 || entries[0].Mode != "第三方接口取链" || !strings.Contains(entries[0].Account, "由第三方管理") {
		t.Fatal("wrong ownership report")
	}
	fail = true
	for i := 0; i < 2; i++ {
		if w := call("GET", path, "failed-player"); w.Code != 502 {
			t.Fatal(w.Code)
		}
	}
	if calls != 4 {
		t.Fatal("failure cached")
	}
}

func TestThirdPartyRejectsOtherOriginsAndRedirects(t *testing.T) {
	h := NewEmbyProxyHandler(service.EmbyProxyConfig{URL: "http://emby:8096", ThirdPartyURL: "http://nas:8115"}, nil, NewShareAPI(func() string { return "" })).(*embyProxyHandler)
	for _, raw := range []string{"http://other:8115/api/redirect115/abc/movie.mkv", "http://nas:8115/api/redirect115/%61bc/movie.mkv", "http://nas:8115/api/redirect115/abc/a%2fb", "http://nas:8115/api/redirect115/abc/movie?token=x", "http://nas:8115/private", "http://u:p@nas:8115/api/redirect115/abc"} {
		if _, ok := h.thirdPartyURL(raw); ok {
			t.Fatal("accepted unsafe endpoint", raw)
		}
	}
	for _, raw := range []string{"http://nas:8115/api/redirect115/abc", "http://nas:8115/api/redirect115/abc/%E7%94%B5%E5%BD%B1.mkv"} {
		if _, ok := h.thirdPartyURL(raw); !ok {
			t.Fatal("rejected supported endpoint")
		}
	}
	for _, location := range []string{"http://127.0.0.1/private", "https://evil.example/video", "/api/redirect115/abc"} {
		calls := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			w.Header().Set("Location", location)
			w.WriteHeader(302)
		}))
		h.config.ThirdPartyURL = server.URL
		target, _ := url.Parse(server.URL + "/api/redirect115/abc")
		if _, err := h.thirdPartyLink(context.Background(), target, "player"); err == nil {
			t.Error("unsafe redirect accepted")
		}
		if calls > 3 {
			t.Error("unbounded redirect chain")
		}
		server.Close()
	}
}
