package service

import (
	"context"
	"fmt"
	"slices"
)

type MediaOrganizeBatchRetry struct {
	Items  []MediaOrganizeDeleteItem `json:"items,omitempty"`
	Status string                    `json:"status,omitempty"`
	Query  string                    `json:"q,omitempty"`
}

func mediaRetryBatchActive(status string) bool { return status == "running" || status == "stopping" }

// 调用方将取消与原任务变更一起保存；此处不额外提交事务。
func (s *MediaOrganizer) cancelRetryBatchRunLocked(run *MediaOrganizeRun) {
	progress := s.doc.RetryBatch
	if progress == nil || !mediaRetryBatchActive(progress.Status) || run.RetryBatchID != progress.ID {
		return
	}
	succeeded := mediaRetryTaskResults(*run)
	for _, fileID := range run.TaskFileIDs {
		switch succeeded[fileID] {
		case 1:
			progress.Success++
		case -1:
			progress.Failed++
		default:
			progress.Skipped++
		}
	}
	run.RetryBatchID = ""
	progress.Pending = max(0, progress.Total-progress.Success-progress.Failed-progress.Skipped)
}

// 删除或协调移除了待执行记录时，剩余选择结束后也必须收束批量摘要。
func (s *MediaOrganizer) normalizeRetryBatchProgressLocked() {
	progress := s.doc.RetryBatch
	if progress == nil || !mediaRetryBatchActive(progress.Status) {
		return
	}
	if slices.ContainsFunc(s.doc.Runs, func(run MediaOrganizeRun) bool {
		return run.RetryBatchID == progress.ID && len(run.TaskFileIDs) > 0
	}) {
		return
	}
	progress.Skipped += max(0, progress.Total-progress.Success-progress.Failed-progress.Skipped)
	progress.Pending = 0
	if progress.Status == "stopping" {
		progress.Status = "stopped"
	} else {
		progress.Status = "completed"
	}
}

// 查询进度仅投影当前工作集，不产生额外任务快照或历史流水。
func (s *MediaOrganizer) retryBatchProgressLocked() *MediaOrganizeRetryProgress {
	if s.doc.RetryBatch == nil {
		return nil
	}
	progress := *s.doc.RetryBatch
	if mediaRetryBatchActive(progress.Status) {
		for _, run := range s.doc.Runs {
			if run.RetryBatchID != progress.ID {
				continue
			}
			succeeded := mediaRetryTaskResults(run)
			for _, fileID := range run.TaskFileIDs {
				switch succeeded[fileID] {
				case 1:
					progress.Success++
				case -1:
					progress.Failed++
				}
			}
		}
		progress.Pending = max(0, progress.Total-progress.Success-progress.Failed-progress.Skipped)
	}
	return &progress
}

// 主视频完成即成功；本轮已写出的失败结果与尚未执行的空结果分开计数。
func mediaRetryTaskResults(run MediaOrganizeRun) map[string]int {
	results := make(map[string]int, len(run.TaskFileIDs))
	for _, entry := range run.Entries {
		if entry.File.MediaType == "video" {
			switch {
			case entry.Status == "done":
				results[entry.File.ID] = 1
			case entry.Result != nil && entry.Result.Status != "done" && entry.Result.Status != "retained":
				results[entry.File.ID] = -1
			}
		}
	}
	return results
}

// 结果与批量摘要由调用方在同一检查点保存，不留完成结果与计数分离的崩溃窗口。
func (s *MediaOrganizer) settleRetryBatchRunLocked(run *MediaOrganizeRun) bool {
	if s.doc.RetryBatch == nil || !mediaRetryBatchActive(s.doc.RetryBatch.Status) || run.RetryBatchID != s.doc.RetryBatch.ID {
		return false
	}
	progress := *s.doc.RetryBatch
	stopped := progress.Status == "stopping" || s.stopping[run.ID]
	succeeded := mediaRetryTaskResults(*run)
	for _, fileID := range run.TaskFileIDs {
		switch succeeded[fileID] {
		case 1:
			progress.Success++
		case -1:
			progress.Failed++
		default:
			if stopped {
				progress.Skipped++
			} else {
				progress.Failed++
			}
		}
	}
	run.RetryBatchID = ""
	progress.Pending = max(0, progress.Total-progress.Success-progress.Failed-progress.Skipped)
	remaining := slices.ContainsFunc(s.doc.Runs, func(other MediaOrganizeRun) bool {
		return other.ID != run.ID && other.RetryBatchID == progress.ID && len(other.TaskFileIDs) > 0
	})
	if !remaining {
		// 记录被显式删除或已被成功结果协调移除，也不继续作为待处理项。
		progress.Skipped += progress.Pending
		progress.Pending = 0
		progress.Status = "completed"
		if stopped {
			progress.Status = "stopped"
		}
	}
	s.doc.RetryBatch = &progress
	return true
}

