package imagecache

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestGyingCoverValidation(t *testing.T) {
	for _, kind := range []string{"mv", "tv", "ac", "zy"} {
		if got := GyingCoverURL(kind, "P8KR"); got != "/api/gying/image/"+kind+"/P8KR.webp" {
			t.Fatal(got)
		}
	}
	cache, err := NewGying(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range [][2]string{{"ac", ""}, {"other", "P8KR"}, {"../ac", "P8KR"}, {"ac", "../secret"}, {"ac", "a/b"}, {"ac", "a\\b"}, {"ac", "a?x=1"}, {"ac", "%2e%2e"}} {
		if GyingCoverURL(input[0], input[1]) != "" {
			t.Fatal("危险参数未拒绝", input)
		}
		if _, err := cache.Open(context.Background(), input[0], input[1]+".webp"); !errors.Is(err, ErrInput) {
			t.Fatal(input, err)
		}
	}
	for _, name := range []string{"P8KR.jpg", "P8KR.svg", "P8KR.webp?url=https://evil.test", "P8KR.webp.webp"} {
		if _, err := cache.Open(context.Background(), "ac", name); !errors.Is(err, ErrInput) {
			t.Fatal(name, err)
		}
	}
}

func TestGyingCachePersistsWithoutCredentials(t *testing.T) {
	body := testPNG(t)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/img/ac/P8KR/256.webp" || r.URL.RawQuery != "" {
			t.Error(r.URL)
		}
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" || r.Header.Get("Referer") != "" {
			t.Error("图片请求不应携带凭据")
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(body)
	}))
	defer server.Close()
	root := t.TempDir()
	for i := 0; i < 2; i++ {
		cache, err := NewGying(root)
		if err != nil {
			t.Fatal(err)
		}
		if cache.baseURL != "https://s.tutu.pm" || cache.maxBytes != 200<<20 || cap(cache.downloads) != 2 {
			t.Fatal("观影缓存配置错误")
		}
		cache.baseURL = server.URL
		asset, err := cache.Open(context.Background(), "ac", "P8KR.webp")
		if err != nil {
			t.Fatal(err)
		}
		data, readErr := io.ReadAll(asset.File)
		_ = asset.File.Close()
		if readErr != nil || !bytes.Equal(data, body) || asset.ContentType != "image/png" || asset.ETag == "" {
			t.Fatal("缓存内容错误", readErr)
		}
		if i == 0 && len(cache.prune) != 1 {
			t.Fatal("未通知后台清理缓存")
		}
	}
	if calls != 1 {
		t.Fatal("未复用磁盘缓存", calls)
	}
}

func TestGyingRejectsBadImageAndRedirect(t *testing.T) {
	for _, scenario := range []string{"html", "fake-image", "redirect", "not-found"} {
		t.Run(scenario, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				switch scenario {
				case "redirect":
					http.Redirect(w, r, "/unexpected", http.StatusFound)
				case "not-found":
					w.WriteHeader(404)
				default:
					w.Header().Set("Content-Type", "text/html")
					if scenario == "fake-image" {
						w.Header().Set("Content-Type", "image/webp")
					}
					_, _ = io.WriteString(w, "<html>验证页面</html>")
				}
			}))
			defer server.Close()
			cache, err := NewGying(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			cache.baseURL = server.URL
			for i := 0; i < 2; i++ {
				if asset, err := cache.Open(context.Background(), "ac", "P8KR.webp"); err == nil || asset != nil {
					t.Fatal("错误响应被当成图片")
				}
			}
			if calls != 1 {
				t.Fatal("重复请求失败图片或跟随重定向", calls)
			}
			if _, err := os.Stat(filepath.Join(cache.root, "ac", "P8KR.webp")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal(err)
			}
		})
	}
}
