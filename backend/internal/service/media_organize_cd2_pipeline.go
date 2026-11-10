package service

import (
	"context"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"

	"strm115/backend/internal/client/clouddrive2"
)

// 每次读取一个完整目录后立即整理；分页读取尚未完成时不修改该目录。
func (s *MediaOrganizer) processCD2Auto(ctx context.Context, files *mediaOrganizeFiles, run *MediaOrganizeRun) error {
	return s.processCloudAuto(ctx, files, run)
}

// 成功记录提供来源与目标的映射，晚到字幕无需重新识别已经移走的视频。
func (s *MediaOrganizer) cd2AddReferences(ctx context.Context, files *mediaOrganizeFiles, run *MediaOrganizeRun, entries []MediaOrganizeEntry) error {
	wanted := make(map[string]bool)
	scopes := make([]string, 0)
	recursiveSubtitles := !run.streaming && slices.ContainsFunc(entries, func(entry MediaOrganizeEntry) bool { return entry.File.MediaType == "subtitle" })
	for _, entry := range entries {
		if entry.File.MediaType != "video" {
			wanted[entry.File.ParentID] = true
			if entry.File.MediaType == "attachment" {
				if scope, _, shared := mediaAttachmentFolder(entry.File); shared {
					scopes = append(scopes, scope)
				}
			}
		}
	}
	if len(wanted) == 0 {
		return nil
	}
	present := make(map[string]bool, len(run.Entries)+len(entries))
	for _, entry := range run.Entries {
		present[entry.File.ID] = true
	}
	for _, entry := range entries {
		present[entry.File.ID] = true
	}
	type reference struct {
		entry MediaOrganizeEntry
		group MediaOrganizeGroup
	}
	refs := make([]reference, 0)
	history, err := s.mediaReferenceHistory(ctx, run, wanted, scopes, recursiveSubtitles)
	if err != nil {
		return fmt.Errorf("读取晚到附件的视频映射: %w", err)
	}
	for _, old := range history {
		if old.ID == run.ID || old.Rule.ID != run.Rule.ID || old.Rule.Storage != "cd2" || old.Rule.ConnectionID != run.Rule.ConnectionID || old.Rule.ConnectionRevision != run.Rule.ConnectionRevision || old.Rule.Source != run.Rule.Source || old.Rule.Target != run.Rule.Target {
			continue
		}
		for _, entry := range old.Entries {
			if entry.File.MediaType != "video" || entry.Status != "done" || entry.Relative == "" || present[entry.File.ID] || !recursiveSubtitles && !wanted[entry.File.ParentID] && !slices.ContainsFunc(scopes, func(scope string) bool { return mediaAttachmentInScope(entry.File, scope) }) {
				continue
			}
			present[entry.File.ID] = true
			var group MediaOrganizeGroup
			for _, candidate := range old.Groups {
				if candidate.ID == entry.Group {
					group = candidate
					break
				}
			}
			refs = append(refs, reference{entry, group})
		}
	}
	// 先用本地文件名确定相关视频，再核验目标；单条字幕不查询同目录的全部历史影片。
	candidates := mediaEntryFiles(entries)
	candidates = append(candidates, mediaEntryFiles(run.Entries)...)
	for _, ref := range refs {
		candidates = append(candidates, ref.entry.File)
	}
	candidates = mediaIncludeVideoCandidates(candidates, files.localCandidates)
	owners := mediaLocalCompanionOwners(candidates)
	required := make(map[string]bool)
	for _, entry := range entries {
		for _, id := range owners[entry.File.ID] {
			required[id] = true
		}
	}
	for _, ref := range refs {
		if err := ctx.Err(); err != nil {
			return err
		}
		entry := ref.entry
		if !required[entry.File.ID] {
			continue
		}
		if _, exists := files.cd2Snapshots[entry.File.ID]; exists {
			continue
		}
		full, err := mediaCD2Relative(run.Rule.Target, entry.Relative)
		if err != nil {
			continue
		}
		if current := entry.CD2Current; current != nil && path.Clean(current.Path) == current.Path &&
			path.Dir(current.Path) == path.Dir(full) && strings.EqualFold(path.Base(current.Path), path.Base(full)) {
			full = current.Path
		}
		actual, err := files.cd2.Stat(ctx, full, true)
		if errors.Is(err, clouddrive2.ErrNotFound) {
			continue
		}
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			mediaRetainReferenceSidecars(files, entries, owners, entry.File.ID, "晚到附件对应目标无法确认，已保留来源："+err.Error())
			continue
		}
		expectedID := entry.CD2FileID
		if entry.CD2Current != nil {
			expectedID = entry.CD2Current.FileID
			entry.Modified = entry.CD2Current.Modified
		}
		if actual.Directory || !(mediaCD2Matches(entry, actual, expectedID) || entry.CD2Duplicate && mediaCD2SameSHA1(entry, actual)) {
			continue
		}
		ref.group.ID = fmt.Sprintf("reference-%d", len(run.Groups)+1)
		ref.group.ResultCode = ""
		run.Groups = append(run.Groups, ref.group)
		entry.Group, entry.Reference, entry.Result, entry.Failure = ref.group.ID, true, nil, nil
		entry.AttachmentKeep = nil
		run.Entries = append(run.Entries, entry)
	}
	return nil
}