// 停止仅取消本次批量的待执行工作，规则监控与其他排队任务继续保留。
func (s *MediaOrganizer) StopRetryBatchContext(ctx context.Context, id string) (*MediaOrganizeRetryProgress, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer s.releaseHistoryLocked()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.doc.RetryBatch == nil || s.doc.RetryBatch.ID != id {
		return nil, ErrMediaOrganizeInput
	}
	if !mediaRetryBatchActive(s.doc.RetryBatch.Status) {
		return s.retryBatchProgressLocked(), nil
	}
	progress := *s.doc.RetryBatch
	progress.Status = "stopping"
	changed, cancelled := make([]string, 0), make([]string, 0)
	active := ""
	for i := range s.doc.Runs {
		run := &s.doc.Runs[i]
		if run.RetryBatchID != id {
			continue
		}
		if run.ID == s.active {
			active = run.ID
			continue
		}
		progress.Skipped += len(run.TaskFileIDs)
		run.RetryBatchID, run.Status, run.Message = "", "interrupted", "已停止批量重试，未执行文件保留原处"
		changed = append(changed, run.ID)
		cancelled = append(cancelled, run.ID)
	}
	progress.Pending = max(0, progress.Total-progress.Success-progress.Failed-progress.Skipped)
	if active == "" {
		progress.Skipped += progress.Pending
		progress.Pending = 0
		progress.Status = "stopped"
	}
	s.doc.RetryBatch = &progress
	if err := s.persistLocked(changed...); err != nil {
		return nil, err
	}
	for _, runID := range cancelled {
		s.removeActionLocked(runID)
	}
	if active != "" {
		if s.stopping == nil {
			s.stopping = make(map[string]bool)
		}
		s.stopping[active] = true
	}
	s.historyChangedLocked()
	s.notify()
	return s.retryBatchProgressLocked(), nil
}

// 服务重启仅保留最近批量摘要，不自动恢复外部写入。
func mediaInterruptRetryBatch(progress *MediaOrganizeRetryProgress) {
	if progress != nil && mediaRetryBatchActive(progress.Status) {
		progress.Status = "interrupted"
		progress.Skipped += progress.Pending
		progress.Pending = 0
	}
}

type MediaOrganizeBatchRetryResult struct {
	Queued  int                         `json:"queued"`
	Skipped int                         `json:"skipped"`
	Reasons map[string]int              `json:"reasons"`
	Batch   *MediaOrganizeRetryProgress `json:"batch,omitempty"`
}

type MediaOrganizeRetryProgress struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Total   int    `json:"total"`
	Pending int    `json:"pending"`
	Success int    `json:"success"`
	Failed  int    `json:"failed"`
	Skipped int    `json:"skipped"`
}

