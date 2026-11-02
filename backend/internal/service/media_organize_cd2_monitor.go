package service

import (
	"context"
	"path"
	"slices"
	"sync"
	"time"

	"strm115/backend/internal/client/clouddrive2"
)

type mediaCD2Ready struct {
	at   time.Time
	file *clouddrive2.File
}

type mediaCD2Runtime struct {
	rule      MediaOrganizeRule
	status    MediaOrganizeMonitorStatus
	dirty     map[string]bool
	pending   map[string]bool
	ready     map[string]mediaCD2Ready
	dirtyDue  time.Time
	lastFull  time.Time
	forceFull bool
	retryDue  time.Time
}

func (s *MediaOrganizer) reconcileCD2MonitorsLocked() bool {
	if s.cd2Monitors == nil {
		s.cd2Monitors = make(map[string]*mediaCD2Runtime)
	}
	wanted := make(map[string]bool)
	for _, rule := range s.doc.Rules {
		if rule.Storage != "cd2" || !rule.Enabled {
			continue
		}
		wanted[rule.ID] = true
		old := s.cd2Monitors[rule.ID]
		if old == nil || old.rule.Source != rule.Source || old.rule.Target != rule.Target || old.rule.ConnectionID != rule.ConnectionID || old.rule.ConnectionRevision != rule.ConnectionRevision {
			s.cd2Monitors[rule.ID] = &mediaCD2Runtime{rule: rule, status: MediaOrganizeMonitorStatus{Mode: "starting", Message: "正在连接 CD2 实时监听"}, dirty: make(map[string]bool), pending: make(map[string]bool), ready: make(map[string]mediaCD2Ready), forceFull: true}
		} else {
			old.rule = rule
		}
	}
	for id := range s.cd2Monitors {
		if !wanted[id] {
			delete(s.cd2Monitors, id)
		}
	}
	return len(wanted) > 0
}

