package imagecache

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBangumiCoverValidationAndDiskCache(t *testing.T) {
	const source = "https://lain.bgm.tv/pic/cover/m/06/8c/278826_test.jpg"
	const local = "/api/bangumi/image/m-06-8c/278826_test.jpg"
	for _, raw := range []string{source, strings.Replace(source, "https:", "http:", 1)} {
		if got := BangumiCoverURL(raw); got != local {
			t.Fatal(got)
		}
	}
	for _, raw := range []string{
		strings.Replace(source, "lain.bgm.tv", "example.test", 1), source + "?url=other", source + "#fragment",
		strings.Replace(source, "lain.bgm.tv", "user@lain.bgm.tv", 1), strings.Replace(source, "lain.bgm.tv", "lain.bgm.tv:443", 1),
		strings.Replace(source, "/m/", "/%6d/", 1), strings.Replace(source, ".jpg", ".svg", 1),
	} {
		if BangumiCoverURL(raw) != "" {
			t.Fatal("invalid cover retained", raw)
		}
	}
	body := testPNG(t)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/pic/cover/m/06/8c/278826_test.jpg" || r.URL.RawQuery != "" || r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			t.Error("unexpected cover request", r.URL)
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(body)
	}))
	defer server.Close()
	root := t.TempDir()
	for i := 0; i < 2; i++ {
		cache, err := NewBangumi(root)
		if err != nil {
			t.Fatal(err)
		}
		cache.baseURL = server.URL
		asset, err := cache.Open(context.Background(), "m-06-8c", "278826_test.jpg")
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(asset.File)
		_ = asset.File.Close()
		if err != nil || !bytes.Equal(data, body) || asset.ContentType != "image/png" {
			t.Fatal(asset, err)
		}
		for _, size := range []string{"m/06/8c", "m-06-../8c", "m-06-8c/", "w342"} {
			if _, err := cache.Open(context.Background(), size, "278826_test.jpg"); !errors.Is(err, ErrInput) {
				t.Fatal(size, err)
			}
		}
	}
	if calls != 1 {
		t.Fatal("persisted cache downloaded again", calls)
	}
}

func TestBangumiCoverRejectsFalseImageContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = io.WriteString(w, "<html>upstream unavailable</html>")
	}))
	defer server.Close()
	cache, err := NewBangumi(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cache.baseURL = server.URL
	if asset, err := cache.Open(context.Background(), "m-06-8c", "278826_test.jpg"); err == nil || asset != nil {
		t.Fatal("non-image was cached", asset, err)
	}
}
