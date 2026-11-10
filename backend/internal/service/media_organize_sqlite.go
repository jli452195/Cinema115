package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"strm115/backend/internal/storage"
)

type mediaOrganizeStorage interface {
	LoadMediaOrganize(context.Context) (storage.MediaOrganizeRows, error)
	SaveMediaOrganize(context.Context, string, []storage.MediaOrganizeRunRow, []string) error
	QueryMediaOrganizeLive(context.Context, string, string, int, []storage.MediaOrganizeLiveTaskRow) (storage.MediaOrganizeQuery, error)
	LoadMediaOrganizeRuns(context.Context, []string, string, string) ([]string, error)
	LoadMediaOrganizeTask(context.Context, string, string) (string, error)
	LoadMediaOrganizeReferences(context.Context, string, []string, []string, bool) ([]string, error)
	LoadMediaOrganizeFailures(context.Context, string, []string) ([]string, error)
}

type mediaOrganizeSettings struct {
	Rules   []MediaOrganizeRule         `json:"rules"`
	Orphans map[string]mediaOrphanState `json:"orphans,omitempty"`
}

// OpenMediaOrganizerSQLite imports the previous local snapshot only once. The
// initialized SQLite row is authoritative even if unlinking the old file fails.
func OpenMediaOrganizerSQLite(ctx context.Context, db *storage.Store, dir, secret string, metadata MediaRecognitionSource, templates NamingTemplates, classification ClassificationRules, accounts *P115Accounts, drive OrganizeDrive, reserve func() (func(), error)) (*MediaOrganizer, error) {
	s := &MediaOrganizer{db: db, metadata: metadata, templates: templates, classification: classification, accounts: accounts, drive: drive, reserve: reserve, actions: make(map[string]string), wake: make(chan struct{}, 1)}
	loaded, err := db.LoadMediaOrganize(ctx)
	if err != nil {
		return nil, err
	}
	if loaded.Initialized {
		if err := s.decodeSQLite(loaded); err != nil {
			return nil, err
		}
	} else {
		old, err := NewSessionStore(filepath.Join(dir, "media-organize.enc"), secret)
		if err != nil {
			return nil, err
		}
		if old.Value() != "" {
			if err := json.Unmarshal([]byte(old.Value()), &s.doc); err != nil {
				return nil, fmt.Errorf("读取原整理记录: %w", err)
			}
			settings, err := mediaStoredSettings([]byte(old.Value()))
			if err != nil {
				return nil, err
			}
			s.doc.Rules, s.doc.Orphans = settings.Rules, settings.Orphans
		}
	}
	if s.doc.Rules == nil {
		s.doc.Rules = []MediaOrganizeRule{}
	}
	if s.doc.Runs == nil {
		s.doc.Runs = []MediaOrganizeRun{}
	}
	changed := make([]string, 0, len(s.doc.Runs))
	for i := range s.doc.Runs {
		r := &s.doc.Runs[i]
		if !loaded.Initialized {
			*r = mediaPersistentRun(*r)
		}
		deferInterruptedSave := false
		if s.resume115EventScanLocked(*r) {
			s.enqueueActionLocked(r.ID, "auto")
		} else if mediaRunActive(r.Status) {
			if s.repair115EventScanLocked(*r) {
				deferInterruptedSave = true
			}
			r.Status = "interrupted"
			r.Message = "服务重启，任务已中断；核对任务记录后可重试"
		}
		// 已结束历史不在启动时加载或重建，只保存本次中断恢复的变更。
		if !deferInterruptedSave && (!loaded.Initialized || r.Status == "interrupted") {
			changed = append(changed, r.ID)
		}
	}
	if err := s.persistSQLiteLockedContext(ctx, changed); err != nil {
		return nil, err
	}
	s.releaseHistoryLocked()
	// Deletion happens strictly after the SQLite transaction committed.
	if err := os.Remove(filepath.Join(dir, "media-organize.enc")); err != nil && !os.IsNotExist(err) && !loaded.Initialized {
		log.Printf("整理记录已保存到 SQLite，旧快照未能清理：%v", err)
	}
	return s, nil
}

// 只恢复尚未开始的事件扫描；已开始的外部写入仍走原有中断核对流程。
func (s *MediaOrganizer) resume115EventScanLocked(run MediaOrganizeRun) bool {
	if run.Status != "queued" || run.TaskFileID != "" || run.Rule.Storage != "115" || s.accounts == nil {
		return false
	}
	if _, err := s.accounts.Primary(run.Rule.AccountID); err != nil {
		return false
	}
	return slices.ContainsFunc(s.doc.Rules, func(rule MediaOrganizeRule) bool {
		return rule.ID == run.Rule.ID && rule.RealtimeEnabled && mediaSameRuleSettings(rule, run.Rule)
	})
}

