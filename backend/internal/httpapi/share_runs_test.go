package httpapi

import (
	"context"
	"encoding/json"
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

type backgroundGenerator func(context.Context, service.GenerateInput) (*service.GenerationResult, error)

func (f backgroundGenerator) Generate(ctx context.Context, in service.GenerateInput) (*service.GenerationResult, error) {
	return f(ctx, in)
}

func TestBackgroundSubmissionAndCancellationHTTP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	root, cancel := context.WithCancel(context.Background())
	defer cancel()
	dir := t.TempDir()
	db, err := storage.Open(root, filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	settings, err := service.NewAutomationStore(filepath.Join(dir, "automation.json"), dir, "http://nas:3002")
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{}, 1)
	tasks := &service.ShareTasks{Store: db, Settings: settings, Cookie: func() string { return "UID=42_A" }, Generator: backgroundGenerator(func(ctx context.Context, _ service.GenerateInput) (*service.GenerationResult, error) {
		started <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	})}
	runs := service.NewShareRuns(root, tasks)
	done := make(chan struct{})
	go func() { defer close(done); runs.Run() }()
	defer func() { cancel(); <-done }()
	api := NewShareAPI("secret", tasks.Cookie)
	api.Runs = runs
	api.BaseURL = func() string { return "http://nas:3002" }
	router := gin.New()
	api.Register(router, router.Group("/api/admin"))
	path := "/api/admin/p115/share/generation-runs"
	id := strings.Repeat("e", 32)
	caller, closePage := context.WithCancel(context.Background())
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"id":"`+id+`","inputs":[{"url":"https://115.com/s/demo","outputDir":"strm"}]}`)).WithContext(caller)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != 202 {
		t.Fatal("没有立即接收后台任务", response.Code, response.Body.String())
	}
	closePage()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("关闭请求后后台任务未执行")
	}
	response = httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path+"/"+id, nil))
	var view service.ShareRunView
	if err := json.Unmarshal(response.Body.Bytes(), &view); err != nil || view.Status != "running" {
		t.Fatal("断开页面后无法查询运行中任务", err)
	}
	response = httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, path+"/"+id+"/cancel", nil))
	if response.Code != 200 {
		t.Fatal("独立停止接口失败")
	}
	response = httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path+"/missing", nil))
	if response.Code != 404 {
		t.Fatal("缺失任务应返回 404")
	}
}
