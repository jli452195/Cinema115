package httpapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/service"
)

type logTestGenerator struct{}

func (logTestGenerator) Generate(context.Context, service.GenerateInput) (*service.GenerationResult, error) {
	return nil, service.ErrNoMedia
}

func TestLogAPIValidationAndAdminBoundary(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	admin := r.Group("/api/admin", func(c *gin.Context) {
		if c.GetHeader("Authorization") != "test-admin" {
			c.AbortWithStatus(401)
		}
	})
	logs := &service.GenerationLogs{}
	g := &service.LoggedGenerator{Logs: logs, Generator: logTestGenerator{}}
	_, _ = g.Generate(context.Background(), service.GenerateInput{})
	logs.Append("Telegram", "", "Bot", "INFO", "消息接收器启动")
	logs.Append("Telegram", "", "用户账号", "DEBUG", "资源搜索复用缓存")
	logs.Append("订阅", "tv:100", "订阅", "INFO", "已追平，本轮跳过 Bot 搜索")
	RegisterLogs(admin, logs)
	for _, tc := range []struct {
		query, auth string
		code, count int
	}{{"", "", 401, 0}, {"?level=ALL", "test-admin", 200, 3}, {"?level=INFO", "test-admin", 200, 2}, {"?level=WARNING", "test-admin", 200, 1}, {"?level=ERROR", "test-admin", 200, 0}, {"?level=invalid", "test-admin", 400, 0}, {"?module=API", "test-admin", 400, 0}, {"?module=Telegram", "test-admin", 200, 2}, {"?module=Telegram&level=INFO", "test-admin", 200, 1}, {"?module=订阅", "test-admin", 200, 1}, {"?module=ALL", "test-admin", 200, 6}, {"?module=ALL&level=DEBUG", "test-admin", 200, 1}} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/api/admin/logs"+tc.query, nil)
		req.Header.Set("Authorization", tc.auth)
		r.ServeHTTP(w, req)
		if w.Code != tc.code {
			t.Fatal(tc, w.Code)
		}
		if tc.code != 200 {
			continue
		}
		var body struct {
			Entries  []service.GenerationLog `json:"entries"`
			Capacity int                     `json:"capacity"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if len(body.Entries) != tc.count || body.Entries == nil || body.Capacity != service.GenerationLogCapacity || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("日志响应错误")
		}
	}
}

func TestLogsEmbyModule(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	logs := &service.GenerationLogs{}
	for _, level := range []string{"INFO", "DEBUG", "WARNING", "ERROR"} {
		logs.Append("Emby", "EMBY-1", "全量同步", level, "测试日志")
	}
	logs.Append("观影", "GYING-1", "搜索", "INFO", "搜索完成")
	RegisterLogs(router.Group("/api/admin"), logs)
	for _, level := range []string{"ALL", "INFO", "DEBUG", "WARNING", "ERROR"} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest("GET", "/api/admin/logs?module=Emby&level="+level, nil))
		if response.Code != 200 {
			t.Fatal(response.Body.String())
		}
		var body struct {
			Entries []service.GenerationLog `json:"entries"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		count := 1
		if level == "ALL" {
			count = 4
		}
		if len(body.Entries) != count {
			t.Fatal("incorrect level filtering")
		}
		for _, entry := range body.Entries {
			if entry.Module != "Emby" {
				t.Fatal("cross-module leak")
			}
		}
	}
}

func TestLogsEmby302Module(t *testing.T) {
	logs := &service.GenerationLogs{}
	proxy := service.NewEmbyProxyLogs(logs)
	ctx := proxy.Trace(context.Background())
	proxy.Add(ctx, "INFO", "直链获取成功")
	proxy.Add(ctx, "ERROR", "直链获取失败")
	logs.Append("Emby", "index", "同步", "INFO", "同步完成")
	router := gin.New()
	RegisterLogs(router.Group("/api/admin"), logs)
	for _, tc := range []struct {
		query string
		count int
	}{
		{"module=Emby%20302&level=ALL", 2},
		{"module=Emby%20302&level=ERROR", 1},
		{"module=Emby&level=ALL", 1},
		{"module=ALL&level=ALL", 3},
	} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("GET", "/api/admin/logs?"+tc.query, nil))
		var body struct{ Entries []service.GenerationLog }
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &body) != nil || len(body.Entries) != tc.count {
			t.Fatal("统一日志模块筛选错误", tc.query, w.Body.String())
		}
	}
}

func TestLogsGyingModule(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	logs := &service.GenerationLogs{}
	logs.Append("观影", "GYING-1", "资源搜索", "INFO", "搜索完成")
	logs.Append("观影", "GYING-1", "资源搜索", "DEBUG", "命中缓存")
	logs.Append("STRM", "STRM-1", "网页", "INFO", "生成完成")
	RegisterLogs(router.Group("/api/admin"), logs)
	for _, tc := range []struct {
		query string
		count int
	}{
		{"?module=观影&level=ALL", 2}, {"?module=观影&level=INFO", 1}, {"?module=观影&level=DEBUG", 1}, {"?module=观影&level=ERROR", 0},
	} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest("GET", "/api/admin/logs"+tc.query, nil))
		var result struct {
			Entries []service.GenerationLog `json:"entries"`
		}
		if response.Code != 200 {
			t.Fatal(response.Body.String())
		}
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if len(result.Entries) != tc.count {
			t.Fatal("incorrect module/level filtering")
		}
		for _, entry := range result.Entries {
			if entry.Module != "观影" {
				t.Fatal("cross-module leak")
			}
		}
	}
}
