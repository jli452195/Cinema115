package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/client/tmdb"
	"strm115/backend/internal/service"
	"strm115/backend/internal/storage"
)

type httpMediaMetadata struct{}

func (httpMediaMetadata) Browse(context.Context, tmdb.Options) (*tmdb.Page, error) {
	return &tmdb.Page{Items: []tmdb.Item{{ID: 42, Kind: "movie", Title: "Work", Date: "2024-01-01"}}}, nil
}
func (httpMediaMetadata) Details(context.Context, string, int) (*tmdb.Detail, error) {
	return &tmdb.Detail{Item: tmdb.Item{ID: 42, Kind: "movie", Title: "作品", Date: "2024-01-01"}}, nil
}

func TestMediaOrganizeAPIWorkflowAndValidation(t *testing.T) {
	store, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "media.db"))
	if err != nil {
		t.Fatal(err)
	}
	classification, err := service.OpenMediaClassification(context.Background(), filepath.Join(t.TempDir(), "classification.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	s, err := service.OpenMediaOrganizerSQLite(context.Background(), store, t.TempDir(), "test-secret", httpMediaMetadata{}, httpNamingTemplates{}, classification, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	admin := router.Group("/api/admin", func(c *gin.Context) {
		if c.GetHeader("Authorization") != "Bearer admin-test" {
			c.AbortWithStatus(401)
			return
		}
		c.Next()
	})
	RegisterMediaOrganize(admin, s)
	request := func(method, path, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "/api/admin/media-organize"+path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer admin-test")
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body)
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("cache enabled")
		}
		return w
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/admin/media-organize", nil))
	if w.Code != 401 {
		t.Fatal("missing admin middleware")
	}
	for _, query := range []string{"?page=0", "?page=-1", "?page=abc"} {
		request("GET", "/history"+query, "", 400)
	}
	request("GET", "/history?status=unknown", "", 400)
	request("DELETE", "/history", `{}`, 400)
	request("DELETE", "/history", `{"status":"done","items":[{"runId":"x","fileId":"y"}]}`, 400)
	for _, status := range []string{"all", "running", "unknown"} {
		request("DELETE", "/history", `{"status":"`+status+`"}`, 400)
	}
	request("DELETE", "/history", `{"status":"done"}`, 200)
	request("DELETE", "/history", `{"status":"failed"}`, 200)
	var lightweight map[string]json.RawMessage
	if err := json.Unmarshal(request("GET", "", "", 200).Body.Bytes(), &lightweight); err != nil || lightweight["history"] != nil {
		t.Fatal("settings endpoint must not load record history", err)
	}
	var state service.MediaOrganizeHistory
	readState := func(query string) {
		t.Helper()
		response := request("GET", "/history"+query, "", 200)
		if err := json.Unmarshal(response.Body.Bytes(), &state); err != nil {
			t.Fatal(err)
		}
	}
	readState("")
	if state.Items == nil || state.Total != 0 || state.Page != 1 {
		t.Fatal("missing empty history contract", state)
	}
	source, target := t.TempDir(), t.TempDir()
	file := filepath.Join(source, "Work.2024.strm")
	const body = "http://unmodified.invalid/strm"
	if err := os.WriteFile(file, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Minute)
	if err := os.Chtimes(file, old, old); err != nil {
		t.Fatal(err)
	}
	rule := service.MediaOrganizeRule{Name: "API验收", Storage: "local", Source: source, Target: target, Failure: t.TempDir(), Classify: true, IntervalSeconds: 300}
	raw, _ := json.Marshal(rule)
	response := request("POST", "/rules", string(raw), 200)
	if err := json.Unmarshal(response.Body.Bytes(), &rule); err != nil {
		t.Fatal(err)
	}
	if rule.Failure == "" {
		t.Fatal("optional failure directory was lost")
	}
	request("POST", "/rules", `{"scope":"strm"}`, 400)
	request("POST", "/rules", `{"extra":"`+strings.Repeat("x", 1<<20)+`"}`, 400)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(3 * time.Second):
			t.Error("worker leaked")
		}
	}()
	response = request("POST", "/rules/"+rule.ID+"/run", "", 202)
	var run service.MediaOrganizeRun
	if err := json.Unmarshal(response.Body.Bytes(), &run); err != nil {
		t.Fatal(err)
	}
	wait := func(status string) {
		t.Helper()
		until := time.Now().Add(3 * time.Second)
		for time.Now().Before(until) {
			snapshot, err := s.Snapshot(run.ID)
			if err == nil && snapshot.Status == status && !s.State().Active {
				run = snapshot
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatal("task did not reach", status)
	}
	wait("completed")
	if len(run.Groups) != 1 || run.Groups[0].Category != "电影/其他电影" || run.Groups[0].Kind != "movie" || run.Groups[0].TMDBID != 42 || len(run.Entries) != 1 || !strings.HasPrefix(run.Entries[0].Relative, "电影/其他电影/") {
		t.Fatal("API worker did not use the configured classification", run)
	}
	organized := filepath.Join(target, filepath.FromSlash(run.Entries[0].Relative))
	if entry := run.Entries[0]; !entry.Reference || entry.Status != "done" || entry.Result == nil || entry.Result.Status != "done" || entry.Result.At.IsZero() || filepath.Clean(entry.Target) != filepath.Clean(organized) {
		t.Fatal("成功后应保留实际完成结果及真实分类目标的精简引用", entry)
	}
	successfulRunID, completedAt := run.ID, run.Entries[0].Result.At
	data, err := os.ReadFile(organized)
	if err != nil || string(data) != body {
		t.Fatal("file contents changed", err)
	}
	if _, err := os.Stat(file); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("成功整理后来源仍存在", err)
	}
	var completed service.MediaOrganizeTask
	if err := json.Unmarshal(request("GET", "/runs/"+run.ID+"/task?fileId=Work.2024.strm", "", 200).Body.Bytes(), &completed); err != nil || completed.Status != "done" || !completed.At.Equal(completedAt) || len(completed.Entries) != 1 || filepath.Clean(completed.Target) != filepath.Clean(organized) {
		t.Fatal("成功历史详情应可查询且保留原完成时间和目标", completed, err)
	}
	request("GET", "/runs/"+run.ID+"/task?fileId=", "", 400)
	request("POST", "/runs/"+run.ID+"/retry", "{\"fileId\":\"Work.2024.strm\",\"season\":-1}", 400)
	request("POST", "/runs/"+run.ID+"/retry", "{}", 400)
	readState("?page=99")
	if state.Page != 1 || state.Total != 1 || state.Counts["done"] != 1 || len(state.Items) != 1 || !state.Items[0].At.Equal(completedAt) || state.Items[0].RunID != successfulRunID {
		t.Fatal("本轮结束后成功历史应继续可查询，并保持实际完成时间", state)
	}
	readState("?q=work&status=done")
	if state.Total != 1 || state.Counts["done"] != 1 || len(state.Items) != 1 || state.Items[0].RunID != successfulRunID {
		t.Fatal("成功历史应支持搜索和筛选", state)
	}
	// 重新放回同名来源，验证真实目标冲突、失败历史及删除不影响文件。
	const conflictingBody = "http://unmodified.invalid/conflicting-strm"
	if err := os.WriteFile(file, []byte(conflictingBody), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(file, old, old); err != nil {
		t.Fatal(err)
	}
	response = request("POST", "/rules/"+rule.ID+"/run", "", 202)
	if err := json.Unmarshal(response.Body.Bytes(), &run); err != nil {
		t.Fatal(err)
	}
	wait("partial")
	request("GET", "/runs/"+run.ID+"/task?fileId=Work.2024.strm", "", 200)
	readState("?page=99&q=work&status=failed")
	if state.Page != 1 || state.Total != 1 || state.Counts["failed"] != 1 || state.Counts["done"] != 1 || state.Items[0].Status != "failed" || !state.Items[0].CanDelete {
		t.Fatal("失败历史筛选或空闲任务删除权限错误", state)
	}
	failedFile := filepath.Join(rule.Failure, "Work.2024.strm")
	if data, err := os.ReadFile(failedFile); err != nil || string(data) != conflictingBody {
		t.Fatal("冲突来源应保留原内容进入失败目录", err)
	}
	response = request("DELETE", "/history", `{"status":"failed"}`, 200)
	if !strings.Contains(response.Body.String(), `"deleted":1`) {
		t.Fatal("single task deletion did not report count", response.Body.String())
	}
	readState("")
	if state.Total != 1 || state.Counts["failed"] != 0 || state.Counts["done"] != 1 || len(state.Items) != 1 || state.Items[0].RunID != successfulRunID || !state.Items[0].At.Equal(completedAt) {
		t.Fatal("删除失败记录不能删除或改写此前的成功历史", state)
	}
	request("GET", "/runs/"+run.ID+"/task?fileId=Work.2024.strm", "", 404)
	response = request("DELETE", "/history", `{"status":"done"}`, 200)
	if !strings.Contains(response.Body.String(), `"deleted":1`) {
		t.Fatal("清理全部成功应准确删除成功历史", response.Body.String())
	}
	readState("")
	if state.Total != 0 || state.Counts["done"] != 0 || state.Counts["failed"] != 0 {
		t.Fatal("手动清理后成功历史仍然存在", state)
	}
	request("GET", "/runs/"+successfulRunID+"/task?fileId=Work.2024.strm", "", 404)
	data, err = os.ReadFile(organized)
	if err != nil || string(data) != body {
		t.Fatal("record deletion changed the organized file", err)
	}
	if data, err := os.ReadFile(failedFile); err != nil || string(data) != conflictingBody {
		t.Fatal("删除失败历史不得删除或修改失败目录中的文件", err)
	}
	request("DELETE", "/rules/"+rule.ID, "", 200)
}

func TestMediaOrganizeClassificationUnavailableAPI(t *testing.T) {
	store, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "media.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	s, err := service.OpenMediaOrganizerSQLite(context.Background(), store, t.TempDir(), "test-secret", nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	rule, err := s.SaveRule(service.MediaOrganizeRule{Name: "分类不可用", Storage: "local", Source: t.TempDir(), Target: t.TempDir(), Classify: true, IntervalSeconds: 300})
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	RegisterMediaOrganize(router.Group("/api/admin"), s)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("POST", "/api/admin/media-organize/rules/"+rule.ID+"/run", nil))
	if w.Code != 503 || !strings.Contains(w.Body.String(), service.ErrClassificationUnavailable.Error()) || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("unavailable configuration: %d %s", w.Code, w.Body.String())
	}
	if s.State().Active || !s.State().Rules[0].LastScan.IsZero() {
		t.Fatal("rejected submission changed monitoring state")
	}
}

