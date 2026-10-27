package httpapi

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/client/tguser"
)

type userTestStore struct{ value string }

func (s *userTestStore) Value() string           { return s.value }
func (s *userTestStore) Save(value string) error { s.value = value; return nil }
func TestTelegramUserAdminAndConfig(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	account, err := tguser.New(&userTestStore{}, &userTestStore{})
	if err != nil {
		t.Fatal(err)
	}
	admin := r.Group("/api/admin", func(c *gin.Context) {
		if c.GetHeader("Authorization") != "test-admin" {
			c.AbortWithStatus(401)
		}
	})
	RegisterTelegramUser(admin, account, &tmdbReaderFake{})
	hash := strings.Repeat("a", 32)
	for _, tc := range []struct {
		method, path, body, auth string
		status                   int
	}{
		{"GET", "config", "", "", 401}, {"POST", "search", `{"kind":"movie","tmdbId":"42"}`, "", 401},
		{"GET", "channels", "", "", 401},
		{"POST", "channels/list", `{}`, "", 401},
		{"POST", "channels", `{"selectionToken":"old","channelIds":["123"]}`, "", 401},
		{"POST", "config", `{"apiId":0,"apiHash":"bad"}`, "test-admin", 400},
		{"POST", "config", `{"apiId":123,"apiHash":"` + hash + `"}`, "test-admin", 200},
		{"POST", "config", `{"apiId":123,"apiHash":"` + hash + `","bot":"example_bot"}`, "test-admin", 200},
		{"GET", "config", "", "test-admin", 200},
		{"GET", "channels", "", "test-admin", 200},
		{"POST", "channels/list", `bad`, "test-admin", 400},
		{"POST", "channels/list", `{}`, "test-admin", 409},
		{"POST", "channels", `{"selectionToken":"old","channelIds":["123"]}`, "test-admin", 409},
		{"POST", "send-code", `{"phone":"bad"}`, "test-admin", 400},
		{"POST", "select", `{"id":"x"}`, "test-admin", 400},
		{"POST", "search", `{"kind":"person","tmdbId":"1"}`, "test-admin", 400},
	} {
		req := httptest.NewRequest(tc.method, "/api/admin/telegram-user/"+tc.path, strings.NewReader(tc.body))
		req.Header.Set("Authorization", tc.auth)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != tc.status {
			t.Fatalf("%s %s: %d", tc.method, tc.path, w.Code)
		}
		if strings.Contains(w.Body.String(), hash) {
			t.Fatal("配置响应泄漏 API Hash")
		}
		if tc.auth != "" && w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("用户配置可被缓存")
		}
	}
}

func TestTelegramUserSearchStreamReturnsEventErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	account, err := tguser.New(&userTestStore{}, &userTestStore{})
	if err != nil {
		t.Fatal(err)
	}
	RegisterTelegramUser(r.Group("/api/admin"), account, &tmdbReaderFake{})
	req := httptest.NewRequest("POST", "/api/admin/telegram-user/search-stream", strings.NewReader(`{"kind":"movie","tmdbId":"42"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/event-stream") {
		t.Fatalf("流式响应状态错误：%d %s", w.Code, w.Header().Get("Content-Type"))
	}
	if body := w.Body.String(); !strings.Contains(body, "event:error") || !strings.Contains(body, `"error"`) {
		t.Fatalf("流式错误事件缺失：%q", body)
	}
}
