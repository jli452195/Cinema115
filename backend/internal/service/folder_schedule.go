package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"strm115/backend/internal/model"
)

const folderBusyRetryDelay = 5 * time.Second

// NotifyOrganized 提前检查同一账号下已开启且空闲的监控目录。
func (s *FolderMonitorService) NotifyOrganized(ctx context.Context, accountID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	values, err := s.Store.ListFolderMonitors(ctx)
	if err != nil {
		return err
	}
	for _, value := range values {
		if value.AccountID != accountID || !value.Enabled || value.ScheduleVersion == 0 || value.SchedulePaused != "" || value.LastError != "" || value.TaskPending || value.BatchCreationPending || value.BatchDirectoryID != "" || !monitorIdleStage(value.Stage) {
			continue
		}
		value.ScheduleVersion++
		value.NextScanAt, value.UpdatedAt = time.Now().UTC(), time.Now().UTC()
		value.ScheduleMessage = "下载整理已完成，正在检查目录"
		if err := s.Store.SaveFolderMonitor(ctx, value); err != nil {
			return err
		}
	}
	s.Wake()
	return nil
}

// 按下次检查或排队重试时间唤醒目录任务。
func folderWakeDelay(values []model.FolderMonitor, now time.Time) time.Duration {
	delay := 15 * time.Second
	for _, value := range values {
		if scheduledFolderDue(value, value.NextScanAt) || (value.TaskPending && !value.NextScanAt.IsZero()) {
			if remaining := value.NextScanAt.Sub(now); remaining < delay {
				delay = remaining
			}
		}
	}
	if delay < time.Millisecond {
		return time.Millisecond
	}
	return delay
}

func scheduledFolderDue(value model.FolderMonitor, now time.Time) bool {
	return value.Enabled && value.ScheduleVersion > 0 && value.IntervalSeconds >= 300 && value.SchedulePaused == "" && value.LastError == "" &&
		!value.TaskPending && !value.BatchCreationPending && value.BatchDirectoryID == "" && monitorIdleStage(value.Stage) &&
		!value.NextScanAt.IsZero() && !now.Before(value.NextScanAt)
}

func (s *FolderMonitorService) checkScheduled(ctx context.Context) error {
	values, err := s.Store.ListFolderMonitors(ctx)
	if err != nil {
		return err
	}
	for _, value := range values {
		if scheduledFolderDue(value, time.Now().UTC()) {
			if err := s.inspectScheduled(ctx, value.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

// 定时器只提交任务，目录读取统一由执行器完成。
func (s *FolderMonitorService) inspectScheduled(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, err := s.Store.GetFolderMonitor(ctx, id)
	if err != nil {
		return err
	}
	if s.running[id] || !scheduledFolderDue(value, time.Now().UTC()) {
		return nil
	}
	logID, err := newFolderLogID()
	if err != nil {
		return err
	}
	if err := s.pinSettings(&value); err != nil {
		value.SchedulePaused, value.ScheduleMessage = "主账号或监控目录不可用，请检查后重新开启", ""
		value.NextScanAt, value.UpdatedAt = time.Time{}, time.Now().UTC()
		return s.Store.SaveFolderMonitor(ctx, value)
	}
	value.LogTaskID, value.LogAttempt = logID, 1
	value.GenerationTaskID, value.RecoveryMessage = "", ""
	value.ShareURL, value.SharePassword = "", ""
	value.Stage, value.TaskTrigger, value.TaskPending = "waiting", "scheduled", true
	value.ScheduleMessage = "任务已排队，等待检查目录"
	value.NextScanAt = time.Time{}
	value.UpdatedAt = time.Now().UTC()
	return s.Store.SaveFolderMonitor(ctx, value)
}

func newFolderLogID() (string, error) {
	var nonce [12]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", fmt.Errorf("创建目录任务编号: %w", err)
	}
	return "STRM-MON-" + hex.EncodeToString(nonce[:]), nil
}

func recordFolderRun(value *model.FolderMonitor) {
	if value.LogTaskID == "" || (value.LastError == "" && value.Stage != "completed" && value.Stage != "ended") {
		return
	}
	run := model.FolderRun{
		TaskID: value.LogTaskID, Trigger: value.TaskTrigger, Status: value.Stage, Message: value.LastError,
		ShareURL: value.ShareURL, DirectoryID: value.BatchDirectoryID, DirectoryName: value.BatchDirectoryName,
		GenerationTaskID: value.GenerationTaskID,
	}
	if value.Stage == "completed" {
		run.Message = "分享已创建"
		if value.CleanupIndex > 0 {
			run.Message = "分享及 STRM 生成完成，临时目录已清理"
		}
	}
	if value.Stage == "ended" {
		run.Message = value.RecoveryMessage
		if run.Message == "" {
			run.Message = "任务已结束，原文件位置见批次详情"
		}
		for _, ended := range value.EndedBatches {
			if ended.LogTaskID == value.LogTaskID {
				run.ShareURL, run.GenerationTaskID = ended.ShareURL, ended.GenerationTaskID
				run.DirectoryID, run.DirectoryName = ended.DirectoryID, ended.DirectoryName
				break
			}
		}
	}
	for _, old := range value.RecentRuns {
		if old.TaskID != run.TaskID {
			continue
		}
		// 完成记录已在清空批次字段前保存，后续检查点沿用它。
		if old.Status == "completed" && run.Status == "completed" {
			return
		}
		old.Time = time.Time{}
		if old == run {
			return
		}
		break
	}
	run.Time = time.Now().UTC()
	runs := make([]model.FolderRun, 0, 20)
	runs = append(runs, run)
	for _, old := range value.RecentRuns {
		if old.TaskID != run.TaskID && len(runs) < 20 {
			runs = append(runs, old)
		}
	}
	value.RecentRuns = runs
}
