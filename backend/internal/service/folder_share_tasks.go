package service

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"strm115/backend/internal/model"
)

// 提交时固定当前主账号和生成设置，分享目录不会转交给其他账号。
func (s *FolderMonitorService) pinSettings(value *model.FolderMonitor) error {
	settings := s.sharedSettings(value)
	if settings.AccountID == "" || settings.AccountID != value.AccountID {
		return generationInputError("监控目录绑定的主账号已变化，请重新选择并保存目录")
	}
	if s.SharedSettings != nil {
		applyFolderGenerationOptions(value, settings.Generation)
	}
	if err := s.validate(*value); err != nil {
		return err
	}
	settings.Generation = s.generationInput(*value, "")
	settings.ReshareEnabled = false
	var err error
	value.GenerationSettings, err = json.Marshal(settings)
	return err
}

func applyFolderGenerationOptions(value *model.FolderMonitor, input GenerateInput) {
	value.BaseURL, value.OutputDir = input.BaseURL, input.OutputDir
	value.VideoExtensions, value.MetadataExtensions = input.VideoExtensions, input.MetadataExtensions
	value.MinSizeMB, value.Recursive = input.MinSizeMB, input.Recursive
}

func (s *FolderMonitorService) batchSettings(value *model.FolderMonitor) (AutomationSettings, error) {
	// 旧批次沿用已记录的生成选项和主账号。
	settings := AutomationSettings{AccountID: value.AccountID, ParentCID: value.BatchParentCID, Generation: s.generationInput(*value, value.ShareURL)}
	if len(value.GenerationSettings) > 0 {
		if err := json.Unmarshal(value.GenerationSettings, &settings); err != nil {
			return settings, fmt.Errorf("读取本批分享设置: %w", err)
		}
	}
	if settings.AccountID != value.AccountID {
		return settings, generationInputError("本批账号绑定不一致")
	}
	return settings, nil
}

// 已确认的分享按需提交公共直生，生成结果由独立任务保存。
func (s *FolderMonitorService) queueFolderGeneration(ctx context.Context, value *model.FolderMonitor) error {
	settings, err := s.batchSettings(value)
	if err != nil {
		return err
	}
	input := settings.Generation
	taskID := value.GenerationTaskID
	if taskID == "" && strings.TrimSpace(input.OutputDir) == "" {
		return nil
	}
	if s.Tasks == nil || s.Tasks.Store == nil || s.Tasks.Settings == nil || s.Tasks.Generator == nil {
		return generationInputError("分享 STRM 服务尚未初始化")
	}
	if taskID == "" {
		if value.LogTaskID == "" {
			return generationInputError("缺少目录分享批次编号")
		}
		digest := sha256.Sum256([]byte("directory:" + value.LogTaskID))
		taskID = hex.EncodeToString(digest[:16])
	}
	// 批次归档失败后可再次提交，已存在的生成任务保留原状态。
	if _, err := s.Tasks.Store.GetShareTask(ctx, taskID); err == nil {
		value.GenerationTaskID = taskID
		return nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("查询目录分享生成记录: %w", err)
	}
	input.URL, input.Mode, input.CID = value.ShareURL, "direct", "0"
	if err := ValidateGenerationOptions(s.Root, input); err != nil {
		return fmt.Errorf("校验目录分享生成设置: %w", err)
	}
	ctx = context.WithValue(ctx, automationContextKey{}, settings)
	ctx = WithGenerationSource(ctx, "目录分享")
	ctx = context.WithValue(ctx, directoryGenerationKey{}, value.LogTaskID)
	if _, _, err := s.Tasks.prepareTaskRecord(ctx, input, taskID, "queued"); err != nil {
		return fmt.Errorf("提交目录分享 STRM 任务: %w", err)
	}
	value.GenerationTaskID = taskID
	logGeneration(ctx, "INFO", "已提交公共分享 STRM 任务："+taskID)
	select {
	case s.Tasks.RetryWake <- struct{}{}:
	default:
	}
	return nil
}

type directoryGenerationKey struct{}

// Trigger 返回当前批次提交记录，重复触发沿用已有批次。
func (s *FolderMonitorService) Trigger(ctx context.Context, id string) (model.FolderMonitor, bool, error) {
	err := s.ScanNow(ctx, id)
	if err != nil && !errors.Is(err, ErrFolderMonitorBusy) {
		return model.FolderMonitor{}, false, err
	}
	value, readErr := s.Store.GetFolderMonitor(ctx, id)
	return value, err == nil, readErr
}
