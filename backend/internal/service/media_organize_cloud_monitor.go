package service

import (
	"slices"
	"strings"
	"time"
)

type mediaScanScope struct{ ID, Relative string }
type mediaCloudRuntime struct {
	rule        MediaOrganizeRule
	status      MediaOrganizeMonitorStatus
	pending     map[string]mediaScanScope
	directories map[string]string
	first, due  time.Time
	full        bool
	initialized bool
}

func (s *MediaOrganizer) cloudRuntimeLocked(rule MediaOrganizeRule) *mediaCloudRuntime {
	if s.cloudMonitors == nil {
		s.cloudMonitors = make(map[string]*mediaCloudRuntime)
	}
	runtime := s.cloudMonitors[rule.ID]
	if runtime == nil || !mediaSameRuleSettings(runtime.rule, rule) {
		runtime = &mediaCloudRuntime{rule: rule, pending: make(map[string]mediaScanScope), directories: make(map[string]string)}
		root := rule.SourceID
		if rule.Storage == "cd2" {
			root = rule.Source
		}
		runtime.directories[root] = ""
		s.cloudMonitors[rule.ID] = runtime
	}
	runtime.rule = rule
	return runtime
}

// 事件只合并目录，不保存逐条事件；重启由首次完整扫描补齐。
func mediaMarkCloudScope(runtime *mediaCloudRuntime, scope mediaScanScope, now time.Time) {
	if runtime.first.IsZero() {
		runtime.first = now
	}
	runtime.due = minMediaTime(now.Add(2*time.Second), runtime.first.Add(10*time.Second))
	if scope.Relative == "" {
		runtime.full = true
		clear(runtime.pending)
		return
	}
	if runtime.full {
		return
	}
	for id, previous := range runtime.pending {
		if previous.Relative == scope.Relative || strings.HasPrefix(scope.Relative, previous.Relative+"/") {
			return
		}
		if strings.HasPrefix(previous.Relative, scope.Relative+"/") {
			delete(runtime.pending, id)
		}
	}
	if len(runtime.pending) >= 5000 {
		runtime.full = true
		clear(runtime.pending)
		return
	}
	runtime.pending[scope.ID] = scope
}

func minMediaTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func (s *MediaOrganizer) noteCloudDirectory(rule MediaOrganizeRule, id, relative string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	runtime := s.cloudMonitors[rule.ID]
	if runtime == nil || !mediaSameRuleSettings(runtime.rule, rule) {
		return
	}
	if relative == "" {
		clear(runtime.directories)
	}
	runtime.directories[id] = relative
}

func mediaCloudWorkScope(runtime *mediaCloudRuntime, id, relative string) mediaScanScope {
	if relative == "" || relative == "." {
		return mediaScanScope{ID: id}
	}
	first := strings.SplitN(relative, "/", 2)[0]
	for candidate, known := range runtime.directories {
		if known == first {
			return mediaScanScope{ID: candidate, Relative: first}
		}
	}
	return mediaScanScope{ID: id, Relative: relative}
}

// 云端监控只初始化 115 事件补扫；CD2 由定时扫描驱动。
func (s *MediaOrganizer) refreshCloudMonitorsLocked(now time.Time) {
	for _, rule := range s.doc.Rules {
		if rule.Storage != "115" || !rule.RealtimeEnabled {
			continue
		}
		runtime := s.cloudRuntimeLocked(rule)
		if !runtime.initialized {
			runtime.initialized = true
			runtime.full, runtime.first, runtime.due = true, time.Time{}, now
			runtime.status = MediaOrganizeMonitorStatus{Mode: "connecting"}
		}
	}
	for id := range s.cloudMonitors {
		if !slices.ContainsFunc(s.doc.Rules, func(rule MediaOrganizeRule) bool {
			return rule.ID == id && rule.Storage != "local" && (rule.RealtimeEnabled || rule.ScheduleEnabled)
		}) {
			delete(s.cloudMonitors, id)
		}
	}
}

// 局部扫描不改完整扫描期限；运行中到达的新事件保留到下一次目录补扫。
func (s *MediaOrganizer) queueCloudChangesLocked(rule MediaOrganizeRule, now time.Time) (bool, error) {
	runtime := s.cloudMonitors[rule.ID]
	if runtime == nil || !rule.RealtimeEnabled && !rule.ScheduleEnabled || !mediaSameRuleSettings(runtime.rule, rule) || runtime.due.IsZero() || now.Before(runtime.due) {
		return false, nil
	}
	run, err := s.queueTriggeredScanLocked(rule, "realtime")
	if err != nil {
		return false, err
	}
	if !runtime.full {
		scopes := make([]mediaScanScope, 0, len(runtime.pending))
		for _, scope := range runtime.pending {
			scopes = append(scopes, scope)
		}
		slices.SortFunc(scopes, func(a, b mediaScanScope) int { return strings.Compare(a.Relative, b.Relative) })
		for i := range s.doc.Runs {
			if s.doc.Runs[i].ID == run.ID {
				s.doc.Runs[i].scanScopes = scopes
				break
			}
		}
	}
	clear(runtime.pending)
	runtime.full = false
	runtime.first, runtime.due = time.Time{}, time.Time{}
	return true, nil
}
