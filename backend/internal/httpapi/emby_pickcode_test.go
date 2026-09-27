package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"strm115/backend/internal/service"
)

func TestPickCodeFriendPlaybackDownloadAndCache(t *testing.T) {
	const userID = "8be403a10974486c94a987b8036c8484"
	accounts, sourceID := proxyTestAccounts(t)
	targetID, err := accounts.Add("friend")
	if err != nil {
		t.Fatal(err)
	}
	cookie := "UID=456_B; SEID=friend-private"
	if err := accounts.Update(targetID, 1, "friend", &cookie); err != nil {
		t.Fatal(err)
	}
	if err := accounts.UpdatePlaybackDirectory(targetID, 2, "0"); err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/emby/Users/AuthenticateByName":
			fmt.Fprintf(w, `{"AccessToken":"viewer","User":{"Id":"%s"},"SessionInfo":{"UserId":"%s"}}`, userID, userID)
		case "/emby/Users/" + userID:
			fmt.Fprintf(w, `{"Id":"%s","Policy":{"EnableMediaPlayback":true,"EnableContentDownloading":true}}`, userID)
		case "/emby/Users/" + userID + "/Items/movie":
			fmt.Fprint(w, `{"Id":"movie","PlayAccess":"Full","CanDownload":true}`)
		case "/emby/Items/movie/PlaybackInfo":
			fmt.Fprint(w, `{"MediaSources":[{"Id":"src","Path":"http://unreachable.invalid:8115/api/redirect115/abc123"}]}`)
		default:
			w.WriteHeader(403)
		}
	}))
	defer upstream.Close()
	store, err := service.NewSessionStore(filepath.Join(t.TempDir(), "cache.enc"), "secret")
	if err != nil {
		t.Fatal(err)
	}
	fake := &proxyCacheFake{t: t, directURL: "https://cdn.115.com/share-that-must-not-be-used"}
	logs := service.NewEmbyProxyLogs(&service.GenerationLogs{})
	cache, err := service.NewPlaybackCache(context.Background(), store, accounts, fake, logs)
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()
	if err := cache.ApplyBindings(upstream.URL, []service.PlaybackBinding{{UserID: userID, AccountID: targetID}}); err != nil {
		t.Fatal(err)
	}
	h := NewEmbyCachedProxyHandler(service.EmbyProxyConfig{URL: upstream.URL, AccountID: sourceID, DirectLinkCacheMinutes: 9}, accounts, NewShareAPI(func() string { return "" }), cache, logs).(*embyProxyHandler)
	h.pickDownload = func(context.Context, string, string, string) (string, error) {
		t.Fatal("source direct link returned to friend")
		return "", nil
	}
	call := func(method, path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, nil)
		r.Header.Set("User-Agent", "player-UA")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	call("POST", "/emby/Users/AuthenticateByName")
	path := "/cinema115/play?ItemId=movie&MediaSourceId=src&api_key=viewer"
	if w := call("GET", "/cinema115/play?ItemId=movie&api_key=invalid"); w.Code != 401 {
		t.Fatal("invalid token accepted")
	}
	if fake.pickReads != 0 || fake.uploads != 0 {
		t.Fatal("cloud access before authorization")
	}
	for _, p := range []string{path, "/Items/movie/Download?api_key=viewer", path} {
		w := call("GET", p)
		if w.Code != 302 || w.Header().Get("Location") != "https://cdn.115.com/friend?private-link" {
			t.Fatal("wrong friend redirect", w.Code, w.Body.String())
		}
	}
	if fake.shareReads != 0 || fake.pickReads != 1 || fake.uploads != 1 || fake.links != 1 {
		t.Fatal("wrong lookup/cache flow", fake)
	}
	h.ClearDirectLinks("")
	if w := call("HEAD", path); w.Code != 302 {
		t.Fatal(w.Code, w.Body.String())
	}
	if fake.pickReads != 1 || fake.uploads != 1 || fake.links != 2 || fake.shareReads != 0 {
		t.Fatal("file cache not reused")
	}
	empty := ""
	if err := accounts.Update(targetID, 3, "friend", &empty); err != nil {
		t.Fatal(err)
	}
	if w := call("GET", path); w.Code != 503 {
		t.Fatal("cached link bypassed deleted credentials", w.Code)
	}
	if fake.links != 2 {
		t.Fatal("unexpected fallback")
	}
}
