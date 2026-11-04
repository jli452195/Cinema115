package service

import (
	"context"
	"errors"
	"slices"
	"time"
)

type MediaOrganizeSchedule struct {
	RealtimeEnabled bool `json:"realtimeEnabled"`
	ScheduleEnabled bool `json:"scheduleEnabled"`
	IntervalSeconds int  `json:"intervalSeconds"`
}

func mediaValidateSchedule(storage string, settings MediaOrganizeSchedule) error {
	if settings.IntervalSeconds < 1 || settings.IntervalSeconds > 86400 || settings.RealtimeEnabled && storage != "local" {
		return ErrMediaOrganizeInput
	}
	return nil
}

func mediaSameRuleSettings(a, b MediaOrganizeRule) bool {
	a.RealtimeEnabled, b.RealtimeEnabled = false, false
	a.ScheduleEnabled, b.ScheduleEnabled = false, false
	a.IntervalSeconds, b.IntervalSeconds = 0, 0
	a.LastScan, b.LastScan = time.Time{}, time.Time{}
	a.NextScanAt, b.NextScanAt = time.Time{}, time.Time{}
	return a == b
}

func mediaNextScanAfterEdit(old, rule MediaOrganizeRule, now time.Time) time.Time {
	if !rule.ScheduleEnabled || !old.ScheduleEnabled || !mediaSameRuleSettings(old, rule) {
		return time.Time{}
	}
	if old.IntervalSeconds != rule.IntervalSeconds {
		return now.Add(time.Duration(rule.IntervalSeconds) * time.Second)
	}
	return old.NextScanAt
}

