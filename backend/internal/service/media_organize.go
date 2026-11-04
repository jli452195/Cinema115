package service

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"strm115/backend/internal/mediacategory"
	"strm115/backend/internal/mediafacts"
)

var ErrMediaOrganizeInput = errors.New("整理参数无效")
var ErrMediaOrganizeBusy = errors.New("整理任务正在处理，请稍后重试")
var ErrMediaOrganizeTaskNotFound = fmt.Errorf("%w：整理任务记录已移除", ErrMediaOrganizeInput)
var errMediaTargetExists = errors.New("目标已存在，不覆盖")
var errMediaDuplicateDone = errors.New("已删除 SHA1 相同的来源重复文件")

type MediaOrganizeRule struct {
	ID                       string    `json:"id"`
	Name                     string    `json:"name"`
	Storage                  string    `json:"storage"`
	Source                   string    `json:"source"`
	Target                   string    `json:"target"`
	Failure                  string    `json:"failure,omitempty"`
	SourceID                 string    `json:"sourceId,omitempty"`
	TargetID                 string    `json:"targetId,omitempty"`
	FailureID                string    `json:"failureId,omitempty"`
	AccountID                string    `json:"accountId,omitempty"`
	ConnectionID             string    `json:"connectionId,omitempty"`
	ConnectionRevision       uint64    `json:"connectionRevision,omitempty"`
	Classify                 bool      `json:"classify"`
	RealtimeEnabled          bool      `json:"realtimeEnabled"`
	ScheduleEnabled          bool      `json:"scheduleEnabled"`
	IntervalSeconds          int       `json:"intervalSeconds"`
	AttachmentCleanupEnabled bool      `json:"attachmentCleanupEnabled"`
	AttachmentGraceSeconds   int       `json:"attachmentGraceSeconds"`
	NextScanAt               time.Time `json:"nextScanAt,omitempty"`
	DeleteDuplicates         bool      `json:"deleteDuplicates,omitempty"`
	LastScan                 time.Time `json:"lastScan"`
}

type MediaOrganizeEntry struct {
	File           OrganizeFile               `json:"file"`
	Modified       int64                      `json:"modified,omitempty"`
	CD2FileID      string                     `json:"cd2FileId,omitempty"`
	CD2Hashes      map[uint32]string          `json:"cd2Hashes,omitempty"`
	CD2Current     *MediaCD2Location          `json:"cd2Current,omitempty"`
	Reference      bool                       `json:"reference,omitempty"`
	CD2Duplicate   bool                       `json:"cd2Duplicate,omitempty"`
	Group          string                     `json:"group"`
	VideoID        string                     `json:"videoId,omitempty"`
	Relative       string                     `json:"relative,omitempty"`
	Target         string                     `json:"target,omitempty"`
	Status         string                     `json:"status"`
	Message        string                     `json:"message,omitempty"`
	Result         *MediaOrganizeResult       `json:"result,omitempty"`
	Failure        *MediaOrganizeFailure      `json:"failure,omitempty"`
	P115Move       *Media115MoveIntent        `json:"p115Move,omitempty"`
	UpdatedAt      time.Time                  `json:"updatedAt,omitempty"`
	ResultCode     string                     `json:"resultCode,omitempty"`
	AttachmentKeep *MediaAttachmentKeepIntent `json:"-"`
}

type MediaOrganizeGroup struct {
	ID            string              `json:"id"`
	Identity      mediafacts.Identity `json:"identity"`
	Kind          string              `json:"kind,omitempty"`
	TMDBID        int                 `json:"tmdbId,omitempty"`
	Title         string              `json:"title,omitempty"`
	Year          string              `json:"year,omitempty"`
	Category      string              `json:"category,omitempty"`
	Message       string              `json:"message,omitempty"`
	MatchedSeason *int                `json:"matchedSeason,omitempty"`
	ResultCode    string              `json:"resultCode,omitempty"`
}

