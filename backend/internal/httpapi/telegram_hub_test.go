package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/bot"
	"strm115/backend/internal/service"
)

func telegramHubRequest(t *testing.T, router http.Handler, method, path, body string, want int) []byte {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != want {
		t.Fatalf("%s %s 返回 %d，期望 %d：%s", method, path, response.Code, want, response.Body.String())
	}
	return response.Body.Bytes()
}

func TestTelegramHubProfileAPI(t *testing.T) {
	gin.SetMode(gin.TestMode)
	hub, err := bot.NewHub(t.TempDir(), "test-secret", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	RegisterTelegramHub(router.Group("/api/admin"), hub)

	raw := telegramHubRequest(t, router, http.MethodPost, "/api/admin/telegram/bots", `{"name":"通知 Bot"}`, http.StatusOK)
	var created struct {
		ID   string               `json:"id"`
		Bots []bot.BotProfileView `json:"bots"`
	}
	if err := json.Unmarshal(raw, &created); err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || len(created.Bots) != 1 || created.Bots[0].Name != "通知 Bot" || strings.Contains(string(raw), `"token"`) {
		t.Fatalf("新增响应错误或泄露 Token：%s", raw)
	}

	profile, err := hub.Profiles().Get(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	profile.Token = "123:private-token"
	if err := hub.Profiles().Update(profile, profile.Revision); err != nil {
		t.Fatal(err)
	}
	profile, _ = hub.Profiles().Get(created.ID)
	path := "/api/admin/telegram/bots/" + created.ID + "/token"
	telegramHubRequest(t, router, http.MethodDelete, path, `{"revision":1}`, http.StatusConflict)
	telegramHubRequest(t, router, http.MethodDelete, path, `{"revision":2}`, http.StatusNoContent)
	cleared, _ := hub.Profiles().Get(created.ID)
	if cleared.Token != "" || cleared.Enabled {
		t.Fatal("清空 Token 后配置仍可运行")
	}

	path = "/api/admin/telegram/bots/" + created.ID
	telegramHubRequest(t, router, http.MethodDelete, path, `{"revision":3}`, http.StatusOK)
	raw = telegramHubRequest(t, router, http.MethodGet, "/api/admin/telegram/bots", "", http.StatusOK)
	if strings.Contains(string(raw), created.ID) {
		t.Fatal("删除后列表仍返回原配置")
	}
}

func TestTelegramModuleAssignmentIsExclusiveAndDeleteReleases(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	settings, err := service.NewAutomationStore(filepath.Join(dir, "automation.json"), dir, "http://nas:8115")
	if err != nil {
		t.Fatal(err)
	}
	value := settings.Value()
	value.Generation.OutputDir = "strm"
	if err := settings.Save(value); err != nil {
		t.Fatal(err)
	}
	settings.ValidateBot = func(string, bool) error { return nil }
	hub, err := bot.NewHub(dir, "test-secret", nil, settings)
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	RegisterTelegramHub(router.Group("/api/admin"), hub)

	firstRaw := telegramHubRequest(t, router, http.MethodPost, "/api/admin/telegram/bots", `{"name":"第一 Bot"}`, http.StatusOK)
	secondRaw := telegramHubRequest(t, router, http.MethodPost, "/api/admin/telegram/bots", `{"name":"第二 Bot"}`, http.StatusOK)
	var first, second struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(firstRaw, &first); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(secondRaw, &second); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{first.ID, second.ID} {
		profile, err := hub.Profiles().Get(id)
		if err != nil {
			t.Fatal(err)
		}
		profile.Token, profile.NotificationChatID, profile.Enabled = "123456:token", "42", true
		if err := hub.Profiles().Update(profile, profile.Revision); err != nil {
			t.Fatal(err)
		}
	}
	modulePath := "/api/admin/telegram/modules/share_notifications"
	telegramHubRequest(t, router, http.MethodPut, modulePath, `{"botId":"`+first.ID+`","expectedBotId":""}`, http.StatusOK)
	telegramHubRequest(t, router, http.MethodPut, modulePath, `{"botId":"`+second.ID+`","expectedBotId":""}`, http.StatusConflict)
	telegramHubRequest(t, router, http.MethodPut, modulePath, `{"botId":"`+second.ID+`","expectedBotId":"`+first.ID+`"}`, http.StatusOK)

	modulesRaw := telegramHubRequest(t, router, http.MethodGet, "/api/admin/telegram/modules", "", http.StatusOK)
	var modules struct {
		Modules []bot.TelegramModuleView `json:"modules"`
	}
	if err := json.Unmarshal(modulesRaw, &modules); err != nil {
		t.Fatal(err)
	}
	var share bot.TelegramModuleView
	for _, module := range modules.Modules {
		if module.ID == "share_notifications" {
			share = module
		}
	}
	if share.AssignedBotID != second.ID || settings.Value().BotID != second.ID || !settings.Value().TelegramEnabled {
		t.Fatalf("module assignment should bind and activate the feature: %+v", share)
	}

	transferStore, err := service.NewSessionStore(filepath.Join(dir, "share-transfer.enc"), "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	transfers, err := service.NewShareTransferSettings(transferStore)
	if err != nil {
		t.Fatal(err)
	}
	transfers.PrimaryID = func() string { return "primary" }
	transfers.ValidateAccount = func(string) error { return nil }
	target, err := transfers.SaveTarget(service.ShareTransferTarget{Name: "接收目录", TargetCID: "88", TargetPath: "/接收"})
	if err != nil {
		t.Fatal(err)
	}
	hub.ShareTransferSettings = transfers
	transferPath := "/api/admin/telegram/modules/share_transfer:" + target.ID
	telegramHubRequest(t, router, http.MethodPut, transferPath, `{"botId":"`+first.ID+`","expectedBotId":""}`, http.StatusOK)
	for _, assignment := range []struct{ path, body string }{
		{modulePath, `{"botId":"` + first.ID + `","expectedBotId":"` + second.ID + `"}`},
		{transferPath, `{"botId":"` + second.ID + `","expectedBotId":"` + first.ID + `"}`},
	} {
		raw := telegramHubRequest(t, router, http.MethodPut, assignment.path, assignment.body, http.StatusConflict)
		if !strings.Contains(string(raw), service.ErrTelegramModuleConflict.Error()) {
			t.Fatalf("missing module conflict explanation: %s", raw)
		}
	}
	if current, _ := transfers.Target(target.ID); current.AssignedBotID != first.ID || settings.Value().BotID != second.ID {
		t.Fatal("a conflicting assignment changed an existing feature owner")
	}

	profile, err := hub.Profiles().Get(second.ID)
	if err != nil {
		t.Fatal(err)
	}
	deletePath := "/api/admin/telegram/bots/" + second.ID
	telegramHubRequest(t, router, http.MethodDelete, deletePath, `{"revision":`+fmt.Sprint(profile.Revision)+`}`, http.StatusConflict)
	telegramHubRequest(t, router, http.MethodDelete, deletePath, `{"revision":`+fmt.Sprint(profile.Revision)+`,"releaseModules":true}`, http.StatusOK)

	modulesRaw = telegramHubRequest(t, router, http.MethodGet, "/api/admin/telegram/modules", "", http.StatusOK)
	if err := json.Unmarshal(modulesRaw, &modules); err != nil {
		t.Fatal(err)
	}
	for _, module := range modules.Modules {
		if module.ID == "share_notifications" && module.AssignedBotID != "" {
			t.Fatalf("deleting Bot left dangling module assignment: %+v", module)
		}
	}
	if settings.Value().TelegramEnabled {
		t.Fatal("deleting the assigned Bot did not disable share handling")
	}
}
