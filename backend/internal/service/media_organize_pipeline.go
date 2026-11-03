package service

import (
	"context"

	"strm115/backend/internal/client/clouddrive2"
)

// 存储共用识别与命名缓存，文件定位和写入仍交给各自的原生入口。
func (s *MediaOrganizer) processMediaBatch(ctx context.Context, files *mediaOrganizeFiles, run *MediaOrganizeRun, entries []MediaOrganizeEntry, recheck bool) error {
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
	}
	mediaAppendScanEntries(run, entries)
	oldGroups := run.Groups
	run.Groups = make([]MediaOrganizeGroup, 0, len(oldGroups))
	for _, group := range oldGroups {
		if !files.recognized[group.ID] {
			run.Groups = append(run.Groups, group)
			files.recognized[group.ID] = true
		}
	}
	err := s.recognize(batchCtx, run)
	updated := make(map[string]MediaOrganizeGroup, len(run.Groups))
	for _, group := range run.Groups {
		updated[group.ID] = group
	}
	run.Groups = oldGroups
	for i, group := range run.Groups {
		if current, ok := updated[group.ID]; ok {
			run.Groups[i] = current
		}
	}
	if err != nil {
		return err
	}
	if err := s.render(batchCtx, files, run); err != nil {
		return err
	}
	mediaDiscardOrphanSidecars(files, run, entries)
	if recheck {
		if err := files.recheckLocalCompanions(batchCtx, run); err != nil {
			return err
		}
	}
	return s.execute(batchCtx, files, run)
}
