package service

import (
	"fmt"
	"path"
	"strings"
	"time"
)

// Only cloud-bound files can resume organization. Website paths are never
// interpreted as 115 IDs, and confirmed files are not scanned again.
func canContinueOrganize(job OrganizeJob) bool {
	if (job.Status != "partial" && job.Status != "interrupted") || len(job.Items) == 0 || !organizeID(job.Config.TargetCID) {
		return false
	}
	for _, item := range job.Items {
		if !organizeID(item.File.ID) || item.File.ID == "0" || !organizeID(item.File.ParentID) {
			return false
		}
		if job.Download != nil && !strings.HasPrefix(item.File.SourcePath, strings.TrimRight(job.Config.SourcePath, "/")+"/") {
			return false
		}
		if item.Directory != "" && (path.IsAbs(item.Directory) || path.Clean(item.Directory) != item.Directory || strings.HasPrefix(item.Directory, "..")) {
			return false
		}
	}
	return true
}

// Resume queues the original manual task, preserving its plan and completed
// files. Persistence and the writer reservation precede all external work.
func (s *ManualOrganize) Resume(id string) (OrganizeJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, saved := range s.doc.Jobs {
		if saved.ID != id || saved.Download != nil {
			continue
		}
		if saved.Status == "completed" || s.pending != nil && s.pending.job.ID == id {
			return cloneOrganizeJob(saved), nil
		}
		if !canContinueOrganize(saved) {
			return OrganizeJob{}, fmt.Errorf("%w：此任务不能继续整理", ErrOrganizeConflict)
		}
		a, err := s.account(saved.Config.AccountID)
		if err != nil {
			return OrganizeJob{}, err
		}
		if a.Revision != saved.Revision {
			return OrganizeJob{}, fmt.Errorf("%w：账号配置已变化，请重新确认账号和目录", ErrOrganizeConflict)
		}
		if s.pending != nil || s.cleaning {
			return OrganizeJob{}, fmt.Errorf("%w：请等待当前目录操作完成", ErrOrganizeConflict)
		}
		job := cloneOrganizeJob(saved)
		job.Status, job.Message, job.UpdatedAt = "queued", "正在检查原任务剩余文件，确认后继续整理", time.Now()
		job.Stats.StartedAt = time.Now().Add(-time.Duration(job.Stats.ElapsedMS) * time.Millisecond)
		doc := s.doc
		doc.Jobs = append([]OrganizeJob{}, doc.Jobs...)
		doc.Jobs[i] = job
		if err := s.saveLocked(doc); err != nil {
			return OrganizeJob{}, err
		}
		s.pending = &organizePending{job: cloneOrganizeJob(job), cookie: a.Cookie}
		select {
		case s.wake <- struct{}{}:
		default:
		}
		return job, nil
	}
	return OrganizeJob{}, fmt.Errorf("%w：手动整理任务不存在", ErrOrganizeInput)
}
