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

func (s *DriveSTRMService) queueAttachments(ctx context.Context, value model.DriveSTRM, jobs []model.DriveSTRMAttachment) error {
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
	// The scan's finish wakes the downloader once, after all video pages.
	return nil
}

// Only one attachment is downloaded at a time, independently of video scans.
// The durable queue retains the original deadline and settings on rediscovery.
func (s *DriveSTRMService) nextAttachment(ctx context.Context) (model.DriveSTRMAttachment, context.Context, time.Duration, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	wait := 30 * time.Second
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

// All mappings share the same start-to-start limit; a slow download never
// earns credit for a later burst. Local skips do not consume the allowance.
func (s *DriveSTRMService) waitAttachment(ctx context.Context, settings model.DriveSTRMSettings) error {
	interval := time.Duration(0)
	if settings.MetadataRateEnabled {
		interval = time.Second / time.Duration(max(1, settings.MetadataFilesPerSecond))
	}
	delay := time.Until(s.metadataStarted.Add(max(interval, s.metadataInterval)))
	if delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.metadataStarted, s.metadataInterval = time.Now(), interval
	return nil
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
	if DirectLinkKey(account.Cookie) != job.CredentialKey {
		return value, "", ErrAccountChanged
	}
	return value, account.Cookie, nil
}

func (s *DriveSTRMService) downloadAttachment(ctx context.Context, job model.DriveSTRMAttachment) (string, error) {
	_, cookie, err := s.attachmentCurrent(ctx, job)
	if err != nil {
		return "", err
	}
	directory, err := ResolveOutputDirectory(s.Root, job.OutputDir)
	if err != nil {
		return cookie, err
	}
	indexed, err := s.Store.ListDriveSTRMFilesByIDs(ctx, job.Record.MappingID, []string{job.Record.FileID})
	if err != nil {
		return cookie, err
	}
	previous := model.DriveSTRMFile{}
	source := model.DriveSTRMFile{}
	for _, record := range indexed {
		if record.FileID == job.Record.FileID {
			source = record
			if driveSTRMSamePath(record.Path, job.Record.Path) {
				previous = record
			}
			break
		}
	}
	record := job.Record
	unchanged := driveSTRMAttachmentUnchanged(directory, record, previous, job.Size)
	var data []byte
	if unchanged {
		record.Digest = previous.Digest
	} else {
		// A pure cloud move can reuse a verified local attachment. Its source
		// signature and local digest must both match before skipping 115.
		if !driveSTRMSamePath(source.Path, record.Path) && source.Digest != "" && source.Signature != "" && source.Signature == record.Signature {
			cached, _, readErr := readDriveSTRMCleanupOutput(directory, source.Path)
			if readErr == nil && int64(len(cached)) == job.Size && driveSTRMDigest(cached) == source.Digest {
				data = cached
			}
		}
		if data == nil {
			if job.Size <= 0 || job.Size > p115.MaxMetadataSize || job.PickCode == "" || len(job.SHA1) != 40 {
				return cookie, fmt.Errorf("附件身份、大小或取链信息无效（最大 32 MB）")
			}
			reader, ok := s.Client.(driveSTRMMetadataReader)
			if !ok {
				return cookie, fmt.Errorf("当前客户端不支持下载附件")
			}
			if err := s.waitAttachment(ctx, job.Settings); err != nil {
				return cookie, err
			}
			// Recheck after a throttle wait, without replacing the fixed cookie.
			if _, _, err := s.attachmentCurrent(ctx, job); err != nil {
				return cookie, err
			}
			data, err = reader.ReadDriveMetadata(ctx, p115.ShareFile{ID: record.FileID, Name: job.Name, PickCode: job.PickCode, SHA1: job.SHA1, Size: job.Size}, cookie)
			if err != nil {
				return cookie, err
			}
			if int64(len(data)) != job.Size {
				return cookie, fmt.Errorf("附件下载不完整")
			}
		}
		record.Digest = driveSTRMDigest(data)
	}
	// Stop, deletion and root changes cannot race the final local write.
	s.mu.Lock()
	defer s.mu.Unlock()
	value, _, err := s.attachmentCurrent(ctx, job)
	if err != nil {
		return cookie, err
	}
	written := false
	if !unchanged {
		written, err = writeDriveSTRMOutput(ctx, directory, record, previous, data)
		if err != nil {
			return cookie, err
		}
	}
	if err := s.Store.CompleteDriveSTRMAttachment(ctx, job, record); err != nil {
		return cookie, err
	}
	if written {
		value.Stats.MetadataWritten++
		value.UpdatedAt = time.Now().UTC()
		return cookie, s.Store.SaveDriveSTRM(ctx, value)
	}
	return cookie, nil
}

func (s *DriveSTRMService) finishAttachment(ctx, workCtx context.Context, job model.DriveSTRMAttachment, cookie string, downloadErr error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.metadataCancel != nil {
		s.metadataCancel()
	}
	s.metadataActive, s.metadataCancel = "", nil
	s.Wake()
	if downloadErr == nil || ctx.Err() != nil || workCtx.Err() != nil && errors.Is(downloadErr, context.Canceled) {
		return nil
	}
	return s.Store.RetryDriveSTRMAttachment(ctx, job, monitorLogDetail(downloadErr.Error(), cookie), time.Now().UTC().Add(driveSTRMRetryDelay(job.Attempts+1)))
}

func (s *DriveSTRMService) runAttachments(ctx context.Context) error {
	for ctx.Err() == nil {
		cleaned, err := s.cleanupDriveSTRMOutput(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		job, workCtx, wait, found, err := s.nextAttachment(ctx)
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
			continue
		}
		if cleaned {
			continue
		}
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
