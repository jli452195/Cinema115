package service

import (
	"context"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
)

// 空目录独立收尾，单个文件整理失败不能阻止其他空子目录清理；来源根目录始终保留。
func (s *MediaOrganizer) cleanupEmptyDirectories(ctx context.Context, files *mediaOrganizeFiles, run *MediaOrganizeRun) error {
	if run.Rule.Storage == "115" {
		if _, supported := files.drive.(media115EmptyDrive); supported {
			cleanupErr := s.cleanup115EmptyDirectories(ctx, files, run)
			if state := files.cloudEmpty; state != nil && state.cleaned > state.summarized {
				message := fmt.Sprintf("已清理 %d 个空目录分支", state.cleaned-state.summarized)
				if run.Message != "" {
					message = run.Message + "；" + message
				}
				run.Message, state.summarized = message, state.cleaned
				cleanupErr = errors.Join(cleanupErr, s.checkpoint(run))
			}
			return cleanupErr
		}
	}
	candidates := make(map[string]MediaOrganizeEntry, len(files.directories)+len(files.cleanupDirectories))
	for _, entry := range files.directories {
		candidates[entry.File.ID] = entry
	}
	for id, entry := range files.cleanupDirectories {
		candidates[id] = entry
	}
	for _, entry := range run.Entries {
		if entry.File.Directory && entry.Status == "done" {
			delete(candidates, entry.File.ID)
		}
	}
	ordered := make([]MediaOrganizeEntry, 0, len(candidates))
	for _, entry := range candidates {
		if !mediaInScope(entry.File.SourcePath, files.cleanupScope) {
			continue
		}
		if !entry.File.Directory || entry.File.SourcePath == "" || entry.File.SourcePath == "." || path.IsAbs(entry.File.SourcePath) || path.Clean(entry.File.SourcePath) != entry.File.SourcePath || strings.HasPrefix(entry.File.SourcePath, "../") {
			continue
		}
		if run.Rule.Storage == "115" && (entry.File.ID == run.Rule.SourceID || entry.File.ID == run.Rule.TargetID || entry.File.ID == run.Rule.FailureID) {
			continue
		}
		ordered = append(ordered, entry)
	}
	slices.SortFunc(ordered, func(a, b MediaOrganizeEntry) int {
		if depth := strings.Count(b.File.SourcePath, "/") - strings.Count(a.File.SourcePath, "/"); depth != 0 {
			return depth
		}
		return strings.Compare(a.File.SourcePath, b.File.SourcePath)
	})
	before := len(run.Entries)
	var cleanupErr error
	if run.Rule.Storage == "cd2" {
		cleanupErr = s.cleanupCD2EmptyDirectories(ctx, files, run, ordered)
	} else {
		for _, entry := range ordered {
			if err := ctx.Err(); err != nil {
				return errors.Join(cleanupErr, err)
			}
			cleanupErr = errors.Join(cleanupErr, s.cleanupEntries(ctx, files, run, []MediaOrganizeEntry{entry}))
		}
	}
	cleaned := 0
	for _, entry := range run.Entries[before:] {
		if entry.Status == "done" {
			cleaned++
		}
	}
	if cleaned > 0 {
		message := fmt.Sprintf("已清理 %d 个空文件夹", cleaned)
		if run.Message != "" {
			message = run.Message + "；" + message
		}
		run.Message = message
		cleanupErr = errors.Join(cleanupErr, s.checkpoint(run))
	}
	return cleanupErr
}