type MediaOrganizeRun struct {
	ID                 string                 `json:"id"`
	Trigger            string                 `json:"trigger,omitempty"`
	Rule               MediaOrganizeRule      `json:"rule"`
	Classification     ClassificationSnapshot `json:"classification"`
	Status             string                 `json:"status"`
	Message            string                 `json:"message,omitempty"`
	CreatedAt          time.Time              `json:"createdAt"`
	UpdatedAt          time.Time              `json:"updatedAt"`
	Groups             []MediaOrganizeGroup   `json:"groups"`
	Entries            []MediaOrganizeEntry   `json:"entries"`
	TaskFileID         string                 `json:"taskFileId,omitempty"`
	RecognitionVersion string                 `json:"recognitionVersion,omitempty"`
	streaming          bool
	processing         map[string]bool
	seenTasks          map[string]bool
	recognitionEvents  []mediaRecognitionLogEvent
	scanScopes         []mediaScanScope
}

// Restore only this batch's rules, never the current global configuration.
func (r *MediaOrganizeRun) prepareClassification() error {
	if !r.Rule.Classify {
		return nil
	}
	snapshot := &r.Classification
	if snapshot.Revision == "" || snapshot.Revision != classificationRevision(snapshot.YAML) {
		return fmt.Errorf("%w：批次分类规则快照无效", ErrMediaOrganizeInput)
	}
	if snapshot.Classifier == nil {
		classifier, err := mediacategory.Compile(snapshot.YAML)
		if err != nil {
			return fmt.Errorf("编译批次分类规则: %w", err)
		}
		snapshot.Classifier = classifier
	}
	return nil
}

type ClassificationRules interface {
	Snapshot() (ClassificationSnapshot, error)
}

type mediaOrganizeDocument struct {
	Rules   []MediaOrganizeRule         `json:"rules"`
	Runs    []MediaOrganizeRun          `json:"runs"`
	Orphans map[string]mediaOrphanState `json:"orphans,omitempty"`
}

// MediaOrganizer has its own state and worker; it never updates anime tasks.
// Only immutable naming facts and the provider's directory reservation are shared.
type MediaOrganizer struct {
	Logs           *GenerationLogs
	mu             sync.Mutex
	db             mediaOrganizeStorage
	doc            mediaOrganizeDocument
	metadata       MediaRecognitionSource
	templates      NamingTemplates
	classification ClassificationRules
	accounts       *P115Accounts
	drive          OrganizeDrive
	cd2            mediaCD2Connections
	reserve        func() (func(), error)
	active         string
	persistErr     error
	actions        map[string]string
	inFlight       map[string]map[string]int
	wake           chan struct{}
	monitorWake    chan struct{}
	monitorRunning bool
	localMonitors  map[string]*mediaLocalRuntime
	monitorOptions mediaLocalMonitorOptions
	stopping       map[string]bool
	cloudMonitors  map[string]*mediaCloudRuntime
}

func (s *MediaOrganizer) persistLocked(changed ...string) error {
	return s.persistSQLiteLocked(changed)
}

func (s *MediaOrganizer) persistMonitorRulesLocked(changed ...string) error {
	err := s.persistLocked(changed...)
	if err == nil {
		select {
		case s.monitorWake <- struct{}{}:
		default:
		}
	}
	return err
}

