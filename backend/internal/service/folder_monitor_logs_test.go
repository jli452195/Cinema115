package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
)

func TestFolderMonitorLogsEntireBatchAndGenerator(t *testing.T) {
	svc, client, generator, value := newMonitorScheduleTest(t)
	svc.Logs = &GenerationLogs{}
	svc.snapshotInterval = time.Millisecond
	calls := 0
	client.listHook = func(_ context.Context, _ string, _ int, cookie string) error {
		if cookie != "UID=42_A" {
			t.Fatal("快照请求未使用绑定账号 Cookie")
		}
		calls++
		if calls == 1 {
			return p115.ErrShareSnapshotPending
		}
		return nil
	}
	generator.hook = func(ctx context.Context, _ GenerateInput) error {
		logGeneration(ctx, "DEBUG", "生成器逐文件日志")
		logGeneration(ctx, "DEBUG", "Cookie: UID=42_A")
		return nil
	}
	ctx := context.Background()
	if err := svc.ScanNow(ctx, value.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.runDue(ctx); err != nil {
		t.Fatal(err)
	}
	value = loadMonitor(t, svc, value.ID)
	if value.Stage != "completed" || value.LogTaskID == "" || value.LogAttempt != 1 {
		t.Fatalf("任务未完成或缺少日志编号: %+v", value)
	}
	var messages strings.Builder
	for _, entry := range svc.Logs.Snapshot("ALL") {
		if entry.TaskID != value.LogTaskID || entry.Module != "STRM" || entry.Source != "目录监控" {
			t.Fatalf("日志归属不一致: %+v", entry)
		}
		messages.WriteString(entry.Message)
	}
	for _, expected := range []string{"开始目录监控处理", "开始检查", "临时目录创建完成", "批量移动完成", "分享创建完成", "正在生成文件快照", "分享快照可读", "生成器逐文件日志", "STRM 生成结果", "STRM 生成完成", "临时目录清理完成", "整批处理完成"} {
		if !strings.Contains(messages.String(), expected) {
			t.Errorf("缺少日志: %s", expected)
		}
	}
	if strings.Contains(messages.String(), "42_A") {
		t.Fatal("日志泄漏凭据")
	}
	if len(client.shareCalls) != 1 || len(client.moveCalls) != 1 || len(client.deleted) != 1 {
		t.Fatal("重试重复执行了写操作")
	}
}

func TestFolderMonitorSnapshotTimeoutResumesSameBatchAfterRestart(t *testing.T) {
	svc, client, generator, value := newMonitorScheduleTest(t)
	svc.Logs = &GenerationLogs{}
	svc.snapshotTimeout, svc.snapshotInterval = 10*time.Millisecond, time.Millisecond
	client.listHook = func(context.Context, string, int, string) error { return p115.ErrShareSnapshotPending }
	ctx := context.Background()
	if err := svc.ScanNow(ctx, value.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.runDue(ctx); err != nil {
		t.Fatal(err)
	}
	failed := loadMonitor(t, svc, value.ID)
	if failed.Stage != "verifying" || failed.BatchDirectoryID == "" || failed.ShareURL == "" || generator.calls != 0 || len(client.deleted) != 0 {
		t.Fatalf("超时破坏了批次: %+v", failed)
	}
	warnings := svc.Logs.Snapshot("WARNING")
	if len(warnings) != 1 || !strings.Contains(warnings[0].Message, "超时") {
		t.Fatalf("超时日志不正确: %+v", warnings)
	}
	client.listHook = nil
	svc = NewFolderMonitorService(svc.Store, svc.Accounts, client, generator, svc.Root, svc.Logs)
	if err := svc.ScanNow(ctx, value.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.runDue(ctx); err != nil {
		t.Fatal(err)
	}
	completed := loadMonitor(t, svc, value.ID)
	if completed.Stage != "completed" || completed.LogTaskID != failed.LogTaskID || completed.LogAttempt != 2 {
		t.Fatalf("恢复失败: %+v", completed)
	}
	if len(client.shareCalls) != 1 || len(client.moveCalls) != 1 || generator.calls != 1 {
		t.Fatal("恢复时重复移动或分享")
	}
	if err := svc.ScanNow(ctx, value.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.runDue(ctx); err != nil {
		t.Fatal(err)
	}
	if next := loadMonitor(t, svc, value.ID); next.LogTaskID == failed.LogTaskID || next.LogAttempt != 1 {
		t.Fatal("新检查未分配独立日志编号")
	}
}

func TestFolderMonitorSnapshotOtherErrorsDoNotRetry(t *testing.T) {
	for _, cause := range []error{errors.New("登录失效 UID=secret SEID=private"), p115.ErrShareUnderReview} {
		t.Run(cause.Error(), func(t *testing.T) {
			svc, client, generator, value := newMonitorScheduleTest(t)
			svc.Logs = &GenerationLogs{}
			calls := 0
			client.listHook = func(context.Context, string, int, string) error { calls++; return cause }
			ctx := context.Background()
			if err := svc.ScanNow(ctx, value.ID); err != nil {
				t.Fatal(err)
			}
			if err := svc.runDue(ctx); err != nil {
				t.Fatal(err)
			}
			if calls != 1 || generator.calls != 0 || len(client.deleted) != 0 {
				t.Fatal("非快照错误仍在重试或清理")
			}
			value = loadMonitor(t, svc, value.ID)
			if strings.Contains(value.LastError, "secret") || strings.Contains(value.LastError, "private") {
				t.Fatal("错误状态泄漏凭据")
			}
			failures := 0
			for _, entry := range svc.Logs.Snapshot("ALL") {
				if entry.Level == "ERROR" || entry.Level == "WARNING" {
					failures++
				}
				if strings.Contains(entry.Message, "secret") || strings.Contains(entry.Message, "private") {
					t.Fatal("日志泄漏凭据")
				}
			}
			if failures != 1 {
				t.Fatalf("失败重复记录: %d", failures)
			}
		})
	}
}

func TestFolderMonitorSnapshotCancellationPreservesBatch(t *testing.T) {
	svc, client, generator, value := newMonitorScheduleTest(t)
	svc.Logs = &GenerationLogs{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client.listHook = func(context.Context, string, int, string) error { cancel(); return p115.ErrShareSnapshotPending }
	if err := svc.ScanNow(ctx, value.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.runDue(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("取消错误丢失: %v", err)
	}
	value = loadMonitor(t, svc, value.ID)
	if value.Stage != "verifying" || value.BatchDirectoryID == "" || generator.calls != 0 || len(client.deleted) != 0 {
		t.Fatal("取消后仍生成或清理")
	}
	if len(svc.Logs.Snapshot("WARNING")) != 1 {
		t.Fatal("取消日志缺失")
	}
}

func TestFolderMonitorCompletesWithoutReadingShareAfterCleanup(t *testing.T) {
	svc, client, generator, value := newMonitorScheduleTest(t)
	svc.Logs = &GenerationLogs{}
	client.listHook = func(context.Context, string, int, string) error {
		if len(client.deleted) > 0 {
			t.Error("share was read after cleanup")
			return p115.ErrShareSnapshotPending
		}
		return nil
	}
	ctx := context.Background()
	if err := svc.ScanNow(ctx, value.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.runDue(ctx); err != nil {
		t.Fatal(err)
	}
	value = loadMonitor(t, svc, value.ID)
	if value.Stage != "completed" || value.TaskPending || value.BatchDirectoryID != "" || generator.calls != 1 || len(client.deleted) != 1 {
		t.Fatalf("cleanup did not complete task: %+v", value)
	}
	if len(svc.Logs.Snapshot("WARNING")) != 0 {
		t.Fatal("unexpected post-cleanup warning")
	}
	if err := svc.runDue(ctx); err != nil {
		t.Fatal(err)
	}
	if generator.calls != 1 || len(client.deleted) != 1 {
		t.Fatal("completed task was replayed")
	}
}

func TestFolderMonitorLegacyPostCleanupResumesLocally(t *testing.T) {
	for _, stage := range []string{"checking_share", "cleaning"} {
		t.Run(stage, func(t *testing.T) {
			svc, client, generator, value := newMonitorScheduleTest(t)
			ctx := context.Background()
			value.Stage, value.CleanupIndex = stage, 1
			value.BatchDirectoryID, value.LogTaskID = "900", "legacy-log"
			value.ShareURL, value.LastError = "no-longer-readable", "old post-cleanup failure"
			if err := svc.Store.SaveFolderMonitor(ctx, value); err != nil {
				t.Fatal(err)
			}
			// No account/client/generator is needed, nor any local media file.
			restarted := NewFolderMonitorService(svc.Store, nil, nil, nil, svc.Root, nil)
			if err := restarted.runDue(ctx); err != nil {
				t.Fatal(err)
			}
			completed := loadMonitor(t, restarted, value.ID)
			if completed.Stage != "completed" || completed.LastError != "" || completed.TaskPending || completed.BatchDirectoryID != "" || completed.LogTaskID != "legacy-log" {
				t.Fatalf("legacy recovery: %+v", completed)
			}
			if generator.calls != 0 || len(client.deleted) != 0 || len(client.shareCalls) != 0 {
				t.Fatal("recovery replayed cloud work")
			}
			if err := restarted.runDue(ctx); err != nil {
				t.Fatal(err)
			}
			if err := restarted.Delete(ctx, value.ID); err != nil {
				t.Fatal("configuration was not unlocked", err)
			}
		})
	}
}

func TestFolderMonitorLegacyVerificationWithoutCleanupReceiptStops(t *testing.T) {
	svc, _, _, value := newMonitorScheduleTest(t)
	ctx := context.Background()
	value.Stage, value.BatchDirectoryID, value.LastError = "checking_share", "900", "legacy error"
	if err := svc.Store.SaveFolderMonitor(ctx, value); err != nil {
		t.Fatal(err)
	}
	restarted := NewFolderMonitorService(svc.Store, nil, nil, nil, svc.Root, nil)
	if err := restarted.runDue(ctx); err != nil {
		t.Fatal(err)
	}
	blocked := loadMonitor(t, restarted, value.ID)
	if blocked.Stage != "blocked" || blocked.BatchDirectoryID != "900" {
		t.Fatalf("missing cleanup receipt accepted: %+v", blocked)
	}
	if err := restarted.runDue(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.EndBatch(ctx, value.ID, blocked.LogTaskID); err != nil {
		t.Fatal(err)
	}
}
