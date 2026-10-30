package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/netproxy"
	"strm115/backend/internal/service"
)

func TestNetworkProxyAPISettingsAndRejectedTestsDoNotExposeCredentials(t *testing.T) {
	store, err := service.NewSessionStore(filepath.Join(t.TempDir(), "proxy.enc"), "test-key")
	if err != nil {
		t.Fatal(err)
	}
	runtime := netproxy.New()
	t.Cleanup(runtime.CloseIdleConnections)
	s, err := service.NewNetworkProxy(store, runtime)
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	RegisterNetworkProxy(router.Group("/api/admin"), s)
	request := func(method, path, body string, status int) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		req := httptest.NewRequest(method, "/api/admin/network-proxy"+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, req)
		if w.Code != status || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("unexpected response %d: %s", w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "private-password") {
			t.Fatal("API response exposed proxy password")
		}
		return w
	}
	request(http.MethodGet, "", "", http.StatusOK)
	request(http.MethodPost, "", `{"enabled":true,"url":"http://proxy.example:7890","username":"user","password":"private-password","services":["tmdb","github"]}`, http.StatusOK)
	w := request(http.MethodGet, "", "", http.StatusOK)
	var view service.NetworkProxyView
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil || !view.Enabled || !view.PasswordConfigured {
		t.Fatal("saved settings not returned", err)
	}
	request(http.MethodPost, "/test", `{"service":"115","enabled":true,"url":"http://proxy.example:7890","services":["tmdb"]}`, http.StatusBadRequest)
	request(http.MethodPost, "", `{"enabled":true,"url":"http://proxy.example:7890","services":["115"]}`, http.StatusBadRequest)
	request(http.MethodPost, "", `{`, http.StatusBadRequest)
	request(http.MethodPost, "", `{"url":"`+strings.Repeat("a", 17000)+`"}`, http.StatusBadRequest)
	request(http.MethodPost, "", `{"enabled":false,"url":"http://proxy.example:7890","username":"user","services":["tmdb"]}`, http.StatusOK)
	if s.View().Enabled || !s.View().PasswordConfigured {
		t.Fatal("disabling proxy changed saved credentials")
	}
}
