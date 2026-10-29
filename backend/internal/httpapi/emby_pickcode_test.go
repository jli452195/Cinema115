package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"strm115/backend/internal/service"
)

func TestPickCodeFriendPlaybackDownloadAndCache(t *testing.T) {
	for _, suffix := range []string{"", "/movie.mkv", "/e63d59350688066658c5d8cf2748b97abb684251", "/e63d59350688066658c5d8cf2748b97abb684251/[标题] 日本語.mkv"} {
		t.Run("abc123"+suffix, func(t *testing.T) {
			testPickCodeFriendPlaybackDownloadAndCache(t, "/api/redirect115/abc123"+suffix)
		})
	}
	t.Run("play115", func(t *testing.T) {
		testPickCodeFriendPlaybackDownloadAndCache(t, "/play115/abc123/3432417343848548293/e63d59350688066658c5d8cf2748b97abb684251/589916580/external-name.mkv")
	})
	t.Run("drive115", func(t *testing.T) {
		testPickCodeFriendPlaybackDownloadAndCache(t, "/api/drive115/abc123/3432417343848548293/e63d59350688066658c5d8cf2748b97abb684251/589916580/native-name.mkv")
	})
}

func testPickCodeFriendPlaybackDownloadAndCache(t *testing.T, sourcePath string) {
	wantSourceReads := 1
	wantName, wantHash, wantSize := "movie", strings.Repeat("A", 40), int64(100)
	completePrefix := "/play115/"
	if strings.HasPrefix(sourcePath, "/api/drive115/") {
		completePrefix = "/api/drive115/"
	}
	if strings.HasPrefix(sourcePath, completePrefix) {
		claim, ok := parsePickCodeSTRMClaim("http://nas" + sourcePath)
		if !ok {
			t.Fatal("invalid play115 fixture")
		}
		wantSourceReads, wantName, wantHash, wantSize = 0, claim.Name, claim.SHA1, claim.Size
	}
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
			fmt.Fprintf(w, `{"Id":"movie","PlayAccess":"Full","CanDownload":true,"MediaSources":[{"Id":"src","Path":"http://unreachable.invalid:8115%s"}]}`, sourcePath)
		case "/emby/Items/movie/PlaybackInfo":
			wantFlag := "false" // Internal fallback queries remain metadata-only.
			if r.Header.Get("X-Cinema115-Emby-Proxy") == "1" {
				wantFlag = "" // Preserve omitted flags on the client negotiation.
			}
			if r.URL.Query().Get("IsPlayback") != wantFlag || r.URL.Query().Get("AutoOpenLiveStream") != wantFlag {
				t.Error("client negotiation or internal lookup flags changed")
				w.WriteHeader(500)
				return
			}
			// The path SHA1 deliberately differs from source metadata. play115
			// uses its declaration directly; redirect115 retains source fallback.
			fmt.Fprintf(w, `{"MediaSources":[{"Id":"src","SupportsDirectPlay":true,"Path":"http://unreachable.invalid:8115%s"}]}`, sourcePath)
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
	defer h.Close()
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
	if w := call("GET", "/emby/Items/movie/PlaybackInfo?api_key=viewer"); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if fake.pickReads != 0 || fake.uploads != 0 || fake.links != 0 || len(h.directLinks.Status().Entries) != 0 {
		t.Fatal("playback negotiation accessed 115 or prepared files")
	}
	for _, p := range []string{path, "/Items/movie/Download?api_key=viewer", path} {
		w := call("GET", p)
		if w.Code != 302 || w.Header().Get("Location") != "https://cdn.115.com/friend?private-link" {
			t.Fatal("wrong friend redirect", w.Code, w.Body.String())
		}
	}
	if fake.shareReads != 0 || fake.pickReads != wantSourceReads || fake.uploads != 1 || fake.links != 1 {
		t.Fatal("wrong lookup/cache flow", fake)
	}
	if len(fake.files) != 1 || fake.files[0].Name != wantName || fake.files[0].Size != wantSize || fake.files[0].SHA1 != wantHash {
		t.Fatal("wrong metadata for STRM format", fake.files)
	}
	h.ClearDirectLinks("")
	if w := call("HEAD", path); w.Code != 302 {
		t.Fatal(w.Code, w.Body.String())
	}
	if fake.pickReads != wantSourceReads || fake.uploads != 1 || fake.links != 2 || fake.shareReads != 0 {
		t.Fatal("file cache not reused")
	}
	empty := ""
	if err := accounts.Update(sourceID, 2, "主账号", &empty); err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(sourcePath, completePrefix) {
		sourcePath = completePrefix + "otherpc/999/" + wantHash + "/589916580/other-name.mkv"
	} else {
		sourcePath = "/api/redirect115/otherpc/" + strings.Repeat("a", 40) + "/other-name.mkv"
	}
	h.ClearDirectLinks("")
	if w := call("GET", path); w.Code != 302 {
		t.Fatal("external SHA1 failed without primary credentials", w.Code, w.Body.String())
	}
	if fake.pickReads != wantSourceReads || fake.uploads != 1 || fake.links != 3 {
		t.Fatal("external SHA1 did not skip primary metadata")
	}
	// A cached CDN URL may outlive a removed local copy. The same request must
	// refetch and recreate the mapping instead of emitting an avoidable 502.
	entries := cache.Status().Entries
	if len(entries) != 1 {
		t.Fatal("expected one target copy")
	}
	if err := cache.Forget(context.Background(), entries[0].Key); err != nil {
		t.Fatal(err)
	}
	if w := call("GET", path); w.Code != 302 {
		t.Fatal("stale direct link did not recover", w.Code, w.Body.String())
	}
	if fake.uploads != 1 || fake.links != 4 {
		t.Fatal("stale link recovery uploaded or failed to refresh")
	}
	if err := accounts.Update(targetID, 3, "friend", &empty); err != nil {
		t.Fatal(err)
	}
	if w := call("GET", path); w.Code != 503 {
		t.Fatal("cached link bypassed deleted credentials", w.Code)
	}
	if fake.links != 4 {
		t.Fatal("unexpected fallback")
	}
}
