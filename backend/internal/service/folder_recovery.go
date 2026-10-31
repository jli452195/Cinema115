package service

import (
	"context"
	"fmt"
	"time"

	"strm115/backend/internal/model"
)

type directoryMissingClient interface {
	DirectoryMissing(context.Context, string, string) (bool, error)
}

func (s *FolderMonitorService) directoryMissing(ctx context.Context, id, cookie string) (bool, error) {
	client, ok := s.Client.(directoryMissingClient)
	if !ok {
		return false, fmt.Errorf("当前客户端无法精确核对目录是否删除")
	}
	return client.DirectoryMissing(ctx, id, cookie)
}

// Recover only removes empty, recorded staging directories. Nonempty batches
// and ambiguous upstream results stay available for manual recovery. It shares
// the worker claim, so cloud I/O never runs while holding the service lock.
func (s *FolderMonitorService) Recover(ctx context.Context, id, expectedTaskID string) (model.FolderMonitor, error) {
	s.mu.Lock()
	value, err := s.Store.GetFolderMonitor(ctx, id)
	if err != nil {
		s.mu.Unlock()
		return value, err
	}
	if value.LogTaskID != expectedTaskID || s.running[id] {
		s.mu.Unlock()
		return value, generationInputError("任务已变化或正在执行，请稍后核对")
	}
	if value.LastError == "" || (value.BatchDirectoryID == "" && !value.BatchCreationPending) {
		s.mu.Unlock()
		return value, nil
	}
	s.running[id] = true
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.running, id); s.mu.Unlock() }()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if s.ReserveWork != nil {
		release, err := s.ReserveWork()
		if err != nil {
			return value, generationInputError("整理或字幕导入正在执行，请稍后核对残留")
		}
		defer release()
	}
	if s.Accounts == nil || s.Client == nil {
		return value, generationInputError("残留核对服务尚未初始化")
	}
	account, err := s.Accounts.Primary(value.AccountID)
	if err != nil {
		return value, generationInputError("原批次主账号已变化或未登录，保留目录，请检查账号配置")
	}
	parent := s.sharedParentCID(&value)
	entries, err := s.listRootEntries(ctx, parent, account.Cookie)
	if err != nil {
		return value, fmt.Errorf("核对临时目录位置: %w", err)
	}
	present := false
	recoveryMessage := ""
	if value.BatchCreationPending && value.BatchDirectoryID == "" {
		matches := 0
		for _, entry := range entries {
			if entry.Name == value.BatchDirectoryName {
				matches++
				if entry.Directory {
					value.BatchDirectoryID = entry.ID
				}
			}
		}
		if matches != 1 || value.BatchDirectoryID == "" {
			return value, generationInputError("创建结果尚未确认：未找到唯一匹配的临时目录，不会重新创建或删除")
		}
		// This exact random name was persisted and checked absent before creation.
		value.BatchCreationPending = false
		if err := s.checkpoint(ctx, &value); err != nil {
			return value, err
		}
	}
	if value.BatchDirectoryID == "" || value.BatchDirectoryID == "0" || value.BatchDirectoryID == parent || value.BatchDirectoryID == value.SourceCID {
		return value, generationInputError("临时目录归属无效，未执行清理")
	}
	for _, entry := range entries {
		if entry.ID == value.BatchDirectoryID && entry.Directory {
			present = true
		}
	}
	if !present {
		missing, err := s.directoryMissing(ctx, value.BatchDirectoryID, account.Cookie)
		if err != nil {
			return value, err
		}
		if !missing {
			return value, generationInputError("目录可能已被移动，不能确认删除；保留批次")
		}
		recoveryMessage = "本批临时目录已确认不存在，已结束失败批次；未将任务标记为生成成功。"
	} else {
		contents, err := s.listRootEntries(ctx, value.BatchDirectoryID, account.Cookie)
		if err != nil {
			return value, fmt.Errorf("核对临时目录内容: %w", err)
		}
		if len(contents) != 0 {
			return value, generationInputError("临时目录仍有文件，已保留；可继续原任务或结束任务后自行整理")
		}
		recoveryMessage = "失败批次的空临时目录已移入回收站，已解除占用；未删除原文件。"
	}
	// CAS retirement prevents a generator/retry from racing the deletion.
	value.Stage, value.TaskPending = "recovering", false
	if err := s.checkpoint(ctx, &value); err != nil {
		return value, err
	}
	if err := s.retireBatchTask(ctx, value); err != nil {
		return value, err
	}
	if present {
		atParent, err := s.driveRootContains(ctx, parent, value.BatchDirectoryID, account.Cookie)
		if err != nil || !atParent {
			return value, generationInputError("清理前临时目录位置发生变化或无法确认，已保留目录")
		}
		contents, err := s.listRootEntries(ctx, value.BatchDirectoryID, account.Cookie)
		if err != nil || len(contents) != 0 {
			return value, generationInputError("清理前目录内容发生变化或无法读取，已保留目录")
		}
		if err := s.Accounts.ValidatePrimary(value.AccountID); err != nil {
			return value, err
		}
		if err := s.Client.DeleteEntries(ctx, []string{value.BatchDirectoryID}, account.Cookie); err != nil {
			return value, fmt.Errorf("回收空临时目录: %w", err)
		}
		stillPresent, err := s.driveRootContains(ctx, parent, value.BatchDirectoryID, account.Cookie)
		if err != nil || stillPresent {
			return value, generationInputError("清理响应已收到，但目录移除结果尚未确认，请再次核对残留")
		}
		value.CleanupIndex = 1
	}
	value.RecoveryMessage = recoveryMessage
	archiveFolderBatch(&value)
	if err := s.checkpoint(ctx, &value); err != nil {
		return value, err
	}
	s.Logs.Append("STRM", value.LogTaskID, "目录分享", "INFO", value.RecoveryMessage)
	return value, nil
}
