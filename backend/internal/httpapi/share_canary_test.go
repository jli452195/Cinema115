package httpapi

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/service"
)

// Real loopback HTTP ingress; upstreams are isolated and cannot write to 115.
func TestShareCanaryLegacyAndNewHTTP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/os_windows/2.0/share/downurl" || r.URL.Query().Get("receive_code") != "fwgl" || r.URL.Query().Get("file_id") != "3532903049309717944" || r.Header.Get("Cookie") != "test-cookie" {
			t.Error("incorrect upstream request")
			w.WriteHeader(400)
			return
		}
		fmt.Fprint(w, `{"state":true,"data":{"url":"https://cdn.115.com/canary"}}`)
	}))
	defer upstream.Close()
	a := NewShareAPI(func() string { return "test-cookie" })
	a.Logs = &service.GenerationLogs{}
	a.Client.AppURL = upstream.URL
	a.MediaHTTP = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Range") != "bytes=0-3" || r.Header.Get("Cookie") != "" {
			t.Error("range lost or credential leaked")
		}
		body := io.MultiReader(mediaReadFunc(func([]byte) (int, error) {
			entries := a.Logs.Snapshot("ALL")
			if len(entries) == 0 || entries[0].Source != "分享播放" || !strings.Contains(entries[0].Message, "HTTP 206") {
				t.Error("share playback log did not appear before media body reading")
			}
			return 0, io.EOF
		}), strings.NewReader("test"))
		return &http.Response{StatusCode: 206, Header: http.Header{"Content-Range": {"bytes 0-3/100"}, "Content-Length": {"4"}}, Body: io.NopCloser(body)}, nil
	})}
	r := gin.New()
	a.Register(r, r.Group("/api/admin"))
	server := httptest.NewServer(r)
	defer server.Close()
	paths := []string{
		"/api/?share_code=sws8jb636j9&id=3532903049309717944&receive_code=fwgl&filename=movie.mp4",
		"/api/share115/sws8jb636j9/3C57FCFD33B9476B43C6BCED7D20B5887EFB2A4E/fwgl/3532903049309717944",
	}
	requests := 0
	for _, path := range paths {
		for _, method := range []string{"GET", "HEAD"} {
			req, err := http.NewRequest(method, server.URL+path, nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Range", "bytes=0-3")
			resp, err := server.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			body, readErr := io.ReadAll(resp.Body)
			resp.Body.Close()
			if readErr != nil || resp.StatusCode != 206 || resp.Header.Get("Content-Range") != "bytes 0-3/100" || (method == "HEAD" && len(body) != 0) || (method == "GET" && string(body) != "test") {
				t.Fatal(method, resp.StatusCode, readErr)
			}
			requests++
			entries := a.Logs.Snapshot("ALL")
			if len(entries) != requests || entries[0].Module != "STRM" || entries[0].Source != "分享播放" || entries[0].Level != "DEBUG" || !strings.Contains(entries[0].Message, method+" HTTP 206") {
				t.Fatal("new or legacy standalone share playback missing or duplicating logs")
			}
			if strings.Contains(entries[0].Message, "test-cookie") || strings.Contains(entries[0].Message, "fwgl") || strings.Contains(entries[0].Message, "https://") {
				t.Fatal("share playback log contains source credentials or signed URL")
			}
		}
	}
}

type canaryAccountsDrive struct {
	proxyCacheFake
	mu    sync.Mutex
	reads map[string]int
}

func (f *canaryAccountsDrive) DownloadInfoWithUA(_ context.Context, share *p115.ShareInfo, id, cookie, _ string) (*p115.DownloadInfo, error) {
	uid := p115.CookieUserID(cookie)
	if (uid != "456" && uid != "789") || share.Code != "abc" || share.Password != "pass" || id != "123" {
		return nil, fmt.Errorf("unexpected target identity")
	}
	f.mu.Lock()
	f.reads[uid]++
	f.mu.Unlock()
	return &p115.DownloadInfo{URL: "https://cdn.115.com/account" + uid}, nil
}

