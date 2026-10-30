package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/service"
	"strm115/backend/internal/strm"
)

type drivePlaybackFunc func(context.Context, p115.DrivePlaybackFile, string, string) (string, error)

func (f drivePlaybackFunc) VerifiedPlaybackURL(ctx context.Context, file p115.DrivePlaybackFile, cookie, ua string) (string, error) {
	return f(ctx, file, cookie, ua)
}

func drivePlaybackPath(t *testing.T) (strm.Drive, string) {
	t.Helper()
	file := strm.Drive{PickCode: "d1o0el582rmrtx29g", FileID: "3549300686904624677", SHA1: "976FB9B61C54E2CC934363D333B62648D20F2E18", Size: 1081549423, Name: "电影 a+b%20#?.mkv"}
	path, err := strm.DrivePath(file)
	if err != nil {
		t.Fatal(err)
	}
	return file, path
}

func drivePlaybackRequest(router http.Handler, method, path, ua string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, nil)
	r.Header.Set("User-Agent", ua)
	r.Header.Set("Range", "bytes=0-1023")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	return w
}

func drivePlaybackMedia(t *testing.T) *http.Client {
	t.Helper()
	return &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.UserAgent() != p115.PlaybackUserAgent || r.Header.Get("Accept-Encoding") != "identity" || r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			t.Error("media transport changed UA or forwarded credentials")
		}
		if _, bounded := r.Context().Deadline(); bounded || r.Context().Err() != nil {
			t.Error("lookup timeout or cancellation leaked into media playback")
		}
		return &http.Response{StatusCode: http.StatusPartialContent, Header: http.Header{"Content-Type": {"video/x-matroska"}, "Content-Length": {"4"}, "Content-Range": {"bytes 0-3/1081549423"}, "Accept-Ranges": {"bytes"}}, Body: io.NopCloser(strings.NewReader("data"))}, nil
	})}
}

func TestDrivePlaybackOriginalFileAndCredentialCache(t *testing.T) {
	gin.SetMode(gin.TestMode)
	accounts, id := proxyTestAccounts(t)
	file, path := drivePlaybackPath(t)
	calls := 0
	wantCookie := "UID=123_A; SEID=private"
	router := gin.New()
	logs := &service.GenerationLogs{}
	RegisterDrivePlayback(router, accounts, drivePlaybackFunc(func(ctx context.Context, got p115.DrivePlaybackFile, cookie, ua string) (string, error) {
		calls++
		if got.ID != file.FileID || got.PickCode != file.PickCode || got.SHA1 != file.SHA1 || got.Size != file.Size || got.Name != file.Name || cookie != wantCookie || ua != p115.PlaybackUserAgent {
			t.Fatal("file declaration, filename decoding or credential snapshot changed")
		}
		if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 30*time.Second {
			t.Fatal("missing bounded playback context")
		}
		return "https://cdn.115.com/video?f=1", nil
	}), drivePlaybackMedia(t), logs)
	check := func(method, ua string, wantCalls int) {
		t.Helper()
		w := drivePlaybackRequest(router, method, path, ua)
		if w.Code != http.StatusPartialContent || w.Header().Get("Location") != "" || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Content-Range") != "bytes 0-3/1081549423" || calls != wantCalls {
			t.Fatalf("standalone playback or cache failed: %d, calls=%d, body=%s", w.Code, calls, w.Body.String())
		}
		if method == http.MethodHead && w.Body.Len() != 0 || method == http.MethodGet && w.Body.String() != "data" {
			t.Fatal("media body lost or HEAD returned a body")
		}
	}
	check(http.MethodHead, "player", 1)
	check(http.MethodGet, "player", 1)
	entries := logs.Snapshot("ALL")
	if len(entries) != 2 || entries[0].Source != "目录播放" || !strings.Contains(entries[0].Message, "缓存命中=true") || !strings.Contains(entries[0].Message, "bytes=0-1023") || strings.Contains(entries[0].Message, "private") || strings.Contains(entries[0].Message, "https://") {
		t.Fatal("playback diagnostics missing or exposing credentials")
	}
	check(http.MethodGet, "other-player", 1)
	check(http.MethodGet, "", 1)
	wantCookie = "UID=123_A; SEID=updated"
	account, _ := accounts.Get(id)
	if err := accounts.Update(id, account.Revision, account.Name, &wantCookie); err != nil {
		t.Fatal(err)
	}
	check(http.MethodGet, "", 2)
	account, _ = accounts.Get(id)
	empty := ""
	if err := accounts.Update(id, account.Revision, account.Name, &empty); err != nil {
		t.Fatal(err)
	}
	if w := drivePlaybackRequest(router, http.MethodGet, path, ""); w.Code != http.StatusServiceUnavailable || calls != 2 {
		t.Fatal("cleared primary credentials reused a cached link")
	}
	if err := accounts.Delete(id, account.Revision+1); err != nil {
		t.Fatal(err)
	}
	secondary, err := accounts.Add("secondary")
	if err != nil {
		t.Fatal(err)
	}
	if err := accounts.Update(secondary, 1, "secondary", &wantCookie); err != nil {
		t.Fatal(err)
	}
	if w := drivePlaybackRequest(router, http.MethodHead, path, ""); w.Code != http.StatusServiceUnavailable || w.Body.Len() != 0 || calls != 2 {
		t.Fatal("missing primary fell back to a secondary or returned a HEAD body")
	}
}

