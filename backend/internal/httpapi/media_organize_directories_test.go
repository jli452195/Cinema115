package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/service"
)

func TestMediaOrganizeLocalDirectoriesAPI(t *testing.T) {
	root := t.TempDir()
	name := "来源 # 中文目录"
	if err := os.Mkdir(filepath.Join(root, name), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "video.strm"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	admin := router.Group("/api/admin", func(c *gin.Context) {
		if c.GetHeader("Authorization") != "Bearer admin-test" {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		c.Next()
	})
	RegisterMediaOrganize(admin, &service.MediaOrganizer{})
	request := func(path, token string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "/api/admin/media-organize/directories?path="+url.QueryEscape(path), nil)
		if token != "" {
			r.Header.Set("Authorization", token)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	if w := request(root, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("directory browsing must use admin authentication: %d", w.Code)
	}
	w := request(root, "Bearer admin-test")
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("directory response: %d %s", w.Code, w.Body)
	}
	var out service.MediaLocalDirectories
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || len(out.Directories) != 1 || out.Directories[0].Name != name || !filepath.IsAbs(out.Path) || out.ParentPath == nil {
		t.Fatal("unexpected directory response", out, err)
	}
	if w = request(out.Directories[0].Path, "Bearer admin-test"); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"directories":[]`) {
		t.Fatalf("encoded path or empty directory contract: %d %s", w.Code, w.Body)
	}
	for _, path := range []string{"relative/path", filepath.Join(root, "not-mounted"), filepath.Join(root, "video.strm")} {
		w = request(path, "Bearer admin-test")
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), `"error":`) || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("invalid directory response: %d %s", w.Code, w.Body)
		}
	}
}
