package service

import (
	"maps"
	"path/filepath"
	"slices"
	"strconv"
	"time"
)

type mediaOrphan struct {
	Since    time.Time         `json:"since"`
	FileID   string            `json:"fileId"`
	Identity string            `json:"identity,omitempty"`
	Size     int64             `json:"size"`
	Modified int64             `json:"modified"`
	Hashes   map[uint32]string `json:"hashes,omitempty"`
}

type mediaOrphanState struct {
	Storage            string                 `json:"storage"`
	AccountID          string                 `json:"accountId,omitempty"`
	SourceID           string                 `json:"sourceId,omitempty"`
	TargetID           string                 `json:"targetId,omitempty"`
	ConnectionID       string                 `json:"connectionId,omitempty"`
	ConnectionRevision uint64                 `json:"connectionRevision,omitempty"`
	Source             string                 `json:"source"`
	Target             string                 `json:"target"`
	Files              map[string]mediaOrphan `json:"files"`
}

func mediaOrphanBinding(rule MediaOrganizeRule) mediaOrphanState {
	return mediaOrphanState{Storage: rule.Storage, AccountID: rule.AccountID, SourceID: rule.SourceID, TargetID: rule.TargetID, ConnectionID: rule.ConnectionID, ConnectionRevision: rule.ConnectionRevision, Source: rule.Source, Target: rule.Target}
}
func (state mediaOrphanState) matches(rule MediaOrganizeRule) bool {
	return state.Storage == rule.Storage && state.AccountID == rule.AccountID && state.SourceID == rule.SourceID && state.TargetID == rule.TargetID && state.ConnectionID == rule.ConnectionID && state.ConnectionRevision == rule.ConnectionRevision && state.Source == rule.Source && state.Target == rule.Target
}
func mediaOrphanEqual(a, b mediaOrphan) bool {
	return a.FileID == b.FileID && a.Identity == b.Identity && a.Size == b.Size && a.Modified == b.Modified && maps.Equal(a.Hashes, b.Hashes)
}

