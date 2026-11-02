package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

type MediaOrganizeMonitorStatus struct {
	Mode           string    `json:"mode"`
	Message        string    `json:"message,omitempty"`
	LastEventAt    time.Time `json:"lastEventAt,omitempty"`
	LastFallbackAt time.Time `json:"lastFallbackAt,omitempty"`
}

type mediaLocalObservation struct {
	info                  os.FileInfo
	identity              mediaLocalIdentity
	observedAt, changedAt time.Time
}

type mediaLocalIdentity struct {
	info          os.FileInfo
	volume, index uint64
}
type mediaLocalRuntime struct {
	rule                                      MediaOrganizeRule
	status                                    MediaOrganizeMonitorStatus
	initialized                               bool
	dirtyFirst, dirtyDue, settleDue, retryDue time.Time
	scanFailures                              int
	files                                     map[string]mediaLocalObservation
}
type mediaFSWatcher interface {
	Add(string) error
	Remove(string) error
	Close() error
	events() <-chan fsnotify.Event
	errors() <-chan error
}
type mediaFSNotify struct{ watcher *fsnotify.Watcher }

func (w mediaFSNotify) Add(path string) error         { return w.watcher.Add(path) }
func (w mediaFSNotify) Remove(path string) error      { return w.watcher.Remove(path) }
func (w mediaFSNotify) Close() error                  { return w.watcher.Close() }
func (w mediaFSNotify) events() <-chan fsnotify.Event { return w.watcher.Events }
func (w mediaFSNotify) errors() <-chan error          { return w.watcher.Errors }

type mediaLocalMonitorOptions struct {
	tick, debounce, maxDebounce, stable, retry time.Duration
	now                                        func() time.Time
	newWatcher                                 func() (mediaFSWatcher, error)
	filesystemReason                           func(string) string
}

func (s *MediaOrganizer) localMonitorSettings() mediaLocalMonitorOptions {
	o := s.monitorOptions
	if o.tick <= 0 {
		o.tick = 500 * time.Millisecond
	}
	if o.debounce <= 0 {
		o.debounce = 2 * time.Second
	}
	if o.maxDebounce <= 0 {
		o.maxDebounce = 10 * time.Second
	}
	if o.stable <= 0 {
		o.stable = 30 * time.Second
	}
	if o.retry <= 0 {
		o.retry = 30 * time.Second
	}
	if o.now == nil {
		o.now = time.Now
	}
	if o.newWatcher == nil {
		o.newWatcher = func() (mediaFSWatcher, error) {
			w, err := fsnotify.NewWatcher()
			if err != nil {
				return nil, fmt.Errorf("创建目录监听: %w", err)
			}
			return mediaFSNotify{w}, nil
		}
	}
	if o.filesystemReason == nil {
		o.filesystemReason = mediaLocalFilesystemReason
	}
	return o
}
func mediaSameWatchRule(a, b MediaOrganizeRule) bool {
	a.LastScan, b.LastScan = time.Time{}, time.Time{}
	return a == b
}
func mediaMonitorDue(at, now time.Time) bool { return !at.IsZero() && !at.After(now) }
func mediaMonitorDirty(local *mediaLocalRuntime, now time.Time, o mediaLocalMonitorOptions) {
	if local.dirtyFirst.IsZero() || now.Before(local.dirtyFirst) {
		local.dirtyFirst = now
	}
	if due := now.Add(o.debounce); local.dirtyDue.IsZero() || due.After(local.dirtyDue) {
		local.dirtyDue = due
	}
	if maximum := local.dirtyFirst.Add(o.maxDebounce); local.dirtyDue.After(maximum) {
		local.dirtyDue = maximum
	}
}

