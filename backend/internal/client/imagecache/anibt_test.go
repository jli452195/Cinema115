package imagecache

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAnibtCoverDerivativeAndBoundary(t *testing.T) {
	hash := strings.Repeat("a", 64)
	raw := "https://r2.anibt.net/anime_covers/native_123/" + hash + ".jpg"
	expected := "/api/anibt/image/native_123/" + hash + ".webp"
	if got := AnibtCoverURL(raw); got != expected {
		t.Fatal(got)
	}
	for _, bad := range []string{strings.Replace(raw, "r2.anibt.net", "evil.test", 1), raw + "?url=x", strings.Replace(raw, "https://", "http://", 1), strings.Replace(raw, "native_123", "..", 1), raw + "#x"} {
		if AnibtCoverURL(bad) != "" {
			t.Fatal(bad)
		}
	}
	body := testPNG(t)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/anime_covers/native_123/"+hash+"/v4/w240.webp" {
			t.Error(r.URL.Path)
		}
		w.Header().Set("Content-Type", "image/png")
		w.Write(body)
	}))
	defer server.Close()
	cache, e := NewAnibt(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	cache.baseURL = server.URL
	first, e := cache.Open(context.Background(), "native_123", hash+".webp")
	if e != nil {
		t.Fatal(e)
	}
	first.File.Close()
	second, e := cache.Open(context.Background(), "native_123", hash+".webp")
	if e != nil || calls != 1 {
		t.Fatal("cache missed", calls, e)
	}
	second.File.Close()
	if _, e = cache.Open(context.Background(), "..", "secret"); e == nil {
		t.Fatal("invalid path accepted")
	}
}
