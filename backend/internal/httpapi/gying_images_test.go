package httpapi

import (
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/client/imagecache"
)

func TestGyingImageRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	path := t.TempDir() + "/image.webp"
	if err := os.WriteFile(path, []byte("image"), 0600); err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	RegisterGyingImages(r, imageStoreFake{path: path})
	for _, tc := range []struct {
		method, etag string
		status       int
	}{{"GET", "", 200}, {"HEAD", "", 200}, {"GET", `"image-etag"`, 304}} {
		request := httptest.NewRequest(tc.method, "/api/gying/image/ac/P8KR.webp", nil)
		request.Header.Set("If-None-Match", tc.etag)
		response := httptest.NewRecorder()
		r.ServeHTTP(response, request)
		if response.Code != tc.status || response.Header().Get("Cache-Control") != "public, max-age=86400, stale-while-revalidate=604800" || response.Header().Get("X-Content-Type-Options") != "nosniff" || response.Header().Get("Cross-Origin-Resource-Policy") != "same-origin" {
			t.Fatal(response.Code, response.Header())
		}
		if tc.method == "HEAD" && response.Body.Len() != 0 {
			t.Fatal("HEAD 不应返回正文")
		}
	}
	cache, err := imagecache.NewGying(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r = gin.New()
	RegisterGyingImages(r, cache)
	for _, path := range []string{"/api/gying/image/other/a.webp", "/api/gying/image/ac/a.svg", "/api/gying/image/ac/a.jpg", "/api/gying/image/ac/https:evil.webp"} {
		response := httptest.NewRecorder()
		r.ServeHTTP(response, httptest.NewRequest("GET", path, nil))
		if response.Code != 400 || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatal(path, response.Code)
		}
	}
}