// 中断的实时任务只触发来源补扫；旧记录保留到补扫持久化，防止启动期间再次退出丢失恢复证据。
func (s *MediaOrganizer) repair115EventScanLocked(run MediaOrganizeRun) bool {
	if run.TaskFileID != "" || run.Rule.Storage != "115" || s.accounts == nil {
		return false
	}
	if _, err := s.accounts.Primary(run.Rule.AccountID); err != nil {
		return false
	}
	for _, rule := range s.doc.Rules {
		if rule.ID != run.Rule.ID || !rule.RealtimeEnabled || !mediaSameRuleSettings(rule, run.Rule) {
			continue
		}
		runtime := s.cloudRuntimeLocked(rule)
		runtime.full, runtime.initialized, runtime.due = true, true, s.localMonitorSettings().now()
		runtime.eventRecoveryIDs = append(runtime.eventRecoveryIDs, run.ID)
		return true
	}
	return false
}

func (s *MediaOrganizer) decodeSQLite(rows storage.MediaOrganizeRows) error {
	doc := mediaOrganizeDocument{Rules: []MediaOrganizeRule{}, Runs: make([]MediaOrganizeRun, 0, len(rows.Runs))}
	if rows.Rules != "" {
		settings, err := mediaStoredSettings([]byte(rows.Rules))
		if err != nil {
			return err
		}
		doc.Rules, doc.Orphans = settings.Rules, settings.Orphans
	}
	for _, raw := range rows.Runs {
		run, err := mediaDecodeStoredRun(raw)
		if err != nil {
			return err
		}
		doc.Runs = append(doc.Runs, run)
	}
	s.doc = doc
	return nil
}

// 仅在读取工作区存储时转换原有分钟配置，运行接口只接受统一的秒数和独立开关。
func mediaStoredRule(raw json.RawMessage) (MediaOrganizeRule, error) {
	type ruleFields MediaOrganizeRule
	var previous struct {
		ruleFields
		Enabled                  bool  `json:"enabled"`
		IntervalMinutes          int   `json:"intervalMinutes"`
		IntervalSeconds          *int  `json:"intervalSeconds"`
		AttachmentCleanupEnabled *bool `json:"attachmentCleanupEnabled"`
		AttachmentGraceSeconds   *int  `json:"attachmentGraceSeconds"`
	}
	if err := json.Unmarshal(raw, &previous); err != nil {
		return MediaOrganizeRule{}, fmt.Errorf("解码整理规则: %w", err)
	}
	rule := MediaOrganizeRule(previous.ruleFields)
	if previous.AttachmentCleanupEnabled == nil {
		rule.AttachmentCleanupEnabled = true
	} else {
		rule.AttachmentCleanupEnabled = *previous.AttachmentCleanupEnabled
	}
	if previous.AttachmentGraceSeconds == nil {
		rule.AttachmentGraceSeconds = 10
	} else {
		rule.AttachmentGraceSeconds = *previous.AttachmentGraceSeconds
	}
	if previous.IntervalSeconds != nil {
		rule.IntervalSeconds = *previous.IntervalSeconds
		return rule, nil
	}
	rule.ScheduleEnabled = previous.Enabled
	rule.RealtimeEnabled = previous.Enabled && rule.Storage == "local"
	rule.IntervalSeconds = previous.IntervalMinutes * 60
	if rule.Storage == "cd2" {
		rule.IntervalSeconds = 20
	} else if rule.IntervalSeconds < 1 || rule.IntervalSeconds > 86400 {
		rule.IntervalSeconds = 300
	}
	return rule, nil
}

func mediaStoredSettings(raw []byte) (mediaOrganizeSettings, error) {
	var stored struct {
		Rules      []json.RawMessage           `json:"rules"`
		Orphans    map[string]mediaOrphanState `json:"orphans"`
		CD2Orphans map[string]mediaOrphanState `json:"cd2Orphans"`
	}
	if strings.HasPrefix(strings.TrimSpace(string(raw)), "[") {
		if err := json.Unmarshal(raw, &stored.Rules); err != nil {
			return mediaOrganizeSettings{}, fmt.Errorf("解码整理规则列表: %w", err)
		}
	} else {
		if err := json.Unmarshal(raw, &stored); err != nil {
			return mediaOrganizeSettings{}, fmt.Errorf("解码整理规则列表: %w", err)
		}
	}
	settings := mediaOrganizeSettings{Rules: make([]MediaOrganizeRule, 0, len(stored.Rules)), Orphans: stored.Orphans}
	if settings.Orphans == nil {
		settings.Orphans = stored.CD2Orphans
		for id, state := range settings.Orphans {
			state.Storage = "cd2"
			settings.Orphans[id] = state
		}
	}
	for _, record := range stored.Rules {
		rule, err := mediaStoredRule(record)
		if err != nil {
			return settings, err
		}
		settings.Rules = append(settings.Rules, rule)
	}
	return settings, nil
}

