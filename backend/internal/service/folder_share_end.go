package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"strm115/backend/internal/model"
)

// EndBatch releases the configuration without any cloud or filesystem writes.
// The common task is retired with CAS before the directory checkpoint changes,
// so it cannot race the generator's claim or a retry from the share logs.
func (s *FolderMonitorService) EndBatch(ctx context.Context, id, expectedTaskID string) (model.FolderMonitor, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, err := s.Store.GetFolderMonitor(ctx, id)
	if err != nil {
		return value, err
	}
	if value.LogTaskID != expectedTaskID {
		return value, generationInputError("目录任务已变化，请刷新后操作")
	}
	if s.running[id] {
		return value, generationInputError("目录操作正在执行，请等待当前操作结束后再结束任务")
	}
	if value.Stage == "ended" {
		return value, nil
	}
	if !value.TaskPending && !value.BatchCreationPending && value.BatchDirectoryID == "" && monitorIdleStage(value.Stage) {
		return value, generationInputError("当前没有需要结束的批次")
	}
	if err := s.retireBatchTask(ctx, value); err != nil {
		return value, err
	}
	archiveFolderBatch(&value)
	value.ScheduleVersion++
	recordFolderRun(&value)
	if err := s.Store.SaveFolderMonitor(ctx, value); err != nil {
		return value, err
	}
	s.Logs.Append("STRM", value.LogTaskID, "目录分享", "INFO", "用户结束本批任务，已解除目录占用；保留现有文件、分享和 STRM，历史位置已归档")
	return value, nil
}

// Retire the durable generator with CAS before releasing its directory.
func (s *FolderMonitorService) retireBatchTask(ctx context.Context, value model.FolderMonitor) error {
	if value.GenerationTaskID != "" {
		if s.Tasks == nil {
			return generationInputError("分享任务服务尚未初始化")
		}
		task, err := s.Tasks.Store.GetShareTask(ctx, value.GenerationTaskID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil {
			if task.Status == "running" {
				return generationInputError("STRM 正在生成，请等待当前执行结束后再结束任务")
			}
			before := task
			var req shareTaskRequest
			if err := json.Unmarshal(task.Request, &req); err != nil {
				return err
			}
			req.DirectoryEnded, req.RetrySafe = true, false
			task.Request, err = json.Marshal(req)
			if err != nil {
				return err
			}
			task.Status, task.NextRetryAt, task.UpdatedAt = "cancelled", nil, time.Now().UTC()
			task.Note = "目录批次已结束，不再重试；文件处理结果见目录批次记录。"
			ok, err := s.Tasks.Store.CompareShareTask(ctx, before, task)
			if err != nil {
				return err
			}
			if !ok {
				return generationInputError("分享任务状态已变化，请刷新后再结束")
			}
		}
	}
	return nil
}

func archiveFolderBatch(value *model.FolderMonitor) {
	value.EndedBatches = append(value.EndedBatches, model.EndedFolderBatch{
		LogTaskID: value.LogTaskID, GenerationTaskID: value.GenerationTaskID,
		AccountID: value.AccountID, SourceCID: value.SourceCID, SourcePath: value.SourcePath,
		DirectoryID: value.BatchDirectoryID, DirectoryName: value.BatchDirectoryName,
		ShareURL: value.ShareURL, Stage: value.Stage, LastError: value.LastError,
		Cleaned: value.CleanupIndex > 0, EndedAt: time.Now().UTC(),
	})
	value.Stage, value.LastError, value.TaskPending = "ended", "", false
	value.GenerationTaskID, value.GenerationSettings = "", nil
	value.BatchIDs, value.BatchFingerprint = nil, ""
	value.BatchDirectoryID, value.BatchDirectoryName, value.BatchParentCID = "", "", ""
	value.BatchCreationPending = false
	value.BatchMoveIndex, value.CleanupIndex = 0, 0
	value.StableFingerprint, value.StableScans = "", 0
	value.ShareURL, value.SharePassword, value.ShareReviewing = "", "", false
	value.Enabled, value.IntervalSeconds, value.NextScanAt = false, 0, time.Time{}
	value.SchedulePaused, value.ScheduleMessage = "", ""
	value.UpdatedAt = time.Now().UTC()
}
