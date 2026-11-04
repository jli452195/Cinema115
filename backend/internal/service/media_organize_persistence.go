package service

import (
	"encoding/json"
	"slices"
	"strings"
	"time"

	"strm115/backend/internal/mediafacts"
	"strm115/backend/internal/storage"
)

// 完成证据取实际结果或已核实的恢复结果；零时间表示未知，引用及批次时间不能补写。
func mediaPrimaryCompletion(entry MediaOrganizeEntry) (MediaOrganizeResult, bool) {
	if entry.File.MediaType != "video" || entry.Status != "done" || entry.Result == nil || entry.Result.Status != "done" {
		return MediaOrganizeResult{}, false
	}
	return MediaOrganizeResult{Status: "done", At: entry.Result.At, Target: entry.Target, Action: entry.Result.Action}, true
}

// 成功视频保留精简历史和晚到附件映射；失败主文件继续保留重试上下文。
func mediaPersistentRun(run MediaOrganizeRun) MediaOrganizeRun {
	stored := run
	stored.Entries = make([]MediaOrganizeEntry, 0, len(run.Entries))
	stored.Groups = make([]MediaOrganizeGroup, 0, len(run.Groups))
	unfinished, groups := make(map[string]bool), make(map[string]bool)
	for _, entry := range run.Entries {
		if entry.File.MediaType != "video" {
			continue
		}
		completed, hasCompletion := mediaPrimaryCompletion(entry)
		// 晚到附件借用的引用只在本轮使用，原成功记录已保存对应映射。
		if entry.Reference && !hasCompletion {
			continue
		}
		groups[entry.Group] = true
		if entry.Status != "done" {
			unfinished[entry.File.ID] = true
			stored.Entries = append(stored.Entries, entry)
			continue
		}
		index := MediaOrganizeEntry{
			File: entry.File, Modified: entry.Modified, CD2FileID: entry.CD2FileID,
			CD2Hashes: entry.CD2Hashes, CD2Current: entry.CD2Current, CD2Duplicate: entry.CD2Duplicate,
			Reference: true, Group: entry.Group, Relative: entry.Relative, Target: entry.Target, Status: "done",
		}
		index.File.TitleOnly, index.File.PickCode, index.File.NamingValues, index.File.filenameFacts = false, "", nil, nil
		if hasCompletion {
			completed.Target = ""
			index.Result = &completed
		}
		stored.Entries = append(stored.Entries, index)
	}
	unfinishedGroups := make(map[string]bool, len(unfinished))
	for _, entry := range run.Entries {
		if entry.File.MediaType == "video" && unfinished[entry.File.ID] {
			unfinishedGroups[entry.Group] = true
		}
		if entry.File.MediaType != "video" && entry.File.MediaType != "cleanup" && unfinished[entry.VideoID] {
			entry.AttachmentKeep = nil
			stored.Entries = append(stored.Entries, entry)
		}
	}
	for _, group := range run.Groups {
		if !groups[group.ID] {
			continue
		}
		if !unfinishedGroups[group.ID] {
			group.Identity, group.Message, group.ResultCode = mediafacts.Identity{}, "", ""
		}
		stored.Groups = append(stored.Groups, group)
	}
	if len(unfinished) == 0 && !mediaRunActive(run.Status) {
		stored.Classification = ClassificationSnapshot{}
		stored.TaskFileID, stored.RecognitionVersion, stored.Trigger = "", "", ""
		if len(stored.Entries) > 0 || stored.Status != "failed" {
			stored.Status, stored.Message = "completed", ""
		}
	}
	return stored
}

// 成功只写一份完成证据，不重复保存时间和目标。
func mediaMarshalPersistentRun(run MediaOrganizeRun) ([]byte, error) {
	type storedEntry struct {
		MediaOrganizeEntry
		Result    *MediaOrganizeResult `json:"result,omitempty"`
		UpdatedAt *time.Time           `json:"updatedAt,omitempty"`
	}
	entries := make([]storedEntry, 0, len(run.Entries))
	for _, entry := range run.Entries {
		stored := storedEntry{MediaOrganizeEntry: entry, Result: entry.Result}
		// 成功时间只保存于结果中，目标使用条目字段，避免重复写入同一事实。
		if !entry.Reference || entry.Status != "done" {
			at := entry.UpdatedAt
			stored.UpdatedAt = &at
		}
		entries = append(entries, stored)
	}
	type runFields MediaOrganizeRun
	return json.Marshal(struct {
		runFields
		Entries []storedEntry `json:"entries"`
	}{runFields: runFields(run), Entries: entries})
}

