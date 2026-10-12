package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"strm115/backend/internal/client/woniu"
)

func woniuServiceFixture(t *testing.T) (*WoniuService, *atomic.Int32, *atomic.Int32) {
	return woniuServiceFixtureWithScenario(t, nil)
}

func woniuServiceFixtureWithScenario(t *testing.T, scenario func(http.ResponseWriter, *http.Request) bool) (*WoniuService, *atomic.Int32, *atomic.Int32) {
	t.Helper()
	searches, logins := &atomic.Int32{}, &atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/vodsearch/") {
			searches.Add(1)
		}
		if scenario != nil && scenario(w, r) {
			return
		}
		switch {
		case r.URL.Path == "/user/login/":
			fmt.Fprint(w, `<input name="user_name"><input name="user_pwd">`)
		case r.URL.Path == "/user/login.html":
			logins.Add(1)
			if r.FormValue("user_pwd") != "correct-pass" {
				fmt.Fprint(w, `{"code":1003,"msg":"private upstream"}`)
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "user_id", Value: "private-session-value", Path: "/"})
			fmt.Fprint(w, `{"code":1}`)
		case r.URL.Path == "/user/index.html":
			fmt.Fprint(w, `<a href="/user/logout.html">退出</a>`)
		case strings.HasPrefix(r.URL.Path, "/vodsearch/"):
			if r.URL.Query().Get("wd") == "无匹配" {
				fmt.Fprint(w, `<title>搜索无匹配 - 蜗牛 I 4K</title>`)
				return
			}
			fmt.Fprint(w, `<title>搜索影片 - 蜗牛 I 4K</title><a class="video-card" href="/voddetail/1/" title="电影"></a>`)
		case r.URL.Path == "/voddetail/1/":
			fmt.Fprint(w, `<h1 class="premium-title">电影</h1><div class="pan-link-item"><span class="pan-link-title">版本</span><a href="https://115cdn.com/s/fixture?password=AbCD">打开</a></div>`)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(server.Close)
	store, err := NewSessionStore(filepath.Join(t.TempDir(), "woniu.enc"), "fixture-key")
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewWoniuService(store)
	if err != nil {
		t.Fatal(err)
	}
	s.newClient = func() (*woniu.Client, error) { return woniu.New(server.URL, server.Client()) }
	return s, searches, logins
}

func TestWoniuCredentialsGateAtomicConfigAndEncryptedStorage(t *testing.T) {
	s, requests, _ := woniuServiceFixture(t)
	ctx := context.Background()
	if s.View().Configured {
		t.Fatal("new source must be unconfigured")
	}
	if _, err := s.Search(ctx, WoniuSearchInput{CacheKey: "movie:1", Title: "电影"}); !errors.Is(err, woniu.ErrNotConfigured) || requests.Load() != 0 {
		t.Fatal("unconfigured search accessed site", err)
	}
	view, err := s.Configure(ctx, WoniuConfigInput{Username: "tester", Password: "correct-pass"})
	if err != nil || !view.Configured {
		t.Fatal(view, err)
	}
	if _, err := s.Search(ctx, WoniuSearchInput{CacheKey: "movie:1", Title: "电影"}); err != nil || requests.Load() != 1 {
		t.Fatal("saved credentials did not enable search", err)
	}
	if _, err := s.Configure(ctx, WoniuConfigInput{Username: "replacement", Password: "bad"}); !errors.Is(err, woniu.ErrLogin) {
		t.Fatal(err)
	}
	if s.View().Username != "tester" || !s.View().Configured {
		t.Fatal("failed replacement changed configuration")
	}
	data, err := os.ReadFile(s.store.path)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"correct-pass", "private-session-value", "tester"} {
		if strings.Contains(string(data), secret) {
			t.Fatal("plaintext configuration")
		}
	}
	if err := s.Clear(ctx); err != nil {
		t.Fatal(err)
	}
	if s.View().Configured {
		t.Fatal("clear did not remove source")
	}
}

