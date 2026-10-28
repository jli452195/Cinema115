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
	RunID     string `json:"runId"`
	FileID    string `json:"fileId"`
	Name      string `json:"name"`
	Title     string `json:"title"`
	Kind      string `json:"kind"`
	MediaType string `json:"mediaType"`
	Directory bool   `json:"directory,omitempty"`
	Season    int    `json:"season"`
	Episode   int    `json:"episode"`
	Source    string `json:"source"`
	Target    string `json:"target,omitempty"`
	MediaOrganizeResult
}

type MediaOrganizeHistory struct {
	Items []MediaOrganizeHistoryItem `json:"items"`
	Page  int                        `json:"page"`
	Pages int                        `json:"pages"`
	Total int                        `json:"total"`
}

type MediaOrganizeState struct {
	Rules   []MediaOrganizeRule  `json:"rules"`
	Active  bool                 `json:"active"`
	History MediaOrganizeHistory `json:"history"`
}

// State projects file results from the existing snapshots. No metadata or drive
// requests are needed, and only the requested history page is sent to clients.
func (s *MediaOrganizer) State(page int) MediaOrganizeState {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := MediaOrganizeState{Rules: append([]MediaOrganizeRule{}, s.doc.Rules...), Active: s.active != "" || len(s.actions) > 0}
	type recorded struct {
		run    *MediaOrganizeRun
		entry  *MediaOrganizeEntry
		result MediaOrganizeResult
	}
	records := make([]recorded, 0)
	for i := range s.doc.Runs {
		r := &s.doc.Runs[i]
		for j := range r.Entries {
			e := &r.Entries[j]
			if e.Result != nil {
				records = append(records, recorded{r, e, *e.Result})
			} else if e.Status == "done" {
				records = append(records, recorded{r, e, MediaOrganizeResult{Status: "done", At: r.UpdatedAt}})
			}
		}
	}
	sort.SliceStable(records, func(i, j int) bool { return records[i].result.At.After(records[j].result.At) })
	const pageSize = 20
	pages := max(1, (len(records)+pageSize-1)/pageSize)
	page = max(1, min(page, pages))
	history := MediaOrganizeHistory{Items: []MediaOrganizeHistoryItem{}, Page: page, Pages: pages, Total: len(records)}
	start := (page - 1) * pageSize
	for _, row := range records[start:min(start+pageSize, len(records))] {
		e, r := row.entry, row.run
		source, target := r.Rule.Source, e.Target
		if row.result.Target != "" {
			target = row.result.Target
		}
		if r.Rule.Storage == "local" {
			source, target = filepath.ToSlash(source), filepath.ToSlash(target)
		}
		item := MediaOrganizeHistoryItem{RunID: r.ID, FileID: e.File.ID, Name: e.File.Name, MediaType: e.File.MediaType, Directory: e.File.Directory, Season: e.File.Season, Episode: e.File.Episode, Source: path.Join(source, e.File.SourcePath), Target: target, MediaOrganizeResult: row.result}
		for _, g := range r.Groups {
			if g.ID == e.Group {
				item.Title, item.Kind = g.Title, g.Kind
				break
			}
		}
		history.Items = append(history.Items, item)
	}
	state.History = history
	return state
}
