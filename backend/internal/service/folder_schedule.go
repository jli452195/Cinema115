package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"strm115/backend/internal/model"
)

const folderStabilityDelay = 5 * time.Second
const folderBusyRetryDelay = 5 * time.Second

// NotifyOrganized advances only enabled, healthy, idle schedules for this
// account. The caller has already released its directory-write reservation.
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
		value.StableFingerprint, value.StableScans = "", 0
		value.NextScanAt, value.UpdatedAt = time.Now().UTC(), time.Now().UTC()
		value.ScheduleMessage = "下载整理已完成，正在检查目录"
		if err := s.Store.SaveFolderMonitor(ctx, value); err != nil {
			return err
		}
	}
	s.Wake()
	return nil
}

// Keep the normal worker cadence, but wake precisely for a stability check.
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

// One bounded inspection, no sleeping goroutine. A second stable observation
// is persisted across restarts; the worker rechecks it before moving files.
func (s *FolderMonitorService) inspectScheduled(root context.Context, id string) error {
	s.mu.Lock()
	value, err := s.Store.GetFolderMonitor(root, id)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	if s.running[id] || !scheduledFolderDue(value, time.Now().UTC()) {
		s.mu.Unlock()
		return nil
	}
	s.running[id] = true
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.running, id); s.mu.Unlock() }()
	ctx, cancel := context.WithTimeout(root, 2*time.Minute)
	defer cancel()
	value.NextScanAt = time.Now().UTC().Add(time.Duration(value.IntervalSeconds) * time.Second)
	if s.ReserveWork != nil {
		release, err := s.ReserveWork()
		if err != nil {
			value.ScheduleMessage = "整理、清理或字幕导入正在执行，5 秒后重试"
			value.NextScanAt = time.Now().UTC().Add(folderBusyRetryDelay)
			value.StableFingerprint, value.StableScans = "", 0
			return s.checkpoint(ctx, &value)
		}
		defer release()
	}
	pause := func(message string) error {
		value.SchedulePaused, value.ScheduleMessage = message, ""
		value.NextScanAt = time.Time{}
		saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		return s.checkpoint(saveCtx, &value)
	}
	previousRevision, previousCredential := value.BatchAccountRevision, value.BatchCredentialHash
	if err := s.pinSettings(&value); err != nil {
		return pause("主账号或生成设置不可用，请检查后重新开启")
	}
	account, err := s.Accounts.Primary(value.AccountID)
	if err != nil || account.Cookie == "" {
		return pause("主账号未登录，请登录后重新开启")
	}
	if account.Revision != value.BatchAccountRevision || cookieHash(account.Cookie) != value.BatchCredentialHash {
		return pause("主账号凭据发生变化，请重新开启自动执行")
	}
	if previousRevision != value.BatchAccountRevision || previousCredential != value.BatchCredentialHash {
		value.StableFingerprint, value.StableScans = "", 0
	}
	roots, err := s.listRootEntries(ctx, value.SourceCID, account.Cookie)
	if err != nil {
		return pause("目录读取失败，请核对来源目录及主账号后重新开启")
	}
	value.LastScanAt = time.Now().UTC()
	if len(roots) == 0 {
		value.Stage, value.ScheduleMessage = "empty", "目录为空，等待下次检查"
		value.StableFingerprint, value.StableScans = "", 0
		return s.checkpoint(ctx, &value)
	}
	if len(roots) > 1000 {
		return pause("目录超过 1000 个顶层条目，请缩小处理范围")
	}
	manifest, _, err := s.scanDrive(ctx, value.SourceCID, nil, account.Cookie)
	if err != nil {
		return pause("目录内容核对失败，请检查后重新开启")
	}
	if len(manifest) == 0 {
		value.StableFingerprint, value.StableScans = "", 0
		return s.checkpoint(ctx, &value)
	}
	fingerprint := manifestFingerprint(manifest)
	if fingerprint != value.StableFingerprint || value.StableScans == 0 {
		value.Stage = "waiting"
		value.StableFingerprint, value.StableScans = fingerprint, 1
		value.ScheduleMessage = "已发现内容，5 秒后确认目录稳定"
		value.NextScanAt = time.Now().UTC().Add(folderStabilityDelay)
		return s.checkpoint(ctx, &value)
	}
	// Re-read the switch under lock: disabling during inspection must prevent
	// submission, and changing interval invalidates the previous observation.
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := s.Store.GetFolderMonitor(ctx, id)
	if err != nil {
		return err
	}
	if !current.Enabled || current.ScheduleVersion != value.ScheduleVersion {
		return nil
	}
	var nonce [12]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return fmt.Errorf("创建定时任务编号: %w", err)
	}
	value.LogTaskID, value.LogAttempt = "STRM-MON-"+hex.EncodeToString(nonce[:]), 1
	value.GenerationTaskID, value.RecoveryMessage = "", ""
	value.ShareURL, value.SharePassword, value.ShareReviewing = "", "", false
	value.Stage, value.TaskTrigger, value.TaskPending = "waiting", "scheduled", true
	value.ScheduleMessage = "目录已稳定，任务已提交"
	value.NextScanAt = time.Time{}
	value.UpdatedAt = time.Now().UTC()
	return s.Store.SaveFolderMonitor(ctx, value)
}

func recordFolderRun(value *model.FolderMonitor) {
	if value.LogTaskID == "" || (value.LastError == "" && value.Stage != "completed" && value.Stage != "ended") {
		return
	}
	message := value.LastError
	if value.Stage == "completed" {
		message = "分享及 STRM 生成完成，临时目录已清理"
	}
	if value.Stage == "ended" {
		message = value.RecoveryMessage
		if message == "" {
			message = "任务已结束，原文件位置见批次详情"
		}
	}
	run := model.FolderRun{TaskID: value.LogTaskID, Trigger: value.TaskTrigger, Status: value.Stage, Message: message, ShareURL: value.ShareURL, Time: time.Now().UTC()}
	for _, old := range value.RecentRuns {
		if old.TaskID == run.TaskID && old.Status == run.Status && old.Message == run.Message {
			return
		}
	}
	runs := make([]model.FolderRun, 0, 20)
	runs = append(runs, run)
	for _, old := range value.RecentRuns {
		if old.TaskID != run.TaskID && len(runs) < 20 {
			runs = append(runs, old)
		}
	}
	value.RecentRuns = runs
}