func cloneMediaRun(source MediaOrganizeRun, target *MediaOrganizeRun) {
	*target = source
	target.processing = nil
	target.scanScopes = slices.Clone(source.scanScopes)
	target.seenTasks = nil
	target.recognitionEvents = nil
	target.Groups = slices.Clone(source.Groups)
	for i := range target.Groups {
		target.Groups[i].MatchedSeason = mediaOptionalNumber(source.Groups[i].MatchedSeason)
	}
	target.Entries = slices.Clone(source.Entries)
	for i := range target.Entries {
		target.Entries[i].File.filenameFacts = nil
		target.Entries[i].AttachmentKeep = cloneMediaAttachmentKeep(source.Entries[i].AttachmentKeep)
		target.Entries[i].File.NamingValues = maps.Clone(source.Entries[i].File.NamingValues)
		target.Entries[i].CD2Hashes = maps.Clone(source.Entries[i].CD2Hashes)
		if source.Entries[i].CD2Current != nil {
			location := *source.Entries[i].CD2Current
			target.Entries[i].CD2Current = &location
		}
		if source.Entries[i].Result != nil {
			result := *source.Entries[i].Result
			target.Entries[i].Result = &result
		}
		if source.Entries[i].Failure != nil {
			failure := *source.Entries[i].Failure
			target.Entries[i].Failure = &failure
		}
		if source.Entries[i].P115Move != nil {
			intent := *source.Entries[i].P115Move
			target.Entries[i].P115Move = &intent
		}
	}
}

func (s *MediaOrganizer) Snapshot(id string) (MediaOrganizeRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.doc.Runs {
		if r.ID == id {
			var copy MediaOrganizeRun
			cloneMediaRun(r, &copy)
			return copy, nil
		}
	}
	return MediaOrganizeRun{}, ErrMediaOrganizeInput
}

func (s *MediaOrganizer) SaveRule(rule MediaOrganizeRule) (MediaOrganizeRule, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return s.SaveRuleContext(ctx, rule)
}