// The reader owns the kernel subscription independently of the synchronous
// organizer. Events only mark bounded in-memory work; SQLite writes and media
// operations always remain on the existing organizer worker.
func (s *MediaOrganizer) runLocalMonitor(ctx context.Context) {
	o := s.localMonitorSettings()
	ticker := time.NewTicker(o.tick)
	defer ticker.Stop()
	var watcher mediaFSWatcher
	var readerCancel context.CancelFunc
	var readers sync.WaitGroup
	inbox := &mediaMonitorInbox{wake: make(chan struct{}, 1), events: make(map[string]mediaMonitorEvent)}
	closeWatcher := func() {
		if readerCancel != nil {
			readerCancel()
			readerCancel = nil
		}
		if watcher != nil {
			_ = watcher.Close()
			watcher = nil
		}
		readers.Wait()
	}
	defer closeWatcher()
	startReader := func(w mediaFSWatcher) {
		var readerCtx context.Context
		readerCtx, readerCancel = context.WithCancel(ctx)
		readers.Add(1)
		go func() { defer readers.Done(); mediaReadLocalEvents(readerCtx, w, inbox, o) }()
	}
	watched := make(map[string]bool)
	plans := make(map[string]*mediaWatchPlan)
	refresh := true
	for ctx.Err() == nil {
		if refresh {
			s.reconcileLocalWatches(ctx, o, plans, &watcher, watched, startReader)
			refresh = false
		}
		select {
		case <-ctx.Done():
			return
		case <-s.monitorWake:
			refresh = true
		case <-ticker.C:
			refresh = true
			s.mu.Lock()
			now := o.now()
			for _, local := range s.localMonitors {
				if mediaMonitorDue(local.dirtyDue, now) || mediaMonitorDue(local.settleDue, now) || mediaMonitorDue(local.retryDue, now) {
					s.notify()
					break
				}
			}
			s.mu.Unlock()
		case <-inbox.wake:
			batch, failure := inbox.drain()
			if failure != "" {
				closeWatcher()
				inbox.drain()
				s.failLocalWatches(o, plans, failure)
				clear(watched)
				refresh = true
				continue
			}
			for name, received := range batch {
				event := fsnotify.Event{Name: name, Op: received.op}
				if event.Op&(fsnotify.Create|fsnotify.Remove|fsnotify.Rename) != 0 {
					// A path may already refer to a replacement inode by the time
					// the old remove/rename notification is read.
					for name := range watched {
						if mediaWithin(event.Name, name) {
							_ = watcher.Remove(name)
							delete(watched, name)
						}
					}
				}
				now := received.at
				for id, plan := range plans {
					if !mediaWithin(plan.rule.Source, event.Name) {
						continue
					}
					if event.Op&(fsnotify.Create|fsnotify.Remove|fsnotify.Rename) != 0 {
						plan.refresh = true
					}
					s.mu.Lock()
					if local := s.localMonitors[id]; local != nil {
						local.status.LastEventAt = now
						mediaMonitorDirty(local, now, o)
						if event.Op&(fsnotify.Remove|fsnotify.Rename) != 0 {
							for name := range local.files {
								if mediaWithin(event.Name, name) {
									delete(local.files, name)
								}
							}
						}
						if event.Op&(fsnotify.Create|fsnotify.Write) != 0 {
							if observation, exists := local.files[event.Name]; exists || len(local.files) < 5000 {
								observation.changedAt = now
								local.files[event.Name] = observation
							}
						}
					}
					s.mu.Unlock()
				}
			}
		}
	}
}

type mediaMonitorEvent struct {
	op fsnotify.Op
	at time.Time
}

// Filesystem enumeration failures get at most three fast retry attempts.
// Metadata/provider failures retain their existing behavior.
type mediaLocalScanError struct{ error }

func (e *mediaLocalScanError) Unwrap() error { return e.error }
func (s *MediaOrganizer) retryLocalScan(rule MediaOrganizeRule, scanErr error) {
	var localErr *mediaLocalScanError
	var pathErr *os.PathError
	if !errors.As(scanErr, &localErr) || !errors.As(localErr.error, &pathErr) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	local := s.localMonitors[rule.ID]
	if local == nil || !mediaSameWatchRule(local.rule, rule) {
		return
	}
	local.scanFailures++
	if local.scanFailures > 3 {
		return
	}
	o := s.localMonitorSettings()
	delay := o.retry * time.Duration(1<<uint(local.scanFailures-1))
	if delay > 2*time.Minute {
		delay = 2 * time.Minute
	}
	due := o.now().Add(delay)
	if local.retryDue.IsZero() || due.Before(local.retryDue) {
		local.retryDue = due
	}
	s.notify()
}
func (s *MediaOrganizer) localScanSucceeded(rule MediaOrganizeRule) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if local := s.localMonitors[rule.ID]; local != nil && mediaSameWatchRule(local.rule, rule) {
		local.retryDue = time.Time{}
		local.scanFailures = 0
	}
}