func TestDrivePlaybackFailuresCanRetry(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		link string
		code int
	}{
		{"timeout", context.DeadlineExceeded, "", http.StatusGatewayTimeout},
		{"missing", p115.ErrDriveFileMissing, "", http.StatusNotFound},
		{"upstream", errors.New("upstream private-cookie https://private.invalid/?secret=value"), "", http.StatusBadGateway},
		{"restricted", nil, "https://cdn.115.com/video?f=3", http.StatusBadGateway},
	} {
		t.Run(tc.name, func(t *testing.T) {
			accounts, _ := proxyTestAccounts(t)
			_, path := drivePlaybackPath(t)
			calls := 0
			router := gin.New()
			RegisterDrivePlayback(router, accounts, drivePlaybackFunc(func(context.Context, p115.DrivePlaybackFile, string, string) (string, error) {
				calls++
				if calls == 1 {
					return tc.link, tc.err
				}
				return "https://cdn.115.com/video", nil
			}), drivePlaybackMedia(t), nil)
			w := drivePlaybackRequest(router, http.MethodGet, path, "player")
			if w.Code != tc.code || w.Header().Get("Location") != "" || strings.Contains(w.Body.String(), "private") || strings.Contains(w.Body.String(), "secret") {
				t.Fatal("wrong failure response", w.Code, w.Body.String())
			}
			if w := drivePlaybackRequest(router, http.MethodGet, path, "player"); w.Code != http.StatusPartialContent || calls != 2 {
				t.Fatal("failure cached or retry blocked")
			}
		})
	}
}

func TestDrivePlaybackCoalescesPlayerRequests(t *testing.T) {
	accounts, _ := proxyTestAccounts(t)
	_, path := drivePlaybackPath(t)
	var calls atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	router := gin.New()
	RegisterDrivePlayback(router, accounts, drivePlaybackFunc(func(ctx context.Context, _ p115.DrivePlaybackFile, _, _ string) (string, error) {
		if calls.Add(1) == 1 {
			close(entered)
		}
		select {
		case <-release:
			return "https://cdn.115.com/video", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}), drivePlaybackMedia(t), nil)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if w := drivePlaybackRequest(router, http.MethodGet, path, "player"); w.Code != http.StatusPartialContent {
				t.Errorf("concurrent request failed: %d", w.Code)
			}
		}()
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Error("playback request did not start")
	}
	close(release)
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatal("concurrent player requests repeated the upstream lookup", calls.Load())
	}
}

