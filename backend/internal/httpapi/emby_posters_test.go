package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/client/imagecache"
	"strm115/backend/internal/model"
	"strm115/backend/internal/service"
	"strm115/backend/internal/storage"
)

func TestEmbyPosterHTTPPrivateCacheAndAuthorization(t *testing.T) {
	var imageCalls, missingCalls atomic.Int32
	image := []byte("\x89PNG\r\n\x1a\nthumbnail")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/emby/System/Info":
			_, _ = w.Write([]byte(`{"Id":"server","ServerName":"Test"}`))
		case "/emby/Library/VirtualFolders":
			_, _ = w.Write([]byte(`[{"ItemId":"lib","Name":"TV"}]`))
		case "/emby/Items/series/Images/Primary":
			imageCalls.Add(1)
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(image)
		case "/emby/Items/missing/Images/Primary":
			missingCalls.Add(1)
			w.WriteHeader(http.StatusNotFound)
		default:
			t.Error("unexpected upstream request", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer upstream.Close()
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	secrets, err := service.NewSessionStore(filepath.Join(t.TempDir(), "emby.enc"), "secret")
	if err != nil {
		t.Fatal(err)
	}
	s, err := service.NewEmbyService(secrets, db)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Save(ctx, service.EmbyConfig{URL: upstream.URL, APIKey: "private-key", LibraryIDs: []string{"lib"}}); err != nil {
		t.Fatal(err)
	}
	state, err := s.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.EmbyStage(ctx, state.Scope, "published", []model.EmbyItem{{ID: "series", Type: "Series"}, {ID: "missing", Type: "Series"}}); err != nil {
		t.Fatal(err)
	}
	if err := db.EmbyPublish(ctx, state.Scope, "published", 0); err != nil {
		t.Fatal(err)
	}
	s.Posters, err = imagecache.NewEmby(t.TempDir(), s.PosterRequest)
	if err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	admin := r.Group("/api/admin", func(c *gin.Context) {
		if c.GetHeader("Authorization") != "Bearer admin" {
			c.AbortWithStatus(http.StatusUnauthorized)
		}
	})
	RegisterEmby(r, admin, s)
	call := func(method, path, token, etag string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if etag != "" {
			req.Header.Set("If-None-Match", etag)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	path := imagecache.EmbyPosterURL(state.Scope, "series")
	if w := call("GET", path, "", ""); w.Code != http.StatusUnauthorized || imageCalls.Load() != 0 {
		t.Fatal("unauthenticated image request reached Emby", w.Code)
	}
	w := call("GET", path, "admin", "")
	if w.Code != http.StatusOK || w.Body.String() != string(image) || w.Header().Get("Cache-Control") != "private, max-age=86400" || w.Header().Get("Vary") != "Authorization" || strings.Contains(w.Body.String(), "private-key") {
		t.Fatal("poster response or private cache headers invalid", w.Code, w.Header())
	}
	etag := w.Header().Get("ETag")
	if etag == "" {
		t.Fatal("poster has no content validator")
	}
	if w := call("GET", path, "admin", etag); w.Code != http.StatusNotModified || w.Body.Len() != 0 {
		t.Fatal("conditional poster request sent image bytes", w.Code)
	}
	if w := call("HEAD", path, "admin", ""); w.Code != http.StatusOK || w.Body.Len() != 0 {
		t.Fatal("HEAD returned image bytes", w.Code)
	}
	for i := 0; i < 2; i++ {
		if w := call("GET", imagecache.EmbyPosterURL(state.Scope, "missing"), "admin", ""); w.Code != http.StatusNotFound || w.Header().Get("Cache-Control") != "private, max-age=3600" {
			t.Fatal("missing posters must have a bounded private cache", w.Code, w.Header())
		}
	}
	if imageCalls.Load() != 1 || missingCalls.Load() != 1 {
		t.Fatal("repeat image requests reached Emby", imageCalls.Load(), missingCalls.Load())
	}
	if w := call("GET", "/api/admin/emby/posters/invalid/series", "admin", ""); w.Code != http.StatusBadRequest {
		t.Fatal("invalid scope accepted", w.Code)
	}
}
