package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

type mediaTestWatcher struct {
	eventsC chan fsnotify.Event
	errorsC chan error
	closed  chan struct{}
	once    sync.Once
	mu      sync.Mutex
	paths   map[string]bool
	addErr  error
}

func newMediaTestWatcher() *mediaTestWatcher {
	return &mediaTestWatcher{eventsC: make(chan fsnotify.Event), errorsC: make(chan error), closed: make(chan struct{}), paths: make(map[string]bool)}
}
func (w *mediaTestWatcher) Add(path string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.addErr != nil {
		return w.addErr
	}
	w.paths[path] = true
	return nil
}
func (w *mediaTestWatcher) Remove(path string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.paths, path)
	return nil
}
func (w *mediaTestWatcher) Close() error                  { w.once.Do(func() { close(w.closed) }); return nil }
func (w *mediaTestWatcher) events() <-chan fsnotify.Event { return w.eventsC }
func (w *mediaTestWatcher) errors() <-chan error          { return w.errorsC }

func mediaMonitorTestOptions() mediaLocalMonitorOptions {
	return mediaLocalMonitorOptions{tick: 5 * time.Millisecond, debounce: 20 * time.Millisecond, maxDebounce: 60 * time.Millisecond, stable: 40 * time.Millisecond, retry: 80 * time.Millisecond, filesystemReason: func(string) string { return "" }}
}
func mediaMonitorWait(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("monitor condition did not become true")
}
func mediaMonitorRun(t *testing.T, s *MediaOrganizer) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Error(err)
				}
			case <-time.After(3 * time.Second):
				t.Error("monitor/organizer did not stop")
			}
		})
	}
	t.Cleanup(stop)
	return stop
}
func mediaMonitorEnable(t *testing.T, s *MediaOrganizer, rule MediaOrganizeRule) MediaOrganizeRule {
	t.Helper()
	rule.RealtimeEnabled, rule.ScheduleEnabled = true, true
	saved, err := s.SaveRule(rule)
	if err != nil {
		t.Fatal(err)
	}
	return saved
}
func mediaMonitorVideoFound(t *testing.T, root, name string) bool {
	t.Helper()
	found := false
	err := filepath.WalkDir(root, func(_ string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && entry.Name() == name {
			found = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return found
}

func TestMediaLocalMonitorRealEventsAndMovedTree(t *testing.T) {
	s, _, _, rule := newMediaOrganizeTest(t)
	s.monitorOptions = mediaMonitorTestOptions()
	mediaTestFile(t, rule.Source, "existing/deep/Work.S01E01.strm", "http://original/one")
	rule = mediaMonitorEnable(t, s, rule)
	stop := mediaMonitorRun(t, s)
	mediaMonitorWait(t, func() bool { return s.State().Monitoring[rule.ID].Mode == "realtime" })
	mediaMonitorWait(t, func() bool { return mediaMonitorVideoFound(t, rule.Target, "作品.S01E01.strm") })
	outside := t.TempDir()
	mediaTestFile(t, outside, "deeper/Work.S01E02.strm", "http://original/two")
	if err := os.Rename(outside, filepath.Join(rule.Source, "moved")); err != nil {
		t.Fatal(err)
	}
	mediaMonitorWait(t, func() bool { return mediaMonitorVideoFound(t, rule.Target, "作品.S01E02.strm") })
	mediaTestFile(t, rule.Source, "moved/deeper/Work.S01E03.strm", "http://original/three")
	mediaMonitorWait(t, func() bool { return mediaMonitorVideoFound(t, rule.Target, "作品.S01E03.strm") })
	rule.RealtimeEnabled = false
	if _, err := s.SaveRule(rule); err != nil {
		t.Fatal(err)
	}
	mediaMonitorWait(t, func() bool {
		state := s.State()
		_, exists := state.Monitoring[rule.ID]
		return !exists && !state.Active
	})
	mediaTestFile(t, rule.Source, "Work.S01E04.strm", "http://original/four")
	time.Sleep(120 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(rule.Source, "Work.S01E04.strm")); err != nil {
		t.Fatal("paused monitor processed a new file", err)
	}
	mediaMonitorEnable(t, s, rule)
	mediaMonitorWait(t, func() bool { return mediaMonitorVideoFound(t, rule.Target, "作品.S01E04.strm") })
	stop()
}

func TestMediaLocalMonitorRunningTaskKeepsNewEvents(t *testing.T) {
	s, metadata, _, rule := newMediaOrganizeTest(t)
	s.monitorOptions = mediaMonitorTestOptions()
	watcher := newMediaTestWatcher()
	s.monitorOptions.newWatcher = func() (mediaFSWatcher, error) { return watcher, nil }
	started, release := make(chan struct{}, 2), make(chan struct{})
	s.metadata = mediaWaitingRecognition{metadata, started, release}
	mediaTestFile(t, rule.Source, "Work.S01E01.strm", "http://original/one")
	rule = mediaMonitorEnable(t, s, rule)
	mediaMonitorRun(t, s)
	select {
	case <-started:
	case <-time.After(4 * time.Second):
		t.Fatal("first task did not enter recognition")
	}
	mediaTestFile(t, rule.Source, "Work.S01E02.strm", "http://original/two")
	select {
	case watcher.eventsC <- fsnotify.Event{Name: filepath.Join(rule.Source, "Work.S01E02.strm"), Op: fsnotify.Create | fsnotify.Write}:
	case <-time.After(time.Second):
		t.Fatal("event reader blocked behind task")
	}
	close(release)
	mediaMonitorWait(t, func() bool { return mediaMonitorVideoFound(t, rule.Target, "作品.S01E02.strm") })
}

func TestMediaLocalMonitorRootReplacementRebinds(t *testing.T) {
	s, _, _, rule := newMediaOrganizeTest(t)
	s.monitorOptions = mediaMonitorTestOptions()
	rule = mediaMonitorEnable(t, s, rule)
	mediaMonitorRun(t, s)
	t.Cleanup(func() {
		if t.Failed() {
			s.mu.Lock()
			defer s.mu.Unlock()
			for _, run := range s.doc.Runs {
				t.Logf("root replacement run: status=%s message=%s entries=%+v", run.Status, run.Message, run.Entries)
			}
			for id, local := range s.localMonitors {
				t.Logf("monitor %s: %+v files=%d dirty=%s settle=%s", id, local.status, len(local.files), local.dirtyDue, local.settleDue)
			}
		}
	})
	mediaMonitorWait(t, func() bool { return s.State().Monitoring[rule.ID].Mode == "realtime" })
	oldRoot := rule.Source + ".previous"
	mediaMonitorWait(t, func() bool {
		state := s.State()
		return !state.Active && !state.Rules[0].NextScanAt.IsZero()
	})
	// Windows temporarily denies directory renames while a scan handle is
	// closing. Retry the fixture operation without relaxing monitor behavior.
	var renameErr error
	deadline := time.Now().Add(time.Second)
	for {
		renameErr = os.Rename(rule.Source, oldRoot)
		if renameErr == nil || time.Now().After(deadline) {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if renameErr != nil {
		t.Fatal(renameErr)
	}
	if err := os.Mkdir(rule.Source, 0755); err != nil {
		t.Fatal(err)
	}
	mediaTestFile(t, rule.Source, "Work.S01E02.strm", "http://replacement/")
	mediaMonitorWait(t, func() bool { return mediaMonitorVideoFound(t, rule.Target, "作品.S01E02.strm") })
	mediaTestFile(t, oldRoot, "Work.S01E99.strm", "http://old-root/")
	time.Sleep(100 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(oldRoot, "Work.S01E99.strm")); err != nil {
		t.Fatal("old root continued to be scanned", err)
	}
}

func TestMediaLocalMonitorAddFailureAndRecovery(t *testing.T) {
	s, _, _, rule := newMediaOrganizeTest(t)
	s.monitorOptions = mediaMonitorTestOptions()
	watcher := newMediaTestWatcher()
	watcher.addErr = errors.New("mock directory permission")
	s.monitorOptions.newWatcher = func() (mediaFSWatcher, error) { return watcher, nil }
	rule = mediaMonitorEnable(t, s, rule)
	mediaMonitorRun(t, s)
	mediaMonitorWait(t, func() bool {
		return strings.Contains(s.State().Monitoring[rule.ID].Message, "mock directory permission")
	})
	watcher.mu.Lock()
	watcher.addErr = nil
	watcher.mu.Unlock()
	mediaMonitorWait(t, func() bool { return s.State().Monitoring[rule.ID].Mode == "realtime" })
	mediaTestFile(t, rule.Source, "Work.S01E03.strm", "http://recovered/")
	select {
	case watcher.eventsC <- fsnotify.Event{Name: filepath.Join(rule.Source, "Work.S01E03.strm"), Op: fsnotify.Create}:
	case <-time.After(time.Second):
		t.Fatal("recovered watcher did not read events")
	}
	mediaMonitorWait(t, func() bool { return mediaMonitorVideoFound(t, rule.Target, "作品.S01E03.strm") })
}

func TestMediaLocalMonitorFallbackIndependentOfEvents(t *testing.T) {
	s, _, _, rule := newMediaOrganizeTest(t)
	rule = mediaMonitorEnable(t, s, rule)
	now := time.Now()
	s.monitorOptions.now = func() time.Time { return now }
	s.monitorRunning = true
	local := &mediaLocalRuntime{rule: rule, initialized: true, status: MediaOrganizeMonitorStatus{Mode: "realtime"}, files: make(map[string]mediaLocalObservation)}
	s.localMonitors = map[string]*mediaLocalRuntime{rule.ID: local}
	queue := func() {
		t.Helper()
		if err := s.schedule(); err != nil {
			t.Fatal(err)
		}
		for i := range s.doc.Runs {
			s.doc.Runs[i].Status = "completed"
		}
		clear(s.actions)
	}
	queue()
	first := s.doc.Rules[0].NextScanAt
	now = now.Add(time.Minute)
	local.dirtyDue = now
	queue()
	if !s.doc.Rules[0].NextScanAt.Equal(first) {
		t.Fatal("event scan postponed scheduled scan")
	}
	now = now.Add(4 * time.Minute)
	queue()
	if !s.doc.Rules[0].NextScanAt.Equal(now.Add(5 * time.Minute)) {
		t.Fatal("scheduled scan was suppressed by recent event scan")
	}
}

func TestMediaLocalMonitorStabilityAndBoundedDebounce(t *testing.T) {
	s, _, _, rule := newMediaOrganizeTest(t)
	rule.RealtimeEnabled = true
	mediaTestFile(t, rule.Source, "Work.S01E01.strm", "original")
	full := filepath.Join(rule.Source, "Work.S01E01.strm")
	info, err := os.Stat(full)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	s.monitorOptions.now = func() time.Time { return now }
	local := &mediaLocalRuntime{rule: rule, files: make(map[string]mediaLocalObservation)}
	s.localMonitors = map[string]*mediaLocalRuntime{rule.ID: local}
	if s.localFileReady(rule, full, info) {
		t.Fatal("first observation trusted preserved old mtime")
	}
	now = now.Add(30 * time.Second)
	if !s.localFileReady(rule, full, info) {
		t.Fatal("unchanged second observation did not settle")
	}
	observation := local.files[full]
	observation.changedAt = now
	local.files[full] = observation
	if s.localFileReady(rule, full, info) {
		t.Fatal("write event with unchanged timestamps was ignored")
	}
	other := filepath.Join(rule.Source, "other.strm")
	mediaTestFile(t, rule.Source, "other.strm", "other")
	otherInfo, err := os.Stat(other)
	if err != nil {
		t.Fatal(err)
	}
	otherIdentity, err := mediaLocalIdentityOf(other, otherInfo)
	if err != nil {
		t.Fatal(err)
	}
	local.files[other] = mediaLocalObservation{info: otherInfo, identity: otherIdentity, observedAt: now.Add(-time.Minute)}
	if !s.localFileReady(rule, other, otherInfo) {
		t.Fatal("another active write starved a settled file")
	}
	start := now
	o := s.localMonitorSettings()
	for i := 0; i < 20; i++ {
		mediaMonitorDirty(local, start.Add(time.Duration(i)*time.Second), o)
	}
	if local.dirtyDue.After(start.Add(o.maxDebounce)) {
		t.Fatal("continuous events postponed a scan indefinitely", local.dirtyDue)
	}
	// Inbox maps are unordered: processing an older path after a newer path
	// must preserve the newer trailing deadline and the earliest burst bound.
	local.dirtyFirst, local.dirtyDue = time.Time{}, time.Time{}
	mediaMonitorDirty(local, start.Add(3*time.Second), o)
	mediaMonitorDirty(local, start.Add(time.Second), o)
	mediaMonitorDirty(local, start, o)
	if !local.dirtyFirst.Equal(start) || !local.dirtyDue.Equal(start.Add(3*time.Second+o.debounce)) {
		t.Fatal("unordered events advanced the trailing debounce deadline", local.dirtyFirst, local.dirtyDue)
	}
	mediaMonitorDirty(local, start.Add(time.Minute), o)
	if !local.dirtyDue.Equal(start.Add(o.maxDebounce)) {
		t.Fatal("unordered events bypassed the maximum debounce bound", local.dirtyDue)
	}
}

func TestMediaLocalMonitorScanFailureRetriesWithoutSourceEvents(t *testing.T) {
	s, _, _, rule := newMediaOrganizeTest(t)
	s.monitorOptions = mediaMonitorTestOptions()
	s.monitorOptions.retry = 250 * time.Millisecond
	watcher := newMediaTestWatcher()
	s.monitorOptions.newWatcher = func() (mediaFSWatcher, error) { return watcher, nil }
	mediaTestFile(t, rule.Source, "Work.S01E01.strm", "http://original/retry")
	rule = mediaMonitorEnable(t, s, rule)
	missingTarget := rule.Target + ".offline"
	if err := os.Rename(rule.Target, missingTarget); err != nil {
		t.Fatal(err)
	}
	mediaMonitorRun(t, s)
	var fallback time.Time
	mediaMonitorWait(t, func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		local := s.localMonitors[rule.ID]
		if local == nil || local.scanFailures == 0 || local.retryDue.IsZero() || s.active != "" {
			return false
		}
		fallback = s.doc.Rules[0].NextScanAt
		return local.status.Mode == "realtime" && !fallback.IsZero()
	})
	if err := os.Rename(missingTarget, rule.Target); err != nil {
		t.Fatal(err)
	}
	// The fake watcher emits nothing: only the independent retry deadline can
	// observe target recovery before the five-minute fallback scan.
	mediaMonitorWait(t, func() bool { return mediaMonitorVideoFound(t, rule.Target, "作品.S01E01.strm") })
	s.mu.Lock()
	local := s.localMonitors[rule.ID]
	failures, retry, lastFallback := local.scanFailures, local.retryDue, s.doc.Rules[0].NextScanAt
	s.mu.Unlock()
	if failures != 0 || !retry.IsZero() || !lastFallback.Equal(fallback) {
		t.Fatal("recovery failed to clear retries or postponed fallback", failures, retry, lastFallback, fallback)
	}
}

func TestMediaLocalMonitorScanRetryBudgetAndClassification(t *testing.T) {
	s, _, _, rule := newMediaOrganizeTest(t)
	rule.RealtimeEnabled = true
	now := time.Now()
	s.monitorOptions.now = func() time.Time { return now }
	local := &mediaLocalRuntime{rule: rule, files: make(map[string]mediaLocalObservation), dirtyDue: now.Add(time.Second), settleDue: now.Add(2 * time.Second)}
	s.localMonitors = map[string]*mediaLocalRuntime{rule.ID: local}
	pathErr := &os.PathError{Op: "open", Path: rule.Source, Err: os.ErrPermission}
	for _, err := range []error{pathErr, errors.New("provider unavailable"), &mediaLocalScanError{errors.New("scan limit")}, &mediaLocalScanError{context.Canceled}} {
		s.retryLocalScan(rule, err)
	}
	if local.scanFailures != 0 || !local.retryDue.IsZero() {
		t.Fatal("non-filesystem or non-scan error scheduled a fast retry", local)
	}
	scanErr := fmt.Errorf("auto scan: %w", &mediaLocalScanError{pathErr})
	var unwrapped *os.PathError
	if !errors.As(scanErr, &unwrapped) || unwrapped != pathErr {
		t.Fatal("scan wrapper did not preserve filesystem error classification", scanErr)
	}
	for attempt, delay := range []time.Duration{30 * time.Second, time.Minute, 2 * time.Minute, 0} {
		local.retryDue = time.Time{} // Simulate the scheduler consuming each retry.
		s.retryLocalScan(rule, scanErr)
		if delay == 0 {
			if !local.retryDue.IsZero() {
				t.Fatal("a fourth fast retry was scheduled", local.retryDue)
			}
		} else if !local.retryDue.Equal(now.Add(delay)) {
			t.Fatal("unexpected retry backoff", attempt, local.retryDue, delay)
		}
		if !local.dirtyDue.Equal(now.Add(time.Second)) || !local.settleDue.Equal(now.Add(2*time.Second)) {
			t.Fatal("scan retry postponed earlier event/stability work", local)
		}
	}
	s.localScanSucceeded(rule)
	if local.scanFailures != 0 || !local.retryDue.IsZero() {
		t.Fatal("successful scan did not clear the retry budget", local)
	}
	local.retryDue = now.Add(5 * time.Second)
	s.retryLocalScan(rule, scanErr)
	if !local.retryDue.Equal(now.Add(5 * time.Second)) {
		t.Fatal("retry postponed already pending scan work", local.retryDue)
	}
}

func TestMediaLocalMonitorReaderDrainsErrorsUntilCancellation(t *testing.T) {
	watcher := newMediaTestWatcher()
	inbox := &mediaMonitorInbox{wake: make(chan struct{}, 1), events: make(map[string]mediaMonitorEvent)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		mediaReadLocalEvents(ctx, watcher, inbox, mediaLocalMonitorOptions{now: time.Now})
	}()
	for _, err := range []error{errors.New("first watch error"), errors.New("second watch error")} {
		select {
		case watcher.errorsC <- err:
		case <-time.After(time.Second):
			t.Fatal("event reader stopped draining errors before manager cancellation")
		}
	}
	close(watcher.errorsC)
	select {
	case watcher.eventsC <- fsnotify.Event{Name: "after-error", Op: fsnotify.Write}:
	case <-time.After(time.Second):
		t.Fatal("a closed error channel stopped event draining")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("event reader did not stop on cancellation")
	}
}

func TestMediaLocalMonitorPausedScanKeepsStabilityProtection(t *testing.T) {
	s, _, _, rule := newMediaOrganizeTest(t)
	mediaTestFile(t, rule.Source, "Work.S01E01.strm", "http://one/")
	mediaTestFile(t, rule.Source, "Work.S01E02.strm", "http://two/")
	rule = mediaMonitorEnable(t, s, rule)
	s.monitorRunning = true
	s.localMonitors = map[string]*mediaLocalRuntime{rule.ID: {rule: rule, files: make(map[string]mediaLocalObservation)}}
	files, err := s.files(context.Background(), rule)
	if err != nil {
		t.Fatal(err)
	}
	ready, calls := files.localReady, 0
	files.localReady = func(full string, info os.FileInfo) bool {
		calls++
		if calls == 2 {
			paused := rule
			paused.RealtimeEnabled = false
			if _, err := s.SaveRule(paused); err != nil {
				t.Fatal(err)
			}
			s.mu.Lock()
			delete(s.localMonitors, rule.ID)
			s.mu.Unlock()
		}
		return ready(full, info)
	}
	entries, err := files.scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 || calls != 2 {
		t.Fatal("pausing an active scan bypassed two-observation stability", entries, calls)
	}
}

func TestMediaLocalMonitorPausedBatchKeepsOnlyVerifiedFiles(t *testing.T) {
	for _, scenario := range []string{"unchanged", "modified", "replaced identity", "later write event"} {
		t.Run(scenario, func(t *testing.T) {
			s, _, _, rule := newMediaOrganizeTest(t)
			const name, original = "Work.S01E01.strm", "http://old/"
			mediaTestFile(t, rule.Source, name, original)
			full := filepath.Join(rule.Source, name)
			info, err := os.Stat(full)
			if err != nil {
				t.Fatal(err)
			}
			rule = mediaMonitorEnable(t, s, rule)
			now := time.Now()
			s.monitorOptions.now = func() time.Time { return now }
			s.monitorRunning = true
			local := &mediaLocalRuntime{rule: rule, files: make(map[string]mediaLocalObservation)}
			s.localMonitors = map[string]*mediaLocalRuntime{rule.ID: local}
			if s.localFileReady(rule, full, info) {
				t.Fatal("a first observation was incorrectly ready")
			}
			now = now.Add(30 * time.Second)
			if !s.localFileReady(rule, full, info) {
				t.Fatal("fixture did not establish stable file evidence")
			}
			files, err := s.files(context.Background(), rule)
			if err != nil {
				t.Fatal(err)
			}
			run, err := s.Start(rule.ID)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.scan(context.Background(), files, &run); err != nil {
				t.Fatal(err)
			}
			if len(run.Entries) != 1 || run.Entries[0].Status != "ready" {
				t.Fatal("fixture did not finish recognition with a ready video", run.Entries)
			}
			paused := rule
			paused.RealtimeEnabled = false
			if _, err := s.SaveRule(paused); err != nil {
				t.Fatal(err)
			}
			s.mu.Lock()
			delete(s.localMonitors, rule.ID)
			s.mu.Unlock()
			// Preserved old mtimes must not make files first seen after pause
			// ready, while the existing batch keeps its verified original.
			mediaTestFile(t, rule.Source, "new.strm", "http://new-file/")
			newInfo, err := os.Stat(filepath.Join(rule.Source, "new.strm"))
			if err != nil {
				t.Fatal(err)
			}
			if files.localReady(filepath.Join(rule.Source, "new.strm"), newInfo) {
				t.Fatal("a new file bypassed stability after pausing")
			}
			switch scenario {
			case "modified":
				modified := info.ModTime().Add(time.Second)
				if err := os.Chtimes(full, modified, modified); err != nil {
					t.Fatal(err)
				}
			case "replaced identity":
				if err := os.Rename(full, full+".original"); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(full, []byte("http://new/"), 0644); err != nil {
					t.Fatal(err)
				}
				if err := os.Chtimes(full, info.ModTime(), info.ModTime()); err != nil {
					t.Fatal(err)
				}
			case "later write event":
				s.mu.Lock()
				observation := local.files[full]
				observation.changedAt = now.Add(time.Millisecond)
				local.files[full] = observation
				s.mu.Unlock()
			}
			if err := files.recheckLocalCompanions(context.Background(), &run); err != nil {
				t.Fatal(err)
			}
			if scenario != "unchanged" && (run.Entries[0].Status != "retained" || run.Entries[0].Relative != "") {
				t.Fatal("paused batch accepted changed identity, metadata or write evidence", run.Entries)
			}
			if err := s.execute(context.Background(), files, &run); err != nil {
				t.Fatal(err)
			}
			if scenario == "unchanged" {
				entry := run.Entries[0]
				body, err := os.ReadFile(filepath.Join(rule.Target, filepath.FromSlash(entry.Relative)))
				if entry.Status != "done" || err != nil || string(body) != original {
					t.Fatal("pausing discarded an in-flight batch's verified stability", entry, err)
				}
			} else if _, err := os.Stat(full); err != nil {
				t.Fatal("changed source was moved after pausing", err)
			}
			if _, err := os.Stat(filepath.Join(rule.Source, "new.strm")); err != nil {
				t.Fatal("a post-pause file was processed by the frozen batch", err)
			}
		})
	}
}

func TestMediaLocalMonitorFullCacheStillRecordsExistingWrites(t *testing.T) {
	s, _, _, rule := newMediaOrganizeTest(t)
	s.monitorOptions = mediaMonitorTestOptions()
	s.monitorOptions.debounce = time.Hour
	s.monitorOptions.maxDebounce = 2 * time.Hour
	watcher := newMediaTestWatcher()
	s.monitorOptions.newWatcher = func() (mediaFSWatcher, error) { return watcher, nil }
	rule = mediaMonitorEnable(t, s, rule)
	mediaMonitorRun(t, s)
	mediaMonitorWait(t, func() bool {
		state := s.State()
		return state.Monitoring[rule.ID].Mode == "realtime" && !state.Rules[0].NextScanAt.IsZero() && !state.Active
	})
	full := filepath.Join(rule.Source, "Work.S01E01.strm")
	s.mu.Lock()
	local := s.localMonitors[rule.ID]
	local.files[full] = mediaLocalObservation{}
	for index := 1; index < 5000; index++ {
		local.files[filepath.Join(rule.Source, time.Unix(int64(index), 0).Format("150405"))] = mediaLocalObservation{}
	}
	count := len(local.files)
	s.mu.Unlock()
	if count != 5000 {
		t.Fatal("full-cache fixture was not full", count)
	}
	select {
	case watcher.eventsC <- fsnotify.Event{Name: full, Op: fsnotify.Write}:
	case <-time.After(time.Second):
		t.Fatal("write not read")
	}
	mediaMonitorWait(t, func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return !s.localMonitors[rule.ID].files[full].changedAt.IsZero()
	})
}

func TestMediaLocalMonitorFailureOverflowRecovery(t *testing.T) {
	s, _, _, rule := newMediaOrganizeTest(t)
	s.monitorOptions = mediaMonitorTestOptions()
	var calls atomic.Int32
	var mu sync.Mutex
	var current *mediaTestWatcher
	s.monitorOptions.newWatcher = func() (mediaFSWatcher, error) {
		if calls.Add(1) == 1 {
			return nil, errors.New("mock watch limit")
		}
		w := newMediaTestWatcher()
		mu.Lock()
		current = w
		mu.Unlock()
		return w, nil
	}
	rule = mediaMonitorEnable(t, s, rule)
	stop := mediaMonitorRun(t, s)
	mediaMonitorWait(t, func() bool { return strings.Contains(s.State().Monitoring[rule.ID].Message, "mock watch limit") })
	mediaMonitorWait(t, func() bool { return s.State().Monitoring[rule.ID].Mode == "realtime" })
	mu.Lock()
	watcher := current
	mu.Unlock()
	select {
	case watcher.errorsC <- fsnotify.ErrEventOverflow:
	case <-time.After(time.Second):
		t.Fatal("watch error was not consumed")
	}
	mediaMonitorWait(t, func() bool { return s.State().Monitoring[rule.ID].Mode == "unavailable" })
	mediaMonitorWait(t, func() bool { return s.State().Monitoring[rule.ID].Mode == "realtime" })
	stop()
	mu.Lock()
	last := current
	mu.Unlock()
	select {
	case <-last.closed:
	default:
		t.Fatal("watcher leaked on cancellation")
	}
}

func TestMediaLocalMonitorNetworkFallbackAndTemporaryFiles(t *testing.T) {
	s, _, _, rule := newMediaOrganizeTest(t)
	s.monitorOptions = mediaMonitorTestOptions()
	s.monitorOptions.filesystemReason = func(string) string { return "网络挂载的实时监听不可用" }
	mediaTestFile(t, rule.Source, "Work.S01E01.mkv", "unfinished")
	for _, name := range []string{"Work.S01E01.mkv.aria2", "Work.S01E01.nfo", "copy.mkv.part", "copy.mkv.!qB", "browser.mkv.crdownload", "cache.tmp"} {
		mediaTestFile(t, rule.Source, name, "keep")
	}
	mediaTestFile(t, rule.Source, "Work.S01E02.strm", "http://original/completed")
	rule = mediaMonitorEnable(t, s, rule)
	mediaMonitorRun(t, s)
	mediaMonitorWait(t, func() bool { return s.State().Monitoring[rule.ID].Mode == "unavailable" })
	mediaMonitorWait(t, func() bool { return mediaMonitorVideoFound(t, rule.Target, "作品.S01E02.strm") })
	for _, name := range []string{"Work.S01E01.mkv", "Work.S01E01.mkv.aria2", "Work.S01E01.nfo", "copy.mkv.part", "copy.mkv.!qB", "browser.mkv.crdownload", "cache.tmp"} {
		if _, err := os.Stat(filepath.Join(rule.Source, name)); err != nil {
			t.Fatal("incomplete download/control/sidecar changed", name, err)
		}
	}
}