func (s *MediaOrganizer) UpdateSchedule(ctx context.Context, id string, settings MediaOrganizeSchedule) (MediaOrganizeRule, error) {
	if err := ctx.Err(); err != nil {
		return MediaOrganizeRule{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.updateScheduleLocked(id, settings)
}

func (s *MediaOrganizer) updateScheduleLocked(id string, settings MediaOrganizeSchedule) (MediaOrganizeRule, error) {
	for i, old := range s.doc.Rules {
		if old.ID != id {
			continue
		}
		if err := mediaValidateSchedule(old.Storage, settings); err != nil {
			return old, err
		}
		if old.RealtimeEnabled == settings.RealtimeEnabled && old.ScheduleEnabled == settings.ScheduleEnabled && old.IntervalSeconds == settings.IntervalSeconds {
			return old, nil
		}
		rule := old
		rule.RealtimeEnabled, rule.ScheduleEnabled, rule.IntervalSeconds = settings.RealtimeEnabled, settings.ScheduleEnabled, settings.IntervalSeconds
		rule.NextScanAt = mediaNextScanAfterEdit(old, rule, s.localMonitorSettings().now())
		queuedSchedule := slices.ContainsFunc(s.doc.Runs, func(run MediaOrganizeRun) bool {
			return run.Rule.ID == id && run.Trigger == "schedule" && run.Status == "queued" && run.ID != s.active
		})
		removed := s.cancelAutomaticScansLocked(id)
		if rule.ScheduleEnabled && queuedSchedule {
			rule.NextScanAt = time.Time{}
		}
		s.doc.Rules[i] = rule
		if err := s.persistMonitorRulesLocked(removed...); err != nil {
			return old, err
		}
		for _, runID := range removed {
			s.removeActionLocked(runID)
		}
		if local := s.localMonitors[id]; local != nil && rule.RealtimeEnabled && len(removed) > 0 {
			local.dirtyDue = s.localMonitorSettings().now()
		}
		s.notify()
		return rule, nil
	}
	return MediaOrganizeRule{}, ErrMediaOrganizeInput
}

// 未启动的自动扫描可以取消，手动运行与已经执行的批次继续使用原快照。
// actions 必须在持久化成功后清除，避免事务回滚后留下无法执行的排队记录。
func (s *MediaOrganizer) cancelAutomaticScansLocked(ruleID string) []string {
	removed := make([]string, 0)
	s.doc.Runs = slices.DeleteFunc(s.doc.Runs, func(run MediaOrganizeRun) bool {
		if run.Rule.ID != ruleID || run.ID == s.active || run.Status != "queued" || run.Trigger != "schedule" && run.Trigger != "realtime" {
			return false
		}
		if run.TaskFileID != "" || s.actions[run.ID] == "retry" {
			return false
		}
		removed = append(removed, run.ID)
		return true
	})
	return removed
}

// 按实际轮次结束时间安排下一次扫描，运行快照不能覆盖管理员修改后的规则。
func (s *MediaOrganizer) finishScanRound(run MediaOrganizeRun) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.localMonitorSettings().now()
	for i, current := range s.doc.Rules {
		if current.ID != run.Rule.ID {
			continue
		}
		snapshot := run.Rule
		snapshot.Name = current.Name
		if !mediaSameRuleSettings(snapshot, current) {
			return nil
		}
		current.LastScan = now
		if !current.ScheduleEnabled {
			current.NextScanAt = time.Time{}
		} else if run.Trigger == "schedule" || current.NextScanAt.IsZero() || !current.NextScanAt.After(now) {
			current.NextScanAt = now.Add(time.Duration(current.IntervalSeconds) * time.Second)
		}
		s.doc.Rules[i] = current
		return s.persistMonitorRulesLocked()
	}
	return nil
}

func (s *MediaOrganizer) ruleExecutingLocked(ruleID string) bool {
	return slices.ContainsFunc(s.doc.Runs, func(run MediaOrganizeRun) bool {
		return run.Rule.ID == ruleID && (run.ID == s.active || s.actions[run.ID] == "retry" || run.Status == "scanning" || run.Status == "running")
	})
}

func (s *MediaOrganizer) rulePendingLocked(ruleID string) bool {
	return slices.ContainsFunc(s.doc.Runs, func(run MediaOrganizeRun) bool {
		return run.Rule.ID == ruleID && (mediaRunActive(run.Status) || s.busyRunLocked(run.ID))
	})
}

func (s *MediaOrganizer) realtimeEnabledLocked(ruleID string) bool {
	return slices.ContainsFunc(s.doc.Rules, func(rule MediaOrganizeRule) bool {
		return rule.ID == ruleID && rule.Storage == "local" && rule.RealtimeEnabled
	})
}

func (s *MediaOrganizer) schedule() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.localMonitorSettings().now()
	for _, rule := range s.doc.Rules {
		if s.rulePendingLocked(rule.ID) {
			continue
		}
		scheduled := rule.ScheduleEnabled && (rule.NextScanAt.IsZero() || !rule.NextScanAt.After(now))
		local := s.localMonitors[rule.ID]
		realtime := false
		if rule.Storage == "local" && local != nil && mediaSameWatchRule(local.rule, rule) && local.initialized {
			watching := rule.RealtimeEnabled && local.status.Mode == "realtime"
			realtime = watching && mediaMonitorDue(local.dirtyDue, now) || (watching || rule.ScheduleEnabled) && (mediaMonitorDue(local.settleDue, now) || mediaMonitorDue(local.retryDue, now))
		}
		if !scheduled && !realtime {
			continue
		}
		trigger := "realtime"
		if scheduled {
			trigger = "schedule"
		}
		if _, err := s.queueTriggeredScanLocked(rule, trigger); err != nil {
			if errors.Is(err, ErrMediaOrganizeBusy) || errors.Is(err, ErrClassificationUnavailable) {
				continue
			}
			return err
		}
		if local == nil {
			continue
		}
		if mediaMonitorDue(local.dirtyDue, now) {
			local.dirtyDue, local.dirtyFirst = time.Time{}, time.Time{}
		}
		if mediaMonitorDue(local.settleDue, now) {
			local.settleDue = time.Time{}
		}
		if mediaMonitorDue(local.retryDue, now) {
			local.retryDue = time.Time{}
		}
	}
	return nil
}
