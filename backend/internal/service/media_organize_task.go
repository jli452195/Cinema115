package service

import (
	"fmt"
	"slices"
)

type MediaOrganizeCorrection struct {
	FileID  string `json:"fileId"`
	Kind    string `json:"kind,omitempty"`
	TMDBID  int    `json:"tmdbId,omitempty"`
	Season  *int   `json:"season,omitempty"`
	Episode *int   `json:"episode,omitempty"`
}

// Retry changes only one media task; the selection survives checkpoints/restarts.
func (s *MediaOrganizer) Retry(id string, input MediaOrganizeCorrection) (MediaOrganizeTask, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if input.FileID == "" || input.TMDBID < 0 || input.TMDBID > 0 && input.Kind != "movie" && input.Kind != "tv" ||
		input.Season != nil && (*input.Season < 0 || *input.Season > 1000) ||
		input.Episode != nil && (*input.Episode < 0 || *input.Episode > 10000) {
		return MediaOrganizeTask{}, ErrMediaOrganizeInput
	}
	if s.active != "" || len(s.actions) > 0 {
		return MediaOrganizeTask{}, ErrMediaOrganizeBusy
	}
	for i := range s.doc.Runs {
		run := &s.doc.Runs[i]
		if run.ID != id {
			continue
		}
		pos := slices.IndexFunc(run.Entries, func(e MediaOrganizeEntry) bool { return e.File.MediaType == "video" && e.File.ID == input.FileID })
		if pos < 0 {
			return MediaOrganizeTask{}, ErrMediaOrganizeInput
		}
		video := &run.Entries[pos]
		unfinished := false
		for _, e := range run.Entries {
			if mediaInTask(e, input.FileID) && e.Status != "done" {
				unfinished = true
			}
		}
		if !unfinished {
			return MediaOrganizeTask{}, fmt.Errorf("%w：当前任务已完成", ErrMediaOrganizeInput)
		}
		if video.Status == "done" && (input.TMDBID > 0 || input.Season != nil || input.Episode != nil) {
			return MediaOrganizeTask{}, fmt.Errorf("%w：已完成的主文件不能修改，仅可重试失败附件", ErrMediaOrganizeInput)
		}
		if err := run.prepareClassification(); err != nil {
			return MediaOrganizeTask{}, err
		}
		if video.Status != "done" {
			old := slices.IndexFunc(run.Groups, func(g MediaOrganizeGroup) bool { return g.ID == video.Group })
			if old < 0 {
				return MediaOrganizeTask{}, ErrMediaOrganizeInput
			}
			group := run.Groups[old]
			group.ID = "task:" + input.FileID
			if input.TMDBID > 0 {
				group.Kind, group.TMDBID = input.Kind, input.TMDBID
				group.Title, group.Message = "", ""
			}
			gi := slices.IndexFunc(run.Groups, func(g MediaOrganizeGroup) bool { return g.ID == group.ID })
			if gi < 0 {
				run.Groups = append(run.Groups, group)
			} else {
				run.Groups[gi] = group
			}
			for j := range run.Entries {
				if mediaInTask(run.Entries[j], input.FileID) {
					run.Entries[j].Group = group.ID
				}
			}
		}
		if input.Season != nil {
			video.File.Season = *input.Season
		}
		if input.Episode != nil {
			video.File.Episode = *input.Episode
		}
		run.TaskFileID, run.Status = input.FileID, "queued"
		run.Message = "等待重新整理当前任务"
		if err := s.persistLocked(); err != nil {
			return MediaOrganizeTask{}, err
		}
		s.actions[id] = "retry"
		s.notify()
		var snapshot MediaOrganizeRun
		cloneMediaRun(*run, &snapshot)
		for _, task := range mediaTasks(snapshot) {
			if task.FileID == input.FileID {
				return task, nil
			}
		}
	}
	return MediaOrganizeTask{}, ErrMediaOrganizeInput
}
