package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"strm115/backend/internal/mediafacts"
)

var ErrMediaOrganizeInput = errors.New("整理参数无效")
var ErrMediaOrganizeBusy = errors.New("整理任务正在处理，请稍后重试")
var errMediaTargetExists = errors.New("目标已存在，不覆盖")

type MediaOrganizeRule struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	Storage         string    `json:"storage"`
	Source          string    `json:"source"`
	Target          string    `json:"target"`
	Failure         string    `json:"failure,omitempty"`
	SourceID        string    `json:"sourceId,omitempty"`
	TargetID        string    `json:"targetId,omitempty"`
	FailureID       string    `json:"failureId,omitempty"`
	AccountID       string    `json:"accountId,omitempty"`
	Classify        bool      `json:"classify"`
	Enabled         bool      `json:"enabled"`
	IntervalMinutes int       `json:"intervalMinutes"`
	LastScan        time.Time `json:"lastScan"`
}

type MediaOrganizeEntry struct {
	File        OrganizeFile          `json:"file"`
	Modified    int64                 `json:"modified,omitempty"`
	Fingerprint string                `json:"fingerprint"`
	Group       string                `json:"group"`
	VideoID     string                `json:"videoId,omitempty"`
	Relative    string                `json:"relative,omitempty"`
	Target      string                `json:"target,omitempty"`
	Status      string                `json:"status"`
	Message     string                `json:"message,omitempty"`
	Result      *MediaOrganizeResult  `json:"result,omitempty"`
	Failure     *MediaOrganizeFailure `json:"failure,omitempty"`
}

type MediaOrganizeGroup struct {
	ID       string              `json:"id"`
	Identity mediafacts.Identity `json:"identity"`
	Kind     string              `json:"kind,omitempty"`
	TMDBID   int                 `json:"tmdbId,omitempty"`
	Title    string              `json:"title,omitempty"`
	Category string              `json:"category,omitempty"`
	Message  string              `json:"message,omitempty"`
}

type MediaOrganizeRun struct {
	ID           string               `json:"id"`
	Rule         MediaOrganizeRule    `json:"rule"`
	Status       string               `json:"status"`
	Message      string               `json:"message,omitempty"`
	CreatedAt    time.Time            `json:"createdAt"`
	UpdatedAt    time.Time            `json:"updatedAt"`
	Groups       []MediaOrganizeGroup `json:"groups"`
	Entries      []MediaOrganizeEntry `json:"entries"`
	ReadyCount   int                  `json:"readyCount"`
	FailureCount int                  `json:"failureCount"`
}

type mediaOrganizeDocument struct {
	Rules []MediaOrganizeRule          `json:"rules"`
	Runs  []MediaOrganizeRun           `json:"runs"`
	Seen  map[string]map[string]string `json:"seen"`
}

// MediaOrganizer has its own state and worker; it never updates anime tasks.
// Only immutable naming facts and the provider's directory reservation are shared.
type MediaOrganizer struct {
	mu        sync.Mutex
	store     accountSecretStore
	doc       mediaOrganizeDocument
	metadata  MediaRecognitionSource
	templates NamingTemplates
	accounts  *P115Accounts
	drive     OrganizeDrive
	reserve   func() (func(), error)
	active    string
	actions   map[string]string
	wake      chan struct{}
}

func NewMediaOrganizer(store accountSecretStore, metadata MediaRecognitionSource, templates NamingTemplates, accounts *P115Accounts, drive OrganizeDrive, reserve func() (func(), error)) (*MediaOrganizer, error) {
	s := &MediaOrganizer{store: store, metadata: metadata, templates: templates, accounts: accounts, drive: drive, reserve: reserve, actions: make(map[string]string), wake: make(chan struct{}, 1)}
	if store.Value() != "" {
		if err := json.Unmarshal([]byte(store.Value()), &s.doc); err != nil {
			return nil, fmt.Errorf("读取通用整理配置: %w", err)
		}
	}
	if s.doc.Seen == nil {
		s.doc.Seen = make(map[string]map[string]string)
	}
	if s.doc.Rules == nil {
		s.doc.Rules = []MediaOrganizeRule{}
	}
	if s.doc.Runs == nil {
		s.doc.Runs = []MediaOrganizeRun{}
	}
	for i := range s.doc.Runs {
		r := &s.doc.Runs[i]
		if r.Status == "queued" || r.Status == "scanning" || r.Status == "running" {
			r.Status = "interrupted"
			r.Message = "服务重启，任务已中断；核对预览后可继续"
		}
	}
	return s, s.persistLocked()
}