func TestMediaOrganizeScheduleAPIKeepsManualWorkAndFrozenRules(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "media.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	s, err := service.OpenMediaOrganizerSQLite(ctx, store, t.TempDir(), "test-secret", nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	rule, err := s.SaveRule(service.MediaOrganizeRule{Name: "独立调度", Storage: "local", Source: t.TempDir(), Target: t.TempDir(), IntervalSeconds: 300})
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	admin := router.Group("/api/admin", func(c *gin.Context) {
		if c.GetHeader("Authorization") != "Bearer admin-test" {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		c.Next()
	})
	RegisterMediaOrganize(admin, s)
	request := func(method, path, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "/api/admin/media-organize"+path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer admin-test")
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		if w.Code != want || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body)
		}
		return w
	}
	path := "/rules/" + rule.ID + "/schedule"
	unauthorized := httptest.NewRecorder()
	router.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodPatch, "/api/admin/media-organize"+path, strings.NewReader(`{"realtimeEnabled":false,"scheduleEnabled":true,"intervalSeconds":17}`)))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatal("调度设置缺少管理员鉴权")
	}
	for _, body := range []string{
		`{}`,
		`{"realtimeEnabled":false,"intervalSeconds":17}`,
		`{"realtimeEnabled":null,"scheduleEnabled":true,"intervalSeconds":17}`,
		`{"realtimeEnabled":false,"scheduleEnabled":true,"intervalSeconds":0}`,
		`{"realtimeEnabled":false,"scheduleEnabled":true,"intervalSeconds":86401}`,
		`{"realtimeEnabled":false,"scheduleEnabled":true,"intervalSeconds":1.5}`,
		`{"realtimeEnabled":false,"scheduleEnabled":true,"intervalSeconds":"20"}`,
		`{"realtimeEnabled":false,"scheduleEnabled":true,"intervalSeconds":17,"source":"/other"}`,
		`{"realtimeEnabled":false,"scheduleEnabled":true,"intervalSeconds":17} {}`,
	} {
		request(http.MethodPatch, path, body, http.StatusBadRequest)
		if got := s.State().Rules[0]; got.RealtimeEnabled || got.ScheduleEnabled || got.IntervalSeconds != 300 {
			t.Fatal("无效请求修改了规则", got)
		}
	}
	response := request(http.MethodPatch, path, `{"realtimeEnabled":true,"scheduleEnabled":true,"intervalSeconds":17}`, http.StatusOK)
	var enabled service.MediaOrganizeRule
	if err := json.Unmarshal(response.Body.Bytes(), &enabled); err != nil {
		t.Fatal(err)
	}
	if !enabled.RealtimeEnabled || !enabled.ScheduleEnabled || enabled.IntervalSeconds != 17 || enabled.Source != rule.Source || enabled.Target != rule.Target {
		t.Fatal("调度设置没有独立保存", enabled)
	}
	run, err := s.Start(rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	request(http.MethodPatch, path, `{"realtimeEnabled":false,"scheduleEnabled":false,"intervalSeconds":23}`, http.StatusOK)
	if !s.State().Active {
		t.Fatal("暂停自动扫描取消了手动任务")
	}
	edited := s.State().Rules[0]
	edited.Name, edited.Target = "运行中编辑", t.TempDir()
	raw, err := json.Marshal(edited)
	if err != nil {
		t.Fatal(err)
	}
	request(http.MethodPost, "/rules", string(raw), http.StatusOK)
	frozen, err := s.Snapshot(run.ID)
	if err != nil || frozen.Rule.Target != rule.Target || frozen.Rule.IntervalSeconds != 17 || !frozen.Rule.RealtimeEnabled || !frozen.Rule.ScheduleEnabled {
		t.Fatal("排队的手动任务配置被修改", frozen.Rule, err)
	}
	other, err := s.SaveRule(service.MediaOrganizeRule{Name: "其他规则", Storage: "local", Source: t.TempDir(), Target: t.TempDir(), IntervalSeconds: 20})
	if err != nil {
		t.Fatal("其他规则被手动任务锁住", err)
	}
	request(http.MethodDelete, "/rules/"+other.ID, "", http.StatusOK)
	if err := os.Remove(rule.Source); err != nil {
		t.Fatal(err)
	}
	request(http.MethodPatch, path, `{"realtimeEnabled":false,"scheduleEnabled":false,"intervalSeconds":23}`, http.StatusOK)
	request(http.MethodPatch, "/rules/missing/schedule", `{"realtimeEnabled":false,"scheduleEnabled":true,"intervalSeconds":23}`, http.StatusBadRequest)
	request(http.MethodDelete, "/rules/"+rule.ID, "", http.StatusOK)
	if s.State().Active || len(s.State().Rules) != 0 {
		t.Fatal("明确删除排队规则后仍残留任务")
	}
}

