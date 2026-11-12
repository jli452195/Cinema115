package service

import (
	"sync"
	"time"
)

// 进度只发布内存摘要，避免扫描和识别期间反复保存整批文件。
func (s *MediaOrganizer) publishMediaProgress(run *MediaOrganizeRun, stage, current string, completed, total int) {
	p := MediaOrganizeActivity{RunID: run.ID, RuleName: run.Rule.Name, Status: "running", Stage: stage, Current: current, Completed: completed, Total: total}
	switch stage {
	case "scanning":
		p.Status, p.Files, p.Completed = "scanning", completed, 0
	case "organizing":
		p.Files = total
	default:
		for _, entry := range run.Entries {
			if entry.File.MediaType == "video" && !entry.Reference && mediaSelectedTask(run, entry) {
				p.Files++
			}
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active != run.ID {
		return
	}
	now := time.Now()
	p.At, p.StartedAt = now, now
	previous := s.progress
	if previous != nil && previous.RunID == run.ID {
		p.notifiedAt = previous.notifiedAt
		if previous.Stage == stage && previous.Current == current {
			p.StartedAt = previous.StartedAt
		}
	}
	// 固定窗口通知，连续文件事件不能一直推迟刷新。
	finished := total > 0 && completed == total && (previous == nil || previous.Completed != completed || previous.Total != total)
	if previous == nil || previous.RunID != p.RunID || previous.Stage != stage || now.Sub(p.notifiedAt) >= time.Second || finished {
		p.notifiedAt = now
		s.historyChangedLocked()
	}
	s.progress = &p
}

// 调用方持锁；单工作线程只有一份实时进度，排队批次使用现有状态。
func (s *MediaOrganizer) mediaActivitiesLocked() []MediaOrganizeActivity {
	items := make([]MediaOrganizeActivity, 0)
	for _, run := range s.doc.Runs {
		if !mediaRunActive(run.Status) && !s.busyRunLocked(run.ID) {
			continue
		}
		if s.progress != nil && s.progress.RunID == run.ID {
			items = append(items, *s.progress)
		} else {
			items = append(items, MediaOrganizeActivity{RunID: run.ID, RuleName: run.Rule.Name, Status: run.Status, Message: run.Message, At: run.UpdatedAt})
		}
	}
	return items
}

func (s *MediaOrganizer) publishMediaEntryProgress(run *MediaOrganizeRun, current string) {
	completed, total := 0, 0
	for _, entry := range run.Entries {
		if entry.File.MediaType == "video" && !entry.Reference && mediaSelectedTask(run, entry) {
			total++
			if entry.Status == "done" || entry.Status == "retained" || entry.Result != nil {
				completed++
			}
		}
	}
	s.publishMediaProgress(run, "organizing", current, completed, total)
}

// 只发送失效通知，不复制任务或查询历史；容量为一，慢页面不阻塞整理线程。
func (s *MediaOrganizer) SubscribeHistoryChanges() (<-chan struct{}, func()) {
	changes := make(chan struct{}, 1)
	s.mu.Lock()
	if s.historyChanges == nil {
		s.historyChanges = make(map[chan struct{}]struct{})
	}
	s.historyChanges[changes] = struct{}{}
	s.mu.Unlock()
	var once sync.Once
	return changes, func() {
		once.Do(func() {
			s.mu.Lock()
			delete(s.historyChanges, changes)
			s.mu.Unlock()
		})
	}
}

// 调用方持有业务锁；只在历史数据或操作权限改变时调用。
func (s *MediaOrganizer) historyChangedLocked() {
	for changes := range s.historyChanges {
		select {
		case changes <- struct{}{}:
		default:
		}
	}
}
