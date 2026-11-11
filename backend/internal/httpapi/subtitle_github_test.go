package httpapi

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/client/subshare"
	"strm115/backend/internal/service"
)

func TestSubtitleGitHubTokenConfig(t *testing.T) {
	const token = "github_pat_test_only_1234567890"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		fmt.Fprintf(w, `{"object":{"sha":%q}}`, strings.Repeat("a", 40))
	}))
	defer upstream.Close()
	filename := filepath.Join(t.TempDir(), "github.enc")
	open := func() (*service.SubtitleGitHubSettings, *gin.Engine) {
		t.Helper()
		store, err := service.NewSessionStore(filename, "test-secret")
		if err != nil {
			t.Fatal(err)
		}
		client := subshare.New("")
		client.API = upstream.URL + "/api"
		settings, err := service.NewSubtitleGitHubSettings(store, client)
		if err != nil {
			t.Fatal(err)
		}
		router := gin.New()
		RegisterSubtitleGitHubSettings(router.Group("/api/admin"), settings)
		return settings, router
	}
	settings, router := open()
	request := func(method, body string, want int, configured bool) {
		t.Helper()
		req := httptest.NewRequest(method, "/api/admin/subtitles/github/config", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		if response.Code != want || settings.View().Configured != configured || strings.Contains(response.Body.String(), token) || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("配置结果不正确：HTTP %d, %s", response.Code, response.Body.String())
		}
		if want == http.StatusOK && strings.TrimSpace(response.Body.String()) != fmt.Sprintf(`{"configured":%t}`, configured) {
			t.Fatal("配置接口只能返回配置状态", response.Body.String())
		}
	}
	request(http.MethodGet, "", http.StatusOK, false)
	request(http.MethodPost, `{"token":"`+token+`"}`, http.StatusOK, true)
	disk, err := os.ReadFile(filename)
	if err != nil || bytes.Contains(disk, []byte(token)) {
		t.Fatal("Token 应加密保存", err)
	}
	request(http.MethodPost, `{"token":"github_pat_invalid_test_12345"}`, http.StatusConflict, true)
	request(http.MethodPost, `{"token":"short"}`, http.StatusBadRequest, true)
	request(http.MethodPost, `{"token":"`+token+`","extra":true}`, http.StatusBadRequest, true)
	settings, router = open()
	request(http.MethodGet, "", http.StatusOK, true)
	request(http.MethodDelete, "", http.StatusOK, false)
	settings, router = open()
	request(http.MethodGet, "", http.StatusOK, false)
}