func (s *MediaOrganizer) persistLocked() error {
	raw, err := json.Marshal(s.doc)
	if err != nil {
		return fmt.Errorf("编码整理记录: %w", err)
	}
	if err = s.store.Save(string(raw)); err != nil {
		var previous mediaOrganizeDocument
		if decodeErr := json.Unmarshal([]byte(s.store.Value()), &previous); decodeErr != nil {
			return fmt.Errorf("恢复整理记录: %w", errors.Join(err, decodeErr))
		}
		s.doc = previous
		return fmt.Errorf("保存整理记录: %w", err)
	}
	return nil
}

func cloneMediaRun(source MediaOrganizeRun, target *MediaOrganizeRun) {
	*target = source
	target.Groups = slices.Clone(source.Groups)
	target.Entries = slices.Clone(source.Entries)
	for i := range target.Entries {
		target.Entries[i].File.NamingValues = maps.Clone(source.Entries[i].File.NamingValues)
		if source.Entries[i].Result != nil {
			result := *source.Entries[i].Result
			target.Entries[i].Result = &result
		}
		if source.Entries[i].Failure != nil {
			failure := *source.Entries[i].Failure
			target.Entries[i].Failure = &failure
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
	if strings.TrimSpace(rule.Name) == "" || len([]rune(rule.Name)) > 80 || rule.IntervalMinutes < 1 || rule.IntervalMinutes > 1440 {
		return rule, ErrMediaOrganizeInput
	}
	if rule.ID != "" {
		s.mu.Lock()
		for i, old := range s.doc.Rules {
			if old.ID != rule.ID {
				continue
			}
			settings := rule
			settings.Enabled, settings.LastScan = old.Enabled, old.LastScan
			if settings != old {
				break
			}
			// Scheduling changes never touch a running task's frozen rule or
			// require an available mount/account merely to pause monitoring.
			if old.Enabled == rule.Enabled {
				s.mu.Unlock()
				return old, nil
			}
			old.Enabled = rule.Enabled
			s.doc.Rules[i] = old
			err := s.persistLocked()
			s.mu.Unlock()
			return old, err
		}
		s.mu.Unlock()
	}
	if rule.Storage == "local" {
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
	} else {
		return rule, ErrMediaOrganizeInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active != "" || len(s.actions) > 0 {
		return rule, ErrMediaOrganizeBusy
	}
	if rule.ID == "" {
		var err error
		rule.ID, err = organizeToken()
		if err != nil {
			return rule, err
		}
		rule.LastScan = time.Time{}
		s.doc.Rules = append(s.doc.Rules, rule)
	} else {
		found := false
		for i, old := range s.doc.Rules {
			if old.ID == rule.ID {
				found = true
				rule.LastScan = old.LastScan
				if old.Storage != rule.Storage || old.Source != rule.Source || old.Target != rule.Target || old.SourceID != rule.SourceID || old.TargetID != rule.TargetID || old.Failure != rule.Failure || old.FailureID != rule.FailureID || old.Classify != rule.Classify || old.AccountID != rule.AccountID {
					delete(s.doc.Seen, rule.ID)
					rule.LastScan = time.Time{}
				}
				s.doc.Rules[i] = rule
				break
			}
		}
		if !found {
			return rule, ErrMediaOrganizeInput
		}
	}
	return rule, s.persistLocked()
}

func (s *MediaOrganizer) DeleteRule(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active != "" || len(s.actions) > 0 {
		return ErrMediaOrganizeBusy
	}
	for i, r := range s.doc.Rules {
		if r.ID == id {
			s.doc.Rules = append(s.doc.Rules[:i], s.doc.Rules[i+1:]...)
			runs := make([]MediaOrganizeRun, 0, len(s.doc.Runs))
			for _, run := range s.doc.Runs {
				if run.Rule.ID != id {
					runs = append(runs, run)
				}
			}
			s.doc.Runs = runs
			delete(s.doc.Seen, id)
			return s.persistLocked()
		}
	}
	return ErrMediaOrganizeInput
}

func (s *MediaOrganizer) Scan(id string) (MediaOrganizeRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.doc.Runs {
		if r.Rule.ID == id && (r.Status == "queued" || r.Status == "scanning" || r.Status == "running") {
			var copy MediaOrganizeRun
			cloneMediaRun(r, &copy)
			return copy, nil
		}
	}
	for _, rule := range s.doc.Rules {
		if rule.ID == id {
			return s.queueScanLocked(rule, false)
		}
	}
	return MediaOrganizeRun{}, ErrMediaOrganizeInput
}

func (s *MediaOrganizer) queueScanLocked(rule MediaOrganizeRule, automatic bool) (MediaOrganizeRun, error) {
	id, err := organizeToken()
	if err != nil {
		return MediaOrganizeRun{}, err
	}
	r := MediaOrganizeRun{ID: id, Rule: rule, Status: "queued", CreatedAt: time.Now(), UpdatedAt: time.Now(), Groups: []MediaOrganizeGroup{}, Entries: []MediaOrganizeEntry{}}
	// Empty scan outcomes are only needed until the next scan.
	// They must not consume history retention or evict actual file results.
	s.doc.Runs = slices.DeleteFunc(s.doc.Runs, func(old MediaOrganizeRun) bool {
		if old.Rule.ID == rule.ID && old.Status != "queued" && old.Status != "scanning" && old.Status != "running" && !slices.ContainsFunc(old.Entries, func(e MediaOrganizeEntry) bool { return e.Result != nil || e.Status == "done" || e.Failure != nil }) {
			return true // A rescan replaces unused previews and diagnostics for this rule.
		}
		return (old.Status == "completed" || old.Status == "preview") && !slices.ContainsFunc(old.Entries, func(e MediaOrganizeEntry) bool { return e.Status != "retained" })
	})
	s.doc.Runs = append([]MediaOrganizeRun{r}, s.doc.Runs...)
	// Count actual terminal batches, not the newly queued (possibly empty) scan.
	retained := 0
	s.doc.Runs = slices.DeleteFunc(s.doc.Runs, func(old MediaOrganizeRun) bool {
		if len(old.Entries) == 0 || old.Status != "completed" && old.Status != "failed" && old.Status != "partial" {
			return false
		}
		for _, e := range old.Entries {
			if e.Status != "done" && e.Failure != nil && (e.Failure.State == "stored" || e.Failure.State == "moving") {
				return false // Keep the only recovery plan for relocated files.
			}
		}
		retained++
		return retained > 20
	})
	if len(s.doc.Runs) > 50 {
		s.doc.Runs = s.doc.Runs[1:]
		return MediaOrganizeRun{}, fmt.Errorf("%w：请先处理已有任务", ErrMediaOrganizeBusy)
	}
	for i := range s.doc.Rules {
		if s.doc.Rules[i].ID == rule.ID {
			s.doc.Rules[i].LastScan = time.Now()
		}
	}
	if err := s.persistLocked(); err != nil {
		return MediaOrganizeRun{}, err
	}
	s.actions[id] = "scan"
	if automatic {
		s.actions[id] = "auto"
	}
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
	r.UpdatedAt = time.Now()
	mediaRunCounts(r)
	for i := range s.doc.Runs {
		if s.doc.Runs[i].ID == r.ID {
			cloneMediaRun(*r, &s.doc.Runs[i])
			return s.persistLocked()
		}
	}
	return ErrMediaOrganizeInput
}

func (s *MediaOrganizer) Run(ctx context.Context) error {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	s.notify()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := s.schedule(); err != nil {
				return err
			}
		case <-s.wake:
		}
		for ctx.Err() == nil {
			s.mu.Lock()
			var id, action string
			var run MediaOrganizeRun
			if s.active != "" {
				s.mu.Unlock()
				break
			}
			for key, value := range s.actions {
				id, action = key, value
				break
			}
			if id != "" {
				delete(s.actions, id)
				s.active = id
				for _, r := range s.doc.Runs {
					if r.ID == id {
						cloneMediaRun(r, &run)
						break
					}
				}
			}
			s.mu.Unlock()
			if id == "" {
				break
			}
			work, cancel := context.WithTimeout(ctx, 2*time.Hour)
			err := s.process(work, &run, action)
			cancel()
			if err != nil {
				run.Status = "failed"
				run.Message = err.Error()
				if saveErr := s.checkpoint(&run); saveErr != nil {
					return saveErr
				}
			}
			s.mu.Lock()
			s.active = ""
			s.mu.Unlock()
		}
	}
}

func (s *MediaOrganizer) schedule() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, rule := range s.doc.Rules {
		if !rule.Enabled || time.Since(rule.LastScan) < time.Duration(rule.IntervalMinutes)*time.Minute {
			continue
		}
		pending := false
		for _, r := range s.doc.Runs {
			if r.Rule.ID == rule.ID && (r.Status == "queued" || r.Status == "scanning" || r.Status == "running") {
				pending = true
				break
			}
		}
		if !pending {
			if _, err := s.queueScanLocked(rule, true); err != nil && !errors.Is(err, ErrMediaOrganizeBusy) {
				return err
			}
		}
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
