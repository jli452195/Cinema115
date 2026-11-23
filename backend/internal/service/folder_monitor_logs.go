package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

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
	if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		level = "WARNING"
	}
	retained := "尚未建立临时批次"
	if value.BatchDirectoryID != "" {
		retained = "临时目录已保留；重试从当前阶段继续"
	}
	if value.Stage == "blocked" {
		retained = "已停止自动处理，需要人工核对"
	}
	detail := generationErrorDetail(err, GenerateInput{})
	s.Logs.Append("STRM", value.LogTaskID, "目录监控", level, fmt.Sprintf("第 %d 次执行未完成，阶段=%s，耗时 %.2f 秒：%s；%s", value.LogAttempt, value.Stage, elapsed.Seconds(), detail, retained))
}
