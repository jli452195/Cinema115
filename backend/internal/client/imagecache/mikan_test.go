package imagecache

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testPNG(t *testing.T) []byte {
	t.Helper()
	var body bytes.Buffer
	if err := png.Encode(&body, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	return body.Bytes()
}

func TestMikanCoverURLValidation(t *testing.T) {
	for _, raw := range []string{"/images/Bangumi/201904/bb09460c.jpg?width=400&height=400&format=webp", "https://mikanani.me/images/Bangumi/201904/bb09460c.jpg"} {
		if got := MikanCoverURL(raw); got != "/api/mikan/image/201904/bb09460c.jpg" {
			t.Fatal(got)
		}
	}
	for _, raw := range []string{
		"https://evil.test/images/Bangumi/201904/a.jpg", "//evil.test/images/Bangumi/201904/a.jpg",
		"http://mikanani.me/images/Bangumi/201904/a.jpg", "https://mikanani.me@127.0.0.1/images/Bangumi/201904/a.jpg",
		"/images/Bangumi/201913/a.jpg", "/images/Bangumi/201900/a.jpg", "/images/Bangumi/../a.jpg",
		"/images/Bangumi/201904/%2e%2e%2fsecret.jpg", "/images/Bangumi/201904/a.svg",
		"/images/Bangumi/201904/a.jpg#fragment", "/images/Bangumi/201904/abc%5cdef.jpg",
		"/images/Bangumi/201904/a.jpg/extra", "https:/images/Bangumi/201904/a.jpg",
	} {
		if got := MikanCoverURL(raw); got != "" {
			t.Fatalf("危险来源未拒绝：%s -> %s", raw, got)
		}
	}
}

func TestMikanDiskCachePersistsAndValidatesRequests(t *testing.T) {
	body := testPNG(t)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/images/Bangumi/201904/poster.jpg" || r.URL.Query().Get("width") != "240" || r.URL.Query().Get("height") != "336" || r.URL.Query().Get("format") != "webp" {
			t.Error(r.URL)
		}
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("Referer") != "" {
			t.Error("向图片上游发送了不必要的凭据")
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("ETag", `"upstream-image"`)
		_, _ = w.Write(body)
	}))
	defer server.Close()
	root := t.TempDir()
	for i := 0; i < 2; i++ {
		cache, err := NewMikan(root)
		if err != nil {
			t.Fatal(err)
		}
		if cache.maxBytes != 200<<20 || cap(cache.downloads) != 2 {
			t.Fatal("蜜柑缓存默认值错误")
		}
		cache.baseURL = server.URL
		asset, err := cache.Open(context.Background(), "201904", "poster.jpg")
		if err != nil {
			t.Fatal(err)
		}
		data, readErr := io.ReadAll(asset.File)
		_ = asset.File.Close()
		if readErr != nil || !bytes.Equal(body, data) || asset.ContentType != "image/png" || asset.ETag == "" {
			t.Fatal(asset, readErr)
		}
		for _, input := range []struct{ month, name string }{{"w342", "poster.jpg"}, {"201913", "poster.jpg"}, {"201904", "../poster.jpg"}, {"201904", "a.svg"}, {"201904", "a.jpg?url=https://evil.test"}} {
			if _, err := cache.Open(context.Background(), input.month, input.name); !errors.Is(err, ErrInput) {
				t.Fatal(input, err)
			}
		}
	}
	if calls.Load() != 1 {
		t.Fatal("重建缓存实例后未复用磁盘图片", calls.Load())
	}
}

func TestMikanRejectsInvalidResponsesAndCachesFailures(t *testing.T) {
	for _, scenario := range []string{"html", "fake-image", "oversize", "redirect", "not-found", "unavailable"} {
		t.Run(scenario, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				switch scenario {
				case "html":
					w.Header().Set("Content-Type", "text/html")
					_, _ = io.WriteString(w, "<html>验证页面</html>")
				case "fake-image":
					w.Header().Set("Content-Type", "image/jpeg")
					_, _ = io.WriteString(w, "<html>验证页面</html>")
				case "oversize":
					w.Header().Set("Content-Type", "image/png")
					w.Header().Set("Content-Length", strconv.FormatInt(maxImageBytes+1, 10))
					w.WriteHeader(200)
				case "redirect":
					http.Redirect(w, r, "/unexpected", http.StatusFound)
				case "not-found":
					w.WriteHeader(404)
				case "unavailable":
					w.WriteHeader(503)
				}
			}))
			defer server.Close()
			cache, err := NewMikan(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			cache.baseURL = server.URL
			for i := 0; i < 2; i++ {
				if asset, err := cache.Open(context.Background(), "201904", "bad.jpg"); err == nil || asset != nil {
					t.Fatal("异常响应被缓存为图片", asset)
				}
			}
			if calls != 1 {
				t.Fatal("未合并失败重试或意外跟随重定向", calls)
			}
			if _, err := os.Stat(filepath.Join(cache.root, "201904", "bad.jpg")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal(err)
			}
		})
	}
}

