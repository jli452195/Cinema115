package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
)

func (s *DriveSTRMService) wakeAttachments() {
	select {
	case s.metadataWake <- struct{}{}:
	default:
	}
}

func driveSTRMAttachmentMatches(value model.DriveSTRM, job model.DriveSTRMAttachment) bool {
	return value.AccountID == job.AccountID && value.SourceCID == job.SourceCID && value.OutputDir == job.OutputDir && value.Status != "stopped"
}

func (s *DriveSTRMService) queueAttachments(ctx context.Context, value model.DriveSTRM, jobs []model.DriveSTRMAttachment, cookies ...string) error {
	if len(jobs) == 0 {
		return ctx.Err()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	current, err := s.Store.GetDriveSTRM(ctx, value.ID)
	if err != nil {
		return err
	}
	if !driveSTRMAttachmentMatches(current, jobs[0]) {
		return context.Canceled
	}
	if err := s.Store.QueueDriveSTRMAttachments(ctx, jobs); err != nil {
		return err
	}
	if len(cookies) > 0 {
		s.cacheAttachmentThumbnailsLocked(jobs, cookies[0])
	}
	// 扫描完成后统一唤醒下载器，不在逐页扫描期间争用网盘请求。
	return nil
}

// 批次由同一个协调器领取，持久队列保留重试时间和任务设置快照。
func (s *DriveSTRMService) nextAttachment(ctx context.Context) (model.DriveSTRMAttachment, context.Context, time.Duration, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	wait := 30 * time.Second
	if delay := s.attachmentCooling(); delay > 0 {
		return model.DriveSTRMAttachment{}, nil, min(wait, delay), false, nil
	}
	jobs, err := s.Store.ListDriveSTRMAttachments(ctx, 32)
	if err != nil {
		return model.DriveSTRMAttachment{}, nil, wait, false, err
	}
	for _, job := range jobs {
		if delay := time.Until(job.NextAttemptAt); delay > 0 {
			wait = min(wait, delay)
			break
		}
		value, err := s.Store.GetDriveSTRM(ctx, job.Record.MappingID)
		if errors.Is(err, storage.ErrNotFound) || err == nil && !driveSTRMAttachmentMatches(value, job) {
			if err := s.Store.DeleteDriveSTRMAttachment(ctx, job.ID); err != nil {
				return job, nil, wait, false, err
			}
			continue
		}
		if err != nil {
			return job, nil, wait, false, err
		}
		if s.active == value.ID || value.Queued {
			continue
		}
		workCtx, cancel := context.WithCancel(ctx)
		s.metadataActive, s.metadataCancel = value.ID, cancel
		return job, workCtx, wait, true, nil
	}
	return model.DriveSTRMAttachment{}, nil, wait, false, nil
}

func (s *DriveSTRMService) attachmentCurrent(ctx context.Context, job model.DriveSTRMAttachment) (model.DriveSTRM, string, error) {
	if err := ctx.Err(); err != nil {
		return model.DriveSTRM{}, "", err
	}
	value, err := s.Store.GetDriveSTRM(ctx, job.Record.MappingID)
	if err != nil {
		return value, "", err
	}
	if !driveSTRMAttachmentMatches(value, job) {
		return value, "", context.Canceled
	}
	account, err := s.Accounts.Primary(job.AccountID)
	if err != nil {
		return value, "", err
	}
	return value, account.Cookie, nil
}

func (s *DriveSTRMService) downloadAttachment(ctx context.Context, job model.DriveSTRMAttachment) (string, error) {
	_, cookie, err := s.attachmentCurrent(ctx, job)
	if err != nil {
		return "", err
	}
	local, network, err := s.prepareAttachmentLocal(ctx, job)
	if err != nil {
		return cookie, err
	}
	if network {
		reader, ok := s.Client.(driveSTRMMetadataReader)
		if !ok {
			return cookie, fmt.Errorf("当前客户端不支持下载附件")
		}
		settings, err := s.attachmentSafetySettings(ctx, job.Settings)
		if err != nil {
			return cookie, err
		}
		if err := s.waitAttachment(ctx, settings); err != nil {
			return cookie, err
		}
		if _, _, err := s.attachmentCurrent(ctx, job); err != nil {
			return cookie, err
		}
		data, err := reader.ReadDriveMetadata(ctx, p115.ShareFile{ID: job.Record.FileID, Name: job.Name, PickCode: job.PickCode, SHA1: job.SHA1, Size: job.Size}, cookie)
		if err != nil {
			s.coolAttachments(err, "附件下载")
			return cookie, err
		}
		if int64(len(data)) != job.Size {
			return cookie, fmt.Errorf("附件下载不完整")
		}
		local.data, local.record.Digest = data, driveSTRMDigest(data)
		local.record.MetadataPreview, local.record.MetadataSize = false, int64(len(data))
		if s.AttachmentState().Probing {
			s.attachmentProbeSucceeded()
		}
	}
	return cookie, s.commitAttachment(ctx, job, local, cookie)
}

func (s *DriveSTRMService) finishAttachment(ctx, workCtx context.Context, job model.DriveSTRMAttachment, cookie string, downloadErr error) error {
	s.releaseAttachmentBatch()
	return s.retryAttachment(ctx, workCtx, job, cookie, downloadErr)
}

func (s *DriveSTRMService) runAttachments(ctx context.Context) error {
	lastLog := time.Now()
	completed, checked := 0, 0
	flush := func() {
		if completed+checked > 0 {
			s.logDriveSTRM("", "INFO", fmt.Sprintf("后台处理进度：完成附件任务 %d，旧输出清理检查 %d 项", completed, checked))
			completed, checked = 0, 0
			lastLog = time.Now()
		}
	}
	defer flush()
	for ctx.Err() == nil {
		cleaned, err := s.cleanupDriveSTRMOutput(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if cleaned {
			checked++
		}
		if time.Since(lastLog) >= 15*time.Second {
			flush()
		}
		wait := 30 * time.Second
		if reader, batchCapable := s.Client.(driveSTRMMetadataBatchReader); batchCapable {
			jobs, workCtx, nextWait, found, err := s.nextAttachmentBatch(ctx)
			wait = nextWait
			if err != nil {
				if ctx.Err() != nil {
					return nil
				}
				return err
			}
			if found {
				done, err := s.downloadAttachmentBatch(ctx, workCtx, jobs, reader)
				completed += done
				if err != nil {
					if ctx.Err() != nil {
						return nil
					}
					return err
				}
				continue
			}
		} else {
			job, workCtx, nextWait, found, err := s.nextAttachment(ctx)
			wait = nextWait
			if err != nil {
				if ctx.Err() != nil {
					return nil
				}
				return err
			}
			if found {
				cookie, downloadErr := s.downloadAttachment(workCtx, job)
				if err := s.finishAttachment(ctx, workCtx, job, cookie, downloadErr); err != nil {
					if ctx.Err() != nil {
						return nil
					}
					return err
				}
				if downloadErr == nil {
					completed++
				}
				continue
			}
		}
		if cleaned {
			continue
		}
		flush()
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
		case <-s.metadataWake:
		case <-timer.C:
		}
		timer.Stop()
	}
	return nil
}
