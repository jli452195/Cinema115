package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"strm115/backend/internal/client/p115"
)

func TestFolderMonitorLogsShareCompletionWithoutTraversingContents(t *testing.T) {
	svc, client, _, value := newMonitorScheduleTest(t)
	svc.Logs = &GenerationLogs{}
	reads := 0
	client.drive["100"] = []p115.ShareFile{{ID: "21", Name: "show", Directory: true}}
	client.driveHook = func(_ context.Context, cid string, _ int, cookie string) error {
		if cookie != "UID=42_A" {
			t.Fatal("目录读取未使用绑定账号的凭据")
		}
		if cid == "21" {
			t.Fatal("分享前不应递归读取子目录")
		}
		if cid == value.SourceCID {
			reads++
		}
		return nil
	}
	client.listHook = func(context.Context, string, int, string) error {
		t.Fatal("目录分享不应读取分享快照")
		return nil
	}
	ctx := context.Background()
	if err := svc.ScanNow(ctx, value.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.runDue(ctx); err != nil {
		t.Fatal(err)
	}
	got := loadMonitor(t, svc, value.ID)
	if got.Stage != "completed" || got.LogTaskID == "" || got.LogAttempt != 1 || got.BatchDirectoryID != "" {
		t.Fatalf("分享未完成或缺少日志编号: %+v", got)
	}
	var messages strings.Builder
	for _, entry := range svc.Logs.Snapshot("ALL") {
		if entry.TaskID != got.LogTaskID || entry.Module != "STRM" {
			t.Fatalf("日志归属不一致: %+v", entry)
		}
		messages.WriteString(entry.Message)
	}
	for _, expected := range []string{"开始目录监控处理", "开始检查", "临时目录创建完成", "已移入本批", "已创建分享", "本批占用已解除"} {
		if !strings.Contains(messages.String(), expected) {
			t.Errorf("缺少日志: %s", expected)
		}
	}
	if strings.Contains(messages.String(), "42_A") {
		t.Fatal("日志泄漏凭据")
	}
	if reads != 1 || len(client.shareCalls) != 1 || len(client.moveCalls) != 1 || len(client.deleted) != 0 {
		t.Fatalf("分享执行次数异常: reads=%d share=%v move=%v deleted=%v", reads, client.shareCalls, client.moveCalls, client.deleted)
	}
}

func TestFolderMonitorConfigureFailureResumesSameShare(t *testing.T) {
	svc, client, _, value := newMonitorScheduleTest(t)
	svc.Logs = &GenerationLogs{}
	client.configureErr = errors.New("修改有效期失败 UID=secret SEID=private")
	ctx := context.Background()
	if err := svc.ScanNow(ctx, value.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.runDue(ctx); err != nil {
		t.Fatal(err)
	}
	failed := loadMonitor(t, svc, value.ID)
	if failed.Stage != "configuring" || failed.BatchDirectoryID == "" || failed.ShareURL == "" || len(client.deleted) != 0 {
		t.Fatalf("分享设置失败破坏了批次: %+v", failed)
	}
	failures := svc.Logs.Snapshot("ERROR")
	if len(failures) != 1 || strings.Contains(failed.LastError+failures[0].Message, "secret") || strings.Contains(failed.LastError+failures[0].Message, "private") {
		t.Fatalf("失败记录异常: %+v", failures)
	}
	client.configureErr = nil
	svc = NewFolderMonitorService(svc.Store, svc.Accounts, client, svc.Root, svc.Logs)
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
	if len(client.shareCalls) != 1 || len(client.moveCalls) != 1 || len(client.deleted) != 0 {
		t.Fatal("恢复时重复搬运、分享或删除文件")
	}
}

func TestFolderMonitorRecordsCreatedShareAfterCancellation(t *testing.T) {
	svc, client, _, value := newMonitorScheduleTest(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client.onShare = cancel
	client.configureErr = context.Canceled
	if err := svc.ScanNow(ctx, value.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.runDue(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("取消错误丢失: %v", err)
	}
	got := loadMonitor(t, svc, value.ID)
	if got.Stage != "configuring" || got.BatchDirectoryID == "" || got.ShareURL == "" || len(client.deleted) != 0 {
		t.Fatalf("取消后未保住已创建的链接: %+v", got)
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
			restarted := NewFolderMonitorService(svc.Store, nil, nil, svc.Root, nil)
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
	restarted := NewFolderMonitorService(svc.Store, nil, nil, svc.Root, nil)
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
