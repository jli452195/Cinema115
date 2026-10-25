package httpapi

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/client/nanshare"
	"strm115/backend/internal/service"
)

func TestOperationLogsResultsAndIsolation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	logs := &service.GenerationLogs{}
	previousTask := ""
	for _, tc := range []struct {
		name, method, path, level string
		summary                   string
		status                    int
		cached, failed, canceled  bool
	}{
		{name: "success", method: "POST", path: "/search", status: 200, level: "INFO"},
		{name: "cached", method: "POST", path: "/search", status: 200, cached: true, level: "DEBUG"},
		{name: "invalid input", method: "POST", path: "/search", status: 400, level: "WARNING"},
		{name: "upstream failure", method: "POST", path: "/search", status: 502, level: "ERROR"},
		{name: "stream failure after HTTP 200", method: "POST", path: "/search", status: 200, failed: true, level: "ERROR"},
		{name: "canceled request", method: "POST", path: "/search", status: 200, canceled: true, level: "WARNING"},
		{name: "canceled stream with summary", method: "POST", path: "/search", status: 200, canceled: true, level: "WARNING", summary: "页面停止等待；Bot 查询继续限时收尾"},
		{name: "config read", method: "GET", path: "/search", status: 200},
		{name: "download excluded", method: "POST", path: "/download", status: 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := gin.New()
			r.Use(operationLogs(logs, "搜索", "Bot", map[string]string{"/search": "资源搜索"}))
			r.Handle(tc.method, tc.path, func(c *gin.Context) {
				if tc.summary != "" {
					c.Set("operationLogSummary", tc.summary)
				} else if !tc.canceled {
					c.Set("operationLogSummary", "返回 3 条资源")
				}
				c.Set("operationLogCached", tc.cached)
				c.Set("operationLogFailed", tc.failed)
				if tc.canceled {
					ctx, cancel := context.WithCancel(c.Request.Context())
					cancel()
					c.Request = c.Request.WithContext(ctx)
				}
				c.String(tc.status, "private-response-token")
			})
			before := len(logs.Snapshot("ALL"))
			req := httptest.NewRequest(tc.method, tc.path+"?token=private-query-token", strings.NewReader("private-request-token"))
			req.Header.Set("Authorization", "private-header-token")
			r.ServeHTTP(httptest.NewRecorder(), req)
			entries := logs.Snapshot("ALL")
			if tc.level == "" {
				if len(entries) != before {
					t.Fatal("非搜索操作产生了搜索日志")
				}
				return
			}
			if len(entries) != before+2 || entries[0].Level != tc.level || entries[1].Level != "DEBUG" {
				t.Fatalf("开始与结果日志错误: %+v", entries)
			}
			if entries[0].TaskID == "" || entries[0].TaskID != entries[1].TaskID || entries[0].TaskID == previousTask {
				t.Fatal("操作编号未正确关联或隔离")
			}
			previousTask = entries[0].TaskID
			if tc.canceled {
				expected := tc.summary
				if expected == "" {
					expected = "请求已取消或超时"
				}
				if !strings.Contains(entries[0].Message, expected) {
					t.Fatalf("取消结果摘要丢失: %+v", entries[0])
				}
			}
			for _, entry := range entries[:2] {
				if entry.Module != "搜索" || entry.Source != "Bot" || strings.Contains(entry.Message, "private-") {
					t.Fatalf("归属错误或泄露请求、响应内容: %+v", entry)
				}
			}
		})
	}
}

type logNanShare struct {
	NanShareConfigurer
	err error
}

func (s *logNanShare) Search(context.Context, string, int64, bool) (service.NanShareSearchView, error) {
	return service.NanShareSearchView{Items: []nanshare.Resource{{}, {}}}, s.err
}

func TestNanShareSearchLogs(t *testing.T) {
	logs := &service.GenerationLogs{}
	s := &logNanShare{}
	r := gin.New()
	RegisterNanShare(r.Group("/api/admin"), s, logs)
	for _, failure := range []bool{false, true} {
		if failure {
			s.err = errors.New("private-upstream-token")
		}
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/admin/nanshare/search", strings.NewReader(`{"kind":"tv","tmdbId":42}`)))
		entries := logs.Snapshot("ALL", "搜索")
		if len(entries) == 0 || entries[0].Source != "RE0" || entries[0].TaskID == "" || strings.Contains(entries[0].Message, "private-") {
			t.Fatalf("RE0 日志错误: %+v", entries)
		}
		if !failure && !strings.Contains(entries[0].Message, "2 条资源") || failure && entries[0].Level != "ERROR" {
			t.Fatalf("RE0 结果摘要错误: %+v", entries)
		}
	}
}
