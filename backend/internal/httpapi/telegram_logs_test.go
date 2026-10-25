package httpapi

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/client/tguser"
	"strm115/backend/internal/service"
)

func TestTelegramOperationLogsExcludeCredentialsAndReads(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	logs := &service.GenerationLogs{}
	account, err := tguser.New(&userTestStore{}, &userTestStore{})
	if err != nil {
		t.Fatal(err)
	}
	RegisterTelegramUser(r.Group("/api/admin"), account, &tmdbReaderFake{}, logs)
	secret := strings.Repeat("b", 32)
	for _, request := range []struct{ method, path, body string }{
		{"POST", "config", `{"apiId":123,"apiHash":"` + secret + `","bot":"example_bot"}`},
		{"POST", "send-code", `{"phone":"private-phone"}`},
		{"POST", "sign-in", `{"code":"private-code","password":"private-password"}`},
		{"GET", "config", ""},
	} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(request.method, "/api/admin/telegram-user/"+request.path, strings.NewReader(request.body))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
	}
	entries := logs.Snapshot("ALL", "Telegram")
	if len(entries) != 6 {
		t.Fatalf("应仅记录三次写操作的开始与结果，得到 %d 条", len(entries))
	}
	for _, entry := range entries {
		for _, sensitive := range []string{secret, "private-phone", "private-code", "private-password"} {
			if strings.Contains(entry.Message, sensitive) {
				t.Fatal("日志包含凭据")
			}
		}
		if entry.Source != "用户账号" {
			t.Fatal("日志来源不正确")
		}
	}
	if len(logs.Snapshot("INFO", "Telegram")) != 1 || len(logs.Snapshot("WARNING", "Telegram")) != 1 || len(logs.Snapshot("ERROR", "Telegram")) != 1 {
		t.Fatal("等级划分不正确")
	}
}

func TestTelegramSearchLogsUseSearchModule(t *testing.T) {
	r := gin.New()
	logs := &service.GenerationLogs{}
	account, err := tguser.New(&userTestStore{}, &userTestStore{})
	if err != nil {
		t.Fatal(err)
	}
	RegisterTelegramUser(r.Group("/api/admin"), account, &tmdbReaderFake{}, logs)
	for _, path := range []string{"search", "search-stream", "select"} {
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/admin/telegram-user/"+path, strings.NewReader(`{"private":"request-token"}`)))
	}
	entries := logs.Snapshot("ALL", "搜索")
	if len(entries) != 6 || len(logs.Snapshot("ALL", "Telegram")) != 0 {
		t.Fatalf("搜索混入 Telegram 或记录缺失: %+v", entries)
	}
	for _, entry := range entries {
		if entry.Source != "Bot" || entry.TaskID == "" || strings.Contains(entry.Message, "request-token") {
			t.Fatalf("Bot 搜索归属或脱敏错误: %+v", entry)
		}
	}
}