type mediaMonitorInbox struct {
	mu      sync.Mutex
	wake    chan struct{}
	events  map[string]mediaMonitorEvent
	failure string
}

func (i *mediaMonitorInbox) signal() {
	select {
	case i.wake <- struct{}{}:
	default:
	}
}
func (i *mediaMonitorInbox) drain() (map[string]mediaMonitorEvent, string) {
	i.mu.Lock()
	defer i.mu.Unlock()
	events, failure := i.events, i.failure
	i.events = make(map[string]mediaMonitorEvent)
	i.failure = ""
	return events, failure
}
func mediaReadLocalEvents(ctx context.Context, w mediaFSWatcher, inbox *mediaMonitorInbox, o mediaLocalMonitorOptions) {
	events, watchErrors := w.events(), w.errors()
	for ctx.Err() == nil {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-events:
			if ctx.Err() != nil {
				return
			}
			if !ok {
				events = nil
				inbox.mu.Lock()
				inbox.failure = "目录监听已关闭，使用定时扫描"
				inbox.mu.Unlock()
				inbox.signal()
				continue
			}
			if event.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Remove|fsnotify.Rename) == 0 {
				continue
			}
			inbox.mu.Lock()
			if previous, exists := inbox.events[event.Name]; exists || len(inbox.events) < 5000 {
				inbox.events[event.Name] = mediaMonitorEvent{op: previous.op | event.Op, at: o.now()}
			} else {
				inbox.failure = "目录事件过多，已切换定时扫描并准备重新监听"
			}
			inbox.mu.Unlock()
			inbox.signal()
		case err, ok := <-watchErrors:
			if ctx.Err() != nil {
				return
			}
			message := "目录监听已关闭，使用定时扫描"
			if !ok {
				watchErrors = nil
			}
			if ok && err != nil {
				message = "实时监控异常，已切换定时扫描: " + err.Error()
			}
			inbox.mu.Lock()
			inbox.failure = message
			inbox.mu.Unlock()
			inbox.signal()
			// Control calls such as Remove may be waiting for the backend to
			// deliver another error. Keep draining until the manager cancels
			// this subscription, even after reporting the first failure.
		}
	}
}

type mediaWatchPlan struct {
	rule    MediaOrganizeRule
	root    mediaLocalIdentity
	paths   map[string]bool
	retryAt time.Time
	refresh bool
}

func (s *MediaOrganizer) failLocalWatches(o mediaLocalMonitorOptions, plans map[string]*mediaWatchPlan, message string) {
	now := o.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, plan := range plans {
		plan.paths = nil
		plan.retryAt = now.Add(o.retry)
		plan.refresh = false
		if local := s.localMonitors[id]; local != nil {
			local.initialized = true
			local.status.Mode = "polling"
			local.status.Message = message
			clear(local.files)
			mediaMonitorDirty(local, now, o)
		}
	}
	s.notify()
}

