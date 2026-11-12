package service

import (
	"slices"
	"sync"
)

func mediaEntryOwner(entry MediaOrganizeEntry) string {
	if entry.File.MediaType == "video" {
		return entry.File.ID
	}
	return entry.VideoID
}

func mediaVideoIDs(run MediaOrganizeRun) map[string]bool {
	ids := make(map[string]bool)
	for _, entry := range run.Entries {
		if entry.File.MediaType == "video" {
			ids[entry.File.ID] = true
		}
	}
	return ids
}

// 只保护实际进行文件操作的主文件；扫描、识别和旧回执不占用执行保护。
func (s *MediaOrganizer) beginMediaEntries(run *MediaOrganizeRun, indices []int) ([]int, func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var current map[string]bool
	for _, stored := range s.doc.Runs {
		if stored.ID == run.ID {
			current = mediaVideoIDs(stored)
			break
		}
	}
	selected := make([]int, 0, len(indices))
	owners := make(map[string]bool)
	for _, index := range indices {
		if index < 0 || index >= len(run.Entries) {
			continue
		}
		owner := mediaEntryOwner(run.Entries[index])
		if run.seenTasks[owner] && !current[owner] {
			continue
		}
		selected = append(selected, index)
		if owner != "" {
			owners[owner] = true
		}
	}
	if len(owners) == 0 {
		return selected, func() {}
	}
	if s.inFlight == nil {
		s.inFlight = make(map[string]map[string]int)
	}
	if s.inFlight[run.ID] == nil {
		s.inFlight[run.ID] = make(map[string]int)
	}
	for owner := range owners {
		s.inFlight[run.ID][owner]++
	}
	s.historyChangedLocked()
	var once sync.Once
	return selected, func() {
		once.Do(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			for owner := range owners {
				s.inFlight[run.ID][owner]--
				if s.inFlight[run.ID][owner] <= 0 {
					delete(s.inFlight[run.ID], owner)
				}
			}
			if len(s.inFlight[run.ID]) == 0 {
				delete(s.inFlight, run.ID)
			}
			s.historyChangedLocked()
		})
	}
}

// 检查点只过滤发布副本，工作副本保持索引稳定，防止批量计划错位。
func mediaPublishRun(worker *MediaOrganizeRun, current MediaOrganizeRun) MediaOrganizeRun {
	present := mediaVideoIDs(current)
	deleted := make(map[string]bool)
	for id := range worker.seenTasks {
		if !present[id] {
			deleted[id] = true
		}
	}
	var published MediaOrganizeRun
	cloneMediaRun(*worker, &published)
	if deleted[published.TaskFileID] {
		published.TaskFileID = ""
	}
	published.TaskFileIDs = slices.DeleteFunc(published.TaskFileIDs, func(id string) bool { return deleted[id] })
	published.Entries = slices.DeleteFunc(published.Entries, func(entry MediaOrganizeEntry) bool {
		return deleted[mediaEntryOwner(entry)]
	})
	used := make(map[string]bool)
	for _, entry := range published.Entries {
		used[entry.Group] = true
	}
	published.Groups = slices.DeleteFunc(published.Groups, func(group MediaOrganizeGroup) bool { return !used[group.ID] })
	return published
}
