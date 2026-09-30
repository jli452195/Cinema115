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
		t.Run("abc123"+suffix, func(t *testing.T) { testPickCodeFriendPlaybackDownloadAndCache(t, suffix, false) })
	}
}

func TestPickCodeFriendPrewarm(t *testing.T) {
	testPickCodeFriendPlaybackDownloadAndCache(t, "", true)
}

func TestPickCodeFilePreparationIndependentOfLinkPrewarm(t *testing.T) {
	testPickCodeFriendPlaybackDownloadAndCache(t, "", false, true)
}

func testPickCodeFriendPlaybackDownloadAndCache(t *testing.T, suffix string, prewarm bool, prepare ...bool) {
	prepareFiles := len(prepare) > 0 && prepare[0]
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
	pickCode := "abc123"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/emby/Users/AuthenticateByName":
			fmt.Fprintf(w, `{"AccessToken":"viewer","User":{"Id":"%s"},"SessionInfo":{"UserId":"%s"}}`, userID, userID)
		case "/emby/Users/" + userID:
			fmt.Fprintf(w, `{"Id":"%s","Policy":{"EnableMediaPlayback":true,"EnableContentDownloading":true}}`, userID)
		case "/emby/Users/" + userID + "/Items/movie":
			fmt.Fprint(w, `{"Id":"movie","PlayAccess":"Full","CanDownload":true}`)
		case "/emby/Items/movie/PlaybackInfo":
			if r.URL.Query().Get("IsPlayback") != "false" || r.URL.Query().Get("AutoOpenLiveStream") != "false" {
				t.Error("pickcode source lookup must not open the retired STRM tool")
				w.WriteHeader(500)
				return
			}
			// The path SHA1 deliberately differs from the source account metadata.
			// It must not replace the trusted source SHA1 or trigger a host request.
			fmt.Fprintf(w, `{"MediaSources":[{"Id":"src","SupportsDirectPlay":true,"Path":"http://unreachable.invalid:8115/api/redirect115/%s%s"}]}`, pickCode, suffix)
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
	h := NewEmbyCachedProxyHandler(service.EmbyProxyConfig{URL: upstream.URL, AccountID: sourceID, DirectLinkCacheMinutes: 9, Prewarm: prewarm, PrepareFiles: prepareFiles, Debug: prepareFiles}, accounts, NewShareAPI(func() string { return "" }), cache, logs).(*embyProxyHandler)
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
	if prewarm || prepareFiles {
		if w := call("GET", "/emby/Items/movie/PlaybackInfo?api_key=viewer"); w.Code != 200 {
			t.Fatal(w.Code)
		}
		awaitPrewarm(t, h)
		if fake.uploads != 1 {
			t.Fatal("friend file not prepared")
		}
		if prepareFiles {
			foundTimeline := false
			for _, entry := range logs.Snapshot() {
				if entry.Details == nil || !strings.Contains(entry.Details.Steps, "提前准备秒传文件开始") {
					continue
				}
				steps := entry.Details.Steps
				if !strings.Contains(steps, "预热文件准备=") || strings.Contains(steps, "等待同源预热=") {
					t.Fatal("preparation mislabeled as waiting", steps)
				}
				foundTimeline = true
			}
			if !foundTimeline {
				t.Fatal("missing preparation timeline")
			}
			if fake.links != 0 || len(h.directLinks.Status().Entries) != 0 {
				t.Fatal("file preparation fetched/cached a playback URL")
			}
			req := httptest.NewRequest("GET", "/emby/Items/movie/PlaybackInfo?api_key=viewer", nil)
			req.Header.Set("User-Agent", "other-detail-UA")
			h.ServeHTTP(httptest.NewRecorder(), req)
			awaitPrewarm(t, h)
			if fake.links != 0 || fake.uploads != 1 || fake.pickReads != 1 {
				t.Fatal("UA change repeated preparation")
			}
		}
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
	if err := accounts.Update(sourceID, 2, "主账号", &empty); err != nil {
		t.Fatal(err)
	}
	pickCode, suffix = "otherpc", "/"+strings.Repeat("a", 40)+"/other-name.mkv"
	h.ClearDirectLinks("")
	if w := call("GET", path); w.Code != 302 {
		t.Fatal("external SHA1 failed without primary credentials", w.Code, w.Body.String())
	}
	if fake.pickReads != 1 || fake.uploads != 1 || fake.links != 3 {
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
