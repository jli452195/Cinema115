package service

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"strm115/backend/internal/client/clouddrive2"
)

// 每次读取一个完整目录后立即整理；分页读取尚未完成时不修改该目录。
func (s *MediaOrganizer) processCD2Auto(ctx context.Context, files *mediaOrganizeFiles, run *MediaOrganizeRun) error {
	run.Status, run.streaming = "scanning", true
	defer func() { run.streaming = false }()
	if err := s.checkpoint(run); err != nil {
		return err
	}
	files.cd2Naming = make(map[string]preparedMediaNaming)
	files.cd2Recognized = make(map[string]bool)
	queue := []string{run.Rule.Source}
	visited := make(map[string]bool)
	deferred := make([]MediaOrganizeEntry, 0)
	unrelated := make([]MediaOrganizeEntry, 0)
	directories := make([]MediaOrganizeEntry, 0)
	allCandidates := make([]OrganizeFile, 0)
	allSettling := make(map[string]bool)
	allPending := make(map[string]bool)
	fileCount := 0
	for len(queue) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		directory := queue[0]
		queue = queue[1:]
		if visited[directory] {
			continue
		}
		if !mediaCloudWithin(run.Rule.Source, directory) || len(visited) >= 300 || strings.Count(strings.TrimPrefix(directory, run.Rule.Source), "/") > 12 {
			return fmt.Errorf("CD2 扫描范围过大，请缩小来源目录")
		}
		visited[directory] = true
		files.cd2ScanPath, files.cd2Shallow = directory, true
		files.unrelated, files.directories = nil, nil
		entries, err := files.scan(ctx)
		if err != nil {
			if directory != run.Rule.Source && errors.Is(err, clouddrive2.ErrNotFound) {
				continue
			}
			return err
		}
		fileCount += len(files.cd2Snapshots)
		if fileCount > 5000 {
			return fmt.Errorf("单次最多 5000 个文件，请缩小扫描范围")
		}
		for _, child := range files.directories {
			queue = append(queue, child.File.ID)
		}
		directories = append(directories, files.directories...)
		allCandidates = append(allCandidates, files.localCandidates...)
		maps.Copy(allSettling, files.settlingVideos)
		maps.Copy(allPending, files.localPending)
		immediate := make([]MediaOrganizeEntry, 0, len(entries))
		for _, entry := range entries {
			if entry.File.MediaType == "attachment" {
				if _, _, shared := mediaAttachmentFolder(entry.File); shared {
					deferred = append(deferred, entry)
					continue
				}
			}
			immediate = append(immediate, entry)
		}
		files.scanned = entries
		if err := s.cd2ProcessBatch(ctx, files, run, immediate, true); err != nil {
			return err
		}
		unrelated = append(unrelated, files.unrelated...)
	}
	files.unrelated, files.directories = unrelated, directories
	files.localCandidates, files.settlingVideos, files.localPending = allCandidates, allSettling, allPending
	files.cd2ScanPath, files.cd2Shallow = "", false
	if len(deferred) > 0 {
		deferred = files.holdLocalCompanions(deferred, allPending)
		if err := s.cd2ProcessBatch(ctx, files, run, deferred, false); err != nil {
			return err
		}
	}
	// 仅在所有待扫描目录完整读取后清理，后续目录失败不会误删附件。
	if err := s.prepareOrphanCleanup(files, run); err != nil {
		return err
	}
	if len(files.unrelated) > 0 {
		if err := s.cleanupCD2(ctx, files, run); err != nil {
			return err
		}
	}
	run.streaming = false
	mediaFinishRun(run)
	if len(run.Entries) == 0 && len(allPending) > 0 {
		run.Message = fmt.Sprintf("有 %d 个临时文件尚未完成，留待后续轮询", len(allPending))
	}
	return nil
}

