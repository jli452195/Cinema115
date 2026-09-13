package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/model"
)

type jobReaderFake struct {
	calls         int
	status        string
	limit, offset int
}

func (f *jobReaderFake) ListReshareJobs(_ context.Context, status string, limit, offset int) ([]model.ReshareJob, error) {
	f.calls++
	f.status, f.limit, f.offset = status, limit, offset
	return []model.ReshareJob{}, nil
}

func (f *jobReaderFake) GetReshareJob(_ context.Context, id string) (model.ReshareJob, error) {
	f.calls++
	if id == "missing" {
		return model.ReshareJob{}, sql.ErrNoRows
	}
	return model.ReshareJob{}, errors.New("敏感内部错误")
}

func TestReshareJobRoutesValidateAndProtectDetails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	fake := &jobReaderFake{}
	admin := r.Group("/api/admin", func(c *gin.Context) {
		if c.GetHeader("Authorization") != "Bearer test" {
			c.AbortWithStatus(401)
		}
	})
	RegisterReshareJobs(admin, fake)
	for _, tc := range []struct {
		path   string
		auth   bool
		status int
	}{
		{"/api/admin/reshare/jobs", false, 401},
		{"/api/admin/reshare/jobs/missing", false, 401},
		{"/api/admin/reshare/jobs?limit=101", true, 400},
		{"/api/admin/reshare/jobs?offset=-1", true, 400},
		{"/api/admin/reshare/jobs?limit=x", true, 400},
		{"/api/admin/reshare/jobs?status=deleting&limit=5&offset=10", true, 200},
		{"/api/admin/reshare/jobs/missing", true, 404},
		{"/api/admin/reshare/jobs/fault", true, 500},
	} {
		req := httptest.NewRequest("GET", tc.path, nil)
		if tc.auth {
			req.Header.Set("Authorization", "Bearer test")
		}
		response := httptest.NewRecorder()
		r.ServeHTTP(response, req)
		if response.Code != tc.status {
			t.Fatalf("%s: %d", tc.path, response.Code)
		}
		if tc.auth && response.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("任务详情不应缓存")
		}
		if strings.Contains(response.Body.String(), "敏感内部错误") {
			t.Fatal("泄漏内部错误")
		}
	}
	if fake.calls != 3 || fake.status != "deleting" || fake.limit != 5 || fake.offset != 10 {
		t.Fatal("无效或未授权请求进入存储层，或筛选参数丢失")
	}
}
