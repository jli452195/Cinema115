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
	"strm115/backend/internal/storage"
)

func TestShareTaskRoutesProtectHistoryAndPendingRetries(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, status := range []string{"success", "waiting_review"} {
		if err := db.SaveShareTask(context.Background(), model.ShareTask{ID: status, Status: status, Mode: "direct", UpdatedAt: time.Now(), Request: json.RawMessage(`{"cookieHash":"private-hash"}`)}); err != nil {
			t.Fatal(err)
		}
	}
	r := gin.New()
	admin := r.Group("/api/admin", func(c *gin.Context) {
		if c.GetHeader("Authorization") != "test" {
			c.AbortWithStatus(401)
		}
	})
	RegisterShareTasks(admin, db)
	for _, tc := range []struct {
		method, path, auth, body string
		code                     int
	}{
		{"GET", "tasks", "", "", 401}, {"POST", "tasks/clear", "", "{}", 401},
		{"GET", "tasks?status=success&mode=direct", "test", "", 200},
		{"GET", "tasks?status=invalid", "test", "", 400}, {"GET", "tasks?offset=-1", "test", "", 400},
		{"POST", "tasks/clear", "test", "{}", 200},
	} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(tc.method, "/api/admin/p115/share/"+tc.path, strings.NewReader(tc.body))
		req.Header.Set("Authorization", tc.auth)
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		if w.Code != tc.code || strings.Contains(w.Body.String(), "private-hash") {
			t.Fatal("鉴权、筛选或私有参数隐藏错误", w.Code)
		}
	}
	remaining, err := db.ListShareTasks(context.Background(), "", "", "", 20, 0)
	if err != nil || len(remaining) != 1 || remaining[0].Status != "waiting_review" {
		t.Fatal("清理日志影响了待重试任务")
	}
}
