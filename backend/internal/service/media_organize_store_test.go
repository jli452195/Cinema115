package service

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"strm115/backend/internal/storage"
)

// 旧快照仅作为轻量故障注入夹具，保存和查询都遵循生产压缩语义。
type testMediaOrganizeStore struct {
	source accountSecretStore
}

func NewMediaOrganizer(store accountSecretStore, metadata MediaRecognitionSource, templates NamingTemplates, classification ClassificationRules, accounts *P115Accounts, drive OrganizeDrive, reserve func() (func(), error)) (*MediaOrganizer, error) {
	repo := &testMediaOrganizeStore{source: store}
	s := &MediaOrganizer{db: repo, metadata: metadata, templates: templates, classification: classification, accounts: accounts, drive: drive, reserve: reserve, actions: map[string]string{}, wake: make(chan struct{}, 1)}
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
	raw, err := json.Marshal(mediaOrganizeSettings{Rules: doc.Rules, Orphans: doc.Orphans})
	if err != nil {
		return out, err
	}
	out.Rules = string(raw)
	for _, run := range doc.Runs {
		raw, err := mediaMarshalPersistentRun(run)
		if err != nil {
			return out, err
		}
		out.Runs = append(out.Runs, string(raw))
	}
	return out, nil
}
func (s *testMediaOrganizeStore) SaveMediaOrganize(ctx context.Context, rules string, updates []storage.MediaOrganizeRunRow, removed []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var doc mediaOrganizeDocument
	if previous := s.source.Value(); previous != "" {
		if err := json.Unmarshal([]byte(previous), &doc); err != nil {
			return err
		}
	}
	settings, err := mediaStoredSettings([]byte(rules))
	if err != nil {
		return err
	}
	doc.Rules, doc.Orphans = settings.Rules, settings.Orphans
	runs := make(map[string]MediaOrganizeRun, len(doc.Runs)+len(updates))
	for _, run := range doc.Runs {
		runs[run.ID] = run
	}
	for _, id := range removed {
		delete(runs, id)
	}
	for _, row := range updates {
		var run MediaOrganizeRun
		if err := json.Unmarshal([]byte(row.Record), &run); err != nil {
			return err
		}
		runs[row.ID] = run
	}
	doc.Runs = make([]MediaOrganizeRun, 0, len(runs))
	for _, run := range runs {
		doc.Runs = append(doc.Runs, run)
	}
	sort.Slice(doc.Runs, func(i, j int) bool {
		if doc.Runs[i].UpdatedAt.Equal(doc.Runs[j].UpdatedAt) {
			return doc.Runs[i].ID < doc.Runs[j].ID
		}
		return doc.Runs[i].UpdatedAt.After(doc.Runs[j].UpdatedAt)
	})
	encoded := make([]json.RawMessage, 0, len(doc.Runs))
	for _, run := range doc.Runs {
		raw, err := mediaMarshalPersistentRun(run)
		if err != nil {
			return err
		}
		encoded = append(encoded, raw)
	}
	raw, err := json.Marshal(struct {
		Rules   []MediaOrganizeRule         `json:"rules"`
		Runs    []json.RawMessage           `json:"runs"`
		Orphans map[string]mediaOrphanState `json:"orphans,omitempty"`
	}{Rules: doc.Rules, Runs: encoded, Orphans: doc.Orphans})
	if err != nil {
		return err
	}
	return s.source.Save(string(raw))
}
func (s *testMediaOrganizeStore) QueryMediaOrganize(ctx context.Context, q, status string, page int) (storage.MediaOrganizeQuery, error) {
	return s.QueryMediaOrganizeLive(ctx, q, status, page, nil)
}

func (s *testMediaOrganizeStore) QueryMediaOrganizeLive(ctx context.Context, q, status string, page int, runtime []storage.MediaOrganizeLiveTaskRow) (storage.MediaOrganizeQuery, error) {
	out := storage.MediaOrganizeQuery{Records: []string{}, Counts: map[string]int{"all": 0, "running": 0, "done": 0, "failed": 0}, Activity: []string{}, Cleanup: []string{}}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	var doc mediaOrganizeDocument
	if previous := s.source.Value(); previous != "" {
		if err := json.Unmarshal([]byte(previous), &doc); err != nil {
			return out, err
		}
	}
	items := make([]storage.MediaOrganizeLiveTaskRow, 0)
	records := make(map[string]storage.MediaOrganizeLiveTaskRow)
	latest := map[string]bool{}
	q = strings.ToLower(q)
	for _, run := range doc.Runs {
		row, err := mediaRunRow(run)
		if err != nil {
			return out, err
		}
		if !latest[run.Rule.ID] {
			latest[run.Rule.ID] = true
			if row.Activity != "" {
				out.Activity = append(out.Activity, row.Activity)
			}
		}
		if row.Cleanup != "" {
			out.Cleanup = append(out.Cleanup, row.Cleanup)
		}
		for _, task := range row.Tasks {
			records[run.ID+"\x00"+task.FileID] = storage.MediaOrganizeLiveTaskRow{RunID: run.ID, MediaOrganizeTaskRow: task}
		}
	}
	for _, row := range runtime {
		if row.Group == "done" || row.Group == "running" {
			records[row.RunID+"\x00"+row.FileID] = row
		}
	}
	for _, row := range records {
		if !strings.Contains(row.Search, q) {
			continue
		}
		out.Counts["all"]++
		out.Counts[row.Group]++
		if status == "all" || status == row.Group {
			items = append(items, row)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].At == items[j].At {
			if items[i].RunID == items[j].RunID {
				return items[i].FileID < items[j].FileID
			}
			return items[i].RunID < items[j].RunID
		}
		return items[i].At > items[j].At
	})
	out.Total = len(items)
	out.Pages = max(1, (out.Total+19)/20)
	out.Page = max(1, min(page, out.Pages))
	start := (out.Page - 1) * 20
	for _, item := range items[start:min(start+20, len(items))] {
		out.Records = append(out.Records, item.Record)
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
