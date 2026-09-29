package httpapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/model"
	"strm115/backend/internal/service"
	"strm115/backend/internal/storage"
)

func TestFollowAdminCaughtUpClassification(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Subscribe(ctx, "alice", model.Follow{Kind: "tv", TMDBID: 282326, Title: "兰香如故"}); err != nil {
		t.Fatal(err)
	}
	items, err := db.AdminFollows(ctx, "tv", "pending", "", 0)
	if err != nil || len(items) != 1 {
		t.Fatal(items, err)
	}
	if err := db.UpdateFollow(ctx, "tv", 282326, "processing", "资源待确认", items[0].Revision); err != nil {
		t.Fatal(err)
	}
	jobs, err := db.DueFollowChecks(ctx, time.Now().Unix())
	if err != nil || len(jobs) != 1 {
		t.Fatal(jobs, err)
	}
	if err := db.SaveFollowCheck(ctx, jobs[0], model.FollowProgress{State: "tracking", Aired: 28, Owned: 28, CheckedAt: time.Now().Unix()}, time.Now().Add(time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	RegisterFollowAdmin(r.Group("/api/admin"), &service.FollowService{Store: db, TMDB: &followTMDBFixture{}, Library: &followLibraryFixture{}})
	for _, status := range []string{"caught_up", "pending", "processing"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", "/api/admin/follows?status="+status, nil))
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		var response struct {
			Items   []model.AdminFollow `json:"items"`
			Counts  map[string]int      `json:"counts"`
			HasMore bool                `json:"hasMore"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if response.HasMore || response.Counts["caught_up"] != 1 || response.Counts["processing"] != 0 || response.Counts["pending"] != 0 {
			t.Fatalf("counts/pagination: %+v", response)
		}
		if status != "caught_up" {
			if len(response.Items) != 0 {
				t.Fatal("追平不得重复归入待处理或处理中", response.Items)
			}
			continue
		}
		if len(response.Items) != 1 || response.Items[0].Status != "processing" || response.Items[0].EffectiveStatus != "caught_up" || response.Items[0].Progress == nil || response.Items[0].Progress.State != "tracking" {
			t.Fatalf("追平展示不应修改原始状态: %+v", response.Items)
		}
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", "/api/admin/follows/tv/282326", strings.NewReader(`{"status":"caught_up","revision":1}`)))
	if w.Code != 400 {
		t.Fatal("禁止手动写入已追平", w.Code, w.Body.String())
	}
}

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
	auth := portalTestAuth(t, db, "admin", "admin-password", "secret")
	u := portalTestUser(t, auth, "alice", "test-password")
	userToken, _, err := auth.Login(ctx, "alice", "test-password")
	if err != nil {
		t.Fatal(err)
	}
	adminToken, _, err := auth.LoginAdmin(ctx, "admin", "admin-password")
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
	RegisterFollowAdmin(r.Group("/api/admin", PortalAuthenticated(auth), PortalAdminOnly()), s)
	RegisterFollows(r.Group("/api/me", PortalAuthenticated(auth)), s)
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
	s.ValidateBot = func(id string, requireNotification bool) error {
		if id != "follow-bot" || !requireNotification {
			t.Fatalf("Bot 绑定校验参数错误：%q %v", id, requireNotification)
		}
		return nil
	}
	if err := db.SaveFollowSettings(ctx, model.FollowSettings{BaseURL: "http://old.example.com", TelegramEnabled: true, BotID: "follow-bot"}); err != nil {
		t.Fatal(err)
	}
	call("POST", "/api/admin/follows/settings", adminToken, map[string]any{"baseUrl": "http://new.example.com"}, 200)
	settings = call("GET", "/api/admin/follows/settings", adminToken, nil, 200)
	if settings["telegramEnabled"] != true || settings["botId"] != "follow-bot" {
		t.Fatal("旧客户端保存时清除了 Telegram Bot 绑定", settings)
	}
}