func (s *MediaOrganizer) persistSQLiteLocked(changed []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return s.persistSQLiteLockedContext(ctx, changed)
}

// Fixed-width UTC timestamps sort in the same order as time.Time, including
// different fractional precision within the same second.
func mediaSortTime(at time.Time) string { return at.UTC().Format("2006-01-02T15:04:05.000000000Z") }

func (s *MediaOrganizer) persistSQLiteLockedContext(ctx context.Context, changed []string) error {
	return s.persistSQLiteRecoveryLockedContext(ctx, changed, nil)
}

func (s *MediaOrganizer) eventRecoveryPendingLocked(id string) bool {
	for _, runtime := range s.cloudMonitors {
		if slices.Contains(runtime.eventRecoveryIDs, id) {
			return true
		}
	}
	return false
}

// 只有替代扫描已经安全入队时，才允许同一事务退役旧的中断恢复证据。
func (s *MediaOrganizer) persist115EventHandoffLocked(runID string, retired []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return s.persistSQLiteRecoveryLockedContext(ctx, append(slices.Clone(retired), runID), retired)
}

func (s *MediaOrganizer) persistSQLiteRecoveryLockedContext(ctx context.Context, changed, retired []string) error {
	if s.persistErr != nil {
		return s.persistErr
	}
	raw, err := json.Marshal(mediaOrganizeSettings{Rules: s.doc.Rules, Orphans: s.doc.Orphans})
	if err != nil {
		return err
	}
	updates := make([]storage.MediaOrganizeRunRow, 0, len(changed))
	removed := []string{}
	for _, id := range changed {
		if s.eventRecoveryPendingLocked(id) && !slices.Contains(retired, id) {
			continue
		}
		var found *MediaOrganizeRun
		for i := range s.doc.Runs {
			if s.doc.Runs[i].ID == id {
				found = &s.doc.Runs[i]
				break
			}
		}
		if found == nil {
			removed = append(removed, id)
			continue
		}
		row, err := mediaRunRow(*found)
		if err != nil {
			return err
		}
		updates = append(updates, row)
	}
	if err := s.db.SaveMediaOrganize(ctx, string(raw), updates, removed); err != nil {
		restoreCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		previous, readErr := s.db.LoadMediaOrganize(restoreCtx)
		if readErr == nil {
			readErr = s.decodeSQLite(previous)
			if readErr == nil {
				// 延迟覆盖的启动恢复记录仍保留原活动状态，回滚后不能把它误当成正在执行。
				for _, runtime := range s.cloudMonitors {
					for _, id := range runtime.eventRecoveryIDs {
						for i := range s.doc.Runs {
							run := &s.doc.Runs[i]
							if run.ID == id && run.ID != s.active && mediaRunActive(run.Status) {
								run.Status, run.Message = "interrupted", "服务重启，等待来源补扫"
							}
						}
					}
				}
			}
		}
		if readErr != nil {
			s.persistErr = fmt.Errorf("保存整理记录失败且无法恢复内存，请重启服务: %w；恢复失败: %v", err, readErr)
			return s.persistErr
		}
		return fmt.Errorf("保存整理记录: %w", err)
	}
	if len(changed) > 0 {
		s.historyChangedLocked()
	}
	return nil
}

func mediaRunRow(run MediaOrganizeRun) (storage.MediaOrganizeRunRow, error) {
	cleanup := mediaRunCleanup(run)
	if run.Status == "queued" && run.Trigger == "realtime" {
		run.StoredScanScopes = slices.Clone(run.scanScopes)
	} else {
		run.StoredScanScopes = nil
	}
	run = mediaPersistentRun(run)
	raw, err := mediaMarshalPersistentRun(run)
	if err != nil {
		return storage.MediaOrganizeRunRow{}, err
	}
	row := storage.MediaOrganizeRunRow{ID: run.ID, RuleID: run.Rule.ID, At: mediaSortTime(run.UpdatedAt), Record: string(raw), Tasks: []storage.MediaOrganizeTaskRow{}}
	for _, task := range mediaTasks(run) {
		item := task.MediaOrganizeHistoryItem
		if item.Status != "failed" && item.Status != "done" {
			continue
		}
		taskRow, err := mediaHistoryTaskRow(item)
		if err != nil {
			return row, err
		}
		row.Tasks = append(row.Tasks, taskRow)
	}
	if len(row.Tasks) == 0 && run.Status == "failed" {
		activity := MediaOrganizeActivity{RunID: run.ID, RuleName: run.Rule.Name, Status: run.Status, Message: run.Message, At: run.UpdatedAt}
		raw, err := json.Marshal(activity)
		if err != nil {
			return row, err
		}
		row.Activity = string(raw)
	}
	if cleanup.Files+cleanup.Directories+cleanup.Failed > 0 {
		raw, err := json.Marshal(cleanup)
		if err != nil {
			return row, err
		}
		row.Cleanup = string(raw)
	}
	return row, nil
}