func (s *MediaOrganizer) reconcileLocalWatches(ctx context.Context, o mediaLocalMonitorOptions, plans map[string]*mediaWatchPlan, watcher *mediaFSWatcher, watched map[string]bool, startReader func(mediaFSWatcher)) {
	now := o.now()
	s.mu.Lock()
	rules := make(map[string]MediaOrganizeRule, len(s.doc.Rules))
	for _, rule := range s.doc.Rules {
		if rule.Enabled && rule.Storage == "local" {
			rules[rule.ID] = rule
		}
	}
	for id := range s.localMonitors {
		if _, ok := rules[id]; !ok {
			delete(s.localMonitors, id)
		}
	}
	for id, rule := range rules {
		if current := s.localMonitors[id]; current == nil || !mediaSameWatchRule(current.rule, rule) {
			s.localMonitors[id] = &mediaLocalRuntime{rule: rule, status: MediaOrganizeMonitorStatus{Mode: "starting"}, files: make(map[string]mediaLocalObservation)}
			delete(plans, id)
		}
	}
	s.mu.Unlock()
	for id := range plans {
		if _, ok := rules[id]; !ok {
			delete(plans, id)
		}
	}
	for id, rule := range rules {
		if ctx.Err() != nil {
			return
		}
		plan := plans[id]
		if plan == nil {
			plan = &mediaWatchPlan{rule: rule, refresh: true}
			plans[id] = plan
		}
		if !plan.retryAt.IsZero() && now.Before(plan.retryAt) {
			continue
		}
		info, err := os.Stat(rule.Source)
		if err == nil && !info.IsDir() {
			err = fmt.Errorf("来源路径不再是目录")
		}
		var identity mediaLocalIdentity
		if err == nil {
			identity, err = mediaLocalIdentityOf(rule.Source, info)
		}
		changed := err == nil && plan.root.info != nil && !mediaSameLocalIdentity(plan.root, identity)
		if !plan.refresh && err == nil && !changed && len(plan.paths) > 0 {
			continue
		}
		paths := make(map[string]bool)
		if changed && *watcher != nil {
			for name := range plan.paths {
				_ = (*watcher).Remove(name)
				delete(watched, name)
			}
		}
		message := ""
		if err == nil {
			message = o.filesystemReason(rule.Source)
		}
		if err == nil && message == "" {
			if *watcher == nil {
				*watcher, err = o.newWatcher()
				if err == nil {
					startReader(*watcher)
				}
			}
			if err == nil {
				err = mediaAddLocalTree(ctx, *watcher, watched, paths, rule.Source)
			}
		}
		if err != nil {
			message = "无法建立实时监听，使用定时扫描: " + err.Error()
		}
		if err == nil {
			plan.root = identity
		}
		plan.paths, plan.refresh = paths, false
		if message != "" {
			plan.paths = nil
			plan.retryAt = now.Add(o.retry)
		} else {
			plan.retryAt = time.Time{}
		}
		s.mu.Lock()
		if local := s.localMonitors[id]; local != nil && mediaSameWatchRule(local.rule, rule) {
			first := !local.initialized
			wasPolling := local.status.Mode == "polling"
			local.initialized = true
			local.status.Mode, local.status.Message = "realtime", ""
			if message != "" {
				local.status.Mode, local.status.Message = "polling", message
			}
			if changed {
				clear(local.files)
			}
			if first || changed || wasPolling && message == "" {
				local.dirtyDue = now
				local.dirtyFirst = now
				s.notify()
			}
		}
		s.mu.Unlock()
	}
	// A shared path stays subscribed until no enabled rule needs it.
	used := make(map[string]bool, len(watched))
	for _, plan := range plans {
		for name := range plan.paths {
			used[name] = true
		}
	}
	if *watcher != nil {
		for name := range watched {
			if !used[name] {
				_ = (*watcher).Remove(name)
				delete(watched, name)
			}
		}
	}
}

func mediaAddLocalTree(ctx context.Context, watcher mediaFSWatcher, watched, paths map[string]bool, root string) error {
	canonical, err := mediaLocalRoot(root)
	if err != nil {
		return err
	}
	if canonical != root {
		return fmt.Errorf("来源挂载或链接已变化，请重新保存目录")
	}
	directories, files := 0, 0
	return filepath.WalkDir(root, func(full string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			if full != root && errors.Is(walkErr, os.ErrNotExist) {
				return nil
			}
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if !entry.IsDir() {
			files++
			if files > 5000 {
				return fmt.Errorf("目录超过 5000 个文件，请缩小监控范围")
			}
			return nil
		}
		if full != root && mediaTemporaryName(entry.Name()) {
			return filepath.SkipDir
		}
		rel, err := filepath.Rel(root, full)
		if err != nil {
			return err
		}
		if full != root {
			directories++
		}
		if directories > 300 || strings.Count(rel, string(filepath.Separator)) > 12 {
			return fmt.Errorf("目录过大，请缩小监控范围")
		}
		if !watched[full] {
			if err := watcher.Add(full); err != nil {
				if full != root && errors.Is(err, os.ErrNotExist) {
					return filepath.SkipDir
				}
				return fmt.Errorf("监听目录 %s: %w", full, err)
			}
			watched[full] = true
		}
		paths[full] = true
		return nil
	})
}

func (s *MediaOrganizer) localFileReady(rule MediaOrganizeRule, full string, info os.FileInfo) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.localFileReadyLocked(rule, s.localMonitors[rule.ID], full, info)
}

type mediaLocalReadyEvidence struct {
	observation mediaLocalObservation
	runtime     *mediaLocalRuntime
}

