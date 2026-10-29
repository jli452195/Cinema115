package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"strm115/backend/internal/storage"
)

type mediaOrganizeStorage interface {
	LoadMediaOrganize(context.Context) (storage.MediaOrganizeRows, error)
	SaveMediaOrganize(context.Context, string, []storage.MediaOrganizeRunRow, []string) error
	QueryMediaOrganize(context.Context, string, string, int) (storage.MediaOrganizeQuery, error)
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
		}
	}
	if s.doc.Rules == nil {
		s.doc.Rules = []MediaOrganizeRule{}
	}
	if s.doc.Runs == nil {
		s.doc.Runs = []MediaOrganizeRun{}
	}
	changed := []string{}
	for i := range s.doc.Runs {
		r := &s.doc.Runs[i]
		if mediaRunActive(r.Status) {
			r.Status = "interrupted"
			r.Message = "服务重启，任务已中断；核对任务记录后可重试"
			changed = append(changed, r.ID)
		} else if !loaded.Initialized {
			changed = append(changed, r.ID)
		}
	}
	if err := s.persistSQLiteLockedContext(ctx, changed); err != nil {
		return nil, err
	}
	// Deletion happens strictly after the SQLite transaction committed.
	if err := os.Remove(filepath.Join(dir, "media-organize.enc")); err != nil && !os.IsNotExist(err) && !loaded.Initialized {
		log.Printf("整理记录已保存到 SQLite，旧快照未能清理：%v", err)
	}
	return s, nil
}

func (s *MediaOrganizer) decodeSQLite(rows storage.MediaOrganizeRows) error {
	doc := mediaOrganizeDocument{Rules: []MediaOrganizeRule{}, Runs: make([]MediaOrganizeRun, 0, len(rows.Runs))}
	if rows.Rules != "" {
		if err := json.Unmarshal([]byte(rows.Rules), &doc.Rules); err != nil {
			return fmt.Errorf("解码整理规则: %w", err)
		}
	}
	for _, raw := range rows.Runs {
		var run MediaOrganizeRun
		if err := json.Unmarshal([]byte(raw), &run); err != nil {
			return fmt.Errorf("解码整理批次: %w", err)
		}
		doc.Runs = append(doc.Runs, run)
	}
	s.doc = doc
	return nil
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
	if s.persistErr != nil {
		return s.persistErr
	}
	raw, err := json.Marshal(s.doc.Rules)
	if err != nil {
		return err
	}
	updates := make([]storage.MediaOrganizeRunRow, 0, len(changed))
	removed := []string{}
	for _, id := range changed {
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
		}
		if readErr != nil {
			s.persistErr = fmt.Errorf("保存整理记录失败且无法恢复内存，请重启服务: %w；恢复失败: %v", err, readErr)
			return s.persistErr
		}
		return fmt.Errorf("保存整理记录: %w", err)
	}
	return nil
}

func mediaRunRow(run MediaOrganizeRun) (storage.MediaOrganizeRunRow, error) {
	raw, err := json.Marshal(run)
	if err != nil {
		return storage.MediaOrganizeRunRow{}, err
	}
	row := storage.MediaOrganizeRunRow{ID: run.ID, RuleID: run.Rule.ID, Status: run.Status, At: mediaSortTime(run.UpdatedAt), Record: string(raw), Tasks: []storage.MediaOrganizeTaskRow{}}
	for _, task := range mediaTasks(run) {
		item := task.MediaOrganizeHistoryItem
		raw, err := json.Marshal(item)
		if err != nil {
			return row, err
		}
		row.Tasks = append(row.Tasks, storage.MediaOrganizeTaskRow{FileID: item.FileID, Group: mediaStatusGroup(item.Status), At: mediaSortTime(item.At), Search: strings.ToLower(strings.Join([]string{item.Title, item.Name, item.Source, item.Target}, "\n")), Record: string(raw)})
	}
	if len(row.Tasks) == 0 {
		activity := MediaOrganizeActivity{RunID: run.ID, RuleName: run.Rule.Name, Status: run.Status, Message: run.Message, At: run.UpdatedAt}
		raw, err := json.Marshal(activity)
		if err != nil {
			return row, err
		}
		row.Activity = string(raw)
	}
	cleanup := mediaRunCleanup(run)
	if cleanup.Files+cleanup.Directories+cleanup.Failed > 0 {
		raw, err := json.Marshal(cleanup)
		if err != nil {
			return row, err
		}
		row.Cleanup = string(raw)
	}
	return row, nil
}