// 数据库和运行时视图共用精简摘要，操作权限始终由当前队列计算。
func mediaHistoryTaskRow(item MediaOrganizeHistoryItem) (storage.MediaOrganizeTaskRow, error) {
	raw, err := json.Marshal(struct {
		MediaOrganizeHistoryItem
		CanDelete *bool `json:"canDelete,omitempty"`
		CanRetry  *bool `json:"canRetry,omitempty"`
	}{MediaOrganizeHistoryItem: item})
	if err != nil {
		return storage.MediaOrganizeTaskRow{}, err
	}
	return storage.MediaOrganizeTaskRow{
		FileID: item.FileID, Group: mediaStatusGroup(item.Status), At: mediaSortTime(item.At),
		Search: strings.ToLower(strings.Join([]string{item.Title, item.Name, item.Source, item.Target}, "\n")), Record: string(raw),
	}, nil
}

// 只有同一冻结来源的实际视频再次成功，才清除其旧失败；队列和活动批次始终跳过。
func mediaForgetResolvedFailures(doc *mediaOrganizeDocument, completed MediaOrganizeRun, busy func(string) bool) []string {
	resolved := make(map[string]MediaOrganizeEntry)
	for _, entry := range completed.Entries {
		if entry.File.MediaType == "video" && entry.Status == "done" && !entry.Reference {
			resolved[entry.File.ID] = entry
		}
	}
	changed := make([]string, 0)
	if len(resolved) == 0 {
		return changed
	}
	kept := make([]MediaOrganizeRun, 0, len(doc.Runs))
	for _, old := range doc.Runs {
		if old.ID == completed.ID || old.Rule.ID != completed.Rule.ID || !mediaOrphanBinding(old.Rule).matches(completed.Rule) ||
			mediaRunActive(old.Status) || busy != nil && busy(old.ID) {
			kept = append(kept, old)
			continue
		}
		removed := make(map[string]bool)
		for _, entry := range old.Entries {
			if entry.File.MediaType != "video" || entry.Status == "done" {
				continue
			}
			if current, exists := resolved[entry.File.ID]; exists && mediaResolvedSameSource(entry, current, old.Rule.Storage) {
				removed[entry.File.ID] = true
			}
		}
		if len(removed) == 0 {
			kept = append(kept, old)
			continue
		}
		old.Entries = slices.DeleteFunc(slices.Clone(old.Entries), func(entry MediaOrganizeEntry) bool {
			return removed[entry.File.ID] || removed[entry.VideoID]
		})
		old = mediaPersistentRun(old)
		changed = append(changed, old.ID)
		if len(old.Entries) > 0 {
			kept = append(kept, old)
		}
	}
	doc.Runs = kept
	return changed
}

func mediaResolvedSameSource(previous, current MediaOrganizeEntry, storageType string) bool {
	if previous.File.ID == "" || previous.File.ID != current.File.ID {
		return false
	}
	previousHash, currentHash := previous.File.SHA1, current.File.SHA1
	switch storageType {
	case "115":
	case "cd2":
		if previous.CD2FileID == "" || previous.CD2FileID != current.CD2FileID {
			return false
		}
		previousHash, currentHash = previous.CD2Hashes[2], current.CD2Hashes[2]
		if previousHash == "" && currentHash == "" && (previous.Modified == 0 || previous.Modified != current.Modified) {
			return false
		}
	case "local":
		if previous.Modified == 0 || previous.Modified != current.Modified || previous.File.SourcePath != current.File.SourcePath {
			return false
		}
	default:
		return false
	}
	if previous.File.Size != current.File.Size {
		// CD2 成功去重会采用保留文件大小，原任务及原生来源身份仍须一致。
		if storageType != "cd2" || current.Status != "done" || current.Result == nil || current.Result.Action != "duplicate" ||
			!validMaterialHash(previousHash) || !validMaterialHash(currentHash) {
			return false
		}
	}
	return previousHash == "" && currentHash == "" || previousHash != "" && currentHash != "" && strings.EqualFold(previousHash, currentHash)
}