func TestMikanStaleImageSurvivesUpstreamFailureAnd304(t *testing.T) {
	body := testPNG(t)
	var mode atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch mode.Load() {
		case 1:
			w.WriteHeader(503)
		case 2:
			if r.Header.Get("If-None-Match") != `"upstream"` {
				t.Error("缺少上游条件验证")
			}
			w.WriteHeader(304)
		default:
			w.Header().Set("Content-Type", "image/png")
			w.Header().Set("ETag", `"upstream"`)
			_, _ = w.Write(body)
		}
	}))
	defer server.Close()
	cache, err := NewMikan(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cache.baseURL = server.URL
	asset, err := cache.Open(context.Background(), "201904", "poster.jpg")
	if err != nil {
		t.Fatal(err)
	}
	path := asset.File.Name()
	_ = asset.File.Close()
	old := time.Now().Add(-8 * 24 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	mode.Store(1)
	if err := cache.download(context.Background(), "201904", "poster.jpg", path, path+".json"); err == nil {
		t.Fatal("预期刷新失败")
	}
	asset, err = cache.Open(context.Background(), "201904", "poster.jpg")
	if err != nil {
		t.Fatal("断网时旧缓存应可读", err)
	}
	data, readErr := io.ReadAll(asset.File)
	_ = asset.File.Close()
	if readErr != nil || !bytes.Equal(body, data) {
		t.Fatal(readErr)
	}
	if !cache.hasPending() {
		t.Fatal("过期图片未进入受控刷新队列")
	}
	mode.Store(2)
	if err := cache.download(context.Background(), "201904", "poster.jpg", path, path+".json"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || time.Since(info.ModTime()) > time.Minute {
		t.Fatal("304 未更新缓存有效期", err)
	}
}

func TestMikanCleanupAndFailureExpiry(t *testing.T) {
	cache, err := NewMikan(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	cache.now = func() time.Time { return now }
	cache.rememberFailure("201904/retry.jpg", errors.New("临时不可用"))
	if cache.cachedFailure("201904/retry.jpg") == nil {
		t.Fatal("临时失败未缓存")
	}
	now = now.Add(61 * time.Second)
	if cache.cachedFailure("201904/retry.jpg") != nil {
		t.Fatal("临时失败到期后不能重试")
	}
	cache.maxBytes = 5
	dir := filepath.Join(cache.root, "201904")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{"older.jpg", "newer.jpg"} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("four"), 0600); err != nil {
			t.Fatal(err)
		}
		modified := now.Add(time.Duration(i-2) * time.Hour)
		if err := os.Chtimes(path, modified, modified); err != nil {
			t.Fatal(err)
		}
	}
	if err := cache.cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "older.jpg")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("超额旧封面未清理", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "newer.jpg")); err != nil {
		t.Fatal("较新封面应保留", err)
	}
}

func TestMikanConcurrencyAndCancellation(t *testing.T) {
	body := testPNG(t)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		time.Sleep(30 * time.Millisecond)
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(body)
	}))
	defer server.Close()
	cache, err := NewMikan(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cache.baseURL = server.URL
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			asset, err := cache.Open(context.Background(), "201904", "shared.png")
			if err != nil {
				t.Error(err)
				return
			}
			_ = asset.File.Close()
		}()
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatal("并发封面请求未合并", calls.Load())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := cache.Open(ctx, "201904", "cancel.png"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if len(cache.negative) != 0 {
		t.Fatal("取消请求不应写入失败缓存")
	}
}

// 实站检查只请求一张公开封面，临时目录由测试框架回收，不修改用户配置。
func TestLiveMikanImage(t *testing.T) {
	if os.Getenv("MIKAN_LIVE") != "1" {
		t.Skip("未启用公开站点检查")
	}
	cache, err := NewMikan(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	asset, err := cache.Open(ctx, "201904", "bb09460c.jpg")
	if err != nil {
		t.Fatal(err)
	}
	defer asset.File.Close()
	if asset.Size == 0 || !strings.HasPrefix(asset.ContentType, "image/") {
		t.Fatal(asset)
	}
	t.Logf("蜜柑封面通过后端读取：%s，%d 字节", asset.ContentType, asset.Size)
}