func TestDrivePlaybackCancellationReleasesLookup(t *testing.T) {
	accounts, _ := proxyTestAccounts(t)
	_, path := drivePlaybackPath(t)
	entered, done := make(chan struct{}), make(chan struct{})
	calls := 0
	router := gin.New()
	RegisterDrivePlayback(router, accounts, drivePlaybackFunc(func(ctx context.Context, _ p115.DrivePlaybackFile, _, _ string) (string, error) {
		calls++
		if calls == 1 {
			close(entered)
			<-ctx.Done()
			return "", ctx.Err()
		}
		return "https://cdn.115.com/video", nil
	}), drivePlaybackMedia(t), nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := httptest.NewRequest(http.MethodGet, path, nil).WithContext(ctx)
	w := httptest.NewRecorder()
	go func() {
		defer close(done)
		router.ServeHTTP(w, r)
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("lookup did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("request cancellation did not end lookup")
	}
	if w.Code != http.StatusBadGateway {
		t.Fatal("cancelled lookup returned a playable response", w.Code)
	}
	if w := drivePlaybackRequest(router, http.MethodGet, path, ""); w.Code != http.StatusPartialContent || calls != 2 {
		t.Fatal("cancelled lookup left a cached failure or blocked retry")
	}
}

type mediaReadFunc func([]byte) (int, error)

func (f mediaReadFunc) Read(p []byte) (int, error) { return f(p) }

func TestDrivePlaybackMediaFailureRecovery(t *testing.T) {
	accounts, _ := proxyTestAccounts(t)
	_, path := drivePlaybackPath(t)
	lookups, reads := 0, 0
	media := drivePlaybackMedia(t)
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		reads++
		if reads == 1 {
			return &http.Response{StatusCode: http.StatusForbidden, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("private upstream error"))}, nil
		}
		resp, err := media.Do(r)
		if reads == 2 && err == nil {
			_ = resp.Body.Close()
			resp.Body = io.NopCloser(mediaReadFunc(func([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }))
		}
		return resp, err
	})}
	router := gin.New()
	RegisterDrivePlayback(router, accounts, drivePlaybackFunc(func(context.Context, p115.DrivePlaybackFile, string, string) (string, error) {
		lookups++
		return "https://cdn.115.com/video", nil
	}), client, nil)
	if w := drivePlaybackRequest(router, http.MethodGet, path, "player"); w.Code != http.StatusBadGateway || strings.Contains(w.Body.String(), "private") || w.Header().Get("Content-Range") != "" {
		t.Fatal("upstream rejection was not handled before sending media headers")
	}
	if w := drivePlaybackRequest(router, http.MethodGet, path, "player"); w.Code != http.StatusPartialContent || w.Body.Len() != 0 || lookups != 2 {
		t.Fatal("stale link was not evicted or JSON was appended to a broken media response")
	}
	if w := drivePlaybackRequest(router, http.MethodGet, path, "player"); w.Code != http.StatusPartialContent || w.Body.String() != "data" || lookups != 2 {
		t.Fatal("partial read failure discarded a usable link or blocked replay")
	}
}

func TestDrivePlaybackMediaCancellationKeepsLink(t *testing.T) {
	accounts, _ := proxyTestAccounts(t)
	_, path := drivePlaybackPath(t)
	entered, done := make(chan struct{}), make(chan struct{})
	lookups, reads := 0, 0
	media := drivePlaybackMedia(t)
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		reads++
		resp, err := media.Do(r)
		if reads == 1 && err == nil {
			_ = resp.Body.Close()
			resp.Body = io.NopCloser(mediaReadFunc(func([]byte) (int, error) {
				close(entered)
				<-r.Context().Done()
				return 0, r.Context().Err()
			}))
		}
		return resp, err
	})}
	router := gin.New()
	RegisterDrivePlayback(router, accounts, drivePlaybackFunc(func(context.Context, p115.DrivePlaybackFile, string, string) (string, error) {
		lookups++
		return "https://cdn.115.com/video", nil
	}), client, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := httptest.NewRequest(http.MethodGet, path, nil).WithContext(ctx)
	go func() {
		defer close(done)
		router.ServeHTTP(httptest.NewRecorder(), r)
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("original-file body did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("client cancellation did not stop original-file reading")
	}
	if w := drivePlaybackRequest(router, http.MethodGet, path, "player"); w.Code != http.StatusPartialContent || lookups != 1 {
		t.Fatal("client cancellation discarded the cached source link")
	}
}