func (s *MediaOrganizer) SaveRuleContext(ctx context.Context, rule MediaOrganizeRule) (MediaOrganizeRule, error) {
	if err := ctx.Err(); err != nil {
		return rule, err
	}
	if strings.TrimSpace(rule.Name) == "" || len([]rune(rule.Name)) > 80 {
		return rule, ErrMediaOrganizeInput
	}
	if rule.AttachmentGraceSeconds < 0 || rule.AttachmentGraceSeconds > 86400 {
		return rule, ErrMediaOrganizeInput
	}
	if rule.Storage == "cd2" && rule.IntervalSeconds == 0 {
		rule.IntervalSeconds = 20
	}
	if err := mediaValidateSchedule(rule.Storage, MediaOrganizeSchedule{rule.RealtimeEnabled, rule.ScheduleEnabled, rule.IntervalSeconds}); err != nil {
		return rule, err
	}
	if rule.Storage == "local" {
		rule.DeleteDuplicates = false
	}
	if rule.ID != "" {
		s.mu.Lock()
		for _, old := range s.doc.Rules {
			if old.ID == rule.ID && mediaSameRuleSettings(old, rule) {
				defer s.mu.Unlock()
				return s.updateScheduleLocked(old.ID, MediaOrganizeSchedule{rule.RealtimeEnabled, rule.ScheduleEnabled, rule.IntervalSeconds})
			}
		}
		s.mu.Unlock()
	}
	if rule.Storage == "local" {
		rule.ConnectionID, rule.ConnectionRevision = "", 0
		source, err := mediaLocalRoot(rule.Source)
		if err != nil {
			return rule, err
		}
		target, err := mediaLocalRoot(rule.Target)
		if err != nil {
			return rule, err
		}
		if mediaWithin(source, target) || mediaWithin(target, source) {
			return rule, fmt.Errorf("%w：来源和目标目录不能重叠", ErrMediaOrganizeInput)
		}
		rule.Source, rule.Target = source, target
		rule.AccountID, rule.SourceID, rule.TargetID, rule.FailureID = "", "", "", ""
		if rule.Failure != "" {
			failure, err := mediaLocalRoot(rule.Failure)
			if err != nil {
				return rule, err
			}
			if mediaWithin(source, failure) || mediaWithin(failure, source) || mediaWithin(target, failure) || mediaWithin(failure, target) {
				return rule, fmt.Errorf("%w：整理失败目录不能与来源、目标目录重叠", ErrMediaOrganizeInput)
			}
			rule.Failure = failure
		}
	} else if rule.Storage == "115" {
		rule.ConnectionID, rule.ConnectionRevision = "", 0
		if s.accounts == nil || s.drive == nil || rule.AccountID == "" {
			return rule, fmt.Errorf("%w：请先配置 115 主账号", ErrMediaOrganizeInput)
		}
		a, err := s.accounts.Primary(rule.AccountID)
		if err != nil {
			return rule, err
		}
		rule.AccountID = a.ID
		if !organizeID(rule.SourceID) || !organizeID(rule.TargetID) || rule.SourceID == "0" || rule.SourceID == rule.TargetID || !organizePath(rule.Source) || !organizePath(rule.Target) {
			return rule, ErrMediaOrganizeInput
		}
		if mediaCloudWithin(rule.Source, rule.Target) || mediaCloudWithin(rule.Target, rule.Source) {
			return rule, fmt.Errorf("%w：来源和目标目录不能重叠", ErrMediaOrganizeInput)
		}
		if rule.Failure != "" || rule.FailureID != "" {
			if !organizeID(rule.FailureID) || rule.FailureID == "0" || rule.FailureID == rule.SourceID || rule.FailureID == rule.TargetID || !organizePath(rule.Failure) {
				return rule, ErrMediaOrganizeInput
			}
			if mediaCloudWithin(rule.Source, rule.Failure) || mediaCloudWithin(rule.Failure, rule.Source) || mediaCloudWithin(rule.Target, rule.Failure) || mediaCloudWithin(rule.Failure, rule.Target) {
				return rule, fmt.Errorf("%w：整理失败目录不能与来源、目标目录重叠", ErrMediaOrganizeInput)
			}
		}
	} else if rule.Storage == "cd2" {
		if err := s.prepareCD2Rule(ctx, &rule); err != nil {
			return rule, err
		}
	} else {
		return rule, ErrMediaOrganizeInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if rule.RealtimeEnabled || rule.ScheduleEnabled {
		for _, run := range s.doc.Runs {
			if run.Rule.ID == rule.ID && s.stopping[run.ID] {
				return rule, ErrMediaOrganizeBusy
			}
		}
	}
	var changed []string
	if rule.ID == "" {
		var err error
		rule.ID, err = organizeToken()
		if err != nil {
			return rule, err
		}
		rule.LastScan, rule.NextScanAt = time.Time{}, time.Time{}
		s.doc.Rules = append(s.doc.Rules, rule)
	} else {
		found := false
		for i, old := range s.doc.Rules {
			if old.ID != rule.ID {
				continue
			}
			found = true
			rule.LastScan = old.LastScan
			rule.NextScanAt = mediaNextScanAfterEdit(old, rule, s.localMonitorSettings().now())
			changed = s.cancelAutomaticScansLocked(rule.ID)
			if rule.ScheduleEnabled && len(changed) > 0 {
				rule.NextScanAt = time.Time{}
			}
			s.doc.Rules[i] = rule
			if !s.doc.Orphans[rule.ID].matches(rule) {
				delete(s.doc.Orphans, rule.ID)
			}
			break
		}
		if !found {
			return rule, ErrMediaOrganizeInput
		}
	}
	if err := s.persistMonitorRulesLocked(changed...); err != nil {
		return rule, err
	}
	for _, id := range changed {
		s.removeActionLocked(id)
	}
	if local := s.localMonitors[rule.ID]; local != nil && rule.RealtimeEnabled && len(changed) > 0 && mediaSameWatchRule(local.rule, rule) {
		local.dirtyDue = s.localMonitorSettings().now()
	}
	s.notify()
	return rule, nil
}

func (s *MediaOrganizer) DeleteRule(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ruleExecutingLocked(id) {
		return ErrMediaOrganizeBusy
	}
	for i, r := range s.doc.Rules {
		if r.ID == id {
			s.doc.Rules = append(s.doc.Rules[:i], s.doc.Rules[i+1:]...)
			delete(s.doc.Orphans, id)
			removed := []string{}
			runs := make([]MediaOrganizeRun, 0, len(s.doc.Runs))
			for _, run := range s.doc.Runs {
				if run.Rule.ID != id {
					runs = append(runs, run)
				} else {
					removed = append(removed, run.ID)
				}
			}
			s.doc.Runs = runs
			if err := s.persistMonitorRulesLocked(removed...); err != nil {
				return err
			}
			for _, runID := range removed {
				s.removeActionLocked(runID)
			}
			return nil
		}
	}
	return ErrMediaOrganizeInput
}

func (s *MediaOrganizer) Start(id string) (MediaOrganizeRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, rule := range s.doc.Rules {
		if rule.ID == id {
			for _, run := range s.doc.Runs {
				if run.Rule.ID == id && s.stopping[run.ID] {
					return MediaOrganizeRun{}, ErrMediaOrganizeBusy
				}
			}
			return s.queueScanLocked(rule)
		}
	}
	return MediaOrganizeRun{}, ErrMediaOrganizeInput
}

func (s *MediaOrganizer) queueScanLocked(rule MediaOrganizeRule) (MediaOrganizeRun, error) {
	return s.queueTriggeredScanLocked(rule, "manual")
}

func (s *MediaOrganizer) queueTriggeredScanLocked(rule MediaOrganizeRule, trigger string) (MediaOrganizeRun, error) {
	for i, run := range s.doc.Runs {
		if run.Rule.ID != rule.ID || run.TaskFileID != "" || !mediaRunActive(run.Status) && !s.busyRunLocked(run.ID) {
			continue
		}
		if trigger == "manual" && run.Status == "queued" && run.ID != s.active && run.Trigger != "manual" {
			run.Trigger, run.UpdatedAt = "manual", s.localMonitorSettings().now()
			s.doc.Runs[i] = run
			if err := s.persistLocked(run.ID); err != nil {
				return MediaOrganizeRun{}, err
			}
			s.enqueueActionLocked(run.ID, "auto")
			s.notify()
		}
		var snapshot MediaOrganizeRun
		cloneMediaRun(run, &snapshot)
		return snapshot, nil
	}
	var classification ClassificationSnapshot
	if rule.Classify {
		if s.classification == nil {
			return MediaOrganizeRun{}, ErrClassificationUnavailable
		}
		var err error
		classification, err = s.classification.Snapshot()
		if err != nil {
			return MediaOrganizeRun{}, err
		}
	}
	id, err := organizeToken()
	if err != nil {
		return MediaOrganizeRun{}, err
	}
	now := s.localMonitorSettings().now()
	r := MediaOrganizeRun{ID: id, Trigger: trigger, Rule: rule, Classification: classification, RecognitionVersion: MediaRecognitionPolicyVersion, Status: "queued", CreatedAt: now, UpdatedAt: now, Groups: []MediaOrganizeGroup{}, Entries: []MediaOrganizeEntry{}}
	if err := r.prepareClassification(); err != nil {
		return MediaOrganizeRun{}, err
	}
	// 下一轮只保留失败和成功视频的只读映射，清除上一轮的临时展示数据。
	removed := []string{}
	for i := range s.doc.Runs {
		old := &s.doc.Runs[i]
		if old.Rule.ID == rule.ID && !mediaRunActive(old.Status) && !s.busyRunLocked(old.ID) &&
			slices.ContainsFunc(old.Entries, func(entry MediaOrganizeEntry) bool {
				return entry.File.MediaType == "video" && entry.Status == "done" && !entry.Reference
			}) {
			*old = mediaPersistentRun(*old)
			removed = append(removed, old.ID)
		}
	}
	s.doc.Runs = slices.DeleteFunc(s.doc.Runs, func(old MediaOrganizeRun) bool {
		if old.Rule.ID != rule.ID {
			return false
		}
		if mediaRunActive(old.Status) || old.ID == s.active {
			return false
		}
		for _, entry := range old.Entries {
			if entry.File.MediaType == "video" {
				return false
			}
		}
		removed = append(removed, old.ID)
		return true
	})
	s.doc.Runs = append([]MediaOrganizeRun{r}, s.doc.Runs...)
	slices.Sort(removed)
	removed = slices.Compact(removed)
	if err := s.persistLocked(append(removed, id)...); err != nil {
		return MediaOrganizeRun{}, err
	}
	for _, removedID := range removed {
		s.removeActionLocked(removedID)
	}
	s.enqueueActionLocked(id, "auto")
	s.notify()
	return r, nil
}

func (s *MediaOrganizer) notify() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *MediaOrganizer) checkpoint(r *MediaOrganizeRun) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r.recognitionEvents = nil
	defer func() { r.recognitionEvents = nil }()
	r.UpdatedAt = time.Now()
	for i := range s.doc.Runs {
		if s.doc.Runs[i].ID == r.ID {
			published := mediaPublishRun(r, s.doc.Runs[i])
			mediaCheckpointResults(&published, s.doc.Runs[i])
			resolved := published
			resolved.Entries = nil
			previousDone := make(map[string]bool)
			for _, entry := range s.doc.Runs[i].Entries {
				if entry.Status == "done" {
					previousDone[entry.File.ID] = true
				}
			}
			for _, entry := range published.Entries {
				if entry.File.MediaType == "video" && entry.Status == "done" && !entry.Reference && !previousDone[entry.File.ID] {
					resolved.Entries = append(resolved.Entries, entry)
				}
			}
			cloneMediaRun(published, &s.doc.Runs[i])
			changed := append([]string{r.ID}, mediaForgetResolvedFailures(&s.doc, resolved, s.busyRunLocked)...)
			if err := s.persistLocked(changed...); err != nil {
				return err
			}
			if r.seenTasks == nil {
				r.seenTasks = make(map[string]bool)
			}
			for id := range mediaVideoIDs(published) {
				r.seenTasks[id] = true
			}
			updated := make(map[string]time.Time, len(published.Entries))
			for _, entry := range published.Entries {
				updated[entry.File.ID] = entry.UpdatedAt
			}
			for j := range r.Entries {
				if at, ok := updated[r.Entries[j].File.ID]; ok {
					r.Entries[j].UpdatedAt = at
				}
			}
			for _, event := range published.recognitionEvents {
				s.Logs.appendRecognition(r.Rule.Name, event.level, event.message, event.details)
			}
			return nil
		}
	}
	return ErrMediaOrganizeInput
}

