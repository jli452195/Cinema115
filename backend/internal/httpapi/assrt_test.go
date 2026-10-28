package httpapi

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/service"
)

func TestAssrtHTTPConfigRedactsTokenAndKeepsLibraryRoutes(t *testing.T) {
	store, err := service.NewSessionStore(filepath.Join(t.TempDir(), "subtitle.enc"), "secret")
	if err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("a", 32)
	if err := store.Save(token); err != nil {
		t.Fatal(err)
	}
	s, err := service.NewAssrtService(store)
	if err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	admin := r.Group("/api/admin")
	RegisterSubtitleLibrary(admin, nil)
	RegisterAssrt(admin, s)
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{
		{http.MethodGet, "/api/admin/subtitles/config", "", 200},
		{http.MethodPost, "/api/admin/subtitles/assrt/config", `{"token":"short"}`, 409},
		{http.MethodGet, "/api/admin/subtitles/assrt/search?pos=invalid&q=Show", "", 400},
		{http.MethodGet, "/api/admin/subtitles/assrt/not-id/download", "", 400},
		{http.MethodPost, "/api/admin/subtitles/assrt/config", `{"token":"` + strings.Repeat("a", 5000) + `"}`, 400},
		{http.MethodDelete, "/api/admin/subtitles/assrt/config", "", 200},
		{http.MethodGet, "/api/admin/subtitles/assrt/search?q=Show", "", 200},
	} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		if w.Code != tc.status || strings.Contains(w.Body.String(), token) || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal(tc.path, w.Code, w.Body.String())
		}
	}
}
