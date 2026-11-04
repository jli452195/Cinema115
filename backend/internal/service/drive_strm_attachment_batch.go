package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"golang.org/x/sync/errgroup"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
)

type driveSTRMMetadataBatchReader interface {
	PrepareDriveMetadata(context.Context, []p115.ShareFile, string, string) (map[string]p115.DriveMetadataLink, error)
	ReadDriveMetadataLink(context.Context, p115.DriveMetadataLink) ([]byte, error)
}

type driveSTRMAttachmentThumbnail struct {
	signature string
	accountID string
	sourceCID string
	uid       string
	revision  uint64
	url       string
	expires   time.Time
}

type driveSTRMAttachmentLocal struct {
	directory string
	previous  model.DriveSTRMFile
	record    model.DriveSTRMFile
	data      []byte
	unchanged bool
}

func attachmentThumbnailKey(job model.DriveSTRMAttachment) string {
	return job.Record.MappingID + ":" + job.Record.FileID
}

// 缩略图签名只在当前进程短期复用，不进入持久化待办。
func (s *DriveSTRMService) cacheAttachmentThumbnailsLocked(jobs []model.DriveSTRMAttachment, cookie string) {
	if s.metadataThumbnails == nil {
		s.metadataThumbnails = make(map[string]driveSTRMAttachmentThumbnail)
	}
	for _, job := range jobs {
		if job.Thumbnail == "" {
			continue
		}
		account, err := s.Accounts.Primary(job.AccountID)
		if err != nil || cookie == "" || account.Cookie != cookie {
			continue
		}
		if len(s.metadataThumbnails) >= 2048 {
			clear(s.metadataThumbnails)
		}
		s.metadataThumbnails[attachmentThumbnailKey(job)] = driveSTRMAttachmentThumbnail{signature: job.Record.Signature, accountID: job.AccountID, sourceCID: job.SourceCID, uid: accountUID(account.Cookie), revision: account.Revision, url: job.Thumbnail, expires: time.Now().Add(5 * time.Minute)}
	}
}

func (s *DriveSTRMService) attachmentThumbnailLocked(job model.DriveSTRMAttachment) string {
	key := attachmentThumbnailKey(job)
	cached := s.metadataThumbnails[key]
	account, err := s.Accounts.Primary(job.AccountID)
	if err != nil || cached.signature != job.Record.Signature || !cached.expires.After(time.Now()) || cached.accountID != job.AccountID || cached.sourceCID != job.SourceCID || cached.uid != accountUID(account.Cookie) || cached.revision != account.Revision {
		delete(s.metadataThumbnails, key)
		return ""
	}
	return cached.url
}

func (s *DriveSTRMService) prepareAttachmentLocal(ctx context.Context, job model.DriveSTRMAttachment) (*driveSTRMAttachmentLocal, bool, error) {
	directory, err := ResolveOutputDirectory(s.Root, job.OutputDir)
	if err != nil {
		return nil, false, err
	}
	indexed, err := s.Store.ListDriveSTRMFilesByIDs(ctx, job.Record.MappingID, []string{job.Record.FileID})
	if err != nil {
		return nil, false, err
	}
	local := &driveSTRMAttachmentLocal{directory: directory, record: job.Record}
	var source model.DriveSTRMFile
	for _, record := range indexed {
		if record.FileID == job.Record.FileID {
			source = record
			if driveSTRMSamePath(record.Path, job.Record.Path) {
				local.previous = record
			}
			break
		}
	}
	local.unchanged = driveSTRMAttachmentUnchanged(directory, local.record, local.previous, job.Size, job.Settings.MetadataImageMode)
	if local.unchanged {
		local.record.Digest = local.previous.Digest
		local.record.MetadataPreview, local.record.MetadataSize = local.previous.MetadataPreview, local.previous.MetadataSize
		return local, false, nil
	}
	// 只复用本程序校验过的本地字节；原文件模式不能继承压缩预览图。
	if !driveSTRMSamePath(source.Path, local.record.Path) && source.Digest != "" && source.Signature != "" && source.Signature == local.record.Signature && (!source.MetadataPreview || job.Settings.MetadataImageMode == "cdn") {
		expected := job.Size
		if source.MetadataPreview {
			expected = source.MetadataSize
		}
		cached, _, readErr := readDriveSTRMCleanupOutput(directory, source.Path)
		if readErr == nil && int64(len(cached)) == expected && driveSTRMDigest(cached) == source.Digest {
			local.data = cached
			local.record.MetadataPreview, local.record.MetadataSize = source.MetadataPreview, int64(len(cached))
			local.record.Digest = source.Digest
			return local, false, nil
		}
	}
	if job.Size <= 0 || job.Size > p115.MaxMetadataSize || job.PickCode == "" || len(job.SHA1) != 40 {
		return nil, false, fmt.Errorf("附件身份、大小或取链信息无效（最大 32 MB）")
	}
	return local, true, nil
}

