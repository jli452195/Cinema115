package service

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
)

func TestED2KSTRMResumeCredentialsKeepsOriginalIntentAndOutput(t *testing.T) {
	ctx := context.Background()
	f := newED2KSTRMFixture(t)
	v := f.imported(t)
	a, err := f.accounts.Primary(v.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.accounts.Update(a.ID, a.Revision, "主账号新名称", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.taskAccount(&v); err != nil {
		t.Fatalf("只改名影响了原凭据任务: %v", err)
	}
	v.Stage, v.CacheCID, v.CacheParentCID = "submit_pending", "11", "10"
	v.CacheTargetCID, v.CacheTargetPath = "10", "/原缓存"
	v.Status, v.Message = "blocked", "生成配置无效：请先在 115 助手中登录主账号"
	v.NextRunAt = time.Now().Add(time.Hour)
	if err := f.service.save(ctx, v); err != nil {
		t.Fatal(err)
	}
	foreign := v
	foreign.Hash, foreign.UID = strings.Repeat("c", 32), "999"
	if err := f.service.save(ctx, foreign); err != nil {
		t.Fatal(err)
	}
	original := f.service.Tasks()[0]
	original.Status, original.Message = "failed", ed2kExportMissingMaterial
	if err := f.service.saveExport(ctx, original); err != nil {
		t.Fatal(err)
	}
	a, _ = f.accounts.Primary(v.AccountID)
	cookie := "UID=123_B; CID=refreshed"
	if err := f.accounts.Update(a.ID, a.Revision, a.Name, &cookie); err != nil {
		t.Fatal(err)
	}
	if got := f.service.CredentialPausedCount(); got != 1 {
		t.Fatalf("可继续资源数 = %d，期望 1", got)
	}
	if n, err := f.service.ResumeCredentials(ctx); n != 1 || err != nil {
		t.Fatalf("继续资源数 = %d，错误 = %v", n, err)
	}
	stored, err := f.store.GetED2KSTRMResource(ctx, v.Hash, v.Size)
	if err != nil || stored.Status != "queued" || stored.Stage != v.Stage || stored.CacheCID != v.CacheCID || stored.CacheParentCID != v.CacheParentCID || stored.CacheTargetPath != v.CacheTargetPath || !stored.NextRunAt.IsZero() {
		t.Fatalf("原缓存意图未正确恢复: %+v，错误 = %v", stored, err)
	}
	tasks, err := f.store.ListED2KSTRMTasks(ctx)
	if err != nil || len(tasks) != 1 || tasks[0].ID != original.ID || tasks[0].Status != "waiting" || tasks[0].OutputDir != original.OutputDir || tasks[0].BaseURL != original.BaseURL {
		t.Fatalf("原生成任务未正确恢复: %+v，错误 = %v", tasks, err)
	}
	if n, err := f.service.ResumeCredentials(ctx); n != 0 || err != nil || len(f.client.calls) != 0 || f.cloud.calls != 0 {
		t.Fatalf("重复恢复或意外访问 115，数量 = %d，错误 = %v", n, err)
	}
}

func TestED2KSTRMCacheFailureLogsHTTPWithoutQuery(t *testing.T) {
	s := &ED2KSTRMService{Logs: &GenerationLogs{}}
	v := model.ED2KSTRMResource{Hash: strings.Repeat("a", 32), Status: "downloading"}
	upstream := &p115.HTTPError{Method: http.MethodGet, Endpoint: "https://115.com/web/?token=private", Status: http.StatusTooManyRequests, RetryAfter: 2 * time.Minute}
	err := ed2kStepFailure("查询原离线任务", upstream)
	s.scheduleCacheError(&v, err)
	if !errors.Is(err, upstream) || v.Attempts != 1 || time.Until(v.NextRunAt) < time.Minute {
		t.Fatalf("应按上游等待重试，实际状态 %+v", v)
	}
	s.logCacheProblem(v, err)
	entries := s.Logs.Snapshot("WARNING", "STRM")
	if len(entries) != 1 || !strings.Contains(entries[0].Message, "查询原离线任务：GET 115.com/web/ HTTP 429") || strings.Contains(entries[0].Message, "private") {
		t.Fatalf("缓存日志未记录脱敏接口: %+v", entries)
	}
}
