package service

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
)

// pinSettings runs at submission, before the worker can start remote writes.
// A directory binding is never transferred to a newly selected share account.
func (s *FolderMonitorService) pinSettings(value *model.FolderMonitor) error {
	settings := s.sharedSettings(value)
	if settings.AccountID == "" || settings.AccountID != value.AccountID {
		return generationInputError("分享 STRM 账号已变化，请重新选择并保存分享目录")
	}
	if s.SharedSettings != nil {
		value.BaseURL, value.OutputDir = settings.Generation.BaseURL, settings.Generation.OutputDir
		value.VideoExtensions, value.MetadataExtensions = settings.Generation.VideoExtensions, settings.Generation.MetadataExtensions
		value.MinSizeMB, value.Recursive = settings.Generation.MinSizeMB, settings.Generation.Recursive
	}
	if err := s.validate(*value); err != nil {
		return err
	}
	account, err := s.Accounts.Primary(value.AccountID)
	if err != nil {
		return err
	}
	value.BatchAccountRevision, value.BatchCredentialHash = account.Revision, cookieHash(account.Cookie)
	settings.Generation = s.generationInput(*value, "")
	settings.ReshareEnabled = false
	value.GenerationSettings, err = json.Marshal(settings)
	return err
}

func (s *FolderMonitorService) batchSettings(value *model.FolderMonitor) (AutomationSettings, error) {
	// Legacy batches retain their saved generation options and account.
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

// sharedGeneration links a durable directory checkpoint to a durable common
// share task. The common worker owns generation and retries; this worker only
// proceeds to cleanup after all video and metadata writes have succeeded.
func (s *FolderMonitorService) sharedGeneration(ctx context.Context, value *model.FolderMonitor) (bool, error) {
	if value.GenerationTaskID == "" {
		digest := sha256.Sum256([]byte("directory:" + value.LogTaskID))
		value.GenerationTaskID = hex.EncodeToString(digest[:16])
		if err := s.checkpoint(ctx, value); err != nil {
			return false, err
		}
	}
	task, err := s.Tasks.Store.GetShareTask(ctx, value.GenerationTaskID)
	if errors.Is(err, sql.ErrNoRows) {
		settings, settingsErr := s.batchSettings(value)
		if settingsErr != nil {
			return false, s.failAt(ctx, value, "generating", settingsErr.Error())
		}
		ctx = context.WithValue(ctx, automationContextKey{}, settings)
		ctx = WithGenerationSource(ctx, "目录分享")
		ctx = context.WithValue(ctx, directoryGenerationKey{}, value.LogTaskID)
		ctx = context.WithValue(ctx, directoryFingerprintKey{}, value.BatchFingerprint)
		ctx = context.WithValue(ctx, directoryIDKey{}, value.BatchDirectoryID)
		_, _, err = s.Tasks.prepareTaskRecord(ctx, s.generationInput(*value, value.ShareURL), value.GenerationTaskID, "queued")
		if err != nil {
			return false, s.failAt(ctx, value, "generating", "提交分享 STRM 任务失败："+err.Error())
		}
		logGeneration(ctx, "INFO", "已提交公共分享 STRM 任务："+value.GenerationTaskID)
		select {
		case s.Tasks.RetryWake <- struct{}{}:
		default:
		}
		return false, nil
	}
	if err != nil {
		return false, err
	}
	switch task.Status {
	case "queued", "running", "retry_queued", "waiting_review":
		return false, nil
	case "success":
		logGeneration(ctx, "INFO", fmt.Sprintf("公共 STRM 任务完成：%d 个视频", task.Written))
		return true, nil
	case "interrupted":
		// Direct generation has no remote writes; retry the same saved request.
		results, retryErr := s.Tasks.RetryTasks(ctx, []string{task.ID})
		if retryErr != nil {
			return false, retryErr
		}
		if len(results) == 1 && results[0].Accepted {
			return false, nil
		}
	}
	return false, s.failAt(ctx, value, "generating", "分享 STRM 任务未完成，临时目录已保留："+task.Error)
}

type directoryGenerationKey struct{}
type directoryFingerprintKey struct{}
type directoryIDKey struct{}

var ErrDirectoryBatchChanged = errors.New("本批临时目录或文件已删除、移动或变化，请结束本次任务后重新分享；不会自动清理剩余文件")

// VerifyGeneration belongs to the shared generation task so both snapshot
// waits and review retries use the task's saved settings and credentials.
func (s *FolderMonitorService) VerifyGeneration(ctx context.Context, input GenerateInput, fingerprint, directoryID string) error {
	share, err := p115.ParseShareURL(input.URL)
	if err != nil {
		return err
	}
	value := model.FolderMonitor{VideoExtensions: input.VideoExtensions, MinSizeMB: input.MinSizeMB, Recursive: input.Recursive}
	cookie := generationCookie(ctx, nil)
	if directoryID == "" {
		// Resolve older requests through their exact parent checkpoint.
		logID, _ := ctx.Value(directoryGenerationKey{}).(string)
		if logID != "" {
			values, err := s.Store.ListFolderMonitors(ctx)
			if err != nil {
				return err
			}
			for _, batch := range values {
				if batch.LogTaskID == logID && batch.BatchDirectoryID != "" {
					directoryID = batch.BatchDirectoryID
					if fingerprint == "" {
						fingerprint = batch.BatchFingerprint
					}
					break
				}
			}
		}
	}
	// Check the owned directory before treating incomplete share contents as
	// an eventually consistent snapshot. A real deletion will never converge.
	if directoryID != "" {
		current, _, err := s.scanDrive(ctx, directoryID, nil, cookie)
		if err != nil {
			return fmt.Errorf("读取本批临时目录失败，请核对目录是否已被删除或移动；可结束任务解除占用: %w", err)
		}
		if len(current) == 0 || (fingerprint != "" && !manifestMatchesFingerprint(current, fingerprint)) {
			return ErrDirectoryBatchChanged
		}
	}
	manifest, mediaID, _, err := s.waitShareSnapshot(ctx, &value, share, cookie)
	if err != nil {
		return err
	}
	if fingerprint != "" && !manifestMatchesFingerprint(manifest, fingerprint) {
		return fmt.Errorf("分享快照尚未包含本批完整内容: %w", p115.ErrShareSnapshotPending)
	}
	if mediaID == "" {
		return fmt.Errorf("%w；分享读取 %d 项，没有匹配的视频", ErrNoMedia, len(manifest))
	}
	if _, err := s.Client.DownloadURL(ctx, share, mediaID, cookie); err != nil {
		return fmt.Errorf("验证分享视频可读性: %w", err)
	}
	return nil
}

// Trigger exposes a receipt for all UI entry points. Duplicate clicks resolve
// to the same persisted directory job rather than creating another batch.
func (s *FolderMonitorService) Trigger(ctx context.Context, id string) (model.FolderMonitor, bool, error) {
	err := s.ScanNow(ctx, id)
	if err != nil && !errors.Is(err, ErrFolderMonitorBusy) {
		return model.FolderMonitor{}, false, err
	}
	value, readErr := s.Store.GetFolderMonitor(ctx, id)
	return value, err == nil, readErr
}
