package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"strm115/backend/internal/client/p115"
	"testing"

	"strm115/backend/internal/service"
)

func TestThirdPartyPlaybackAuthorizationUAAndCache(t *testing.T) {
	for _, path := range []string{"/api/redirect115/abc123/%E7%94%B5%E5%BD%B1.mkv", "/play115/abc123/3432417343848548293/" + strings.Repeat("a", 40) + "/589916580/%E7%94%B5%E5%BD%B1.mkv", "/api/drive115/abc123/3432417343848548293/" + strings.Repeat("a", 40) + "/589916580/%E7%94%B5%E5%BD%B1.mkv"} {
		t.Run(path, func(t *testing.T) { testThirdPartyPlaybackAuthorizationUAAndCache(t, path) })
	}
}

func testThirdPartyPlaybackAuthorizationUAAndCache(t *testing.T, sourcePath string) {
	accounts, id := proxyTestAccounts(t)
	calls := 0
	fail := false
	third := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("STRM host must never be contacted")
		w.WriteHeader(500)
	}))
	defer third.Close()
	source := third.URL + sourcePath
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
	h := NewEmbyProxyHandler(service.EmbyProxyConfig{URL: upstream.URL, AccountID: id, DirectLinkCacheMinutes: 9}, accounts, NewShareAPI(func() string { return "" }), logs).(*embyProxyHandler)
	h.pickDownload = func(_ context.Context, pc, cookie, ua string) (string, error) {
		calls++
		if pc != "abc123" || cookie != "UID=123_A; SEID=private" {
			t.Fatal("wrong source credentials or pickcode")
		}
		if fail {
			return "", fmt.Errorf("source unavailable")
		}
		if ua == "" {
			ua = p115.PlaybackUserAgent
		}
		return "https://cdn.115cdn.net/video?sign=test", nil
	}

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

	entries := h.DirectLinkStatus().Entries
	if len(entries) != 2 || entries[0].Mode != "源账号网盘直取" || !strings.Contains(entries[0].Account, id) {
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

func TestPickCodeRecognition(t *testing.T) {
	const hash = "e63d59350688066658c5d8cf2748b97abb684251"
	for _, raw := range []string{
		"http://nas:8115/api/redirect115/abc123", "https://another:443/api/redirect115/abc123/电影.mkv", "http://nas/api/redirect115/abc123/", "http://nas/api/redirect115/abc123/%E7%94%B5%E5%BD%B1.mkv",
		"http://nas/api/redirect115/abc123/" + hash,
		"http://nas/api/redirect115/abc123/" + hash + "/",
		"http://nas/api/redirect115/abc123/" + hash + "/[标题] 日本語 文件.mkv",
		"http://nas/api/redirect115/abc123/" + strings.ToUpper(hash) + "/%E7%94%B5%E5%BD%B1.mkv",
	} {
		if pc, ok := parsePickCodeSTRM(raw); !ok || pc != "abc123" {
			t.Fatal("supported link rejected", raw)
		}
	}
	for _, raw := range []string{
		"http://nas/api/redirect115/abc", "http://nas/api/redirect115/%61bc123", "http://nas/api/redirect115/abc123/a%2fb", "http://nas/api/redirect115/abc123/..", "http://nas/api/redirect115/abc123?token=x", "http://nas/api/redirect115/abc123#x", "http://u:p@nas/api/redirect115/abc123", "file:///api/redirect115/abc123", "http://nas/api/redirect115/abc123/a/b",
		"http://nas/api/redirect115/abc123/" + hash[:39] + "/movie.mkv",
		"http://nas/api/redirect115/abc123/" + hash + "z/movie.mkv",
		"http://nas/api/redirect115/abc123/" + hash + "/a%2fb",
		"http://nas/api/redirect115/abc123/" + hash + "/a%5cb",
		"http://nas/api/redirect115/abc123/" + hash + "/%2e%2e",
		"http://nas/api/redirect115/abc123/" + hash + "/a%0ab",
		"http://nas/api/redirect115/abc123/" + hash + "/a%00b",
		"http://nas/api/redirect115/abc123/" + hash + "/a/b",
		"http://nas/api/redirect115/abc123/" + hash + "/movie.mkv?token=x",
	} {
		if _, ok := parsePickCodeSTRM(raw); ok {
			t.Fatal("invalid link accepted", raw)
		}
	}
}
