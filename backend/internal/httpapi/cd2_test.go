package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/service"
)

func TestCD2ConfigurationAPI(t *testing.T) {
	store, err := service.NewSessionStore(filepath.Join(t.TempDir(), "cd2.enc"), "test-key")
	if err != nil {
		t.Fatal(err)
	}
	s, err := service.NewCD2Connections(store)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	router := gin.New()
	RegisterCD2(router.Group("/api/admin"), s)
	request := func(method, endpoint, body string, expected int) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		req := httptest.NewRequest(method, "/api/admin/cd2"+endpoint, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, req)
		if w.Code != expected || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("HTTP %d: %s", w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "private-cd2-token") {
			t.Fatal("管理接口回显了Token")
		}
		return w
	}
	request(http.MethodGet, "", "", http.StatusOK)
	request(http.MethodPost, "", `{"enabled":true,"url":"http://cd2.example:19798","token":"private-cd2-token"}`, http.StatusOK)
	w := request(http.MethodGet, "", "", http.StatusOK)
	var view service.CD2View
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil || !view.Enabled || !view.TokenConfigured {
		t.Fatal(view, err)
	}
	request(http.MethodPost, "", `{"enabled":false,"url":"http://cd2.example:19798"}`, http.StatusOK)
	request(http.MethodGet, "/directories?path=/", "", http.StatusConflict)
	request(http.MethodGet, "/directories?revision=invalid", "", http.StatusBadRequest)
	request(http.MethodPost, "/test", `{"url":""}`, http.StatusBadRequest)
	request(http.MethodPost, "", `{`, http.StatusBadRequest)
	request(http.MethodPost, "", `{"url":"http://cd2.example:19798/path"}`, http.StatusBadRequest)
}
