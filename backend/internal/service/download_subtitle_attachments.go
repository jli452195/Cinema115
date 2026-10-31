package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func subtitleAttachmentID(id string) bool {
	b, err := hex.DecodeString(id)
	return err == nil && len(b) == 16 && hex.EncodeToString(b) == id
}

func subtitleDigest(value string) bool {
	b, err := hex.DecodeString(value)
	return err == nil && len(b) == sha256.Size && hex.EncodeToString(b) == value
}

func (s *DownloadOrganize) attachmentPath(sub DownloadSubtitle) (string, error) {
	if s.SubtitleAttachments == "" || !subtitleAttachmentID(sub.AttachmentID) || !subtitleDigest(sub.SHA256) {
		return "", fmt.Errorf("%w：本地字幕附件无效", ErrOrganizeInput)
	}
	return filepath.Join(s.SubtitleAttachments, sub.AttachmentID), nil
}

// Preview metadata never contains file bytes. Validate and atomically persist
// attachments before Start can accept a task; filenames always come from us.
func (s *DownloadOrganize) SaveSubtitleAttachments(ctx context.Context, previewID string, files map[string][]byte) error {
	s.mu.Lock()
	s.Organize.mu.Lock()
	s.Organize.pruneLocked()
	s.prunePreviewsLocked()
	jobs := []OrganizeJob{}
	if batch, ok := s.batches[previewID]; ok {
		jobs = cloneDownloadJobs(batch.jobs)
	} else if job, ok := s.Organize.plans[previewID]; ok && job.Download != nil {
		jobs = append(jobs, cloneOrganizeJob(job))
	}
	s.Organize.mu.Unlock()
	s.mu.Unlock()
	if len(jobs) == 0 {
		return fmt.Errorf("%w：预览已过期，请重新确认", ErrOrganizeConflict)
	}
	seen := make(map[string]bool)
	var total int64
	for _, job := range jobs {
		if _, err := s.Organize.account(job.Config.AccountID); err != nil {
			return err
		}
		for _, sub := range job.Download.Subtitles {
			if sub.LocalID == "" {
				continue
			}
			data, ok := files[sub.LocalID]
			digest := sha256.Sum256(data)
			if !ok || int64(len(data)) != sub.File.Size || hex.EncodeToString(digest[:]) != sub.SHA256 {
				return fmt.Errorf("%w：本地字幕内容与预览不一致", ErrOrganizeInput)
			}
			if !seen[sub.LocalID] {
				total += int64(len(data))
				seen[sub.LocalID] = true
			}
		}
	}
	if len(seen) != len(files) || len(files) == 0 || len(files) > 100 || total > MaxSubtitleImportBytes {
		return fmt.Errorf("%w：字幕附件重复、未选中或超过大小限制", ErrOrganizeInput)
	}
	s.attachmentMu.Lock()
	defer s.attachmentMu.Unlock()
	if err := os.MkdirAll(s.SubtitleAttachments, 0700); err != nil {
		return fmt.Errorf("创建字幕附件目录: %w", err)
	}
	for _, job := range jobs {
		for _, sub := range job.Download.Subtitles {
			if sub.LocalID == "" {
				continue
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			target, err := s.attachmentPath(sub)
			if err != nil {
				return err
			}
			if _, err := s.readDownloadSubtitle(ctx, sub); err == nil {
				continue // Idempotent retry after a lost HTTP receipt.
			}
			if err := writeSubtitleAttachment(target, files[sub.LocalID]); err != nil {
				return err
			}
		}
	}
	return nil
}

func writeSubtitleAttachment(target string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(target), ".subtitle-*")
	if err != nil {
		return fmt.Errorf("准备字幕附件: %w", err)
	}
	defer os.Remove(f.Name())
	_, writeErr := f.Write(data)
	if writeErr == nil {
		writeErr = f.Sync()
	}
	closeErr := f.Close()
	if writeErr != nil {
		return fmt.Errorf("保存字幕附件: %w", writeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("关闭字幕附件: %w", closeErr)
	}
	if err := os.Rename(f.Name(), target); err != nil {
		return fmt.Errorf("提交字幕附件: %w", err)
	}
	return nil
}

func (s *DownloadOrganize) readDownloadSubtitle(ctx context.Context, sub DownloadSubtitle) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if sub.LocalID == "" {
		if s.Subtitles == nil {
			return nil, fmt.Errorf("字幕库不可用")
		}
		return s.Subtitles.Read(ctx, sub.File)
	}
	filename, err := s.attachmentPath(sub)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("读取本地字幕附件: %w", err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, sub.File.Size+1))
	if err != nil {
		return nil, fmt.Errorf("读取本地字幕附件内容: %w", err)
	}
	digest := sha256.Sum256(data)
	if int64(len(data)) != sub.File.Size || hex.EncodeToString(digest[:]) != sub.SHA256 {
		return nil, fmt.Errorf("本地字幕附件大小或内容已变化")
	}
	return data, nil
}

func (s *DownloadOrganize) requireSubtitleAttachments(job OrganizeJob) error {
	for _, sub := range job.Download.Subtitles {
		if sub.LocalID == "" || sub.Status == "done" || sub.Status == "skipped" {
			continue
		}
		filename, err := s.attachmentPath(sub)
		if err != nil {
			return err
		}
		info, err := os.Stat(filename)
		if err != nil || !info.Mode().IsRegular() || info.Size() != sub.File.Size {
			return fmt.Errorf("%w：请先上传本次选择的字幕附件", ErrOrganizeConflict)
		}
	}
	return nil
}

// Existing worker owns cleanup. Pending/interrupted jobs retain attachments;
// completed files are removed promptly, abandoned previews after one hour.
func (s *DownloadOrganize) pruneSubtitleAttachments(now time.Time) error {
	if s.SubtitleAttachments == "" {
		return nil
	}
	keep, finished := make(map[string]bool), make(map[string]bool)
	add := func(job OrganizeJob) {
		if job.Download == nil {
			return
		}
		for _, sub := range job.Download.Subtitles {
			if sub.LocalID != "" {
				if sub.Status == "done" || sub.Status == "skipped" {
					finished[sub.AttachmentID] = true
				} else {
					keep[sub.AttachmentID] = true
				}
			}
		}
	}
	s.mu.Lock()
	s.Organize.mu.Lock()
	s.Organize.pruneLocked()
	s.prunePreviewsLocked()
	for _, job := range s.Organize.doc.Jobs {
		add(job)
	}
	for _, job := range s.Organize.plans {
		add(job)
	}
	for _, batch := range s.batches {
		for _, job := range batch.jobs {
			add(job)
		}
	}
	s.Organize.mu.Unlock()
	s.mu.Unlock()
	s.attachmentMu.Lock()
	defer s.attachmentMu.Unlock()
	entries, err := os.ReadDir(s.SubtitleAttachments)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("读取字幕附件目录: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !subtitleAttachmentID(entry.Name()) && !strings.HasPrefix(entry.Name(), ".subtitle-") || keep[entry.Name()] {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("读取字幕附件信息: %w", err)
		}
		if !finished[entry.Name()] && now.Sub(info.ModTime()) < time.Hour {
			continue
		}
		if err := os.Remove(filepath.Join(s.SubtitleAttachments, entry.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("删除未使用字幕附件: %w", err)
		}
	}
	return nil
}
