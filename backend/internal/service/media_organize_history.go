package service

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

type MediaOrganizeResult struct {
	Status  string    `json:"status"`
	Message string    `json:"message,omitempty"`
	At      time.Time `json:"at"`
	Target  string    `json:"target,omitempty"`
}
type MediaOrganizeHistoryItem struct {
	RunID           string `json:"runId"`
	FileID          string `json:"fileId"`
	Name            string `json:"name"`
	Title           string `json:"title"`
	Kind            string `json:"kind"`
	MediaType       string `json:"mediaType"`
	Season          int    `json:"season"`
	Episode         int    `json:"episode"`
	Source          string `json:"source"`
	AttachmentCount int    `json:"attachmentCount"`
	CanDelete       bool   `json:"canDelete"`
	MediaOrganizeResult
}
type MediaOrganizeHistory struct {
	Items    []MediaOrganizeHistoryItem `json:"items"`
	Page     int                        `json:"page"`
	Pages    int                        `json:"pages"`
	Total    int                        `json:"total"`
	Counts   map[string]int             `json:"counts"`
	Active   bool                       `json:"active"`
	Activity []MediaOrganizeActivity    `json:"activity"`
	Cleanup  []MediaOrganizeCleanup     `json:"cleanup"`
}
type MediaOrganizeCleanup struct {
	RunID       string    `json:"runId"`
	RuleName    string    `json:"ruleName"`
	Files       int       `json:"files"`
	Directories int       `json:"directories"`
	Failed      int       `json:"failed"`
	At          time.Time `json:"at"`
}
type MediaOrganizeState struct {
	Rules       []MediaOrganizeRule `json:"rules"`
	Active      bool                `json:"active"`
	ActiveRules []string            `json:"activeRules"`
}
type MediaOrganizeActivity struct {
	RunID    string    `json:"runId"`
	RuleName string    `json:"ruleName"`
	Status   string    `json:"status"`
	Message  string    `json:"message,omitempty"`
	At       time.Time `json:"at"`
}
type MediaOrganizeDeleteItem struct {
	RunID  string `json:"runId"`
	FileID string `json:"fileId"`
}
type MediaOrganizeDelete struct {
	Items     []MediaOrganizeDeleteItem `json:"items"`
	Completed bool                      `json:"completed"`
}

func mediaStatusGroup(status string) string {
	if status == "done" {
		return "done"
	}
	if mediaRunActive(status) {
		return "running"
	}
	return "attention"
}

type MediaOrganizeTask struct {
	MediaOrganizeHistoryItem
	Rule    MediaOrganizeRule    `json:"rule"`
	Group   MediaOrganizeGroup   `json:"group"`
	Entries []MediaOrganizeEntry `json:"entries"`
}

func mediaInTask(e MediaOrganizeEntry, fileID string) bool {
	return fileID == "" || e.File.ID == fileID || e.VideoID == fileID
}
func mediaRunActive(status string) bool {
	return status == "queued" || status == "scanning" || status == "running"
}