func (s *DriveSTRMService) commitAttachment(ctx context.Context, job model.DriveSTRMAttachment, local *driveSTRMAttachmentLocal, cookie string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, currentCookie, err := s.attachmentCurrent(ctx, job)
	if err != nil {
		return err
	}
	if accountUID(currentCookie) != accountUID(cookie) {
		return ErrAccountChanged
	}
	written := false
	if !local.unchanged {
		written, err = writeDriveSTRMOutput(ctx, local.directory, local.record, local.previous, local.data)
		if err != nil {
			return err
		}
	}
	if err := s.Store.CompleteDriveSTRMAttachment(ctx, job, local.record); err != nil {
		return err
	}
	delete(s.metadataThumbnails, attachmentThumbnailKey(job))
	if written {
		value.Stats.MetadataWritten++
		value.UpdatedAt = time.Now().UTC()
		return s.Store.SaveDriveSTRM(ctx, value)
	}
	return nil
}

func (s *DriveSTRMService) nextAttachmentBatch(ctx context.Context) ([]model.DriveSTRMAttachment, context.Context, time.Duration, bool, error) {
	first, workCtx, wait, found, err := s.nextAttachment(ctx)
	if err != nil || !found {
		return nil, workCtx, wait, found, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	jobs, err := s.Store.ListDriveSTRMAttachments(ctx, 32)
	if err != nil {
		if s.metadataCancel != nil {
			s.metadataCancel()
		}
		s.metadataActive, s.metadataCancel = "", nil
		return nil, nil, wait, false, err
	}
	first.Thumbnail = s.attachmentThumbnailLocked(first)
	batch := []model.DriveSTRMAttachment{first}
	if s.AttachmentState().Probing {
		return batch, workCtx, wait, true, nil
	}
	for _, job := range jobs {
		if job.ID == first.ID || job.NextAttemptAt.After(time.Now()) || job.Record.MappingID != first.Record.MappingID || job.Settings != first.Settings || job.AccountID != first.AccountID || job.SourceCID != first.SourceCID || job.OutputDir != first.OutputDir {
			continue
		}
		job.Thumbnail = s.attachmentThumbnailLocked(job)
		batch = append(batch, job)
	}
	return batch, workCtx, wait, true, nil
}

func (s *DriveSTRMService) releaseAttachmentBatch() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.metadataCancel != nil {
		s.metadataCancel()
	}
	s.metadataActive, s.metadataCancel = "", nil
	s.Wake()
}

func (s *DriveSTRMService) retryAttachment(ctx, workCtx context.Context, job model.DriveSTRMAttachment, cookie string, downloadErr error) error {
	if downloadErr == nil || ctx.Err() != nil || errors.Is(downloadErr, context.Canceled) {
		return nil
	}
	detail := monitorLogDetail(downloadErr.Error(), cookie)
	delay := max(driveSTRMRetryDelay(job.Attempts+1), s.attachmentCooling())
	if err := s.Store.RetryDriveSTRMAttachment(ctx, job, detail, time.Now().UTC().Add(delay)); err != nil {
		return err
	}
	s.logDriveSTRM(job.Record.MappingID, "WARNING", fmt.Sprintf("附件下载未完成：%s；将在 %s 后单独重试，不重扫目录；原因：%s", job.Record.Path, delay, detail))
	return nil
}

