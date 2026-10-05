package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/client/p115"
)

// Opt-in, read-only real-upstream canary. Supply only the pinned primary cookie
// via process environment. No credentials or signed URLs are written to output.
func TestLiveShareCompatibilityCanary(t *testing.T) {
	cookie := os.Getenv("CINEMA115_CANARY_COOKIE")
	if cookie == "" {
		t.Skip("requires opt-in primary account")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	a := NewShareAPI(func() string { return cookie })
	share := &p115.ShareInfo{Code: "sws8jb636j9", Password: "fwgl"}
	page, err := a.Client.List(ctx, share, "3532903049016116662", 0, cookie)
	if err != nil {
		t.Fatal("live share metadata lookup failed")
	}
	var file p115.ShareFile
	for _, f := range page.Files {
		if f.ID == "3532903049309717944" {
			file = f
			break
		}
	}
	if file.SHA1 != "3C57FCFD33B9476B43C6BCED7D20B5887EFB2A4E" {
		t.Fatal("live metadata mismatch")
	}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	a.Register(r, r.Group("/api/admin"))
	server := httptest.NewServer(r)
	defer server.Close()
	a.BaseURL = func() string { return server.URL }
	body, err := json.Marshal(shareInput{URL: "https://115.com/s/" + share.Code, Password: share.Password, FileID: file.ID, Name: file.Name, SHA1: file.SHA1})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", "/api/admin/p115/share/strm", strings.NewReader(string(body))))
	var generated struct{ PlayURL string }
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &generated) != nil {
		t.Fatal("live single-file generation failed")
	}
	client := server.Client()
	client.Timeout = 35 * time.Second
	for _, path := range []string{generated.PlayURL, "/api/?share_code=" + share.Code + "&id=" + file.ID + "&receive_code=" + share.Password} {
		for _, method := range []string{"HEAD", "GET"} {
			req, err := http.NewRequestWithContext(ctx, method, server.URL+path, nil)
			if err != nil {
				t.Fatal("request creation failed")
			}
			req.Header.Set("Range", "bytes=0-3")
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal("live playback request failed")
			}
			data, readErr := io.ReadAll(io.LimitReader(resp.Body, 4))
			resp.Body.Close()
			if readErr != nil || resp.StatusCode != 206 || !strings.HasPrefix(resp.Header.Get("Content-Range"), "bytes 0-3/") || (method == "GET" && len(data) != 4) || (method == "HEAD" && len(data) != 0) {
				t.Fatalf("live %s range check failed: HTTP %d", method, resp.StatusCode)
			}
		}
	}
	t.Log("real primary: metadata and new generation verified; new/legacy HEAD and four-byte GET passed")
}

// A pinned administrator pool credential, read-only: proves that the share
// endpoint can provide a direct link without a preceding directory/login call.
func TestLiveShareDirectReadCanary(t *testing.T) {
	cookie := os.Getenv("CINEMA115_CANARY_TARGET_COOKIE")
	if cookie == "" {
		t.Skip("requires opt-in target account")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	a := NewShareAPI(func() string { return cookie })
	var calls atomic.Int32
	client := *a.Client.HTTP
	transport := client.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		if r.URL.Path != "/os_windows/2.0/share/downurl" {
			t.Error("unexpected preflight endpoint")
		}
		return transport.RoundTrip(r)
	})
	a.Client.HTTP = &client
	info, err := a.Client.DownloadInfoWithUA(ctx, &p115.ShareInfo{Code: "sws8jb636j9", Password: "fwgl"}, "3532903049309717944", cookie, p115.PlaybackUserAgent)
	if err != nil || info == nil || !p115.AllowedDirectPlaybackURL(info.URL) {
		t.Fatal("target share direct read failed")
	}
	if calls.Load() != 1 {
		t.Fatalf("unexpected HTTP count: requests=%d", calls.Load())
	}
	t.Log("real target: valid share direct URL obtained with one HTTP request, no preflight, no retry, no cloud writes")
}
