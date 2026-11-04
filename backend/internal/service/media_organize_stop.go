package service

import (
	"context"
	"errors"
	"slices"
	"time"
)

var errMediaOrganizeStopped = errors.New("已停止整理")

// 停止只拦截新的工作；已发出的云端操作继续核对结果，不产生额外恢复账本。
func (s *MediaOrganizer) checkOrganizeStop(run *MediaOrganizeRun) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopping[run.ID] {
		return errMediaOrganizeStopped
	}
	return nil
}

func (s *MediaOrganizer) Stop(ctx context.Context, ruleID string) (MediaOrganizeRule, error) {
	if err := ctx.Err(); err != nil {
		return MediaOrganizeRule{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	index := slices.IndexFunc(s.doc.Rules, func(rule MediaOrganizeRule) bool { return rule.ID == ruleID })
	if index < 0 {
		return MediaOrganizeRule{}, ErrMediaOrganizeInput
	}
	rule := s.doc.Rules[index]
	rule.RealtimeEnabled, rule.ScheduleEnabled = false, false
	// 规则、排队工作先持久化成功，再通知当前执行器停止。
	rule.NextScanAt = time.Time{}
	s.doc.Rules[index] = rule
	changed, queued := make([]string, 0), make([]string, 0)
	for i := range s.doc.Runs {
		run := &s.doc.Runs[i]
		if run.Rule.ID != ruleID || run.ID == s.active || s.actions[run.ID] == "" {
			continue
		}
		queued = append(queued, run.ID)
		mediaFinishStoppedRun(run)
		changed = append(changed, run.ID)
	}
	if err := s.persistMonitorRulesLocked(changed...); err != nil {
		return MediaOrganizeRule{}, err
	}
	for _, id := range queued {
		s.removeActionLocked(id)
	}
	if s.stopping == nil {
		s.stopping = make(map[string]bool)
	}
	for _, run := range s.doc.Runs {
		if run.ID == s.active && run.Rule.ID == ruleID {
			s.stopping[run.ID] = true
		}
	}
	delete(s.cloudMonitors, ruleID)
	s.notify()
	return rule, nil
}

func mediaFinishStoppedRun(run *MediaOrganizeRun) {
	run.Entries = slices.DeleteFunc(run.Entries, func(entry MediaOrganizeEntry) bool {
		return entry.Result == nil && entry.P115Move == nil && entry.Failure == nil && entry.CD2Current == nil
	})
	mediaFinishRun(run)
	run.Message = "已停止整理"
}
