package service

import (
	"context"
	"fmt"
	"path"
	"slices"
	"strings"

	"strm115/backend/internal/client/p115"
)

// 115 晚到附件只复用相同冻结规则中的成功视频，再核对原定目标的实际身份。
func (s *MediaOrganizer) add115References(ctx context.Context, files *mediaOrganizeFiles, run *MediaOrganizeRun, entries []MediaOrganizeEntry) error {
	wanted, present := make(map[string]bool), make(map[string]bool)
	scopes := make([]string, 0)
	recursiveSubtitles := !run.streaming && slices.ContainsFunc(entries, func(entry MediaOrganizeEntry) bool { return entry.File.MediaType == "subtitle" })
	for _, entry := range entries {
		present[entry.File.ID] = true
		if mediaSidecar(entry) {
			wanted[entry.File.ParentID] = true
			if scope, _, shared := mediaAttachmentFolder(entry.File); entry.File.MediaType == "attachment" && shared {
				scopes = append(scopes, scope)
			}
		}
	}
	if len(wanted) == 0 {
		return nil
	}
	for _, entry := range run.Entries {
		present[entry.File.ID] = true
	}
	type reference struct {
		entry MediaOrganizeEntry
		group MediaOrganizeGroup
	}
	refs := make([]reference, 0)
	s.mu.Lock()
	for _, old := range s.doc.Runs {
		if old.ID == run.ID || old.Rule.ID != run.Rule.ID || old.Rule.Storage != "115" || old.Rule.AccountID != run.Rule.AccountID ||
			old.Rule.SourceID != run.Rule.SourceID || old.Rule.TargetID != run.Rule.TargetID || old.Rule.Source != run.Rule.Source || old.Rule.Target != run.Rule.Target {
			continue
		}
		for _, entry := range old.Entries {
			if entry.File.MediaType != "video" || entry.Status != "done" || entry.Relative == "" || present[entry.File.ID] ||
				!recursiveSubtitles && !wanted[entry.File.ParentID] && !slices.ContainsFunc(scopes, func(scope string) bool { return mediaAttachmentInScope(entry.File, scope) }) {
				continue
			}
			for _, group := range old.Groups {
				if group.ID == entry.Group && group.TMDBID > 0 {
					refs = append(refs, reference{entry: entry, group: group})
					present[entry.File.ID] = true
					break
				}
			}
		}
	}
	s.mu.Unlock()
	candidates := mediaEntryFiles(entries)
	candidates = append(candidates, mediaEntryFiles(run.Entries)...)
	for _, ref := range refs {
		candidates = append(candidates, ref.entry.File)
	}
	candidates = mediaIncludeVideoCandidates(candidates, files.localCandidates)
	owners := mediaLocalCompanionOwners(candidates)
	required := make(map[string]bool)
	for _, entry := range entries {
		for _, owner := range owners[entry.File.ID] {
			required[owner] = true
		}
	}
	type targetSnapshot struct {
		files []MediaAttachmentFileSnapshot
		err   error
	}
	snapshots := make(map[string]targetSnapshot)
	for _, ref := range refs {
		if err := ctx.Err(); err != nil {
			return err
		}
		entry := ref.entry
		if !required[entry.File.ID] {
			continue
		}
		directory := path.Dir(entry.Relative)
		snapshot, loaded := snapshots[directory]
		if !loaded {
			snapshot.files, snapshot.err = files.attachmentTargets(ctx, entry)
			snapshots[directory] = snapshot
		}
		if snapshot.err != nil {
			if err := ctx.Err(); err != nil {
				return err
			}
			mediaRetainReferenceSidecars(files, entries, owners, entry.File.ID, "晚到附件对应目标无法确认，已保留来源："+snapshot.err.Error())
			continue
		}
		confirmed, collision := false, false
		for _, target := range snapshot.files {
			if target.Name != path.Base(entry.Relative) {
				continue
			}
			moved := target.ID == entry.File.ID && target.Size == entry.File.Size && (entry.File.SHA1 == "" || strings.EqualFold(entry.File.SHA1, target.SHA1))
			duplicate := entry.Result != nil && entry.Result.Action == "duplicate" && media115SameSHA1(entry, p115.ShareFile{ID: target.ID, Name: target.Name, Size: target.Size, SHA1: target.SHA1})
			if moved || duplicate {
				confirmed = true
			} else {
				collision = true
			}
		}
		if !confirmed || collision {
			continue
		}
		ref.group.ID = fmt.Sprintf("reference-%d", len(run.Groups)+1)
		ref.group.ResultCode = ""
		run.Groups = append(run.Groups, ref.group)
		// 现有只读引用标志同时保护历史视频，不参与新的整理或结果计数。
		result := entry.Result
		entry.Group, entry.Reference, entry.Result, entry.Failure = ref.group.ID, true, nil, nil
		if result != nil && result.Action == "duplicate" {
			entry.Result = &MediaOrganizeResult{Action: "duplicate"}
		}
		entry.AttachmentKeep = nil
		run.Entries = append(run.Entries, entry)
	}
	return nil
}