// Group saved results in one pass; viewing tasks never reads the drive or TMDB.
func mediaTasks(run MediaOrganizeRun) []MediaOrganizeTask {
	groups := make(map[string]MediaOrganizeGroup, len(run.Groups))
	for _, g := range run.Groups {
		groups[g.ID] = g
	}
	companions := make(map[string][]MediaOrganizeEntry, len(run.Entries))
	for _, e := range run.Entries {
		if e.VideoID != "" && e.File.MediaType != "cleanup" {
			companions[e.VideoID] = append(companions[e.VideoID], e)
		}
	}
	tasks := make([]MediaOrganizeTask, 0, len(run.Entries))
	for _, e := range run.Entries {
		if e.File.MediaType != "video" {
			continue
		}
		g := groups[e.Group]
		entries := append([]MediaOrganizeEntry{e}, companions[e.File.ID]...)
		result := MediaOrganizeResult{Status: "done", At: run.UpdatedAt, Target: e.Target}
		if e.Result != nil {
			result = *e.Result
		}
		allDone := true
		for _, entry := range entries {
			if entry.Result != nil && entry.Result.At.After(result.At) {
				result.At = entry.Result.At
			}
			if entry.Status != "done" {
				allDone = false
				if result.Message == "" && entry.Message != "" {
					result.Message = entry.File.Name + "：" + entry.Message
				}
			}
		}
		switch {
		case allDone:
			result.Status, result.Message = "done", ""
		case mediaRunActive(run.Status) && (run.TaskFileID == e.File.ID || run.TaskFileID == "" && (e.Status == "pending" || e.Status == "ready" || e.Status == "done")):
			result.Status = run.Status
		case e.Status == "done":
			result.Status = "partial"
		default:
			result.Status = "error"
			if g.Message != "" {
				result.Message = g.Message
			}
		}
		if result.Status != "done" && result.Message == "" {
			result.Message = run.Message
		}
		source := run.Rule.Source
		if run.Rule.Storage == "local" {
			source = filepath.ToSlash(source)
			result.Target = filepath.ToSlash(result.Target)
		}
		item := MediaOrganizeHistoryItem{RunID: run.ID, FileID: e.File.ID, Name: e.File.Name, Title: g.Title, Kind: g.Kind, MediaType: e.File.MediaType, Season: e.File.Season, Episode: e.File.Episode, Source: path.Join(source, e.File.SourcePath), AttachmentCount: len(entries) - 1, CanDelete: !mediaRunActive(run.Status), MediaOrganizeResult: result}
		tasks = append(tasks, MediaOrganizeTask{MediaOrganizeHistoryItem: item, Rule: run.Rule, Group: g, Entries: entries})
	}
	return tasks
}

func (s *MediaOrganizer) busyRunLocked(id string) bool { return s.active == id || s.actions[id] != "" }

func (s *MediaOrganizer) Task(id, fileID string) (MediaOrganizeTask, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, run := range s.doc.Runs {
		if run.ID != id {
			continue
		}
		var snapshot MediaOrganizeRun
		cloneMediaRun(run, &snapshot)
		for _, task := range mediaTasks(snapshot) {
			if task.FileID == fileID {
				task.CanDelete = task.CanDelete && !s.busyRunLocked(id)
				return task, nil
			}
		}
	}
	return MediaOrganizeTask{}, ErrMediaOrganizeInput
}

func (s *MediaOrganizer) State() MediaOrganizeState {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := MediaOrganizeState{Rules: append([]MediaOrganizeRule{}, s.doc.Rules...), Active: s.active != "" || len(s.actions) > 0, ActiveRules: []string{}}
	for _, run := range s.doc.Runs {
		if mediaRunActive(run.Status) || s.busyRunLocked(run.ID) {
			state.ActiveRules = append(state.ActiveRules, run.Rule.ID)
		}
	}
	return state
}

func mediaRunCleanup(run MediaOrganizeRun) MediaOrganizeCleanup {
	out := MediaOrganizeCleanup{RunID: run.ID, RuleName: run.Rule.Name, At: run.UpdatedAt}
	for _, e := range run.Entries {
		if e.File.MediaType == "video" || e.VideoID != "" || e.Result == nil || e.Target != "" || e.Failure != nil {
			continue
		}
		if e.Status != "done" {
			out.Failed++
		} else if e.File.Directory {
			out.Directories++
		} else {
			out.Files++
		}
	}
	return out
}

