package httpapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/model"
	"strm115/backend/internal/service"
	"strm115/backend/internal/storage"
)

type followSettingsAccountFixture struct{}

func (followSettingsAccountFixture) ValidateSelection(id string) error {
	if id != "follow-account" {
		return service.ErrAccountMissing
	}
	return nil
}

func (followSettingsAccountFixture) CookieFor(id string) string {
	if id == "follow-account" {
		return "UID=42_A; SEID=test"
	}
	return ""
}

func TestFollowAdminPermissionsAndUserVisibleNote(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	auth := service.NewWebsiteAuth(db, "admin", "admin-password", "secret")
	u, err := auth.Create(ctx, "alice", "test-password")
	if err != nil {
		t.Fatal(err)
	}
	userToken, _, err := auth.Login(ctx, "alice", "test-password")
	if err != nil {
		t.Fatal(err)
	}
	adminToken, _, err := auth.Login(ctx, "admin", "admin-password")
	if err != nil {
		t.Fatal(err)
	}
	automation, err := service.NewAutomationStore(filepath.Join(t.TempDir(), "automation.json"), t.TempDir(), "http://example.com")
	if err != nil {
		t.Fatal(err)
	}
	automation.ValidateAccount = followSettingsAccountFixture{}.ValidateSelection
	s := &service.FollowService{Store: db, TMDB: &followTMDBFixture{}, Library: &followLibraryFixture{}, GenerationSettings: automation}
	if err := db.Subscribe(ctx, u.ID, model.Follow{Kind: "tv", TMDBID: 100, Title: "标题"}); err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	RegisterFollowAdmin(r.Group("/api/admin", WebsiteAuthenticated(auth), WebsiteAdminOnly()), s)
	RegisterFollows(r.Group("/api/me", WebsiteAuthenticated(auth)), s)
	call := func(method, path, token string, body any, want int) map[string]any {
		t.Helper()
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest(method, path, strings.NewReader(string(raw)))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != want {
			t.Fatalf("%s %s=%d want %d: %s", method, path, w.Code, want, w.Body.String())
		}
		var data map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
			t.Fatal(err)
		}
		return data
	}
	for _, token := range []string{"", userToken} {
		want := 401
		if token != "" {
			want = 403
		}
		call("GET", "/api/admin/follows", token, nil, want)
		call("GET", "/api/admin/follows/settings", token, nil, want)
		call("POST", "/api/admin/follows/tv/100", token, map[string]any{"status": "processing", "revision": 2}, want)
	}
	list := call("GET", "/api/admin/follows?q=100", adminToken, nil, 200)["items"].([]any)
	if len(list) != 1 {
		t.Fatal("没有读取申请")
	}
	revision := list[0].(map[string]any)["revision"]
	call("POST", "/api/admin/follows/tv/100", adminToken, map[string]any{"status": "completed", "revision": revision}, 400)
	call("POST", "/api/admin/follows/tv/100", adminToken, map[string]any{"status": "processing", "note": "已开始寻找资源", "revision": revision}, 200)
	call("POST", "/api/admin/follows/tv/100", adminToken, map[string]any{"status": "pending", "revision": revision}, 409)
	state := call("GET", "/api/me/follows/tv/100", userToken, nil, 200)["request"].(map[string]any)
	if state["status"] != "processing" || state["note"] != "已开始寻找资源" {
		t.Fatal("用户状态未更新", state)
	}
	list = call("GET", "/api/admin/follows?q=100", adminToken, nil, 200)["items"].([]any)
	revision = list[0].(map[string]any)["revision"]
	call("POST", "/api/admin/follows/tv/100", adminToken, map[string]any{"status": "closed", "revision": revision}, 400)
	call("POST", "/api/admin/follows/tv/100", adminToken, map[string]any{"status": "closed", "note": "暂未找到可用资源", "revision": revision}, 200)
	closed := call("GET", "/api/admin/follows?status=closed", adminToken, nil, 200)["items"].([]any)
	if len(closed) != 1 || closed[0].(map[string]any)["status"] != "closed" {
		t.Fatal("管理员已关闭筛选错误", closed)
	}
	state = call("GET", "/api/me/follows/tv/100", userToken, nil, 200)["request"].(map[string]any)
	if state["status"] != "closed" || state["note"] != "暂未找到可用资源" {
		t.Fatal("用户未看到关闭原因", state)
	}
	userClosed := call("GET", "/api/me/follows?status=closed", userToken, nil, 200)["items"].([]any)
	if len(userClosed) != 1 || userClosed[0].(map[string]any)["status"] != "closed" {
		t.Fatal("用户已关闭筛选错误", userClosed)
	}
	notifications, err := db.WebsiteNotifications(ctx, u.ID, 0)
	if err != nil || len(notifications) != 1 || notifications[0].Message != "追更已关闭：暂未找到可用资源" {
		t.Fatal("关闭通知错误", notifications, err)
	}
	for _, value := range []string{"javascript:alert(1)", "https://user:password@example.com", "https://example.com/path", "https://example.com/?token=secret"} {
		call("POST", "/api/admin/follows/settings", adminToken, map[string]any{"baseUrl": value}, 400)
	}
	call("POST", "/api/admin/follows/settings", adminToken, map[string]any{"baseUrl": "http://192.168.0.24:5173/"}, 200)
	settings := call("GET", "/api/admin/follows/settings", adminToken, nil, 200)
	if settings["baseUrl"] != "http://192.168.0.24:5173" || settings["notificationsConfigured"] != false {
		t.Fatal("通知配置状态错误", settings)
	}
	call("POST", "/api/admin/follows/settings", adminToken, map[string]any{"autoEnabled": true}, 400)
	automationSettings := automation.Value()
	automationSettings.AccountID = "follow-account"
	if err := automation.Save(automationSettings); err != nil {
		t.Fatal(err)
	}
	call("POST", "/api/admin/follows/settings", adminToken, map[string]any{"baseUrl": "http://192.168.0.24:5173", "autoEnabled": true}, 200)
	settings = call("GET", "/api/admin/follows/settings", adminToken, nil, 200)
	if settings["autoEnabled"] != true {
		t.Fatal("追更自动搜索开关未保存", settings)
	}
	call("POST", "/api/admin/follows/settings", adminToken, map[string]any{"autoEnabled": false}, 200)
	settings = call("GET", "/api/admin/follows/settings", adminToken, nil, 200)
	if settings["autoEnabled"] != false {
		t.Fatal("关闭自动搜索失败", settings)
	}
}