// 批量重试先固定本次选择，再按原批次一次保存，避免同批多个单项请求互相覆盖。
func (s *MediaOrganizer) RetryBatchContext(ctx context.Context, input MediaOrganizeBatchRetry) (MediaOrganizeBatchRetryResult, error) {
	out := MediaOrganizeBatchRetryResult{Reasons: make(map[string]int)}
	if (input.Status == "") == (len(input.Items) == 0) || input.Status != "" && input.Status != "failed" ||
		len(input.Items) > 1000 || len(input.Query) > 500 || input.Status == "" && input.Query != "" {
		return out, ErrMediaOrganizeInput
	}
	selected := make(map[string]map[string]bool, len(input.Items))
	skip := func(reason string) { out.Skipped++; out.Reasons[reason]++ }
	for _, item := range input.Items {
		if item.RunID == "" || item.FileID == "" {
			return out, ErrMediaOrganizeInput
		}
		if selected[item.RunID] == nil {
			selected[item.RunID] = make(map[string]bool)
		}
		if selected[item.RunID][item.FileID] {
			skip("重复选择")
		}
		selected[item.RunID][item.FileID] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	defer s.releaseHistoryLocked()
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if s.doc.RetryBatch != nil && mediaRetryBatchActive(s.doc.RetryBatch.Status) {
		return out, ErrMediaOrganizeBusy
	}
	if input.Status != "" {
		refs, err := s.db.QueryMediaOrganizeFailedRefs(ctx, input.Query)
		if err != nil {
			return out, fmt.Errorf("查询待重试记录: %w", err)
		}
		for _, ref := range refs {
			if selected[ref.RunID] == nil {
				selected[ref.RunID] = make(map[string]bool)
			}
			selected[ref.RunID][ref.FileID] = true
		}
	}
	ids := make([]string, 0, len(selected))
	for id := range selected {
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return out, nil
	}
	if err := s.reconcileResolvedFailuresLocked(ctx, ids); err != nil {
		return out, err
	}
	if err := s.loadHistoryLocked(ctx, ids, "", ""); err != nil {
		return out, err
	}
	type selectedSource struct {
		rule  MediaOrganizeRule
		entry MediaOrganizeEntry
	}
	sources := make(map[string][]selectedSource)
	prepared := make(map[string]MediaOrganizeRun)
	changed := make([]string, 0, len(s.doc.Runs))
	for _, original := range s.doc.Runs {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		permissions := s.taskPermissionsLocked(original)
		var run MediaOrganizeRun
		cloneMediaRun(original, &run)
		fileIDs := make([]string, 0)
		for _, task := range mediaTasks(original) {
			if !selected[original.ID][task.FileID] {
				continue
			}
			delete(selected[original.ID], task.FileID)
			if task.Status == "done" {
				skip("主视频已完成，失败附件请单独重试")
				continue
			}
			if !permissions[task.FileID].CanRetry {
				if s.busyRunLocked(original.ID) || mediaRunActive(original.Status) || s.eventRecoveryPendingLocked(original.ID) {
					skip("正在排队或处理")
				} else {
					skip("当前任务已完成")
				}
				continue
			}
			entry := task.Entries[0]
			duplicate := slices.ContainsFunc(sources[task.FileID], func(previous selectedSource) bool {
				return previous.rule.ID == original.Rule.ID && mediaOrphanBinding(previous.rule).matches(original.Rule) &&
					mediaResolvedSameSource(previous.entry, entry, original.Rule.Storage)
			})
			if !duplicate {
				for _, active := range s.doc.Runs {
					if !s.busyRunLocked(active.ID) && !mediaRunActive(active.Status) || active.ID == original.ID ||
						active.Rule.ID != original.Rule.ID || !mediaOrphanBinding(active.Rule).matches(original.Rule) {
						continue
					}
					duplicate = slices.ContainsFunc(active.Entries, func(current MediaOrganizeEntry) bool {
						return current.File.MediaType == "video" && mediaInRunTask(&active, current) && mediaResolvedSameSource(entry, current, original.Rule.Storage)
					})
					if duplicate {
						break
					}
				}
			}
			if duplicate {
				skip("同一来源文件已选择")
				continue
			}
			if err := mediaPrepareRetry(&run, MediaOrganizeCorrection{FileID: task.FileID}); err != nil {
				return out, fmt.Errorf("准备批量重试: %w", err)
			}
			sources[task.FileID] = append(sources[task.FileID], selectedSource{rule: original.Rule, entry: entry})
			fileIDs = append(fileIDs, task.FileID)
		}
		if len(fileIDs) == 0 {
			continue
		}
		run.TaskFileID, run.TaskFileIDs, run.Status = "", fileIDs, "queued"
		run.UpdatedAt = s.localMonitorSettings().now()
		run.Message = fmt.Sprintf("等待重新整理选中的 %d 个任务", len(fileIDs))
		prepared[run.ID] = run
		changed = append(changed, run.ID)
		out.Queued += len(fileIDs)
	}
	for _, files := range selected {
		for range files {
			skip("任务记录已移除")
		}
	}
	if len(changed) == 0 {
		return out, nil
	}
	batchID, err := organizeToken()
	if err != nil {
		return out, err
	}
	s.doc.RetryBatch = &MediaOrganizeRetryProgress{ID: batchID, Status: "running", Total: out.Queued + out.Skipped, Pending: out.Queued, Skipped: out.Skipped}
	for i := range s.doc.Runs {
		if run, ok := prepared[s.doc.Runs[i].ID]; ok {
			run.RetryBatchID = batchID
			s.doc.Runs[i] = run
		}
	}
	if err := s.persistLocked(changed...); err != nil {
		return MediaOrganizeBatchRetryResult{}, err
	}
	for _, id := range changed {
		s.enqueueActionLocked(id, "retry")
	}
	s.notify()
	out.Batch = s.retryBatchProgressLocked()
	return out, nil
}
