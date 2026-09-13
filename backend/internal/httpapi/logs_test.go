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
	RegisterLogs(admin, logs)
	for _, tc := range []struct {
		query, auth string
		code, count int
	}{{"", "", 401, 0}, {"?level=ALL", "test-admin", 200, 3}, {"?level=INFO", "test-admin", 200, 2}, {"?level=WARNING", "test-admin", 200, 1}, {"?level=ERROR", "test-admin", 200, 0}, {"?level=invalid", "test-admin", 400, 0}, {"?module=API", "test-admin", 400, 0}} {
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