func (s *DriveSTRMService) downloadAttachmentBatch(ctx, workCtx context.Context, jobs []model.DriveSTRMAttachment, reader driveSTRMMetadataBatchReader) (int, error) {
	defer s.releaseAttachmentBatch()
	if len(jobs) == 0 {
		return 0, nil
	}
	settings, err := s.attachmentSafetySettings(workCtx, jobs[0].Settings)
	if err != nil {
		if workCtx.Err() != nil {
			return 0, nil
		}
		return 0, err
	}
	_, cookie, err := s.attachmentCurrent(workCtx, jobs[0])
	if err != nil {
		for _, job := range jobs {
			if retryErr := s.retryAttachment(ctx, workCtx, job, "", err); retryErr != nil {
				return 0, retryErr
			}
		}
		return 0, nil
	}
	probe := s.AttachmentState().Probing
	locals := make([]*driveSTRMAttachmentLocal, len(jobs))
	errorsByJob := make([]error, len(jobs))
	networks := make([]bool, len(jobs))
	committed := make([]bool, len(jobs))
	files := make([]p115.ShareFile, 0, len(jobs))
	completed := 0
	needsLinks := false
	for i, job := range jobs {
		local, network, err := s.prepareAttachmentLocal(workCtx, job)
		locals[i], errorsByJob[i] = local, err
		if err != nil {
			continue
		}
		if !network {
			errorsByJob[i] = s.commitAttachment(workCtx, job, local, cookie)
			local.data = nil
			if errorsByJob[i] == nil {
				completed++
			}
			continue
		}
		file := p115.ShareFile{ID: job.Record.FileID, Name: job.Name, PickCode: job.PickCode, SHA1: job.SHA1, Size: job.Size, Thumbnail: job.Thumbnail}
		networks[i] = true
		files = append(files, file)
		needsLinks = needsLinks || !p115.DriveMetadataUsesPreview(file, settings.MetadataImageMode)
	}
	var links map[string]p115.DriveMetadataLink
	if len(files) > 0 {
		if needsLinks {
			err = s.waitAttachment(workCtx, settings)
		} else if delay := s.attachmentCooling(); delay > 0 {
			err = fmt.Errorf("附件队列正在冷却")
		}
		if err == nil {
			_, currentCookie, currentErr := s.attachmentCurrent(workCtx, jobs[0])
			if currentErr != nil {
				err = currentErr
			} else if accountUID(currentCookie) != accountUID(cookie) {
				err = ErrAccountChanged
			} else {
				links, err = reader.PrepareDriveMetadata(workCtx, files, cookie, settings.MetadataImageMode)
			}
		}
		if err != nil {
			s.coolAttachments(err, "附件取链")
			for i := range jobs {
				if errorsByJob[i] == nil && networks[i] {
					errorsByJob[i] = err
				}
			}
		} else {
			batchCtx, cancel := context.WithCancel(workCtx)
			group, groupCtx := errgroup.WithContext(batchCtx)
			group.SetLimit(max(1, min(settings.MetadataConcurrency, 8)))
			for i, job := range jobs {
				local := locals[i]
				if errorsByJob[i] != nil || !networks[i] {
					continue
				}
				group.Go(func() error {
					if groupCtx.Err() != nil {
						errorsByJob[i] = groupCtx.Err()
						return nil
					}
					link, exists := links[job.Record.FileID]
					if !exists {
						errorsByJob[i] = fmt.Errorf("附件批量取链缺少当前文件")
						return nil
					}
					if link.Err != nil {
						errorsByJob[i] = link.Err
						if attachmentThrottled(link.Err) {
							cancel()
							s.coolAttachments(link.Err, "附件取链")
						}
						return nil
					}
					_, currentCookie, currentErr := s.attachmentCurrent(groupCtx, job)
					if currentErr != nil {
						errorsByJob[i] = currentErr
						return nil
					}
					if accountUID(currentCookie) != accountUID(cookie) {
						errorsByJob[i] = ErrAccountChanged
						return nil
					}
					if groupCtx.Err() != nil || s.attachmentCooling() > 0 {
						errorsByJob[i] = context.Canceled
						return nil
					}
					data, readErr := reader.ReadDriveMetadataLink(groupCtx, link)
					if readErr != nil {
						errorsByJob[i] = readErr
						if attachmentThrottled(readErr) {
							cancel()
							s.coolAttachments(readErr, "附件下载")
						}
						return nil
					}
					if !link.Preview && int64(len(data)) != job.Size || len(data) == 0 || int64(len(data)) > p115.MaxMetadataSize {
						errorsByJob[i] = fmt.Errorf("附件下载大小不符合预期")
						return nil
					}
					local.data, local.record.Digest = data, driveSTRMDigest(data)
					local.record.MetadataPreview, local.record.MetadataSize = link.Preview, int64(len(data))
					errorsByJob[i] = s.commitAttachment(groupCtx, job, local, cookie)
					committed[i] = errorsByJob[i] == nil
					local.data = nil
					return nil
				})
			}
			_ = group.Wait()
			cancel()
			for i := range jobs {
				if networks[i] && committed[i] {
					completed++
				}
			}
		}
	}
	for i, job := range jobs {
		if err := s.retryAttachment(ctx, workCtx, job, cookie, errorsByJob[i]); err != nil {
			return completed, err
		}
	}
	if probe && len(files) == 1 && completed == len(jobs) {
		s.attachmentProbeSucceeded()
	}
	return completed, nil
}