func (s *MediaOrganizer) Run(ctx context.Context) error {
	monitorCtx, stopMonitor := context.WithCancel(ctx)
	s.mu.Lock()
	s.monitorRunning = true
	s.monitorWake = make(chan struct{}, 1)
	s.localMonitors = make(map[string]*mediaLocalRuntime)
	s.mu.Unlock()
	var monitors sync.WaitGroup
	monitors.Add(1)
	go func() { defer monitors.Done(); s.runLocalMonitor(monitorCtx) }()
	defer func() {
		stopMonitor()
		monitors.Wait()
		s.mu.Lock()
		s.monitorRunning = false
		s.mu.Unlock()
	}()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	s.notify()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		case <-s.wake:
		}
		for ctx.Err() == nil {
			if err := s.schedule(); err != nil {
				return err
			}
			s.mu.Lock()
			if s.persistErr != nil {
				err := s.persistErr
				s.mu.Unlock()
				return err
			}
			if s.active != "" {
				s.mu.Unlock()
				break
			}
			run, action, found := s.takeQueuedRunLocked()
			s.mu.Unlock()
			if !found {
				break
			}
			if err := s.processQueuedRun(ctx, run, action); err != nil {
				return err
			}
		}
	}
}

// 入队顺序仅用于当前工作线程，任务提交成功后才登记，避免持久化回滚留下幽灵动作。
func (s *MediaOrganizer) enqueueActionLocked(id, action string) {
	if s.actions == nil {
		s.actions = make(map[string]string)
	}
	s.actions[id] = action
}

