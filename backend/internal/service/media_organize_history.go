package service

import (
	"path"
	"path/filepath"
	"sort"
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
	MediaOrganizeResult
}
type MediaOrganizeHistory struct {
	Items []MediaOrganizeHistoryItem `json:"items"`
	Page  int                        `json:"page"`
	Pages int                        `json:"pages"`
	Total int                        `json:"total"`
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
	Rules       []MediaOrganizeRule    `json:"rules"`
	Active      bool                   `json:"active"`
	ActiveRules []string               `json:"activeRules"`
	History     MediaOrganizeHistory   `json:"history"`
	Cleanup     []MediaOrganizeCleanup `json:"cleanup"`
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
		case mediaRunActive(run.Status) && (run.TaskFileID == "" || run.TaskFileID == e.File.ID):
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
		item := MediaOrganizeHistoryItem{RunID: run.ID, FileID: e.File.ID, Name: e.File.Name, Title: g.Title, Kind: g.Kind, MediaType: e.File.MediaType, Season: e.File.Season, Episode: e.File.Episode, Source: path.Join(source, e.File.SourcePath), AttachmentCount: len(entries) - 1, MediaOrganizeResult: result}
		tasks = append(tasks, MediaOrganizeTask{MediaOrganizeHistoryItem: item, Rule: run.Rule, Group: g, Entries: entries})
	}
	return tasks
}

func (s *MediaOrganizer) Task(id, fileID string) (MediaOrganizeTask, error) {
	run, err := s.Snapshot(id)
	if err != nil {
		return MediaOrganizeTask{}, err
	}
	for _, task := range mediaTasks(run) {
		if task.FileID == fileID {
			return task, nil
		}
	}
	return MediaOrganizeTask{}, ErrMediaOrganizeInput
}

func (s *MediaOrganizer) State(page int) MediaOrganizeState {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := MediaOrganizeState{Rules: append([]MediaOrganizeRule{}, s.doc.Rules...), Active: s.active != "" || len(s.actions) > 0, ActiveRules: []string{}, Cleanup: []MediaOrganizeCleanup{}}
	records := make([]MediaOrganizeHistoryItem, 0)
	for _, run := range s.doc.Runs {
		if mediaRunActive(run.Status) {
			state.ActiveRules = append(state.ActiveRules, run.Rule.ID)
		}
		for _, task := range mediaTasks(run) {
			records = append(records, task.MediaOrganizeHistoryItem)
		}
		cleanup := MediaOrganizeCleanup{RunID: run.ID, RuleName: run.Rule.Name, At: run.UpdatedAt}
		for _, e := range run.Entries {
			if e.File.MediaType == "video" || e.VideoID != "" || e.Result == nil || e.Target != "" || e.Failure != nil {
				continue
			}
			if e.Status != "done" {
				cleanup.Failed++
			} else if e.File.Directory {
				cleanup.Directories++
			} else {
				cleanup.Files++
			}
		}
		if cleanup.Files+cleanup.Directories+cleanup.Failed > 0 {
			state.Cleanup = append(state.Cleanup, cleanup)
		}
	}
	sort.SliceStable(records, func(i, j int) bool { return records[i].At.After(records[j].At) })
	const pageSize = 20
	pages := max(1, (len(records)+pageSize-1)/pageSize)
	page = max(1, min(page, pages))
	start := (page - 1) * pageSize
	state.History = MediaOrganizeHistory{Items: records[start:min(start+pageSize, len(records))], Page: page, Pages: pages, Total: len(records)}
	return state
}
