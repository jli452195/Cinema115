package httpapi

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestFrontendAndAPIShareServer(t *testing.T) {
	gin.SetMode(gin.TestMode)
	directory := t.TempDir()
	if err := os.Mkdir(filepath.Join(directory, "assets"), 0755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"index.html": "<html>Cinema115</html>", "assets/app.js": "console.log('cinema115')"} {
		if err := os.WriteFile(filepath.Join(directory, filepath.FromSlash(name)), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	r := gin.New()
	r.GET("/api/health", func(c *gin.Context) { c.JSON(200, gin.H{"status": "ok"}) })
	if err := RegisterFrontend(r, directory); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method, url string
		code        int
		body, cache string
	}{
		{"GET", "/", 200, "Cinema115", "no-cache"},
		{"GET", "/login", 200, "Cinema115", "no-cache"},
		{"GET", "/assets/app.js", 200, "console.log", "immutable"},
		{"GET", "/assets/missing.js", 404, "", ""},
		{"GET", "/assets/", 404, "", ""},
		{"GET", "/api/health", 200, `"status":"ok"`, ""},
		{"GET", "/api/missing", 404, `"error"`, ""},
		{"POST", "/api/missing", 404, `"error"`, ""},
		{"POST", "/", 405, `"error"`, ""},
		{"HEAD", "/", 200, "", "no-cache"},
	} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(tc.method, tc.url, nil))
		if w.Code != tc.code || !strings.Contains(w.Body.String(), tc.body) || !strings.Contains(w.Header().Get("Cache-Control"), tc.cache) {
			t.Errorf("%s %s: HTTP %d body=%s", tc.method, tc.url, w.Code, w.Body.String())
		}
		if tc.method == "HEAD" && w.Body.Len() != 0 {
			t.Error("HEAD 不应返回页面内容")
		}
		if strings.HasPrefix(tc.url, "/api/") && !strings.Contains(w.Header().Get("Content-Type"), "application/json") {
			t.Error("API 不应返回 HTML")
		}
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/assets/app.js", nil)
	req.Header.Set("Range", "bytes=0-6")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusPartialContent || w.Body.String() != "console" {
		t.Fatal("静态文件 Range 响应不正确")
	}
}

func TestFrontendRequiresBuiltIndex(t *testing.T) {
	gin.SetMode(gin.TestMode)
	if err := RegisterFrontend(gin.New(), t.TempDir()); err == nil {
		t.Fatal("前端入口缺失应报告错误")
	}
}