func (s *MediaOrganizer) runCD2Monitor(ctx context.Context) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	var watching sync.WaitGroup
	var stop context.CancelFunc
	defer func() {
		if stop != nil {
			stop()
		}
		watching.Wait()
	}()
	for {
		s.mu.Lock()
		wanted := s.reconcileCD2MonitorsLocked()
		s.mu.Unlock()
		if wanted && stop == nil {
			watcher, ok := s.cd2.(interface {
				Watch(context.Context, func(CD2Event)) error
			})
			if ok {
				watchCtx, cancel := context.WithCancel(ctx)
				stop = cancel
				watching.Add(1)
				go func() { defer watching.Done(); _ = watcher.Watch(watchCtx, s.handleCD2Event) }()
			} else {
				s.handleCD2Event(CD2Event{Mode: "polling", Message: "使用 CD2 定时补查"})
			}
		} else if !wanted && stop != nil {
			stop()
			watching.Wait()
			stop = nil
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *MediaOrganizer) handleCD2Event(event CD2Event) {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, monitor := range s.cd2Monitors {
		if event.Revision != 0 && event.Revision != monitor.rule.ConnectionRevision {
			monitor.status.Mode, monitor.status.Message = "polling", "CD2 配置已变化，请重新选择规则目录"
			continue
		}
		previousMode := monitor.status.Mode
		if event.Mode != "" {
			monitor.status.Mode, monitor.status.Message = event.Mode, event.Message
		}
		if event.Rescan && (event.Mode != "polling" || previousMode != "polling") {
			monitor.forceFull = true
		}
		changes := slices.Clone(event.Completed)
		if event.Change != nil {
			changes = append(changes, *event.Change)
		}
		for _, change := range changes {
			for _, full := range []string{change.Path, change.NewPath} {
				if full == "" || !mediaCloudWithin(monitor.rule.Source, full) {
					continue
				}
				monitor.status.LastEventAt = now
				directory := path.Dir(full)
				if change.Directory && change.Type != "delete" {
					directory = full
				}
				if !mediaCloudWithin(monitor.rule.Source, directory) {
					directory = monitor.rule.Source
				}
				monitor.dirty[directory] = true
				if monitor.dirtyDue.IsZero() {
					monitor.dirtyDue = now.Add(200 * time.Millisecond)
				}
				if change.Ready {
					monitor.ready[full] = mediaCD2Ready{at: now, file: change.File}
				} else if change.Type == "modify" || change.Type == "delete" || change.Type == "create" || change.Type == "rename" {
					delete(monitor.ready, full)
				}
			}
		}
		for full, ready := range monitor.ready {
			if now.Sub(ready.at) > 2*time.Minute {
				delete(monitor.ready, full)
			}
		}
	}
	s.notify()
}

func (s *MediaOrganizer) cd2ScheduleDueLocked(rule MediaOrganizeRule, now time.Time) bool {
	monitor := s.cd2Monitors[rule.ID]
	if !s.monitorRunning || monitor == nil {
		return now.Sub(rule.LastScan) >= mediaCD2Interval(rule)
	}
	if !monitor.retryDue.IsZero() && now.Before(monitor.retryDue) {
		return false
	}
	if monitor.forceFull || monitor.lastFull.IsZero() || now.Sub(monitor.lastFull) >= mediaCD2Interval(rule) {
		return true
	}
	if !monitor.dirtyDue.IsZero() && !now.Before(monitor.dirtyDue) {
		return true
	}
	return now.Sub(monitor.status.LastFallbackAt) >= mediaCD2Interval(rule)
}

func (s *MediaOrganizer) cd2QueuedLocked(rule MediaOrganizeRule, id string, now time.Time) {
	monitor := s.cd2Monitors[rule.ID]
	if monitor == nil {
		return
	}
	full := monitor.forceFull || monitor.lastFull.IsZero() || now.Sub(monitor.lastFull) >= mediaCD2Interval(rule) || monitor.status.Mode != "realtime"
	paths := make([]string, 0, len(monitor.dirty)+len(monitor.pending))
	if !full {
		for directory := range monitor.dirty {
			paths = append(paths, directory)
		}
		for directory := range monitor.pending {
			paths = append(paths, directory)
		}
		slices.Sort(paths)
		paths = slices.Compact(paths)
		// 没有事件和待稳定文件时，低频全量兜底负责发现云端漏报。
		if len(paths) == 0 {
			paths = []string{rule.Source}
		}
	}
	for i := range s.doc.Runs {
		if s.doc.Runs[i].ID == id {
			s.doc.Runs[i].CD2ScanPaths = paths
			break
		}
	}
	monitor.status.LastFallbackAt = now
	monitor.forceFull, monitor.dirtyDue = false, time.Time{}
	clear(monitor.dirty)
	// 首个工作检查点会把局部目录快照与冻结规则一起持久保存。
}

func (s *MediaOrganizer) cd2FileReadiness(rule MediaOrganizeRule) func(clouddrive2.File) bool {
	return func(file clouddrive2.File) bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		monitor := s.cd2Monitors[rule.ID]
		if monitor == nil || monitor.rule.ConnectionRevision != rule.ConnectionRevision {
			return false
		}
		ready, ok := monitor.ready[file.Path]
		if !ok || time.Since(ready.at) > 2*time.Minute {
			return false
		}
		if ready.file != nil && (ready.file.ID != "" && ready.file.ID != file.ID || ready.file.Size != file.Size || !ready.file.Modified.IsZero() && !ready.file.Modified.Equal(file.Modified)) {
			return false
		}
		// 云端时间不能与本机接收时间比较；首次核验后固定同一 CD2 文件的身份。
		if ready.file == nil || ready.file.ID == "" {
			snapshot := file
			ready.file = &snapshot
			monitor.ready[file.Path] = ready
		}
		return true
	}
}

func (s *MediaOrganizer) cd2ScanSucceeded(rule MediaOrganizeRule, roots []string, pending map[string]bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	monitor := s.cd2Monitors[rule.ID]
	if monitor == nil {
		return
	}
	full := len(roots) == 0 || slices.Contains(roots, rule.Source)
	if full {
		monitor.lastFull = time.Now()
	}
	monitor.retryDue = time.Time{}
	for directory := range monitor.pending {
		if full || slices.ContainsFunc(roots, func(root string) bool { return mediaCloudWithin(root, directory) }) {
			delete(monitor.pending, directory)
		}
	}
	for full := range pending {
		monitor.pending[path.Dir(full)] = true
	}
}

func (s *MediaOrganizer) cd2ScanFailed(rule MediaOrganizeRule) {
	if rule.Storage != "cd2" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if monitor := s.cd2Monitors[rule.ID]; monitor != nil {
		monitor.forceFull, monitor.retryDue = true, time.Now().Add(mediaCD2Interval(rule))
	}
}
