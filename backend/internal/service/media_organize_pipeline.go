package service

import (
	"context"
	"slices"

	"strm115/backend/internal/client/clouddrive2"
)

// 存储共用识别与命名缓存，文件定位和写入仍交给各自的原生入口。
func (s *MediaOrganizer) processMediaBatch(ctx context.Context, files *mediaOrganizeFiles, run *MediaOrganizeRun, entries []MediaOrganizeEntry) error {
	if err := s.checkOrganizeStop(run); err != nil {
		return err
	}
	if len(entries) == 0 {
		return nil
	}
	run.processing = make(map[string]bool, len(entries))
	for _, entry := range entries {
		run.processing[entry.File.ID] = true
	}
	defer func() { run.processing = nil }()
	if files.recognized == nil {
		files.recognized = make(map[string]bool)
	}
	batchCtx := ctx
	if run.Rule.Storage == "cd2" {
		batchCtx = clouddrive2.WithDirectoryCache(ctx)
		if err := s.cd2AddReferences(batchCtx, files, run, entries); err != nil {
			return err
		}
	} else if run.Rule.Storage == "115" {
		if err := s.add115References(batchCtx, files, run, entries); err != nil {
			return err
		}
	}
	retained := make(map[string]string)
	for _, entry := range entries {
		if mediaSidecar(entry) && entry.Status == "retained" {
			retained[entry.File.ID] = entry.Message
		}
	}
	mediaAppendScanEntries(run, entries)
	groups := make([]int, 0, len(run.Groups))
	for i, group := range run.Groups {
		if !files.recognized[group.ID] {
			groups = append(groups, i)
			files.recognized[group.ID] = true
		}
	}
	if err := s.recognize(batchCtx, run, groups); err != nil {
		return err
	}
	if err := s.render(batchCtx, files, run); err != nil {
		return err
	}
	mediaDiscardOrphanSidecars(files, run, entries)
	// 部分历史目标核验失败时，共享附件不能被其余成功引用重新激活。
	for i := range run.Entries {
		entry := &run.Entries[i]
		if message, keep := retained[entry.File.ID]; keep {
			entry.Status, entry.Message = "retained", message
			entry.Group, entry.VideoID, entry.Relative, entry.Target, entry.ResultCode = "", "", "", "", ""
			entry.Result = nil
		}
	}
	return s.execute(batchCtx, files, run)
}

// 历史目标读取异常只保护相关附件，不扩大为整个目录。
func mediaRetainReferenceSidecars(files *mediaOrganizeFiles, entries []MediaOrganizeEntry, owners map[string][]string, videoID, message string) {
	for i := range entries {
		entry := &entries[i]
		if !mediaSidecar(*entry) || !slices.Contains(owners[entry.File.ID], videoID) {
			continue
		}
		entry.Status, entry.Message = "retained", message
		if files.localPending == nil {
			files.localPending = make(map[string]bool)
		}
		files.localPending[entry.File.ID] = true
	}
}