// Pausing stops future monitoring, while an in-flight batch keeps evidence for
// files it already confirmed ready. New identities and later writes invalidate
// that evidence; a missing runtime never makes a first observation ready.
func (s *MediaOrganizer) localTaskReadiness(rule MediaOrganizeRule) func(string, os.FileInfo) bool {
	verified := make(map[string]mediaLocalReadyEvidence)
	return func(full string, info os.FileInfo) bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		local := s.localMonitors[rule.ID]
		if prior, exists := verified[full]; exists {
			identity, err := mediaLocalIdentityOf(full, info)
			unchanged := err == nil && mediaSameLocalIdentity(prior.observation.identity, identity) && prior.observation.info.Size() == info.Size() && prior.observation.info.ModTime().Equal(info.ModTime())
			for _, runtime := range []*mediaLocalRuntime{prior.runtime, local} {
				if runtime == nil || !mediaSameWatchRule(runtime.rule, rule) {
					continue
				}
				observation, observed := runtime.files[full]
				if !observed && runtime == prior.runtime || observation.changedAt.After(prior.observation.changedAt) {
					unchanged = false
				}
			}
			if unchanged {
				return true
			}
			// Preserve ordinary observation/deadline updates, but a changed
			// member must be reconsidered by a newly built plan.
			s.localFileReadyLocked(rule, local, full, info)
			return false
		}
		if !s.localFileReadyLocked(rule, local, full, info) {
			return false
		}
		var observation mediaLocalObservation
		if local != nil && mediaSameWatchRule(local.rule, rule) {
			observation = local.files[full]
		} else {
			identity, err := mediaLocalIdentityOf(full, info)
			if err != nil {
				return false
			}
			observation = mediaLocalObservation{info: info, identity: identity}
		}
		verified[full] = mediaLocalReadyEvidence{observation: observation, runtime: local}
		return true
	}
}

func (s *MediaOrganizer) localFileReadyLocked(rule MediaOrganizeRule, local *mediaLocalRuntime, full string, info os.FileInfo) bool {
	if local == nil || !mediaSameWatchRule(local.rule, rule) {
		if s.monitorRunning {
			return false
		}
		return time.Since(info.ModTime()) >= 30*time.Second
	}
	o := s.localMonitorSettings()
	now := o.now()
	observation := local.files[full]
	identity, err := mediaLocalIdentityOf(full, info)
	if err != nil {
		if due := now.Add(o.stable); local.settleDue.IsZero() || due.Before(local.settleDue) {
			local.settleDue = due
		}
		return false
	}
	if observation.info == nil || !mediaSameLocalIdentity(observation.identity, identity) || observation.info.Size() != info.Size() || !observation.info.ModTime().Equal(info.ModTime()) {
		observation.info, observation.observedAt = info, now
		observation.identity = identity
	}
	deadline := observation.observedAt.Add(o.stable)
	if eventDue := observation.changedAt.Add(o.stable); eventDue.After(deadline) {
		deadline = eventDue
	}
	if modifiedDue := info.ModTime().Add(o.stable); modifiedDue.After(deadline) {
		deadline = modifiedDue
	}
	if _, exists := local.files[full]; exists || len(local.files) < 5000 {
		local.files[full] = observation
	}
	if now.Before(deadline) {
		if local.settleDue.IsZero() || deadline.Before(local.settleDue) {
			local.settleDue = deadline
		}
		return false
	}
	return true
}

func (s *MediaOrganizer) pruneLocalObservations(rule MediaOrganizeRule, seen map[string]bool, started time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if local := s.localMonitors[rule.ID]; local != nil && mediaSameWatchRule(local.rule, rule) {
		for full, observation := range local.files {
			if !seen[full] && !observation.changedAt.After(started) {
				delete(local.files, full)
			}
		}
	}
}

func mediaTemporaryName(name string) bool {
	name = strings.ToLower(name)
	for _, suffix := range []string{".part", ".partial", ".tmp", ".temp", ".!qb", ".crdownload", ".download", ".filepart", ".aria2"} {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return strings.HasPrefix(name, ".nfs")
}

func mediaLocalDownloadPending(full string) bool {
	for _, suffix := range []string{".aria2", ".part", ".!qB", ".crdownload"} {
		if _, err := os.Lstat(full + suffix); err == nil {
			return true
		}
	}
	return false
}
