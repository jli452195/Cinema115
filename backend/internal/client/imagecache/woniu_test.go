package imagecache

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWoniuImagesFixedSourceValidationAndNoCredentials(t *testing.T) {
	cache, err := NewWoniu(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	name := "16ba17a96691e6dd57ac40d8236bf9ca.jpg"
	for _, input := range [][2]string{{"vod", "../" + name}, {"other", name}, {"20260621-1/..", name}, {"../20260621-1", name}, {"vod", "a.jpg"}, {"vod", name + "?url=http://localhost"}, {"vod", "https://evil.test/a.jpg"}} {
		if _, err := cache.Open(context.Background(), input[0], input[1]); !errors.Is(err, ErrInput) {
			t.Fatal(input, err)
		}
	}
	body := testPNG(t)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/upload/vod/"+name && r.URL.Path != "/upload/vod/20260621-1/"+name || r.URL.RawQuery != "" {
			t.Error(r.URL)
		}
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" || r.Header.Get("Referer") != "" {
			t.Error("image credentials leaked")
		}
		w.Header().Set("Content-Type", "image/png")
		w.Write(body)
	}))
	defer server.Close()
	cache.baseURL = server.URL
	for _, directory := range []string{"vod", "20260621-1", "vod", "20260621-1"} {
		asset, err := cache.Open(context.Background(), directory, name)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(asset.File)
		asset.File.Close()
		if err != nil || len(data) != len(body) {
			t.Fatal(err)
		}
	}
	if calls != 2 {
		t.Fatal("root and dated image caches must be separate and reused", calls)
	}
}
