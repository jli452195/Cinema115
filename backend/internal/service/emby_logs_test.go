package service

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"
)

func embyLogMatches(logs *GenerationLogs, level, message string) []GenerationLog {
	matches := make([]GenerationLog, 0)
	for _, entry := range logs.Snapshot(level, "Emby") {
		if strings.Contains(entry.Message, message) {
			matches = append(matches, entry)
		}
	}
	return matches
}

func requireEmbyLog(t *testing.T, logs *GenerationLogs, level, message string) GenerationLog {
	t.Helper()
	matches := embyLogMatches(logs, level, message)
	if len(matches) != 1 {
		t.Fatalf("want one %s log %q, got %d: %+v", level, message, len(matches), logs.Snapshot("ALL", "Emby"))
	}
	return matches[0]
}

func TestEmbyLogsSyncAndFailure(t *testing.T) {
	f := newEmbyFixture(t)
	logs := &GenerationLogs{}
	f.service.Logs = logs
	f.sync(t)
	queued := requireEmbyLog(t, logs, "INFO", "已加入后台队列")
	started := requireEmbyLog(t, logs, "INFO", "开始全量同步")
	finished := requireEmbyLog(t, logs, "INFO", "全量同步完成")
	if queued.TaskID == "" || queued.TaskID != started.TaskID || queued.TaskID != finished.TaskID {
		t.Fatal("sync task ID not preserved")
	}
	if len(logs.Snapshot("ERROR", "Emby")) != 0 {
		t.Fatal("successful sync logged an error")
	}
	if len(logs.Snapshot("ALL", "Emby")) > 12 {
		t.Fatal("sync logs individual items")
	}
	f.mu.Lock()
	f.failPage = true
	f.mu.Unlock()
	f.sync(t)
	failed := requireEmbyLog(t, logs, "ERROR", "未替换现有索引")
	if !strings.Contains(failed.Message, "503") {
		t.Fatal("missing failure reason")
	}
	if failed.TaskID == finished.TaskID {
		t.Fatal("new task reused old ID")
	}
	if len(embyLogMatches(logs, "INFO", "全量同步完成")) != 1 {
		t.Fatal("failed sync reported success")
	}
	state, err := f.service.Status(context.Background())
	if err != nil || state.Status != "failed" || state.Active == "" {
		t.Fatal("old snapshot not retained", state, err)
	}
}

