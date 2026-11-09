package httpapi

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/service"
	"strm115/backend/internal/storage"
)

func TestMediaHistoryEventsMergeAndCancel(t *testing.T) {
	db, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s, err := service.OpenMediaOrganizerSQLite(context.Background(), db, t.TempDir(), "test-secret", httpMediaMetadata{}, httpNamingTemplates{}, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	rule, err := s.SaveRule(service.MediaOrganizeRule{Name: "通知验收", Storage: "local", Source: t.TempDir(), Target: t.TempDir(), IntervalSeconds: 300})
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	admin := router.Group("/api/admin", func(c *gin.Context) {
		if c.GetHeader("Authorization") != "Bearer test-admin" {
			c.AbortWithStatus(http.StatusUnauthorized)
		}
	})
	RegisterMediaOrganize(admin, s)
	unauthorized := httptest.NewRecorder()
	router.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/api/admin/media-organize/history/events", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatal("通知接口必须沿用管理员鉴权")
	}
	done := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(done)
		router.ServeHTTP(w, r)
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/admin/media-organize/history/events", nil)
	request.Header.Set("Authorization", "Bearer test-admin")
	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") || response.Header.Get("X-Accel-Buffering") != "no" || response.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("流响应应禁止缓存与代理缓冲", response.Header)
	}
	reader := bufio.NewReader(response.Body)
	frame := make([]byte, len("event: ready\ndata: {}\n\n"))
	if _, err := io.ReadFull(reader, frame); err != nil || string(frame) != "event: ready\ndata: {}\n\n" {
		t.Fatal("建立连接应立即发送 ready", string(frame), err)
	}
	started := time.Now()
	if _, err := s.Start(rule.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteRule(rule.ID); err != nil {
		t.Fatal(err)
	}
	frame = make([]byte, len("event: changed\ndata: {}\n\n"))
	if _, err := io.ReadFull(reader, frame); err != nil || string(frame) != "event: changed\ndata: {}\n\n" || time.Since(started) < 450*time.Millisecond {
		t.Fatal("相邻状态变化应先合并，再发送轻量通知", string(frame), err)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("取消页面连接后通知处理器没有释放")
	}
}
