package service

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"strm115/backend/internal/storage"
)

// The former encrypted store survives only as an inexpensive fault-injection
// fixture. Production has a single SQLite repository and query implementation.
type testMediaOrganizeStore struct {
	source accountSecretStore
	owner  *MediaOrganizer
}

func NewMediaOrganizer(store accountSecretStore, metadata MediaRecognitionSource, templates NamingTemplates, classification ClassificationRules, accounts *P115Accounts, drive OrganizeDrive, reserve func() (func(), error)) (*MediaOrganizer, error) {
	repo := &testMediaOrganizeStore{source: store}
	s := &MediaOrganizer{db: repo, metadata: metadata, templates: templates, classification: classification, accounts: accounts, drive: drive, reserve: reserve, actions: map[string]string{}, wake: make(chan struct{}, 1)}
	repo.owner = s
	rows, err := repo.LoadMediaOrganize(context.Background())
	if err != nil {
		return nil, err
	}
	if err := s.decodeSQLite(rows); err != nil {
		return nil, err
	}
	changed := []string{}
	for i := range s.doc.Runs {
		r := &s.doc.Runs[i]
		if mediaRunActive(r.Status) {
			r.Status = "interrupted"
			r.Message = "服务重启，任务已中断；核对任务记录后可重试"
		}
		changed = append(changed, r.ID)
	}
	return s, s.persistLocked(changed...)
}
func (s *testMediaOrganizeStore) LoadMediaOrganize(context.Context) (storage.MediaOrganizeRows, error) {
	out := storage.MediaOrganizeRows{Rules: "[]", Runs: []string{}, Initialized: s.source.Value() != ""}
	var doc mediaOrganizeDocument
	if out.Initialized {
		if err := json.Unmarshal([]byte(s.source.Value()), &doc); err != nil {
			return out, err
		}
	}
	raw, err := json.Marshal(doc.Rules)
	if err != nil {
		return out, err
	}
	out.Rules = string(raw)
	for _, run := range doc.Runs {
		raw, err := json.Marshal(run)
		if err != nil {
			return out, err
		}
		out.Runs = append(out.Runs, string(raw))
	}
	return out, nil
}
func (s *testMediaOrganizeStore) SaveMediaOrganize(_ context.Context, _ string, _ []storage.MediaOrganizeRunRow, _ []string) error {
	raw, err := json.Marshal(s.owner.doc)
	if err != nil {
		return err
	}
	return s.source.Save(string(raw))
}
func (s *testMediaOrganizeStore) QueryMediaOrganize(_ context.Context, q, status string, page int) (storage.MediaOrganizeQuery, error) {
	out := storage.MediaOrganizeQuery{Records: []string{}, Counts: map[string]int{"all": 0, "running": 0, "done": 0, "attention": 0}, Activity: []string{}, Cleanup: []string{}}
	items := []MediaOrganizeHistoryItem{}
	latest := map[string]bool{}
	q = strings.ToLower(q)
	for _, run := range s.owner.doc.Runs {
		tasks := mediaTasks(run)
		if !latest[run.Rule.ID] {
			latest[run.Rule.ID] = true
			if len(tasks) == 0 {
				raw, _ := json.Marshal(MediaOrganizeActivity{RunID: run.ID, RuleName: run.Rule.Name, Status: run.Status, Message: run.Message, At: run.UpdatedAt})
				out.Activity = append(out.Activity, string(raw))
			}
		}
		cleanup := mediaRunCleanup(run)
		if cleanup.Files+cleanup.Directories+cleanup.Failed > 0 {
			raw, _ := json.Marshal(cleanup)
			out.Cleanup = append(out.Cleanup, string(raw))
		}
		for _, task := range tasks {
			item := task.MediaOrganizeHistoryItem
			if !strings.Contains(strings.ToLower(strings.Join([]string{item.Name, item.Title, item.Source, item.Target}, "\n")), q) {
				continue
			}
			group := mediaStatusGroup(item.Status)
			out.Counts["all"]++
			out.Counts[group]++
			if status == "all" || status == group {
				items = append(items, item)
			}
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].At.Equal(items[j].At) {
			if items[i].RunID == items[j].RunID {
				return items[i].FileID < items[j].FileID
			}
			return items[i].RunID < items[j].RunID
		}
		return items[i].At.After(items[j].At)
	})
	out.Total = len(items)
	out.Pages = max(1, (out.Total+19)/20)
	out.Page = max(1, min(page, out.Pages))
	start := (out.Page - 1) * 20
	for _, item := range items[start:min(start+20, len(items))] {
		raw, _ := json.Marshal(item)
		out.Records = append(out.Records, string(raw))
	}
	return out, nil
}

func mediaTestState(s *MediaOrganizer, page int) struct {
	MediaOrganizeState
	History MediaOrganizeHistory
	Cleanup []MediaOrganizeCleanup
} {
	h, _ := s.History(context.Background(), "", "all", page)
	return struct {
		MediaOrganizeState
		History MediaOrganizeHistory
		Cleanup []MediaOrganizeCleanup
	}{s.State(), h, h.Cleanup}
}
