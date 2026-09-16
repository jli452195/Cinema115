package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/bot"
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
