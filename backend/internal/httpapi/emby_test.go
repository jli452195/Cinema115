package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/service"
	"strm115/backend/internal/storage"
)

func TestEmbyHTTPWebhookCharsetAndAdminBoundary(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/emby/System/Info":
			w.Write([]byte(`{"Id":"server","ServerName":"测试 Emby"}`))
		case "/emby/Library/VirtualFolders":
			w.Write([]byte(`[{"ItemId":"lib","Name":"电影"}]`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer upstream.Close()
	db, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
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
	if err := s.Save(context.Background(), service.EmbyConfig{URL: upstream.URL, APIKey: "private-key", LibraryIDs: []string{"lib"}}); err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	admin := r.Group("/api/admin")
	admin.Use(func(c *gin.Context) {
		if c.GetHeader("Authorization") != "Bearer test-admin" {
			c.AbortWithStatus(401)
			return
		}
		c.Next()
	})
	RegisterEmby(r, admin, s)
	call := func(method, path, body string, authorized bool) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
		if authorized {
			req.Header.Set("Authorization", "Bearer test-admin")
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	if w := call("GET", "/api/admin/emby/config", "", false); w.Code != 401 {
		t.Fatal("管理员接口未鉴权", w.Code)
	}
	w := call("GET", "/api/admin/emby/config", "", true)
	if w.Code != 200 || strings.Contains(w.Body.String(), "private-key") || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("配置泄露或允许缓存")
	}
	var config service.EmbyConfigView
	if err := json.Unmarshal(w.Body.Bytes(), &config); err != nil {
		t.Fatal(err)
	}
	if w := call("POST", "/api/admin/emby/webhook-config", `{"baseUrl":"http://192.168.0.24:3002"}`, false); w.Code != 401 {
		t.Fatal("地址保存接口未鉴权")
	}
	if w := call("POST", "/api/admin/emby/webhook-config", `{"baseUrl":"http://192.168.0.24:3002"}`, true); w.Code != 200 || !strings.Contains(w.Body.String(), `"webhookBaseUrl":"http://192.168.0.24:3002"`) || strings.Contains(w.Body.String(), "private-key") {
		t.Fatal("地址未保存或泄露 API Key")
	}
	if w := call("POST", config.WebhookPath, `{"Event":"system.webhooktest","Server":{"Id":"server"}}`, false); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	parsed, _ := url.Parse(config.WebhookPath)
	if w := call("POST", parsed.Path, `{}`, false); w.Code != 401 {
		t.Fatal("外部接口未检查专用密钥")
	}
	if w := call("POST", config.WebhookPath, `{"Event":"library.new","Server":{"Id":"server"},"Item":{"Id":9007199254740993}}`, false); w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := call("POST", config.WebhookPath, strings.Repeat("x", 256*1024+1), false); w.Code != 413 {
		t.Fatal("请求体大小限制无效", w.Code)
	}
	if w := call("POST", "/api/admin/emby/library-check", `{"items":[{"kind":"tv","tmdbId":"1e3"}]}`, true); w.Code != 400 {
		t.Fatal("TMDB ID 校验无效")
	}
	for _, path := range []string{"/api/admin/emby/versions/tv/100", "/api/admin/emby/versions/tv/100?season=-1&episode=2", "/api/admin/emby/versions/tv/100?season=1&episode=x", "/api/admin/emby/versions/movie/100?offset=-1"} {
		if w := call("GET", path, "", true); w.Code != 400 {
			t.Fatal("媒体版本参数校验无效", path, w.Code)
		}
	}
}
