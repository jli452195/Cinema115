package tmdbimage

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCacheDownloadsOnceAndRejectsUnsafePaths(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		if r.URL.Path != "/t/p/w342/poster_1.jpg" {
			t.Errorf("上游图片路径错误: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("ETag", `"upstream-v1"`)
		_, _ = w.Write([]byte("image-v1"))
	}))
	defer server.Close()

	cache, err := New(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	cache.baseURL = server.URL
	for i := 0; i < 2; i++ {
		asset, err := cache.Open(context.Background(), "w342", "poster_1.jpg")
		if err != nil {
			t.Fatal(err)
		}
		data, readErr := io.ReadAll(asset.File)
		_ = asset.File.Close()
		if readErr != nil || string(data) != "image-v1" || asset.ContentType != "image/jpeg" || asset.ETag == "" {
			t.Fatal("图片缓存内容或元数据错误", string(data), readErr, asset)
		}
	}
	mu.Lock()
	requestCount := calls
	mu.Unlock()
	if requestCount != 1 {
		t.Fatalf("重复读取访问上游 %d 次", requestCount)
	}
	for _, input := range []struct{ size, name string }{{"original", "poster.jpg"}, {"w342", "../poster.jpg"}, {"w342", "https:.jpg"}, {"w342", "poster.svg"}} {
		if asset, err := cache.Open(context.Background(), input.size, input.name); err == nil || asset != nil {
			t.Fatal("危险图片参数未被拒绝", input)
		}
	}
}

func TestCacheServesStaleAndRefreshesInBackground(t *testing.T) {
	var mu sync.Mutex
	body := "image-v1"
	calls := make(chan struct{}, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		current := body
		mu.Unlock()
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte(current))
		calls <- struct{}{}
	}))
	defer server.Close()

	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	cache, err := New(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	cache.baseURL = server.URL
	cache.now = func() time.Time { return now }
	asset, err := cache.Open(context.Background(), "w500", "same.jpg")
	if err != nil {
		t.Fatal(err)
	}
	path := asset.File.Name()
	_ = asset.File.Close()
	<-calls
	if err := os.Chtimes(path, now.Add(-8*24*time.Hour), now.Add(-8*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	body = "image-v2"
	mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); cache.Run(ctx) }()

	stale, err := cache.Open(context.Background(), "w500", "same.jpg")
	if err != nil {
		t.Fatal(err)
	}
	staleData, _ := io.ReadAll(stale.File)
	_ = stale.File.Close()
	if string(staleData) != "image-v1" {
		t.Fatal("过期图片未立即返回")
	}
	select {
	case <-calls:
	case <-time.After(3 * time.Second):
		t.Fatal("后台图片刷新未运行")
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		fresh, openErr := cache.Open(context.Background(), "w500", "same.jpg")
		if openErr != nil {
			t.Fatal(openErr)
		}
		data, readErr := io.ReadAll(fresh.File)
		_ = fresh.File.Close()
		if readErr == nil && string(data) == "image-v2" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("后台刷新没有替换同路径图片")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("图片缓存 worker 未响应取消")
	}
}

func TestNotFoundIsNegativelyCached(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	cache, err := New(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	cache.baseURL = server.URL
	for i := 0; i < 2; i++ {
		if _, err := cache.Open(context.Background(), "w185", "missing.jpg"); !errors.Is(err, ErrNotFound) {
			t.Fatal("未返回图片不存在", err)
		}
	}
	if calls != 1 {
		t.Fatal("不存在的图片未命中失败缓存", calls)
	}
}

func TestConcurrentMissIsCoalesced(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		time.Sleep(20 * time.Millisecond)
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("shared-image"))
	}))
	defer server.Close()
	cache, err := New(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	cache.baseURL = server.URL
	var workers sync.WaitGroup
	errorsSeen := make(chan error, 8)
	for i := 0; i < 8; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			asset, openErr := cache.Open(context.Background(), "w342", "shared.jpg")
			if openErr != nil {
				errorsSeen <- openErr
				return
			}
			_ = asset.File.Close()
		}()
	}
	workers.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("并发缓存未合并上游请求", calls.Load())
	}
}

func TestCleanupRemovesExpiredAndLimitsSize(t *testing.T) {
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	cache, err := New(t.TempDir(), 5)
	if err != nil {
		t.Fatal(err)
	}
	cache.now = func() time.Time { return now }
	directory := filepath.Join(cache.root, "w342")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	files := []struct {
		name    string
		content string
		age     time.Duration
	}{
		{"expired.jpg", "old", 91 * 24 * time.Hour},
		{"oldest.jpg", "four", 20 * 24 * time.Hour},
		{"recent.jpg", "four", 10 * 24 * time.Hour},
	}
	for _, file := range files {
		path := filepath.Join(directory, file.name)
		if err := os.WriteFile(path, []byte(file.content), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, now.Add(-file.age), now.Add(-file.age)); err != nil {
			t.Fatal(err)
		}
	}
	if err := cache.cleanup(); err != nil {
		t.Fatal(err)
	}
	for _, removed := range []string{"expired.jpg", "oldest.jpg"} {
		if _, err := os.Stat(filepath.Join(directory, removed)); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("过期或超量图片未清理", removed, err)
		}
	}
	if _, err := os.Stat(filepath.Join(directory, "recent.jpg")); err != nil {
		t.Fatal("错误清理了最近使用的图片", err)
	}
}