func TestWoniuSearchCacheIsolationRefreshAndFallback(t *testing.T) {
	s, requests, _ := woniuServiceFixture(t)
	ctx := context.Background()
	if _, err := s.Configure(ctx, WoniuConfigInput{Username: "tester", Password: "correct-pass"}); err != nil {
		t.Fatal(err)
	}
	input := WoniuSearchInput{CacheKey: "movie:1", Title: "无匹配", OriginalTitle: "原名"}
	first, err := s.Search(ctx, input)
	if err != nil || first.MatchedBy != "original" || len(first.Attempts) != 2 || requests.Load() != 2 {
		t.Fatal(first, err, requests.Load())
	}
	first.Items[0].Links[0].Password = "changed"
	second, err := s.Search(ctx, input)
	if err != nil || !second.Cached || second.Items[0].Links[0].Password != "AbCD" || requests.Load() != 2 {
		t.Fatal("cache mutation or duplicate request", second, err)
	}
	input.Refresh = true
	if refreshed, err := s.Search(ctx, input); err != nil || refreshed.Cached || requests.Load() != 4 {
		t.Fatal("refresh", err)
	}
	if err := s.Clear(ctx); err != nil {
		t.Fatal(err)
	}
	input.Refresh = false
	if _, err := s.Search(ctx, input); !errors.Is(err, woniu.ErrNotConfigured) {
		t.Fatal("cached result bypassed cleared credentials", err)
	}
}

func TestWoniuQueuedCancellationAndSafeLogs(t *testing.T) {
	s, _, _ := woniuServiceFixture(t)
	s.Logs = &GenerationLogs{}
	s.gate <- struct{}{}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := s.Configure(ctx, WoniuConfigInput{Username: "private-user", Password: "private-password"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	<-s.gate
	entries := s.Logs.Snapshot("ALL", "蜗牛")
	if len(entries) != 2 {
		t.Fatal(entries)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Message, "private") {
			t.Fatal("credential in logs")
		}
	}
}

func TestWoniuSessionRetryBoundedAndLimitsNotRelogin(t *testing.T) {
	for _, scenario := range []struct {
		name     string
		err      error
		logins   int32
		searches int32
	}{{"session", woniu.ErrSession, 2, 2}, {"rate", woniu.ErrRateLimited, 1, 1}, {"challenge", woniu.ErrChallenge, 1, 1}} {
		t.Run(scenario.name, func(t *testing.T) {
			t.Parallel()
			s, searches, logins := woniuServiceFixtureWithScenario(t, func(w http.ResponseWriter, r *http.Request) bool {
				if scenario.name == "session" && strings.HasPrefix(r.URL.Path, "/voddetail/") {
					fmt.Fprint(w, `<div class="pan-lock-box"></div>`)
					return true
				}
				if strings.HasPrefix(r.URL.Path, "/vodsearch/") {
					switch scenario.name {
					case "rate":
						fmt.Fprint(w, `<title>跳转提示</title>请不要频繁操作，搜索时间间隔为3秒`)
						return true
					case "challenge":
						w.WriteHeader(http.StatusForbidden)
						fmt.Fprint(w, `<div id="cf-chl-fixture">Just a moment...</div>`)
						return true
					}
				}
				return false
			})
			ctx := context.Background()
			if _, err := s.Configure(ctx, WoniuConfigInput{Username: "tester", Password: "correct-pass"}); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Search(ctx, WoniuSearchInput{CacheKey: "tv:1", Title: "电影"}); !errors.Is(err, scenario.err) {
				t.Fatal(err)
			}
			if logins.Load() != scenario.logins || searches.Load() != scenario.searches || len(s.cache) != 0 {
				t.Fatal("retry or error cache escaped bounds", logins.Load(), searches.Load(), len(s.cache))
			}
		})
	}
}

func TestWoniuPartialDetailsAreNotCached(t *testing.T) {
	s, searches, logins := woniuServiceFixtureWithScenario(t, func(w http.ResponseWriter, r *http.Request) bool {
		if strings.HasPrefix(r.URL.Path, "/vodsearch/") {
			fmt.Fprint(w, `<title>搜索电影 - 蜗牛 I 4K</title><a class="video-card" href="/voddetail/1/" title="电影"></a><a class="video-card" href="/voddetail/2/" title="第二版"></a>`)
			return true
		}
		if r.URL.Path == "/voddetail/2/" {
			w.WriteHeader(http.StatusServiceUnavailable)
			return true
		}
		return false
	})
	ctx := context.Background()
	if _, err := s.Configure(ctx, WoniuConfigInput{Username: "tester", Password: "correct-pass"}); err != nil {
		t.Fatal(err)
	}
	input := WoniuSearchInput{CacheKey: "tv:1", Title: "电影"}
	for i := 0; i < 2; i++ {
		result, err := s.Search(ctx, input)
		if err != nil || result.Cached || !result.Partial || result.FailedDetails != 1 || len(result.Items) != 2 || result.Items[0].DetailStatus != "ready" || result.Items[1].DetailStatus != "failed" {
			t.Fatal(result, err)
		}
	}
	if searches.Load() != 2 || logins.Load() != 1 || len(s.cache) != 0 {
		t.Fatal("partial result was cached or retried login", searches.Load(), logins.Load(), len(s.cache))
	}
}