func TestShareCanaryMultiUserMultiAccount(t *testing.T) {
	accounts, primary := proxyTestAccounts(t)
	ids := make([]string, 2)
	for i, uid := range []string{"456", "789"} {
		id, err := accounts.Add("target" + uid)
		if err != nil {
			t.Fatal(err)
		}
		cookie := "UID=" + uid + "_A; SEID=canary"
		if err := accounts.Update(id, 1, "target"+uid, &cookie); err != nil {
			t.Fatal(err)
		}
		ids[i] = id
	}
	var mu sync.Mutex
	legacy, denied := false, false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		old, deny := legacy, denied
		mu.Unlock()
		user := strings.TrimPrefix(r.Header.Get("X-Emby-Token"), "token-")
		if user != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" && user != "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" && user != "cccccccccccccccccccccccccccccccc" {
			w.WriteHeader(401)
			return
		}
		source := "http://unused/api/share115/abc/0123456789ABCDEF0123456789ABCDEF01234567/pass/123"
		if old {
			source = "http://unused/api/?share_code=abc&id=123&receive_code=pass"
		}
		switch r.URL.Path {
		case "/emby/Users/" + user:
			fmt.Fprintf(w, `{"Id":%q,"Policy":{"EnableMediaPlayback":%t}}`, user, !(deny && user == "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"))
		case "/emby/Users/" + user + "/Items/movie":
			fmt.Fprintf(w, `{"Id":"movie","PlayAccess":"Full","MediaSources":[{"Id":"src","Path":%q,"SupportsDirectPlay":true}]}`, source)
		default:
			w.WriteHeader(404)
		}
	}))
	defer upstream.Close()
	store, err := service.NewSessionStore(filepath.Join(t.TempDir(), "cache.enc"), "secret")
	if err != nil {
		t.Fatal(err)
	}
	drive := &canaryAccountsDrive{reads: make(map[string]int)}
	logs := service.NewEmbyProxyLogs(&service.GenerationLogs{})
	cache, err := service.NewPlaybackCache(context.Background(), store, accounts, drive, logs)
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()
	if err := cache.ApplyBindings(upstream.URL, []service.PlaybackBinding{{UserID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", AccountID: ids[0]}, {UserID: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", AccountID: ids[1]}, {UserID: "cccccccccccccccccccccccccccccccc", AccountID: ids[0]}}); err != nil {
		t.Fatal(err)
	}
	h := NewEmbyCachedProxyHandler(service.EmbyProxyConfig{URL: upstream.URL, AccountID: primary, DirectLinkCacheMinutes: 9}, accounts, NewShareAPI(func() string { return "" }), cache, logs).(*embyProxyHandler)
	defer h.Close()
	for _, user := range []string{"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "cccccccccccccccccccccccccccccccc"} {
		if err := h.identities.Remember(upstream.URL, "token-"+user, user); err != nil {
			t.Fatal(err)
		}
	}
	call := func(user string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/cinema115/play?ItemId=movie&MediaSourceId=src&UserId=forged&api_key=token-"+user, nil)
		req.Header.Set("User-Agent", "canary-player")
		h.ServeHTTP(w, req)
		return w
	}
	for phase := 0; phase < 2; phase++ {
		mu.Lock()
		legacy = phase == 1
		mu.Unlock()
		var wg sync.WaitGroup
		for _, user := range []string{"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "cccccccccccccccccccccccccccccccc"} {
			wg.Add(1)
			go func(user string) {
				defer wg.Done()
				w := call(user)
				uid := "456"
				if user == "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" {
					uid = "789"
				}
				if w.Code != 302 || w.Header().Get("Location") != "https://cdn.115.com/account"+uid {
					t.Errorf("%s: %d %s", user, w.Code, w.Body.String())
				}
			}(user)
		}
		wg.Wait()
	}
	drive.mu.Lock()
	a, b := drive.reads["456"], drive.reads["789"]
	drive.mu.Unlock()
	if a != 2 || b != 1 {
		t.Fatalf("user cache isolation or format reuse failed: %d %d", a, b)
	}
	mu.Lock()
	denied = true
	mu.Unlock()
	if w := call("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"); w.Code != 403 {
		t.Fatal("cached request bypassed live permission", w.Code)
	}
	if w := call("cccccccccccccccccccccccccccccccc"); w.Code != 302 {
		t.Fatal("other user affected", w.Code)
	}
	empty := ""
	if err := accounts.Update(ids[1], 2, "target789", &empty); err != nil {
		t.Fatal(err)
	}
	if w := call("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"); w.Code != 503 || w.Header().Get("Location") != "" {
		t.Fatal("invalid target fell back or reused cached link", w.Code)
	}
	if w := call("cccccccccccccccccccccccccccccccc"); w.Code != 302 {
		t.Fatal("other account affected", w.Code)
	}
}
