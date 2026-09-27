package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/service"
)

func TestCached302StillAuthorizesAndInvalidatesChangedCredentials(t *testing.T) {
	accounts, id := proxyTestAccounts(t)
	denied := false
	authorizations, downloads := 0, 0
	sourcePath := "http://nas/api/?share_code=abc&receive_code=pass&id=1"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/emby/Users/viewer":
			authorizations++
			if denied {
				w.WriteHeader(401)
				return
			}
			fmt.Fprint(w, `{"Id":"viewer","Policy":{"EnableMediaPlayback":true}}`)
		case "/emby/Users/viewer/Items/movie":
			fmt.Fprint(w, `{"Id":"movie","PlayAccess":"Full"}`)
		case "/emby/Items/movie/PlaybackInfo":
			fmt.Fprintf(w, `{"MediaSources":[{"Id":"src","Path":%q}]}`, sourcePath)
		default:
			w.WriteHeader(404)
		}
	}))
	defer upstream.Close()
	logs := service.NewEmbyProxyLogs(&service.GenerationLogs{})
	h := NewEmbyProxyHandler(service.EmbyProxyConfig{URL: upstream.URL, AccountID: id, DirectLinkCacheMinutes: 9, Debug: true}, accounts, NewShareAPI(func() string { return "" }), logs).(*embyProxyHandler)
	if err := h.identities.Remember(upstream.URL, "token", "viewer"); err != nil {
		t.Fatal(err)
	}
	h.download = func(context.Context, *p115.ShareInfo, string, string, string) (string, error) {
		downloads++
		return "https://cdn.115.com/v", nil
	}
	call := func(ua string) int {
		r := httptest.NewRequest("GET", "/cinema115/play?ItemId=movie&api_key=token", nil)
		r.Header.Set("User-Agent", ua)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	for i := 0; i < 2; i++ {
		if code := call("player"); code != 302 {
			t.Fatal(code)
		}
	}
	if downloads != 1 || authorizations != 2 {
		t.Fatal("cache bypassed auth or did not reuse URL")
	}
	if len(logs.Snapshot()) != 2 {
		t.Fatal("playback produced more than one row per request")
	}
	for _, entry := range logs.Snapshot() {
		if entry.Details == nil || entry.Details.URL != "https://cdn.115.com/v" || !strings.Contains(entry.Details.RequestHeaders, "player") {
			t.Fatal("missing URL/headers on cache miss or hit")
		}
	}
	if h.DirectLinkStatus().Entries[0].URL != "https://cdn.115.com/v" {
		t.Fatal("admin cache link differs from response")
	}
	sourcePath = "http://other-tool:9527/s/abc_pass_1.mp4?/movie.mp4"
	if code := call("player"); code != 302 || downloads != 1 {
		t.Fatal("路径式 STRM 未与普通参数链接共用缓存", code)
	}
	denied = true
	if code := call("player"); code != 401 || downloads != 1 {
		t.Fatal("cached unauthorized playback")
	}
	denied = false
	if code := call("other-player"); code != 302 || downloads != 2 {
		t.Fatal("UA not isolated")
	}
	cookie := "UID=123_A; SEID=updated"
	if err := accounts.Update(id, 2, "播放账号", &cookie); err != nil {
		t.Fatal(err)
	}
	if code := call("player"); code != 302 || downloads != 3 {
		t.Fatal("changed credentials reused cache")
	}
	h.ClearDirectLinks("")
	if code := call("player"); code != 302 || downloads != 4 {
		t.Fatal("clear ineffective")
	}
	cookie = ""
	if err := accounts.Update(id, 3, "播放账号", &cookie); err != nil {
		t.Fatal(err)
	}
	if code := call("player"); code != 503 || downloads != 4 {
		t.Fatal("empty credentials reused cache")
	}
}
