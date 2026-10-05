package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/service"
)

func TestDownloadRoutes(t *testing.T) {
	for _, p := range []string{"/Items/123/Download", "/emby/items/123/download", "/Items/abc/Download/"} {
		if downloadItem(p) == "" {
			t.Fatal("download missed", p)
		}
	}
	for _, p := range []string{"/Items/123/Download/other", "/x/Items/123/Download", "/Items/123/PlaybackInfo", "/Items/123/Download.zip"} {
		if downloadItem(p) != "" {
			t.Fatal("unrelated route captured", p)
		}
	}
}

func TestDownloadsAuthorizeAndReuseCorrectAccount(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprintf("legacy=%t", legacy), func(t *testing.T) { checkDownloadsAuthorizeAndReuseCorrectAccount(t, legacy) })
	}
}

func checkDownloadsAuthorizeAndReuseCorrectAccount(t *testing.T, legacy bool) {
	sourcePath := "http://nas/api/share115/abc/0123456789ABCDEF0123456789ABCDEF01234567/pass/123"
	if legacy {
		sourcePath = "http://nas/api/?share_code=abc&id=123&receive_code=pass&filename=movie.mkv"
	}
	for _, mode := range []string{"source", "friend", "third"} {
		t.Run(mode, func(t *testing.T) {
			accounts, sourceAccount := proxyTestAccounts(t)
			const uid = "8be403a10974486c94a987b8036c8484"
			const targetURL = "https://cdn.115cdn.net/file?sign=test"
			downloadAllowed := true
			playbackAllowed := true
			missingPolicy := false
			itemAllowed := true
			multi := false
			userChecks, thirdCalls, shareCalls, fallbackCalls := 0, 0, 0, 0
			third := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				thirdCalls++
				t.Error("STRM host contacted")
				if r.UserAgent() != "player-UA" && r.UserAgent() != "download-UA" {
					t.Error("wrong download UA")
				}
				if r.Header.Get("Cookie") != "" || r.Header.Get("X-Emby-Token") != "" {
					t.Error("credentials leaked")
				}
				w.Header().Set("Location", targetURL)
				w.WriteHeader(302)
			}))
			defer third.Close()
			source := sourcePath
			if mode == "third" {
				source = third.URL + "/api/redirect115/abc123/movie.mkv"
			}
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/emby/Users/AuthenticateByName":
					fmt.Fprintf(w, `{"AccessToken":"viewer","User":{"Id":"%s"},"SessionInfo":{"UserId":"%s"}}`, uid, uid)
				case "/emby/Users/" + uid:
					userChecks++
					policy := map[string]any{"EnableMediaPlayback": playbackAllowed}
					if !missingPolicy {
						policy["EnableContentDownloading"] = downloadAllowed
					}
					json.NewEncoder(w).Encode(map[string]any{"Id": uid, "Name": "viewer", "Policy": policy})
				case "/emby/Users/" + uid + "/Items/movie":
					sources := []any{map[string]any{"Id": "src", "Path": source, "SupportsDirectPlay": true}}
					if multi {
						sources = append(sources, map[string]any{"Id": "other", "Path": source})
					}
					playAccess := "Full"
					if !playbackAllowed {
						playAccess = "None"
					}
					json.NewEncoder(w).Encode(map[string]any{"Id": "movie", "PlayAccess": playAccess, "CanDownload": itemAllowed, "MediaSources": sources})
				case "/emby/Items/movie/PlaybackInfo":
					if !playbackAllowed {
						t.Error("download called playback negotiation")
						w.WriteHeader(403)
						return
					}
					sources := []any{map[string]any{"Id": "src", "Path": source, "SupportsDirectPlay": true}}
					if multi {
						sources = append(sources, map[string]any{"Id": "other", "Path": source})
					}
					json.NewEncoder(w).Encode(map[string]any{"MediaSources": sources})
				case "/emby/Items/movie/Download":
					fallbackCalls++
					w.WriteHeader(206)
					fmt.Fprint(w, "native")
				default:
					w.WriteHeader(403)
				}
			}))
			defer upstream.Close()
			fake := &proxyCacheFake{t: t, directURL: targetURL}
			logs := service.NewEmbyProxyLogs(&service.GenerationLogs{})
			var cache *service.PlaybackCache
			if mode == "friend" {
				target, _ := accounts.Add("friend")
				cookie := "UID=456_B; SEID=friend-private"
				if err := accounts.Update(target, 1, "friend", &cookie); err != nil {
					t.Fatal(err)
				}
				store, err := service.NewSessionStore(filepath.Join(t.TempDir(), "cache.enc"), "secret")
				if err != nil {
					t.Fatal(err)
				}
				cache, err = service.NewPlaybackCache(context.Background(), store, accounts, fake, logs)
				if err != nil {
					t.Fatal(err)
				}
				defer cache.Close()
				if err := cache.ApplyBindings(upstream.URL, []service.PlaybackBinding{{UserID: uid, AccountID: target}}); err != nil {
					t.Fatal(err)
				}
			}
			h := NewEmbyCachedProxyHandler(service.EmbyProxyConfig{URL: upstream.URL, AccountID: sourceAccount, DirectLinkCacheMinutes: 9, Debug: true}, accounts, NewShareAPI(func() string { return "" }), cache, logs).(*embyProxyHandler)
			h.download = func(_ context.Context, _ *p115.ShareInfo, _ string, cookie, ua string) (string, error) {
				shareCalls++
				if mode != "source" || cookie != "UID=123_A; SEID=private" || (ua != "player-UA" && ua != "download-UA") {
					t.Error("wrong account or UA")
				}
				return targetURL, nil
			}
			h.pickDownload = func(_ context.Context, pc, cookie, ua string) (string, error) {
				shareCalls++
				if mode != "third" || pc != "abc123" || cookie != "UID=123_A; SEID=private" || (ua != "player-UA" && ua != "download-UA") {
					t.Fatal("wrong pickcode source or UA")
				}
				return targetURL, nil
			}

			call := func(method, path, ua string) *httptest.ResponseRecorder {
				r := httptest.NewRequest(method, path, nil)
				r.Header.Set("User-Agent", ua)
				r.Header.Set("Range", "bytes=100-")
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				return w
			}
			call("POST", "/emby/Users/AuthenticateByName", "player-UA")
			path := "/emby/Items/movie/Download?MediaSourceId=src&X-Emby-Token=viewer"
			if w := call("GET", "/Items/movie/Download?X-Emby-Token=bad", "player-UA"); w.Code != 401 {
				t.Fatal("invalid token accepted", w.Code)
			}
			if w := call("POST", path, "player-UA"); w.Code != 405 {
				t.Fatal("write method accepted")
			}
			if thirdCalls+shareCalls != 0 {
				t.Fatal("unauthorized cloud lookup")
			}
			// Playback cache must not bypass download authorization.
			if w := call("GET", "/cinema115/play?ItemId=movie&MediaSourceId=src&api_key=viewer", "player-UA"); w.Code != 302 {
				t.Fatal("playback setup failed", w.Code, w.Body.String())
			}
			downloadAllowed = false
			if w := call("GET", path, "player-UA"); w.Code != 403 {
				t.Fatal("download denied policy bypassed", w.Code)
			}
			missingPolicy = true
			if w := call("GET", path, "player-UA"); w.Code != 403 {
				t.Fatal("missing permission accepted")
			}
			missingPolicy = false
			downloadAllowed = true
			itemAllowed = false
			if w := call("GET", path, "player-UA"); w.Code != 403 {
				t.Fatal("item prohibition ignored")
			}
			itemAllowed = true
			playbackAllowed = false
			if w := call("GET", "/cinema115/play?ItemId=movie&MediaSourceId=src&api_key=viewer", "player-UA"); w.Code != 403 {
				t.Fatal("playback permission bypassed by cached download", w.Code)
			}
			for _, method := range []string{"GET", "HEAD"} {
				w := call(method, path, "player-UA")
				if w.Code != 302 || w.Header().Get("Location") != targetURL || w.Body.Len() != 0 || w.Header().Get("Cache-Control") != "no-store" {
					t.Fatal("download redirect failed", w.Code, w.Body.String())
				}
			}
			if userChecks < 6 {
				t.Fatal("cache bypassed permission checks")
			}
			if mode != "friend" {
				if thirdCalls+shareCalls != 1 {
					t.Fatal("cache not reused")
				}
				if w := call("GET", path, "download-UA"); w.Code != 302 {
					t.Fatal("downloader UA failed")
				}
				if thirdCalls+shareCalls != 2 {
					t.Fatal("different UA reused wrong cache")
				}
			}
			multi = true
			if w := call("GET", "/Items/movie/Download?X-Emby-Token=viewer", "player-UA"); w.Code != 409 {
				t.Fatal("ambiguous media source accepted")
			}
			multi = false
			found := false
			for _, e := range logs.Snapshot() {
				if strings.HasPrefix(e.Message, "下载取链结果：") {
					found = true
					if e.Details == nil || e.Details.Path != "/Items/{Id}/Download" || e.Details.URL != targetURL {
						t.Fatal("download details missing")
					}
				}
			}
			if !found {
				t.Fatal("download summary missing")
			}
			source = "/media/local.mkv"
			if w := call("GET", path, "player-UA"); w.Code != 206 || w.Body.String() != "native" || fallbackCalls != 1 {
				t.Fatal("native download fallback broken", w.Code, w.Body.String())
			}
		})
	}
}
