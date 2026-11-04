package service

import (
	"path"
	"slices"
	"time"
)

// 附件到期只补查所在作品，不推迟下一次完整扫描，也不保存独立定时任务。
func (s *MediaOrganizer) scheduleOrphanScopes(snapshot MediaOrganizeRule, entries []MediaOrganizeEntry, observations map[string]mediaOrphan, due map[string]bool) {
	if snapshot.Storage == "local" || !snapshot.AttachmentCleanupEnabled {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	index := slices.IndexFunc(s.doc.Rules, func(rule MediaOrganizeRule) bool {
		return rule.ID == snapshot.ID && mediaSameRuleSettings(rule, snapshot)
	})
	if index < 0 {
		return
	}
	rule := s.doc.Rules[index]
	if !rule.RealtimeEnabled && !rule.ScheduleEnabled {
		return
	}
	runtime := s.cloudRuntimeLocked(rule)
	now := s.localMonitorSettings().now()
	for _, entry := range entries {
		observation, exists := observations[entry.File.ID]
		if !exists || due[entry.File.ID] {
			continue
		}
		deadline := observation.Since.Add(time.Duration(rule.AttachmentGraceSeconds) * time.Second)
		// 已到期但仍受视频或临时文件保护的附件，留给下一次实际变化或完整扫描。
		if !deadline.After(now) {
			continue
		}
		relative := path.Dir(entry.File.SourcePath)
		if relative == "." {
			relative = ""
		}
		id := entry.File.ParentID
		scope := mediaCloudWorkScope(runtime, id, relative)
		if scope.Relative == "" {
			runtime.full = true
			clear(runtime.pending)
		} else if !runtime.full {
			runtime.pending[scope.ID] = scope
		}
		if runtime.due.IsZero() || deadline.Before(runtime.due) {
			runtime.due = deadline
		}
	}
	s.notify()
}
