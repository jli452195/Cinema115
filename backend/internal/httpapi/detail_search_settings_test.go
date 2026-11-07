package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
)

func TestDetailSearchSettingsPermissionsValidationAndPersistence(t *testing.T) {
	db, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "search.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	auth := portalTestAuth(t, db, "administrator", "admin-password", "test-secret")
	router := gin.New()
	RegisterPortalAuth(router, auth)
	RegisterDetailSearchSettings(router.Group("/api/admin", PortalAuthenticated(auth), PortalAdminOnly()), db)
	call := func(method, path, token, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		out := httptest.NewRecorder()
		router.ServeHTTP(out, req)
		if out.Code != want {
			t.Fatalf("%s %s: status=%d body=%s", method, path, out.Code, out.Body.String())
		}
		return out
	}
	login := func(path, body string) string {
		t.Helper()
		var data struct{ Token string }
		if err := json.Unmarshal(call(http.MethodPost, path, "", body, 200).Body.Bytes(), &data); err != nil || data.Token == "" {
			t.Fatal("测试登录失败", err)
		}
		return data.Token
	}
	manager := login("/api/auth/admin/login", `{"username":"administrator","password":"admin-password"}`)
	reader := login("/api/auth/login", `{"username":"reader","password":"test-password"}`)
	endpoint := "/api/admin/resources/search-settings"
	for _, method := range []string{http.MethodGet, http.MethodPut} {
		call(method, endpoint, "", "{}", 401)
		call(method, endpoint, reader, "{}", 403)
	}
	read := func() model.DetailSearchSettings {
		t.Helper()
		response := call(http.MethodGet, endpoint, manager, "", 200)
		if response.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("搜索配置响应不能缓存")
		}
		var out model.DetailSearchSettings
		if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	if !reflect.DeepEqual(read().Order, model.DefaultDetailSearchOrder()) {
		t.Fatal("未配置时没有返回默认顺序")
	}
	order := []string{"garden", "woniu", "mikan", "gying", "channels", "bot", "re0"}
	raw, err := json.Marshal(model.DetailSearchSettings{Order: order})
	if err != nil {
		t.Fatal(err)
	}
	call(http.MethodPut, endpoint, manager, string(raw), 200)
	if !reflect.DeepEqual(read().Order, order) {
		t.Fatal("保存后读取顺序不一致")
	}
	for _, input := range []string{
		`{}`, `{"order":null}`, `{"order":[]}`, `{"order":["garden"]}`,
		`{"order":["garden","woniu","mikan","gying","channels","bot","bot"]}`,
		`{"order":["garden","woniu","mikan","gying","channels","bot","unknown"]}`,
		`{"order":"re0"}`, `{"order":[],"autoSearch":true}`, `{} {}`,
	} {
		call(http.MethodPut, endpoint, manager, input, 400)
		if !reflect.DeepEqual(read().Order, order) {
			t.Fatal("无效输入覆盖了已保存的顺序")
		}
	}
}
