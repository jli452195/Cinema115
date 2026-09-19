package httpapi

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/client/imagecache"
)

func TestMikanImageRouteAndBrowserCache(t *testing.T) {
	gin.SetMode(gin.TestMode)
	path := t.TempDir() + "/poster.jpg"
	if err := os.WriteFile(path, []byte("image"), 0600); err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	RegisterMikanImages(r, imageStoreFake{path: path})
	for _, tc := range []struct {
		method, etag string
		status       int
	}{{"GET", "", 200}, {"GET", `"image-etag"`, 304}, {"HEAD", "", 200}} {
		request := httptest.NewRequest(tc.method, "/api/mikan/image/201904/poster.jpg", nil)
		request.Header.Set("If-None-Match", tc.etag)
		response := httptest.NewRecorder()
		r.ServeHTTP(response, request)
		if response.Code != tc.status || response.Header().Get("Cache-Control") != "public, max-age=86400, stale-while-revalidate=604800" || response.Header().Get("Cross-Origin-Resource-Policy") != "same-origin" || response.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatal(response.Code, response.Header())
		}
		if tc.method == "HEAD" && response.Body.Len() != 0 {
			t.Fatal("HEAD 不应包含响应正文")
		}
	}
}

func TestMikanImageRouteRejectsUnsafePaths(t *testing.T) {
	cache, err := imagecache.NewMikan(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	RegisterMikanImages(r, cache)
	for _, path := range []string{"/api/mikan/image/201913/a.jpg", "/api/mikan/image/201904/a.svg", "/api/mikan/image/w342/a.jpg", "/api/mikan/image/201904/https:evil.jpg"} {
		response := httptest.NewRecorder()
		r.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != 400 || response.Header().Get("Cache-Control") != "no-store" || response.Body.Len() != 0 {
			t.Fatal(path, response.Code, response.Body.String())
		}
	}
}
