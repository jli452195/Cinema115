package service

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"path"
	"slices"
	"strings"

	"strm115/backend/internal/client/clouddrive2"
	"strm115/backend/internal/client/p115"
)

func mediaInScope(relative, scope string) bool {
	return scope == "" || relative == scope || strings.HasPrefix(relative, scope+"/")
}

// 同一执行器按子目录先完成、父目录后收尾，完整读取一个目录后才整理其中的文件。
func (s *MediaOrganizer) processCloudAuto(ctx context.Context, files *mediaOrganizeFiles, run *MediaOrganizeRun) (processErr error) {
	run.Status, run.streaming = "scanning", true
	files.deferSubtitles = true
	files.naming = make(map[string]preparedMediaNaming)
	files.recognized = make(map[string]bool)
	files.cloudEmpty = nil
	files.cd2Snapshots = nil
	if run.Rule.Storage == "115" {
		var err error
		files.pending115, files.pending115Dirs, err = s.pending115Tasks(ctx, run)
		if err != nil {
			return err
		}
	}
	defer func() {
		run.streaming = false
		files.deferSubtitles = false
		files.scan115ID, files.scan115Relative, files.shallow115 = "", "", false
		files.cd2ScanPath, files.cd2Shallow = "", false
		files.cleanupScope, files.partialCleanup = "", false
		if processErr != nil {
			if files.cloudEmpty != nil {
				files.cloudEmpty.complete = false
			}
			files.directories, files.cleanupDirectories = nil, nil
		}
	}()
	if err := s.checkpoint(run); err != nil {
		return err
	}
	visited := make(map[string]bool)
	candidates := make([]OrganizeFile, 0)
	subtitles := make([]MediaOrganizeEntry, 0)
	directories := make([]MediaOrganizeEntry, 0)
	settling, pending := make(map[string]bool), make(map[string]bool)
	cleanupRoots := make(map[string]bool, len(run.scanScopes))
	for _, scope := range run.scanScopes {
		cleanupRoots[scope.ID] = true
	}
	var walk func(mediaScanScope, int) error
	walk = func(scope mediaScanScope, depth int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := s.checkOrganizeStop(run); err != nil {
			return err
		}
		if err := mediaVisitScanDirectory(visited, scope.ID, path.Join(run.Rule.Source, scope.Relative), depth); err != nil {
			return err
		}
		files.unrelated, files.directories = nil, nil
		if run.Rule.Storage == "115" {
			files.scan115ID, files.scan115Relative, files.shallow115 = scope.ID, scope.Relative, true
		} else {
			files.cd2ScanPath, files.cd2Shallow = scope.ID, true
		}
		s.publishMediaProgress(run, "scanning", path.Join(run.Rule.Source, scope.Relative), 0, 0)
		entries, err := files.scan(ctx)
		if err != nil {
			if scope.Relative != "" && errors.Is(err, clouddrive2.ErrNotFound) {
				return nil
			}
			return err
		}
		s.noteCloudDirectory(run.Rule, scope.ID, scope.Relative)
		children := slices.Clone(files.directories)
		if err := mediaCheckScanDirectoryCount(len(visited)+len(children), path.Join(run.Rule.Source, scope.Relative)); err != nil {
			return err
		}
		directories = append(directories, children...)
		candidates = append(candidates, files.localCandidates...)
		maps.Copy(settling, files.settlingVideos)
		maps.Copy(pending, files.localPending)
		unrelated := slices.Clone(files.unrelated)
		immediate, deferred := make([]MediaOrganizeEntry, 0, len(entries)), make([]MediaOrganizeEntry, 0)
		matcher := organizeSubtitleMatcher(files.localCandidates)
		for _, entry := range entries {
			if entry.File.MediaType == "subtitle" {
				owners, _ := organizeSubtitleCandidates(entry.File, matcher)
				if len(owners) == 0 {
					// 跨目录字幕留到本轮扫描结束，完整候选集避免依赖目录遍历顺序。
					subtitles = append(subtitles, entry)
					continue
				}
			}
			if _, _, shared := mediaAttachmentFolder(entry.File); entry.File.MediaType == "attachment" && shared {
				deferred = append(deferred, entry)
			} else {
				immediate = append(immediate, entry)
			}
		}
		files.scanned = entries
		if err := s.processMediaBatch(ctx, files, run, immediate); err != nil {
			return err
		}
		maps.Copy(pending, files.localPending)
		for _, child := range children {
			if err := walk(mediaScanScope{ID: child.File.ID, Relative: child.File.SourcePath}, depth+1); err != nil {
				if run.Rule.Storage == "cd2" && ctx.Err() == nil && !errors.Is(err, errMediaOrganizeStopped) {
					files.directories, files.cleanupDirectories = nil, nil
					for _, directory := range directories {
						if _, complete := files.cd2DirectoryChildren[directory.File.ID]; complete && mediaInScope(directory.File.SourcePath, scope.Relative) {
							files.directories = append(files.directories, directory)
						}
					}
					files.cleanupScope = scope.Relative
					return errors.Join(err, s.cleanupEmptyDirectories(ctx, files, run))
				}
				return err
			}
		}
		if err := s.checkOrganizeStop(run); err != nil {
			return err
		}
		files.localCandidates = make([]OrganizeFile, 0)
		files.directories = make([]MediaOrganizeEntry, 0)
		for _, candidate := range candidates {
			if mediaInScope(candidate.SourcePath, scope.Relative) {
				files.localCandidates = append(files.localCandidates, candidate)
			}
		}
		for _, directory := range directories {
			if mediaInScope(directory.File.SourcePath, scope.Relative) {
				files.directories = append(files.directories, directory)
			}
		}
		files.settlingVideos, files.localPending = settling, pending
		files.scan115ID, files.scan115Relative, files.shallow115 = "", "", false
		files.cd2ScanPath, files.cd2Shallow = "", false
		if err := s.processMediaBatch(ctx, files, run, files.holdLocalCompanions(deferred, pending)); err != nil {
			return err
		}
		maps.Copy(pending, files.localPending)
		// 每个作品分支收尾一次，空分支合并到父目录批量清理。
		if scope.Relative != "" && !cleanupRoots[scope.ID] && (strings.Contains(scope.Relative, "/") || len(files.localCandidates) == 0 && len(unrelated) == 0) {
			return nil
		}
		// 已完成的主文件不再保护来源附件；失败或结果未确认的主文件继续保护。
		departed := make(map[string]bool)
		for _, entry := range run.Entries {
			if !entry.Reference && entry.File.MediaType == "video" && (entry.Status == "done" || entry.Failure != nil && entry.Failure.State == "stored") {
				departed[entry.File.ID] = true
			}
		}
		files.sourceVacated = false
		for _, candidate := range files.localCandidates {
			if departed[candidate.ID] {
				files.sourceVacated = true
			}
		}
		files.localCandidates = slices.DeleteFunc(files.localCandidates, func(file OrganizeFile) bool { return departed[file.ID] })
		if slices.ContainsFunc(files.localCandidates, func(file OrganizeFile) bool { return file.MediaType == "video" }) || slices.ContainsFunc(files.localCandidates, func(file OrganizeFile) bool { return pending[file.ID] || settling[file.ParentID] }) {
			files.sourceVacated = false
		}
		files.cleanupScope, files.partialCleanup = scope.Relative, scope.Relative != "" || len(run.scanScopes) > 0
		files.unrelated = unrelated
		if err := s.prepareOrphanCleanup(files, run); err != nil {
			return err
		}
		// 先移除空季目录，再处理公共附件，避免空目录反过来阻止附件清理。
		if files.cloudEmpty != nil {
			files.cloudEmpty.complete = true
		}
		if err := s.cleanupEmptyDirectories(ctx, files, run); err != nil {
			return err
		}
		if err := s.cleanup(ctx, files, run); err != nil {
			return err
		}
		if err := s.cleanupEmptyDirectories(ctx, files, run); err != nil {
			return err
		}
		if files.cloudEmpty != nil {
			files.cloudEmpty.complete = false
		}
		return nil
	}
	scopes := run.scanScopes
	if len(scopes) == 0 {
		root := run.Rule.Source
		if run.Rule.Storage == "115" {
			root = run.Rule.SourceID
		}
		scopes = []mediaScanScope{{ID: root}}
	}
	for _, scope := range scopes {
		if scope.Relative != "" && (path.IsAbs(scope.Relative) || path.Clean(scope.Relative) != scope.Relative || strings.HasPrefix(scope.Relative, "../")) {
			return fmt.Errorf("整理扫描范围无效")
		}
		if run.Rule.Storage == "cd2" && scope.ID != path.Join(run.Rule.Source, scope.Relative) {
			return fmt.Errorf("CD2 扫描范围已变化")
		}
		if scope.Relative != "" {
			entry, err := files.cloudScopeDirectory(ctx, scope)
			if errors.Is(err, clouddrive2.ErrNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			directories = append(directories, entry)
		}
		if err := walk(scope, strings.Count(scope.Relative, "/")); err != nil {
			return err
		}
	}
	run.streaming = false
	files.deferSubtitles = false
	files.localCandidates, files.settlingVideos, files.localPending = candidates, settling, pending
	if err := s.processMediaBatch(ctx, files, run, files.holdLocalCompanions(subtitles, pending)); err != nil {
		return err
	}
	files.cleanupScope, files.partialCleanup = "", len(run.scanScopes) > 0
	files.unrelated = nil
	if err := s.prepareOrphanCleanup(files, run); err != nil {
		return err
	}
	if err := s.cleanup(ctx, files, run); err != nil {
		return err
	}
	files.scanned = nil
	if files.cloudEmpty != nil {
		files.cloudEmpty.complete = true
	}
	mediaFinishRun(run)
	return nil
}

// 局部扫描只补读起点身份，不读取兄弟作品。
func (f *mediaOrganizeFiles) cloudScopeDirectory(ctx context.Context, scope mediaScanScope) (MediaOrganizeEntry, error) {
	if f.rule.Storage == "cd2" {
		file, err := f.cd2.Stat(ctx, scope.ID, false)
		if err != nil {
			return MediaOrganizeEntry{}, err
		}
		if !file.Directory || file.Path != path.Join(f.rule.Source, scope.Relative) {
			return MediaOrganizeEntry{}, ErrMediaOrganizeInput
		}
		return MediaOrganizeEntry{File: OrganizeFile{ShareFile: mediaCD2ShareFile(file), ParentID: path.Dir(file.Path), SourcePath: scope.Relative, MediaType: "cleanup"}, CD2FileID: file.ID}, nil
	}
	drive, ok := f.drive.(mediaOrganizeDrive)
	if !ok {
		return MediaOrganizeEntry{}, ErrMediaOrganizeInput
	}
	directory, err := drive.ReadDirectory(ctx, scope.ID, f.cookie)
	if err != nil {
		return MediaOrganizeEntry{}, err
	}
	if directory.CID != scope.ID || directory.ParentCID == "" || directory.Path != path.Join(f.rule.Source, scope.Relative) {
		return MediaOrganizeEntry{}, ErrMediaOrganizeInput
	}
	parent := path.Dir(scope.Relative)
	if parent == "." {
		parent = ""
	}
	f.remember115Directory(directory.ParentCID, parent, nil)
	entry := MediaOrganizeEntry{File: OrganizeFile{ShareFile: p115.ShareFile{ID: scope.ID, Name: path.Base(scope.Relative), Directory: true}, ParentID: directory.ParentCID, SourcePath: scope.Relative, MediaType: "cleanup"}}
	f.cloudEmpty.dirs[scope.ID] = entry
	return entry, nil
}
