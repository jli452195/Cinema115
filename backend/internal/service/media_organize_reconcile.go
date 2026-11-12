package service

import (
	"context"
	"fmt"
	"slices"
)

// 仅以已保存的成功证据收敛旧失败；查询交集，不加载无关成功历史或访问存储服务。
func (s *MediaOrganizer) reconcileResolvedFailuresLocked(ctx context.Context, ids []string) error {
	candidates, err := s.db.LoadMediaOrganizeResolvedCandidates(ctx, ids)
	if err != nil {
		return fmt.Errorf("查询已解决的整理失败: %w", err)
	}
	if len(candidates) == 0 {
		return nil
	}
	failedIDs := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		failedIDs = append(failedIDs, candidate.RunID)
	}
	slices.Sort(failedIDs)
	failedIDs = slices.Compact(failedIDs)
	if err := s.loadHistoryLocked(ctx, failedIDs, "", ""); err != nil {
		return err
	}
	removed := make(map[string]map[string]bool)
	for _, candidate := range candidates {
		completed, err := mediaDecodeStoredRun(candidate.Completed)
		if err != nil {
			return err
		}
		pos := slices.IndexFunc(s.doc.Runs, func(run MediaOrganizeRun) bool { return run.ID == candidate.RunID })
		if pos < 0 {
			continue
		}
		old := &s.doc.Runs[pos]
		if old.ID == s.active || len(s.inFlight[old.ID]) > 0 || s.eventRecoveryPendingLocked(old.ID) ||
			old.Rule.ID != completed.Rule.ID || !mediaOrphanBinding(old.Rule).matches(completed.Rule) {
			continue
		}
		for _, current := range completed.Entries {
			proof, ok := mediaPrimaryCompletion(current)
			// 成功后重新放回来源的新扫描属于新任务，不能被先前的成功历史吞掉。
			if !ok || proof.At.IsZero() || old.CreatedAt.IsZero() || old.CreatedAt.After(proof.At) {
				continue
			}
			for _, previous := range old.Entries {
				if previous.File.MediaType == "video" && previous.Status != "done" && mediaResolvedSameSource(previous, current, old.Rule.Storage) {
					if removed[old.ID] == nil {
						removed[old.ID] = make(map[string]bool)
					}
					removed[old.ID][previous.File.ID] = true
				}
			}
		}
	}
	changed := make([]string, 0, len(removed))
	for i := range s.doc.Runs {
		run := &s.doc.Runs[i]
		if len(removed[run.ID]) == 0 {
			continue
		}
		s.skipRemovedRetryTasksLocked(run, removed[run.ID])
		mediaRemoveResolvedTasks(run, removed[run.ID])
		changed = append(changed, run.ID)
	}
	if len(changed) == 0 {
		return nil
	}
	s.doc.Runs = slices.DeleteFunc(s.doc.Runs, func(run MediaOrganizeRun) bool { return len(removed[run.ID]) > 0 && len(run.Entries) == 0 })
	s.normalizeRetryBatchProgressLocked()
	if err := s.persistSQLiteLockedContext(ctx, changed); err != nil {
		return err
	}
	for _, id := range changed {
		pos := slices.IndexFunc(s.doc.Runs, func(run MediaOrganizeRun) bool { return run.ID == id })
		if pos < 0 || !mediaRunActive(s.doc.Runs[pos].Status) {
			s.removeActionLocked(id)
		}
	}
	return nil
}

func (s *MediaOrganizer) skipRemovedRetryTasksLocked(run *MediaOrganizeRun, removed map[string]bool) {
	if s.doc.RetryBatch == nil || run.RetryBatchID != s.doc.RetryBatch.ID || !mediaRetryBatchActive(s.doc.RetryBatch.Status) {
		return
	}
	for _, id := range run.TaskFileIDs {
		if removed[id] {
			s.doc.RetryBatch.Skipped++
		}
	}
	progress := s.doc.RetryBatch
	progress.Pending = max(0, progress.Total-progress.Success-progress.Failed-progress.Skipped)
}

func mediaRemoveResolvedTasks(run *MediaOrganizeRun, removed map[string]bool) {
	selected := mediaHasTaskSelection(run)
	run.Entries = slices.DeleteFunc(slices.Clone(run.Entries), func(entry MediaOrganizeEntry) bool {
		return removed[entry.File.ID] || removed[entry.VideoID]
	})
	if removed[run.TaskFileID] {
		run.TaskFileID = ""
	}
	run.TaskFileIDs = slices.DeleteFunc(slices.Clone(run.TaskFileIDs), func(id string) bool { return removed[id] })
	if selected && run.TaskFileID == "" && len(run.TaskFileIDs) == 0 {
		mediaFinishRun(run)
	}
	*run = mediaPersistentRun(*run)
}