func (s *MediaOrganizer) removeActionLocked(id string) {
	delete(s.actions, id)
}

func mediaActionPriority(action string, run MediaOrganizeRun) int {
	if action == "retry" {
		return 0
	}
	if run.Trigger == "manual" {
		return 1
	}
	return 2
}

func (s *MediaOrganizer) takeQueuedRunLocked() (MediaOrganizeRun, string, bool) {
	selected := -1
	for i, run := range s.doc.Runs {
		if s.actions[run.ID] == "" {
			continue
		}
		if selected < 0 || mediaActionBefore(run, s.actions[run.ID], s.doc.Runs[selected], s.actions[s.doc.Runs[selected].ID]) {
			selected = i
		}
	}
	if selected < 0 {
		return MediaOrganizeRun{}, "", false
	}
	var snapshot MediaOrganizeRun
	cloneMediaRun(s.doc.Runs[selected], &snapshot)
	snapshot.seenTasks = mediaVideoIDs(snapshot)
	action := s.actions[snapshot.ID]
	s.removeActionLocked(snapshot.ID)
	s.active = snapshot.ID
	return snapshot, action, true
}

func mediaActionBefore(a MediaOrganizeRun, actionA string, b MediaOrganizeRun, actionB string) bool {
	if priorityA, priorityB := mediaActionPriority(actionA, a), mediaActionPriority(actionB, b); priorityA != priorityB {
		return priorityA < priorityB
	}
	if !a.UpdatedAt.Equal(b.UpdatedAt) {
		return a.UpdatedAt.Before(b.UpdatedAt)
	}
	if !a.CreatedAt.Equal(b.CreatedAt) {
		return a.CreatedAt.Before(b.CreatedAt)
	}
	return a.ID < b.ID
}