func (s *MediaOrganizer) cd2ProcessBatch(ctx context.Context, files *mediaOrganizeFiles, run *MediaOrganizeRun, entries []MediaOrganizeEntry, recheck bool) error {
	if len(entries) == 0 {
		return nil
	}
	run.processing = make(map[string]bool, len(entries))
	for _, entry := range entries {
		run.processing[entry.File.ID] = true
	}
	defer func() { run.processing = nil }()
	batchCtx := clouddrive2.WithDirectoryCache(ctx)
	if err := s.cd2AddReferences(batchCtx, files, run, entries); err != nil {
		return err
	}
	mediaAppendScanEntries(run, entries)
	oldGroups := run.Groups
	run.Groups = make([]MediaOrganizeGroup, 0, len(oldGroups))
	for _, group := range oldGroups {
		if !files.cd2Recognized[group.ID] {
			run.Groups = append(run.Groups, group)
			files.cd2Recognized[group.ID] = true
		}
	}
	err := s.recognize(ctx, run)
	updated := make(map[string]MediaOrganizeGroup, len(run.Groups))
	for _, group := range run.Groups {
		updated[group.ID] = group
	}
	run.Groups = oldGroups
	for i, group := range run.Groups {
		if current, ok := updated[group.ID]; ok {
			run.Groups[i] = current
		}
	}
	if err != nil {
		return err
	}
	if err := s.render(batchCtx, files, run); err != nil {
		return err
	}
	mediaDiscardOrphanSidecars(files, run, entries)
	if recheck {
		if err := files.recheckLocalCompanions(batchCtx, run); err != nil {
			return err
		}
	}
	return s.execute(batchCtx, files, run)
}

// 成功记录提供来源与目标的映射，晚到字幕无需重新识别已经移走的视频。
func (s *MediaOrganizer) cd2AddReferences(ctx context.Context, files *mediaOrganizeFiles, run *MediaOrganizeRun, entries []MediaOrganizeEntry) error {
	wanted := make(map[string]bool)
	scopes := make([]string, 0)
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
	s.mu.Lock()
	for _, old := range s.doc.Runs {
		if old.ID == run.ID || old.Rule.ID != run.Rule.ID || old.Rule.ConnectionID != run.Rule.ConnectionID || old.Rule.ConnectionRevision != run.Rule.ConnectionRevision || old.Rule.Source != run.Rule.Source || old.Rule.Target != run.Rule.Target {
			continue
		}
		for _, entry := range old.Entries {
			if entry.CD2Reference || entry.File.MediaType != "video" || entry.Status != "done" || entry.Relative == "" || present[entry.File.ID] || !wanted[entry.File.ParentID] && !slices.ContainsFunc(scopes, func(scope string) bool { return mediaAttachmentInScope(entry.File, scope) }) {
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
	s.mu.Unlock()
	// 先用本地文件名确定相关视频，再核验目标；单条字幕不查询同目录的全部历史影片。
	candidates := mediaEntryFiles(entries)
	candidates = append(candidates, mediaEntryFiles(run.Entries)...)
	for _, ref := range refs {
		candidates = append(candidates, ref.entry.File)
	}
	owners := mediaLocalCompanionOwners(candidates)
	required := make(map[string]bool)
	for _, entry := range entries {
		for _, id := range owners[entry.File.ID] {
			required[id] = true
		}
	}
	for _, ref := range refs {
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
		actual, err := files.cd2.Stat(ctx, full, true)
		if err != nil {
			return fmt.Errorf("晚到附件对应目标无法确认，已保留来源: %w", err)
		}
		expectedID := entry.CD2FileID
		if entry.CD2Current != nil {
			expectedID = entry.CD2Current.FileID
			entry.Modified = entry.CD2Current.Modified
		}
		if actual.Directory || !(mediaCD2Matches(entry, actual, expectedID) || entry.CD2Duplicate && mediaCD2SameSHA1(entry, actual)) {
			return fmt.Errorf("晚到附件对应目标身份已变化，已保留来源")
		}
		ref.group.ID = fmt.Sprintf("reference-%d", len(run.Groups)+1)
		run.Groups = append(run.Groups, ref.group)
		entry.Group, entry.CD2Reference, entry.Result, entry.Failure = ref.group.ID, true, nil, nil
		run.Entries = append(run.Entries, entry)
	}
	return nil
}
