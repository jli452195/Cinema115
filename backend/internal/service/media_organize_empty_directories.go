package service

import (
	"context"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
	"time"

	"strm115/backend/internal/client/clouddrive2"
)

// 空目录独立收尾，单个文件整理失败不能阻止其他空子目录清理；来源根目录始终保留。
func (s *MediaOrganizer) cleanupEmptyDirectories(ctx context.Context, files *mediaOrganizeFiles, run *MediaOrganizeRun) error {
	candidates := make(map[string]MediaOrganizeEntry, len(files.directories)+len(files.cleanupDirectories))
	for _, entry := range files.directories {
		candidates[entry.File.ID] = entry
	}
	for id, entry := range files.cleanupDirectories {
		candidates[id] = entry
	}
	ordered := make([]MediaOrganizeEntry, 0, len(candidates))
	for _, entry := range candidates {
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
	for _, entry := range ordered {
		if err := ctx.Err(); err != nil {
			return errors.Join(cleanupErr, err)
		}
		var err error
		if run.Rule.Storage == "cd2" {
			err = s.cleanupCD2EmptyDirectory(ctx, files, run, entry)
		} else {
			err = s.cleanupEntries(ctx, files, run, []MediaOrganizeEntry{entry})
		}
		cleanupErr = errors.Join(cleanupErr, err)
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

func (s *MediaOrganizer) cleanupCD2EmptyDirectory(ctx context.Context, files *mediaOrganizeFiles, run *MediaOrganizeRun, entry MediaOrganizeEntry) error {
	full, err := mediaCD2Relative(run.Rule.Source, entry.File.SourcePath)
	if err != nil || full != entry.File.ID || full == run.Rule.Target || full == run.Rule.Failure {
		return fmt.Errorf("CD2 空目录路径无法确认")
	}
	cleaner, ok := files.cd2.(clouddrive2.EmptyDirectoryFilesystem)
	if !ok {
		return fmt.Errorf("当前 CD2 连接不支持空目录复核清理")
	}
	work, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	entry.Status, entry.Message = "error", "CD2 空目录清理结果待核实；下轮监控会重新扫描"
	entry.Result = &MediaOrganizeResult{Status: entry.Status, Message: entry.Message, At: time.Now()}
	index := len(run.Entries)
	run.Entries = append(run.Entries, entry)
	if err := s.checkpoint(run); err != nil {
		return err
	}
	removed, err := cleaner.RemoveEmptyDirectory(work, full, entry.CD2FileID)
	result := &run.Entries[index]
	if err != nil {
		result.Message += "：" + err.Error()
	} else if removed {
		result.Status, result.Message = "done", "已通过 CD2 删除空文件夹"
	} else {
		result.Status, result.Message, result.Result = "retained", "目录非空或身份已变化，已保留来源", nil
	}
	if result.Status != "retained" {
		result.Result = &MediaOrganizeResult{Status: result.Status, Message: result.Message, At: time.Now()}
	}
	return errors.Join(err, s.checkpoint(run))
}
