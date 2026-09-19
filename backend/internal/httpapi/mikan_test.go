package httpapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/client/mikan"
	"strm115/backend/internal/service"
	"strm115/backend/internal/storage"
)

type mikanTestReader struct{ calls int }

func (s *mikanTestReader) Search(_ context.Context, q, g string) (mikan.Result, error) {
	s.calls++
	if q == "受限" {
		return mikan.Result{}, mikan.ErrBlocked
	}
	return mikan.Result{Items: []mikan.Item{}, Groups: []mikan.Group{}}, nil
}

func (s *mikanTestReader) Bangumi(_ context.Context, id, group string, take int) (mikan.Result, error) {
	return mikan.Result{Items: []mikan.Item{}, Groups: []mikan.Group{}, Bangumi: &mikan.Bangumi{ID: id, Title: "一弦定音！"}}, nil
}

func TestMikanAdminSearchAndLogs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "users.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	auth := service.NewWebsiteAuth(db, "admin", "admin-password", "test-secret")
	adminToken, _, err := auth.Login(ctx, "admin", "admin-password")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = auth.Create(ctx, "reader", "reader-password"); err != nil {
		t.Fatal(err)
	}
	userToken, _, err := auth.Login(ctx, "reader", "reader-password")
	if err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	admin := r.Group("/api/admin", WebsiteAuthenticated(auth), WebsiteAdminOnly())
	reader := &mikanTestReader{}
	logs := &service.GenerationLogs{}
	RegisterMikan(admin, service.NewMikanService(reader, logs))
	RegisterLogs(admin, logs)
	for _, tc := range []struct {
		token, body string
		status      int
	}{
		{"", `{"query":"测试"}`, 401}, {userToken, `{"query":"测试"}`, 403},
		{adminToken, `{"query":"测试"}`, 200}, {adminToken, `{"query":"测试"}`, 200},
		{adminToken, `{"query":""}`, 400}, {adminToken, `{"query":"测试","group":"1&bad=1"}`, 400},
		{adminToken, `{broken`, 400}, {adminToken, `{"query":"受限"}`, 502},
	} {
		request := httptest.NewRequest("POST", "/api/admin/mikan/search", strings.NewReader(tc.body))
		request.Header.Set("Authorization", "Bearer "+tc.token)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		r.ServeHTTP(response, request)
		if response.Code != tc.status {
			t.Fatalf("got %d want %d: %s", response.Code, tc.status, response.Body.String())
		}
		if tc.status == 200 {
			var result service.MikanResult
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || result.Items == nil {
				t.Fatal(err, result)
			}
			if response.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("缺少缓存保护")
			}
		}
	}
	if reader.calls != 2 {
		t.Fatal("鉴权或缓存未阻止重复请求", reader.calls)
	}
	for _, tc := range []struct {
		token, body string
		status      int
	}{
		{"", `{"id":"1940"}`, 401}, {userToken, `{"id":"1940"}`, 403},
		{adminToken, `{"id":"1940"}`, 200}, {adminToken, `{"id":"1940","group":"19","take":65}`, 200},
		{adminToken, `{"id":"https://evil.test"}`, 400}, {adminToken, `{"id":"1940","group":"19","take":0}`, 400},
		{adminToken, `{"id":"1940","group":"19","take":10001}`, 400}, {adminToken, `{broken`, 400},
	} {
		request := httptest.NewRequest("POST", "/api/admin/mikan/bangumi", strings.NewReader(tc.body))
		request.Header.Set("Authorization", "Bearer "+tc.token)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		r.ServeHTTP(response, request)
		if response.Code != tc.status {
			t.Fatalf("番剧接口状态 %d，预期 %d：%s", response.Code, tc.status, response.Body.String())
		}
		if tc.status == 200 && (!strings.Contains(response.Body.String(), "一弦定音！") || response.Header().Get("Cache-Control") != "no-store") {
			t.Fatal(response.Body.String())
		}
	}
	request := httptest.NewRequest("GET", "/api/admin/logs?module=蜜柑&level=ALL", nil)
	request.Header.Set("Authorization", "Bearer "+adminToken)
	response := httptest.NewRecorder()
	r.ServeHTTP(response, request)
	if response.Code != 200 || !strings.Contains(response.Body.String(), "MIKAN-") {
		t.Fatal(response.Body.String())
	}
}