// 所有退出路径都释放当前批次，包括错误结果检查点或调度持久化失败。
func (s *MediaOrganizer) processQueuedRun(ctx context.Context, run MediaOrganizeRun, action string) error {
	defer func() {
		s.mu.Lock()
		if s.active == run.ID {
			s.active = ""
		}
		delete(s.inFlight, run.ID)
		delete(s.stopping, run.ID)
		for i := range s.doc.Runs {
			if s.doc.Runs[i].ID == run.ID {
				s.doc.Runs[i] = mediaPersistentRun(s.doc.Runs[i])
				break
			}
		}
		s.mu.Unlock()
	}()
	work, cancel := context.WithTimeout(ctx, 2*time.Hour)
	err := s.process(work, &run, action)
	cancel()
	if errors.Is(err, errMediaOrganizeStopped) {
		mediaFinishStoppedRun(&run)
		if saveErr := s.checkpoint(&run); saveErr != nil {
			return saveErr
		}
		return nil
	}
	if err != nil {
		s.retryLocalScan(run.Rule, err)
		run.Status, run.Message = "failed", err.Error()
		if saveErr := s.checkpoint(&run); saveErr != nil {
			return saveErr
		}
	}
	if action == "auto" && len(run.scanScopes) == 0 {
		return s.finishScanRound(run)
	}
	return nil
}

func mediaWithin(root, child string) bool {
	rel, err := filepath.Rel(root, child)
	return err == nil && (rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel))
}
func mediaCloudWithin(root, child string) bool {
	return root == child || strings.HasPrefix(strings.TrimRight(child, "/")+"/", strings.TrimRight(root, "/")+"/")
}