func (s *MediaOrganizer) History(ctx context.Context, query, status string, page int) (MediaOrganizeHistory, error) {
	if status == "" {
		status = "all"
	}
	if !slices.Contains([]string{"all", "running", "done", "attention"}, status) || len(query) > 500 {
		return MediaOrganizeHistory{}, ErrMediaOrganizeInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := MediaOrganizeHistory{Items: []MediaOrganizeHistoryItem{}, Counts: map[string]int{"all": 0, "running": 0, "done": 0, "attention": 0}, Activity: []MediaOrganizeActivity{}, Cleanup: []MediaOrganizeCleanup{}, Active: s.active != "" || len(s.actions) > 0}
	rows, err := s.db.QueryMediaOrganize(ctx, strings.TrimSpace(query), status, page)
	if err != nil {
		return out, fmt.Errorf("查询整理记录: %w", err)
	}
	out.Page, out.Pages, out.Total, out.Counts = rows.Page, rows.Pages, rows.Total, rows.Counts
	for _, raw := range rows.Records {
		var item MediaOrganizeHistoryItem
		if err := json.Unmarshal([]byte(raw), &item); err != nil {
			return out, err
		}
		item.CanDelete = item.CanDelete && !s.busyRunLocked(item.RunID)
		out.Items = append(out.Items, item)
	}
	for _, raw := range rows.Activity {
		var item MediaOrganizeActivity
		if err := json.Unmarshal([]byte(raw), &item); err != nil {
			return out, err
		}
		out.Activity = append(out.Activity, item)
	}
	for _, raw := range rows.Cleanup {
		var item MediaOrganizeCleanup
		if err := json.Unmarshal([]byte(raw), &item); err != nil {
			return out, err
		}
		out.Cleanup = append(out.Cleanup, item)
	}
	return out, nil
}

// Deletion and checkpoints share the organizer mutex. A running batch is never
// modified, including already-successful children, so workers cannot revive it.
func (s *MediaOrganizer) DeleteHistory(ctx context.Context, input MediaOrganizeDelete) (int, error) {
	if input.Completed == (len(input.Items) > 0) || len(input.Items) > 20 {
		return 0, ErrMediaOrganizeInput
	}
	selected := make(map[string]map[string]bool)
	for _, item := range input.Items {
		if item.RunID == "" || item.FileID == "" {
			return 0, ErrMediaOrganizeInput
		}
		if selected[item.RunID] == nil {
			selected[item.RunID] = map[string]bool{}
		}
		selected[item.RunID][item.FileID] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	// Validate the complete request before mutating any batch.
	for _, run := range s.doc.Runs {
		if selected[run.ID] != nil && (mediaRunActive(run.Status) || s.busyRunLocked(run.ID)) {
			for _, entry := range run.Entries {
				if entry.File.MediaType == "video" && selected[run.ID][entry.File.ID] {
					return 0, ErrMediaOrganizeBusy
				}
			}
		}
	}
	deleted := 0
	changed := []string{}
	runs := make([]MediaOrganizeRun, 0, len(s.doc.Runs))
	for _, original := range s.doc.Runs {
		ids := selected[original.ID]
		if input.Completed {
			if mediaRunActive(original.Status) || s.busyRunLocked(original.ID) {
				runs = append(runs, original)
				continue
			}
			ids = map[string]bool{}
			for _, task := range mediaTasks(original) {
				if task.Status == "done" {
					ids[task.FileID] = true
				}
			}
		}
		if len(ids) == 0 {
			runs = append(runs, original)
			continue
		}
		valid := map[string]bool{}
		for _, entry := range original.Entries {
			if entry.File.MediaType == "video" && ids[entry.File.ID] {
				valid[entry.File.ID] = true
			}
		}
		if len(valid) == 0 {
			runs = append(runs, original)
			continue
		}
		var run MediaOrganizeRun
		cloneMediaRun(original, &run)
		run.Entries = slices.DeleteFunc(run.Entries, func(e MediaOrganizeEntry) bool { return valid[e.File.ID] || valid[e.VideoID] })
		used := map[string]bool{}
		hasVideo := false
		for _, entry := range run.Entries {
			used[entry.Group] = true
			if entry.File.MediaType == "video" {
				hasVideo = true
			}
		}
		run.Groups = slices.DeleteFunc(run.Groups, func(g MediaOrganizeGroup) bool { return !used[g.ID] })
		if valid[run.TaskFileID] {
			run.TaskFileID = ""
		}
		deleted += len(valid)
		changed = append(changed, run.ID)
		if hasVideo {
			mediaRunCounts(&run)
			runs = append(runs, run)
		}
	}
	if deleted == 0 {
		return 0, nil
	}
	s.doc.Runs = runs
	if err := s.persistSQLiteLockedContext(ctx, changed); err != nil {
		return 0, err
	}
	return deleted, nil
}
