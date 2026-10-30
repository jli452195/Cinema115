package service

import (
	"fmt"
	"time"

	"strm115/backend/internal/model"
)

func driveSTRMLogMode(mode string) string {
	switch mode {
	case "tree":
		return "增量核对"
	case "changes":
		return "变更补扫"
	default:
		return "全量生成"
	}
}

func (s *DriveSTRMService) logDriveSTRM(id, level, message string) {
	if s.Logs != nil {
		s.Logs.Append("STRM", id, "目录生成", level, monitorLogDetail(message, ""))
	}
}

func driveSTRMLogStats(stats model.DriveSTRMStats) string {
	return fmt.Sprintf("目录 %d，视频 %d，STRM 写入 %d，未变化 %d，失败 %d", stats.Directories, stats.Videos, stats.Written, stats.Unchanged, stats.Failed)
}

// Record the saved outcome once; cancellation is distinct from a failed scan.
func (s *DriveSTRMService) logDriveSTRMResult(value model.DriveSTRM, scanErr error, processCanceled bool, elapsed time.Duration) {
	if s.Logs == nil {
		return
	}
	message := fmt.Sprintf("目录「%s」%s：%s，耗时 %.2f 秒", value.Name, driveSTRMLogMode(value.Mode), driveSTRMLogStats(value.Stats), elapsed.Seconds())
	level := "INFO"
	switch {
	case value.Status == "stopped":
		level, message = "WARNING", message+"；本次任务已停止"
	case processCanceled:
		level, message = "WARNING", message+"；服务退出，本次未完成任务将在重启后恢复"
	case scanErr != nil:
		level = "ERROR"
		if value.Enabled {
			message += "；将在 " + driveSTRMRetryDelay(value.FailureCount).String() + " 后重试"
		} else {
			message += "；监控已暂停，恢复后重试"
		}
		message += "；未完成：" + scanErr.Error()
	default:
		message += "；扫描完成，待办附件由后台继续处理"
	}
	s.logDriveSTRM(value.ID, level, message)
}