// 三种存储共用首次发现时间和文件身份；正常匹配的字幕及附件继续立即整理。
func (s *MediaOrganizer) prepareOrphanCleanup(files *mediaOrganizeFiles, run *MediaOrganizeRun) error {
	now := s.localMonitorSettings().now()
	entries := slices.DeleteFunc(slices.Clone(run.Entries), func(entry MediaOrganizeEntry) bool {
		return entry.Reference || !mediaInScope(entry.File.SourcePath, files.cleanupScope)
	})
	indices := make(map[string]int, len(entries))
	for i, entry := range run.Entries {
		indices[entry.File.ID] = i
	}
	// 本地尚未稳定的附件也从首次发现开始计时，但只能在后续稳定扫描中进入删除候选。
	if run.Rule.Storage == "local" {
		for _, candidate := range files.localCandidates {
			if candidate.MediaType != "subtitle" && candidate.MediaType != "attachment" {
				continue
			}
			if _, exists := indices[candidate.ID]; exists {
				continue
			}
			if info := files.localSnapshots[candidate.ID]; info != nil {
				entries = append(entries, MediaOrganizeEntry{File: candidate, Modified: info.ModTime().UnixNano(), Status: "retained"})
			}
		}
	}
	for i := range entries {
		entry := &entries[i]
		if entry.VideoID == "" || entry.P115Move != nil {
			continue
		}
		if slices.ContainsFunc(run.Entries, func(video MediaOrganizeEntry) bool {
			return video.File.ID == entry.VideoID && (video.Status == "done" || video.Failure != nil && video.Failure.State == "stored")
		}) {
			entry.VideoID, entry.Group = "", ""
		}
	}
	hasOrphans := slices.ContainsFunc(entries, func(entry MediaOrganizeEntry) bool {
		return (entry.File.MediaType == "subtitle" || entry.File.MediaType == "attachment") && entry.Status != "done"
	})
	s.mu.Lock()
	previous, existed := s.doc.Orphans[run.Rule.ID]
	if !previous.matches(run.Rule) {
		previous.Files = nil
	}
	previous.Files = maps.Clone(previous.Files)
	s.mu.Unlock()
	var owners map[string][]string
	if hasOrphans {
		candidates := files.localCandidates
		if files.localCandidates == nil {
			candidates = mediaEntryFiles(files.scanned)
		}
		owners = mediaLocalCompanionOwners(candidates)
	}
	state := mediaOrphanBinding(run.Rule)
	state.Files = make(map[string]mediaOrphan)
	if files.partialCleanup {
		state.Files = maps.Clone(previous.Files)
		if state.Files == nil {
			state.Files = make(map[string]mediaOrphan)
		}
	}
	files.orphanDue = make(map[string]bool)
	for i := range entries {
		entry := &entries[i]
		if entry.File.MediaType != "subtitle" && entry.File.MediaType != "attachment" || entry.Status == "done" || entry.Failure != nil && entry.Failure.State == "stored" {
			delete(state.Files, entry.File.ID)
			continue
		}
		if entry.VideoID != "" {
			continue
		}
		observation := mediaOrphan{Since: now, FileID: entry.File.ID, Size: entry.File.Size, Modified: entry.Modified, Hashes: maps.Clone(entry.CD2Hashes)}
		if run.Rule.Storage == "cd2" {
			observation.FileID = entry.CD2FileID
		} else if run.Rule.Storage == "115" && entry.File.SHA1 != "" {
			observation.Hashes = map[uint32]string{2: entry.File.SHA1}
		} else if run.Rule.Storage == "local" {
			info := files.localSnapshots[entry.File.ID]
			if info == nil {
				continue
			}
			identity := files.localSnapshotKeys[entry.File.ID]
			if identity == "" {
				var err error
				identity, err = mediaLocalFileIdentityKey(filepath.Join(run.Rule.Source, filepath.FromSlash(entry.File.SourcePath)), info)
				if err != nil {
					continue
				}
			}
			observation.Identity = identity
		}
		if old, ok := previous.Files[entry.File.ID]; ok && !old.Since.IsZero() && mediaOrphanEqual(old, observation) {
			observation.Since = old.Since
		}
		state.Files[entry.File.ID] = observation
		entry.Status, entry.Message = "retained", "未匹配对应视频，按附件宽限 "+strconv.Itoa(run.Rule.AttachmentGraceSeconds)+" 秒等待后续核对"
		if !run.Rule.AttachmentCleanupEnabled {
			entry.Message = "未匹配对应视频，未匹配附件清理已关闭，保留来源"
		}
		index, present := indices[entry.File.ID]
		if present {
			run.Entries[index] = *entry
		}
		protected := !present || len(owners[entry.File.ID]) > 0 || files.settlingVideos[entry.File.ParentID] || files.localPending[entry.File.ID]
		if run.Rule.AttachmentCleanupEnabled && !protected && (files.sourceVacated || now.Sub(observation.Since) >= time.Duration(run.Rule.AttachmentGraceSeconds)*time.Second) {
			files.orphanDue[entry.File.ID] = true
			candidate := *entry
			candidate.Group, candidate.VideoID, candidate.Relative, candidate.Target = "", "", "", ""
			files.unrelated = append(files.unrelated, candidate)
		}
	}
	run.Entries = slices.DeleteFunc(run.Entries, func(entry MediaOrganizeEntry) bool { return files.orphanDue[entry.File.ID] })
	unchanged := previous.matches(run.Rule) && maps.EqualFunc(previous.Files, state.Files, func(a, b mediaOrphan) bool { return a.Since.Equal(b.Since) && mediaOrphanEqual(a, b) })
	s.scheduleOrphanScopes(run.Rule, entries, state.Files, files.orphanDue)
	if unchanged || !existed && len(state.Files) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// 执行中的旧快照不能覆盖编辑后的存储绑定。
	if !slices.ContainsFunc(s.doc.Rules, func(rule MediaOrganizeRule) bool { return rule.ID == run.Rule.ID && state.matches(rule) }) {
		return nil
	}
	if s.doc.Orphans == nil {
		s.doc.Orphans = make(map[string]mediaOrphanState)
	}
	if len(state.Files) == 0 {
		delete(s.doc.Orphans, run.Rule.ID)
	} else {
		s.doc.Orphans[run.Rule.ID] = state
	}
	return s.persistLocked()
}

// 成功删除后只清除相同绑定的计时，旧批次不得修改新规则的账本。
func (s *MediaOrganizer) forgetOrphanLocked(rule MediaOrganizeRule, fileID string) {
	state := s.doc.Orphans[rule.ID]
	if !state.matches(rule) {
		return
	}
	delete(state.Files, fileID)
	if len(state.Files) == 0 {
		delete(s.doc.Orphans, rule.ID)
	}
}
