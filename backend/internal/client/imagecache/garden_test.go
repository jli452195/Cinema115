package imagecache

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGardenCoverSmallCacheAndBoundary(t *testing.T) {
	body := testPNG(t)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/bangumi/subject/623249/poster.jpeg" || r.URL.RawQuery != "quality=small" {
			t.Error(r.URL)
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(body)
	}))
	defer server.Close()
	c, e := NewGarden(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	c.baseURL = server.URL
	for i := 0; i < 2; i++ {
		asset, e := c.Open(context.Background(), "623249", "poster.jpeg")
		if e != nil {
			t.Fatal(e)
		}
		asset.File.Close()
	}
	if calls != 1 {
		t.Fatal("cache miss", calls)
	}
	for _, p := range [][2]string{{"..", "poster.jpeg"}, {"623249", "other.jpeg"}, {"1?url=evil", "poster.jpeg"}, {"0", "poster.jpeg"}} {
		if _, e = c.Open(context.Background(), p[0], p[1]); e == nil {
			t.Fatal("invalid path accepted", p)
		}
	}
}
