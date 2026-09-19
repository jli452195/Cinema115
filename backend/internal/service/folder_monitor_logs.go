package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
)

// 保留错误分类，但不让原始上游响应绕过脱敏再次进入日志。
type folderMonitorFailure struct {
	message string
	cause   error
}

func (e *folderMonitorFailure) Error() string { return e.message }
func (e *folderMonitorFailure) Unwrap() error { return e.cause }

func monitorLogDetail(message, cookie string) string {
	if cookie != "" {
		message = strings.ReplaceAll(message, cookie, "[凭据已隐藏]")
	}
	return generationErrorDetail(errors.New(message), GenerateInput{})
}

// logFailure 是调度器的错误消费边界，失败状态与后续恢复阶段由工作器保存。
func (s *FolderMonitorService) logFailure(ctx context.Context, id string, err error, elapsed time.Duration) {
	readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	value, readErr := s.Store.GetFolderMonitor(readCtx, id)
	if readErr != nil {
		s.Logs.Append("STRM", id, "目录监控", "ERROR", "目录监控失败，且无法读取任务检查点："+generationErrorDetail(errors.Join(err, readErr), GenerateInput{}))
		return
	}
	level := "ERROR"
	if ctx.Err() != nil || errors.Is(err, p115.ErrShareSnapshotPending) || errors.Is(err, p115.ErrShareUnderReview) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		level = "WARNING"
	}
	retained := "尚未建立临时批次"
	if value.BatchDirectoryID != "" {
		retained = "临时目录已保留；重试从当前阶段继续"
	}
	if value.CleanupIndex > 0 {
		retained = "临时目录已完成清理；仅重试分享复核"
	}
	if value.Stage == "blocked" {
		retained = "已停止自动处理，需要人工核对"
	}
	detail := generationErrorDetail(err, GenerateInput{})
	s.Logs.Append("STRM", value.LogTaskID, "目录监控", level, fmt.Sprintf("第 %d 次执行未完成，阶段=%s，耗时 %.2f 秒：%s；%s", value.LogAttempt, value.Stage, elapsed.Seconds(), detail, retained))
}

// waitShareSnapshot 只重试明确的快照生成中响应；登录、审核及其他错误立即交给上层处理。
func (s *FolderMonitorService) waitShareSnapshot(ctx context.Context, value *model.FolderMonitor, share *p115.ShareInfo, cookie string) ([]folderManifestEntry, string, bool, error) {
	timeout, interval := s.snapshotTimeout, s.snapshotInterval
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	if interval <= 0 {
		interval = 5 * time.Second
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	started := time.Now()
	var pending bool
	for attempt := 1; ; attempt++ {
		manifest, mediaID, reviewing, err := s.scanShare(waitCtx, value, share, cookie)
		if err == nil {
			logGeneration(ctx, "INFO", fmt.Sprintf("分享快照可读：%d 个条目，审核中=%t，耗时 %.2f 秒", len(manifest), reviewing, time.Since(started).Seconds()))
			return manifest, mediaID, reviewing, nil
		}
		if !errors.Is(err, p115.ErrShareSnapshotPending) {
			if pending && errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
				return nil, "", false, fmt.Errorf("等待分享快照超时（%.0f 秒）: %w", timeout.Seconds(), errors.Join(p115.ErrShareSnapshotPending, err))
			}
			return nil, "", false, err
		}
		pending = true
		if attempt == 1 {
			logGeneration(ctx, "INFO", fmt.Sprintf("115 正在生成文件快照，最多等待 %.0f 秒；不会重新移动或创建分享", timeout.Seconds()))
		}
		logGeneration(ctx, "DEBUG", fmt.Sprintf("等待分享快照：第 %d 次，已等待 %.2f 秒", attempt, time.Since(started).Seconds()))
		timer := time.NewTimer(interval)
		select {
		case <-waitCtx.Done():
			timer.Stop()
			if ctx.Err() != nil {
				return nil, "", false, ctx.Err()
			}
			return nil, "", false, fmt.Errorf("等待分享快照超时（%.0f 秒）: %w", timeout.Seconds(), errors.Join(p115.ErrShareSnapshotPending, waitCtx.Err()))
		case <-timer.C:
		}
	}
}