func TestEmbyLogsConfigurationAndPolling(t *testing.T) {
	f := newEmbyFixture(t)
	logs := &GenerationLogs{}
	f.service.Logs = logs
	ctx := context.Background()
	c := f.service.snapshot()
	if _, _, err := f.service.Test(ctx, EmbyConfig{URL: c.URL}); err != nil {
		t.Fatal(err)
	}
	requireEmbyLog(t, logs, "INFO", "读取到 3 个媒体库")
	if err := f.service.Save(ctx, EmbyConfig{URL: c.URL, LibraryIDs: []string{"movies"}}); err != nil {
		t.Fatal(err)
	}
	requireEmbyLog(t, logs, "WARNING", "同步范围已改变")
	if err := f.service.SaveWebhookBaseURL(ctx, "http://cinema.test:3002"); err != nil {
		t.Fatal(err)
	}
	requireEmbyLog(t, logs, "INFO", "保存 Webhook 地址完成")
	if _, _, err := f.service.Test(ctx, EmbyConfig{URL: "invalid"}); err == nil {
		t.Fatal("invalid URL accepted")
	}
	requireEmbyLog(t, logs, "ERROR", "测试 Emby 连接失败")
	before := len(logs.Snapshot("ALL", "Emby"))
	for i := 0; i < 4; i++ {
		if _, err := f.service.Status(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if len(logs.Snapshot("ALL", "Emby")) != before {
		t.Fatal("status polling creates logs")
	}
	for _, entry := range logs.Snapshot("ALL", "Emby") {
		if strings.Contains(entry.Message, c.APIKey) || strings.Contains(entry.Message, c.WebhookSecret) {
			t.Fatal("secret leaked")
		}
	}
}

func TestEmbyLogsWebhookLifecycle(t *testing.T) {
	f := newEmbyFixture(t)
	f.sync(t)
	logs := &GenerationLogs{}
	f.service.Logs = logs
	ctx := context.Background()
	c := f.service.snapshot()
	testBody := fmt.Sprintf(`{"Event":"system.notificationtest","Server":{"Id":%q}}`, c.ServerID)
	if code, err := f.service.Webhook(ctx, c.WebhookSecret, []byte(testBody)); err != nil || code != 200 {
		t.Fatal(code, err)
	}
	requireEmbyLog(t, logs, "INFO", "仅验证连通性，未修改索引")
	unknown := fmt.Sprintf(`{"Event":"unknown","Server":{"Id":%q}}`, c.ServerID)
	if code, err := f.service.Webhook(ctx, c.WebhookSecret, []byte(unknown)); err != nil || code != 202 {
		t.Fatal(code, err)
	}
	requireEmbyLog(t, logs, "WARNING", "未加入处理队列")
	for i := 0; i < 5; i++ {
		_, _ = f.service.Webhook(ctx, "bad-token", []byte(testBody))
	}
	requireEmbyLog(t, logs, "WARNING", "密钥无效")
	first := f.event(t, "movie-a", "upsert")
	second := f.event(t, "movie-a", "upsert")
	f.apply(t, first)
	requireEmbyLog(t, logs, "DEBUG", "未重复应用")
	if len(embyLogMatches(logs, "INFO", "索引更新完成")) != 0 {
		t.Fatal("superseded event logged success")
	}
	f.apply(t, second)
	updated := requireEmbyLog(t, logs, "INFO", "索引更新完成")
	if updated.TaskID != fmt.Sprintf("EMBY-EVENT-%d", second.ID) {
		t.Fatal("event ID missing")
	}
	var receiptFound bool
	for _, e := range embyLogMatches(logs, "DEBUG", "已入队") {
		receiptFound = receiptFound || e.TaskID == updated.TaskID
	}
	if !receiptFound {
		t.Fatal("receipt and application IDs differ")
	}
	f.mu.Lock()
	f.items = f.items[1:]
	f.mu.Unlock()
	f.apply(t, f.event(t, "movie-a", "delete"))
	requireEmbyLog(t, logs, "INFO", "未删除 Emby 文件")
	f.apply(t, f.event(t, "virtual", "upsert"))
	requireEmbyLog(t, logs, "DEBUG", "条目为虚拟媒体")
}

func TestEmbyLogsMetadataRetries(t *testing.T) {
	f := newEmbyFixture(t)
	f.sync(t)
	logs := &GenerationLogs{}
	f.service.Logs = logs
	f.mu.Lock()
	f.items[0].ProviderIDs = nil
	f.mu.Unlock()
	event := f.event(t, "movie-a", "upsert")
	for attempt := 0; attempt < 6; attempt++ {
		event.Attempts = attempt
		f.apply(t, event)
	}
	failed := requireEmbyLog(t, logs, "ERROR", "已达 6 次尝试上限")
	if failed.TaskID != fmt.Sprintf("EMBY-EVENT-%d", event.ID) {
		t.Fatal("retry task ID changed")
	}
	if len(embyLogMatches(logs, "DEBUG", "后重试")) != 5 {
		t.Fatal("missing retry diagnostics")
	}
	if len(embyLogMatches(logs, "INFO", "索引更新完成")) != 0 {
		t.Fatal("metadata retry reported success")
	}
	state, err := f.service.Status(context.Background())
	if err != nil || state.MetadataFailed != 1 {
		t.Fatal(state, err)
	}
}

func TestEmbyLogsConnectionTransitionsAndRedaction(t *testing.T) {
	logs := &GenerationLogs{}
	c := EmbyConfig{APIKey: "key+/secret", WebhookSecret: "hook-secret"}
	s := &EmbyService{Logs: logs, config: c}
	ctx := context.Background()
	connectionErr := errors.New("无法连接 Emby")
	s.mu.Lock()
	s.logConnection(ctx, c, connectionErr)
	s.connectionError = connectionErr.Error()
	s.logConnection(ctx, c, connectionErr)
	s.logConnection(ctx, c, nil)
	s.connectionError = ""
	s.logConnection(ctx, c, nil)
	s.mu.Unlock()
	requireEmbyLog(t, logs, "WARNING", "连接异常")
	requireEmbyLog(t, logs, "INFO", "连接已恢复")
	logCtx := s.logContext(ctx, c, "test", "测试")
	logEmby(logCtx, "DEBUG", "条目 "+c.APIKey+" "+url.QueryEscape(c.APIKey)+" "+c.WebhookSecret+" https://test.invalid/?token=unknown\napi_key=unknown-secret")
	entry := requireEmbyLog(t, logs, "DEBUG", "条目")
	for _, secret := range []string{c.APIKey, url.QueryEscape(c.APIKey), c.WebhookSecret, "unknown-secret", "test.invalid", "\n"} {
		if strings.Contains(entry.Message, secret) {
			t.Fatalf("leaked %q: %s", secret, entry.Message)
		}
	}
	_, finish := s.startOperation(ctx, "取消测试")
	finish(context.Canceled)
	requireEmbyLog(t, logs, "WARNING", "操作已取消")
	_, finish = s.startOperation(ctx, "超时测试")
	finish(context.DeadlineExceeded)
	requireEmbyLog(t, logs, "ERROR", "请求超时")
}

func TestEmbyLogsWorkerFailure(t *testing.T) {
	f := newEmbyFixture(t)
	logs := &GenerationLogs{}
	f.service.Logs = logs
	if err := f.db.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := f.service.Run(ctx); err == nil {
		t.Fatal("closed DB did not stop worker")
	}
	requireEmbyLog(t, logs, "ERROR", "后台处理停止")
}

func TestEmbyLogsPublishFailureDoesNotReportSuccess(t *testing.T) {
	f := newEmbyFixture(t)
	f.sync(t)
	logs := &GenerationLogs{}
	f.service.Logs = logs
	ctx := context.Background()
	c := f.service.snapshot()
	scope := embyScope(c)
	// Simulate a completed checkpoint whose snapshot cannot be published.
	if err := f.db.EmbyBeginSync(ctx, scope, "missing-snapshot", 0); err != nil {
		t.Fatal(err)
	}
	if err := f.db.EmbyCompleteStage(ctx, scope, "missing-snapshot", 10); err != nil {
		t.Fatal(err)
	}
	if err := f.service.syncLibrary(ctx, c); err != nil {
		t.Fatal(err)
	}
	requireEmbyLog(t, logs, "INFO", "恢复发布")
	requireEmbyLog(t, logs, "ERROR", "快照发布失败")
	if len(embyLogMatches(logs, "INFO", "全量同步完成")) != 0 {
		t.Fatal("failed publish logged success")
	}
	state, err := f.service.Status(ctx)
	if err != nil || state.Active == "" || !state.StagingComplete {
		t.Fatal("publish recovery semantics changed", state, err)
	}
}

func TestEmbyLogsBackgroundErrorRetainsContext(t *testing.T) {
	logs := &GenerationLogs{}
	s := &EmbyService{Logs: logs}
	ctx := s.logContext(context.Background(), EmbyConfig{APIKey: "old-task-key"}, "EMBY-1", "全量同步")
	err := embyFailure(ctx, fmt.Errorf("write failed: %w", context.DeadlineExceeded))
	var operationErr *embyOperationError
	if !errors.Is(err, context.DeadlineExceeded) || !errors.As(err, &operationErr) {
		t.Fatal("error chain lost")
	}
	logCtx := context.WithValue(context.Background(), embyLogKey{}, operationErr.logger)
	logEmby(logCtx, "ERROR", "failed old-task-key")
	entry := requireEmbyLog(t, logs, "ERROR", "failed")
	if entry.TaskID != "EMBY-1" || strings.Contains(entry.Message, "old-task-key") {
		t.Fatal("task snapshot lost")
	}
}
