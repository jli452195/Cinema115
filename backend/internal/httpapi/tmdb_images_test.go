package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/client/tmdbimage"
)

type imageStoreFake struct{ path string }

func (f imageStoreFake) Open(context.Context, string, string) (*tmdbimage.Asset, error) {
	file, err := os.Open(f.path)
	if err != nil {
		return nil, err
	}
	return &tmdbimage.Asset{File: file, Size: 5, ModTime: time.Unix(100, 0), ContentType: "image/jpeg", ETag: `"image-etag"`}, nil
}

func TestTMDBImageRouteCachesInBrowser(t *testing.T) {
	gin.SetMode(gin.TestMode)
	path := t.TempDir() + "/image.jpg"
	if err := os.WriteFile(path, []byte("image"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	RegisterTMDBImages(r, imageStoreFake{path: path})
	for _, tc := range []struct {
		method, etag string
		status       int
	}{
		{http.MethodGet, "", http.StatusOK},
		{http.MethodGet, `"image-etag"`, http.StatusNotModified},
		{http.MethodHead, "", http.StatusOK},
	} {
		request := httptest.NewRequest(tc.method, "/api/tmdb/image/w342/poster.jpg", nil)
		request.Header.Set("If-None-Match", tc.etag)
		response := httptest.NewRecorder()
		r.ServeHTTP(response, request)
		if response.Code != tc.status {
			t.Fatalf("%s: HTTP %d", tc.method, response.Code)
		}
		if response.Header().Get("ETag") != `"image-etag"` || response.Header().Get("Cache-Control") != "public, max-age=86400, stale-while-revalidate=604800" {
			t.Fatal("图片浏览器缓存响应头错误", response.Header())
		}
		if tc.method == http.MethodGet && tc.status == http.StatusOK && response.Body.String() != "image" {
			t.Fatal("图片响应内容错误")
		}
	}
}
