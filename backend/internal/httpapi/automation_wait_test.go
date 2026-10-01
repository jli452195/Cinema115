package httpapi

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/service"
)

func TestAutomationBatchWaitDefaultValidationAndPersistence(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	path := filepath.Join(dir, "automation.json")
	// 模拟升级前没有等待字段的配置。
	if err := os.WriteFile(path, []byte(`{"parentCID":"0","generation":{"baseUrl":"http://nas:3002","outputDir":"strm"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	store, err := service.NewAutomationStore(path, dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if store.Value().TGBatchWaitSeconds != 5 {
		t.Fatal("旧配置未采用默认 5 秒")
	}
	r := gin.New()
	RegisterAutomation(r.Group("/api/admin"), store, func() bool { return false })
	for _, tc := range []struct {
		body       string
		code, want int
	}{
		{`{"tgBatchWaitSeconds":12}`, 200, 12},
		{`{"parentCID":"0"}`, 200, 12},
		{`{"tgBatchWaitSeconds":0}`, 200, 0},
		{`{"tgBatchWaitSeconds":-1}`, 400, 0},
		{`{"tgBatchWaitSeconds":301}`, 400, 0},
		{`{"tgBatchWaitSeconds":1.5}`, 400, 0},
		{`{"tgBatchWaitSeconds":"5"}`, 400, 0},
		{`{"tgBatchWaitSeconds":300}`, 200, 300},
	} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("POST", "/api/admin/p115/share/automation-config", strings.NewReader(tc.body)))
		if w.Code != tc.code || store.Value().TGBatchWaitSeconds != tc.want {
			t.Fatal("等待配置保存或校验错误", tc.body, w.Code)
		}
		reloaded, err := service.NewAutomationStore(path, dir, "")
		if err != nil || reloaded.Value().TGBatchWaitSeconds != tc.want {
			t.Fatal("等待配置未持久化", err)
		}
		if tc.code == 200 {
			var response struct {
				Settings service.AutomationSettings `json:"settings"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || response.Settings.TGBatchWaitSeconds != tc.want {
				t.Fatal("返回配置不正确", err)
			}
		}
	}
}

func TestAutomationIgnoresRemovedParallelLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	path := filepath.Join(dir, "automation.json")
	if err := os.WriteFile(path, []byte(`{"maxParallelBatches":1,"parentCID":"0","generation":{"baseUrl":"http://nas:3002","outputDir":"strm"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	store, err := service.NewAutomationStore(path, dir, "")
	if err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	RegisterAutomation(r.Group("/api/admin"), store, func() bool { return false })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", "/api/admin/p115/share/automation-config", strings.NewReader(`{"maxParallelBatches":1}`)))
	if w.Code != 200 || strings.Contains(w.Body.String(), "maxParallelBatches") {
		t.Fatalf("removed limit returned: %d %s", w.Code, w.Body.String())
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "maxParallelBatches") {
		t.Fatal("removed limit persisted")
	}
}
