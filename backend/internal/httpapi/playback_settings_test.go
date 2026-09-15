package httpapi

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/service"
)

func TestPlaybackAddressUsesSavedWebConfigAndRequestFallback(t *testing.T) {
	t.Setenv("PUBLIC_BASE_URL", "http://obsolete-env:3002")
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	store, err := service.NewAutomationStore(filepath.Join(dir, "automation.json"), dir, "")
	if err != nil {
		t.Fatal(err)
	}
	api := NewShareAPI("secret", func() string { return "selected-cookie" })
	api.BaseURL = func() string { return store.Value().Generation.BaseURL }
	api.OutputRoot = dir
	r := gin.New()
	admin := r.Group("/api/admin")
	api.Register(r, admin)
	RegisterAutomation(admin, store, func() bool { return false })
	call := func(method, path, body string) map[string]any {
		t.Helper()
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(method, "https://nas.example"+path, strings.NewReader(body)))
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		var data map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
			t.Fatal(err)
		}
		return data
	}
	if got := call("GET", "/api/admin/p115/share/generation-config", "")["baseUrl"]; got != "" {
		t.Fatal("未保存时不应读取环境变量", got)
	}
	input := `{"url":"https://115.com/s/source","fileId":"1","name":"电影.mkv"}`
	if got := call("POST", "/api/admin/p115/share/strm", input)["content"].(string); !strings.HasPrefix(got, "https://nas.example/api/") {
		t.Fatal("未使用请求地址回退", got)
	}
	for _, base := range []string{"http://nas:8115", "https://cinema.example/media"} {
		settings := store.Value()
		settings.Generation.BaseURL = base
		raw, err := json.Marshal(settings)
		if err != nil {
			t.Fatal(err)
		}
		call("POST", "/api/admin/p115/share/automation-config", string(raw))
		if got := call("GET", "/api/admin/p115/share/generation-config", "")["baseUrl"]; got != base {
			t.Fatal("保存的地址没有立即生效", got)
		}
		if got := call("POST", "/api/admin/p115/share/strm", input)["content"].(string); !strings.HasPrefix(got, base+"/api/") {
			t.Fatal("单文件生成没有使用新地址", got)
		}
	}
	reopened, err := service.NewAutomationStore(filepath.Join(dir, "automation.json"), dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Value().Generation.BaseURL != "https://cinema.example/media" {
		t.Fatal("重启丢失网页播放地址")
	}
}