func TestMediaOrganizeAttachmentGraceAPI(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "media.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	s, err := service.OpenMediaOrganizerSQLite(ctx, store, t.TempDir(), "test-secret", nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	RegisterMediaOrganize(router.Group("/api/admin"), s)
	request := func(path, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		method := http.MethodPost
		if strings.HasSuffix(path, "/schedule") {
			method = http.MethodPatch
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(method, "/api/admin/media-organize"+path, strings.NewReader(body)))
		if w.Code != want {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body)
		}
		return w
	}
	source, target := t.TempDir(), t.TempDir()
	var ruleID string
	for _, test := range []struct {
		value       any
		enabled     any
		want        int
		wantEnabled bool
	}{
		{nil, nil, 10, true},
		{0, true, 0, true},
		{1200, true, 1200, true},
		{10, true, 10, true},
		{37, false, 37, false},
		{86400, true, 86400, true},
		{nil, false, 10, false},
	} {
		body := map[string]any{"name": "附件宽限", "storage": "local", "source": source, "target": target, "intervalSeconds": 20}
		if ruleID != "" {
			body["id"] = ruleID
		}
		if test.value != nil {
			body["attachmentGraceSeconds"] = test.value
		}
		if test.enabled != nil {
			body["attachmentCleanupEnabled"] = test.enabled
		}
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		var saved service.MediaOrganizeRule
		if err := json.Unmarshal(request("/rules", string(raw), http.StatusOK).Body.Bytes(), &saved); err != nil {
			t.Fatal(err)
		}
		if saved.AttachmentGraceSeconds != test.want || saved.AttachmentCleanupEnabled != test.wantEnabled {
			t.Fatal("附件清理默认值、关闭状态或明确的零值未保留", saved)
		}
		ruleID = saved.ID
		if err := json.Unmarshal(request("/rules/"+saved.ID+"/schedule", `{"realtimeEnabled":false,"scheduleEnabled":false,"intervalSeconds":31}`, http.StatusOK).Body.Bytes(), &saved); err != nil || saved.AttachmentGraceSeconds != test.want || saved.AttachmentCleanupEnabled != test.wantEnabled {
			t.Fatal("独立开关接口不应改动附件清理设置", saved, err)
		}
		reopened, err := service.OpenMediaOrganizerSQLite(ctx, store, t.TempDir(), "test-secret", nil, nil, nil, nil, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		loaded := reopened.State().Rules
		if len(loaded) != 1 || loaded[0].ID != ruleID || loaded[0].AttachmentGraceSeconds != test.want || loaded[0].AttachmentCleanupEnabled != test.wantEnabled {
			t.Fatal("SQLite 重新加载后必须保持明确保存的秒数和开关", loaded)
		}
	}
	for _, value := range []any{-1, 86401, 0.5, "20"} {
		body := map[string]any{"name": "无效宽限", "storage": "local", "source": source, "target": target, "intervalSeconds": 20, "attachmentGraceSeconds": value}
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		request("/rules", string(raw), http.StatusBadRequest)
	}
	request("/rules", `{"name":"无效开关","storage":"local","source":"source","target":"target","intervalSeconds":20,"attachmentCleanupEnabled":"false"}`, http.StatusBadRequest)
	if len(s.State().Rules) != 1 {
		t.Fatal("无效宽限不应创建规则", s.State().Rules)
	}
}
