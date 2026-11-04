package service

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"path"
	"strings"
	"time"

	"strm115/backend/internal/client/clouddrive2"
)

const mediaCD2BatchSize = 20

type mediaCD2BatchItem struct {
	index       int
	source      string
	destination string
}

// 完整读完当前目录就执行小批次，不等待其他目录或后续监控事件凑满。
func (s *MediaOrganizer) executeCD2Batches(ctx context.Context, files *mediaOrganizeFiles, run *MediaOrganizeRun, bulk clouddrive2.BatchFilesystem, failures map[string]bool) error {
	for _, videos := range []bool{true, false} {
		done := make(map[string]bool, len(run.Entries))
		for _, entry := range run.Entries {
			done[entry.File.ID] = entry.Status == "done"
		}
		groups := make(map[string][]int)
		order := make([]string, 0)
		for i := range run.Entries {
			entry := &run.Entries[i]
			if (entry.File.MediaType == "video") != videos || failures[entry.File.ID] || !mediaInRunTask(run, *entry) || entry.Relative == "" || entry.Status != "ready" && entry.Status != "error" {
				continue
			}
			if entry.VideoID != "" && !done[entry.VideoID] {
				mediaCD2SetResult(entry, fmt.Errorf("对应视频尚未整理成功，关联文件保留来源"))
				entry.ResultCode = "WAITING_MAIN_FILE"
				continue
			}
			mediaClearMainFileWaiting(entry)
			if entry.AttachmentKeep != nil {
				continue
			}
			key := path.Dir(entry.Relative)
			if _, exists := groups[key]; !exists {
				order = append(order, key)
			}
			groups[key] = append(groups[key], i)
		}
		for _, key := range order {
			indices := groups[key]
			for len(indices) > 0 {
				count := min(len(indices), mediaCD2BatchSize)
				if err := s.executeCD2Batch(ctx, files, run, bulk, indices[:count]); err != nil {
					return err
				}
				indices = indices[count:]
			}
		}
		// 视频阶段提交完成后才放行字幕；无可执行批次时也保留依赖错误。
		if err := s.checkpoint(run); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func (s *MediaOrganizer) executeCD2Batch(ctx context.Context, files *mediaOrganizeFiles, run *MediaOrganizeRun, bulk clouddrive2.BatchFilesystem, indices []int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// 零散单文件直接处理，省去只对多文件有收益的分组预检。
	if len(indices) == 1 {
		return s.executeCD2SingleEntry(ctx, files, run, indices[0])
	}
	indices, releaseEntries := s.beginMediaEntries(run, indices)
	if len(indices) == 0 {
		return nil
	}
	defer func() {
		if releaseEntries != nil {
			releaseEntries()
		}
	}()
	for _, index := range indices {
		mediaClearMainFileWaiting(&run.Entries[index])
	}
	first := run.Entries[indices[0]]
	destination, err := files.cd2Destination(ctx, first.Relative, true)
	if err != nil {
		for _, index := range indices {
			mediaCD2SetResult(&run.Entries[index], err)
		}
		if err := s.checkpoint(run); err != nil {
			return err
		}
		return ctx.Err()
	}
	targets, err := files.cd2.List(ctx, path.Dir(destination), true)
	if err != nil {
		return err
	}
	occupied := make(map[string]bool, len(targets)+len(indices))
	for _, target := range targets {
		occupied[strings.ToLower(target.Name)] = true
	}
	items := make([]mediaCD2BatchItem, 0, len(indices))
	singles := make([]int, 0)
	renames := make([]clouddrive2.RenameOperation, 0, len(indices))
	renameTargets := make(map[string]bool, len(indices))
	for _, index := range indices {
		entry := &run.Entries[index]
		full, err := mediaCD2Relative(files.rule.Target, entry.Relative)
		if err != nil || path.Dir(full) != path.Dir(destination) {
			mediaCD2SetResult(entry, ErrMediaOrganizeInput)
			continue
		}
		key := strings.ToLower(path.Base(full))
		// 恢复、失败目录与同名判重沿用精确路径核验，不能混入普通批量写入。
		if entry.CD2Current != nil || entry.Failure != nil || occupied[key] {
			singles = append(singles, index)
			continue
		}
		source, err := mediaCD2Relative(files.rule.Source, entry.File.SourcePath)
		if err != nil {
			mediaCD2SetResult(entry, err)
			continue
		}
		actual, err := files.cd2.Stat(ctx, source, true)
		if errors.Is(err, clouddrive2.ErrNotFound) {
			// 改名已生效但回执未确认时，沿用原名、中间名和精确目标的恢复核验。
			singles = append(singles, index)
			continue
		}
		if err != nil || !mediaCD2Matches(*entry, actual, entry.CD2FileID) {
			if err == nil {
				err = fmt.Errorf("CD2 来源文件身份或大小已变化，请重新扫描")
			}
			mediaCD2SetResult(entry, err)
			continue
		}
		if actual.Name != path.Base(full) {
			renamed := path.Join(path.Dir(source), path.Base(full))
			renameKey := strings.ToLower(renamed)
			_, statErr := files.cd2.Stat(ctx, renamed, true)
			if renameTargets[renameKey] || !errors.Is(statErr, clouddrive2.ErrNotFound) {
				if statErr != nil && !errors.Is(statErr, clouddrive2.ErrNotFound) {
					mediaCD2SetResult(entry, statErr)
				} else {
					singles = append(singles, index)
				}
				continue
			}
			renameTargets[renameKey] = true
			renames = append(renames, clouddrive2.RenameOperation{Source: source, Name: path.Base(full)})
		}
		occupied[key] = true
		items = append(items, mediaCD2BatchItem{index: index, source: source, destination: full})
	}
	if len(renames) > 0 {
		results, renameErr := bulk.RenameMany(ctx, renames)
		bySource := mediaCD2BatchResults(results)
		ready := items[:0]
		for _, item := range items {
			if path.Base(item.source) != path.Base(item.destination) {
				renamed := path.Join(path.Dir(item.source), path.Base(item.destination))
				result, err := mediaCD2BatchResult(bySource, item.source, renamed, renameErr)
				if err == nil && !mediaCD2BatchMatches(run.Entries[item.index], result.File) {
					err = fmt.Errorf("CD2 改名回执与原文件身份不符，结果待核实")
				}
				if err != nil {
					mediaCD2SetResult(&run.Entries[item.index], err)
					continue
				}
				mediaCD2SetLocation(&run.Entries[item.index], result.File)
				item.source = renamed
			}
			ready = append(ready, item)
		}
		items = ready
		// 改名回执先持久化，再开始移动；重启后只恢复未完成的位置。
		if err := s.checkpoint(run); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(items) > 0 {
		sources := make([]string, 0, len(items))
		for _, item := range items {
			sources = append(sources, item.source)
		}
		results, moveErr := bulk.MoveMany(ctx, sources, path.Dir(destination))
		bySource := mediaCD2BatchResults(results)
		for _, item := range items {
			result, err := mediaCD2BatchResult(bySource, item.source, item.destination, moveErr)
			entry := &run.Entries[item.index]
			if err == nil {
				if !mediaCD2BatchMatches(*entry, result.File) {
					err = fmt.Errorf("CD2 移动回执与原文件身份不符，结果待核实")
				} else {
					mediaCD2SetLocation(entry, result.File)
				}
			}
			mediaCD2SetResult(entry, err)
		}
	}
	if err := s.checkpoint(run); err != nil {
		return err
	}
	// 批量操作已结束，回退单项只保护各自所属视频。
	releaseEntries()
	releaseEntries = nil
	for _, index := range singles {
		if err := s.executeCD2SingleEntry(ctx, files, run, index); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func (s *MediaOrganizer) executeCD2SingleEntry(ctx context.Context, files *mediaOrganizeFiles, run *MediaOrganizeRun, index int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	entry := &run.Entries[index]
	if mediaSidecar(*entry) {
		return s.executeMediaEntry(ctx, files, run, index)
	}
	indices, releaseEntry := s.beginMediaEntries(run, []int{index})
	if len(indices) == 0 {
		return nil
	}
	defer releaseEntry()
	mediaClearMainFileWaiting(entry)
	mediaCD2SetResult(entry, files.move(ctx, *entry))
	if err := s.checkpoint(run); err != nil {
		return err
	}
	return ctx.Err()
}

func mediaCD2BatchResults(results []clouddrive2.BatchFileResult) map[string]clouddrive2.BatchFileResult {
	indexed := make(map[string]clouddrive2.BatchFileResult, len(results))
	for _, result := range results {
		if _, duplicate := indexed[result.Source]; duplicate {
			result.Err = fmt.Errorf("CD2 批量结果含重复来源，结果待核实")
		}
		indexed[result.Source] = result
	}
	return indexed
}

func mediaCD2BatchResult(results map[string]clouddrive2.BatchFileResult, source, target string, requestErr error) (clouddrive2.BatchFileResult, error) {
	result, ok := results[source]
	if !ok {
		if requestErr == nil {
			requestErr = fmt.Errorf("CD2 未返回该文件的核对结果")
		}
		return result, requestErr
	}
	if result.Err != nil {
		return result, result.Err
	}
	if result.Target != target || result.File.Path != target || result.File.Directory || result.File.ReadOnly {
		return result, fmt.Errorf("CD2 批量结果与计划目标不符，结果待核实")
	}
	return result, nil
}

func mediaCD2SetLocation(entry *MediaOrganizeEntry, current clouddrive2.File) {
	location := &MediaCD2Location{Path: current.Path, FileID: current.ID}
	if !current.Modified.IsZero() {
		location.Modified = current.Modified.UnixNano()
	}
	entry.CD2Current = location
}

// 使用已返回的身份资料核对原任务；路径型编号变化时允许稳定时间戳，不额外请求网盘。
func mediaCD2BatchMatches(entry MediaOrganizeEntry, current clouddrive2.File) bool {
	expectedID := entry.CD2FileID
	if entry.CD2Current != nil {
		expectedID, entry.Modified = entry.CD2Current.FileID, entry.CD2Current.Modified
	}
	if mediaCD2Matches(entry, current, expectedID) || mediaCD2Matches(entry, current, "") {
		return true
	}
	return entry.Modified != 0 && !current.Modified.IsZero() && entry.Modified == current.Modified.UnixNano() && mediaCD2Matches(entry, current, current.ID)
}

// 已核实的去重目标可与旧来源有不同身份和大小，成功结果复用现有实际位置字段。
func mediaCD2ApplyDuplicateReceipt(entry *MediaOrganizeEntry, err error) {
	var receipt *mediaDuplicateReceipt
	if !errors.As(err, &receipt) || receipt.cd2 == nil {
		return
	}
	current := *receipt.cd2
	entry.File.Size = current.Size
	entry.CD2Hashes = maps.Clone(current.Hashes)
	mediaCD2SetLocation(entry, current)
}

func mediaCD2SetResult(entry *MediaOrganizeEntry, err error) {
	entry.Status, entry.Message = "done", ""
	if err != nil && !errors.Is(err, errMediaDuplicateDone) {
		entry.Status, entry.Message = "error", organizePublicError(err, "")
		if errors.Is(err, errMediaTargetExists) || errors.Is(err, clouddrive2.ErrExists) {
			entry.Status = "conflict"
		}
	} else {
		mediaCD2ApplyDuplicateReceipt(entry, err)
		entry.Failure = nil
		mediaClearMainFileWaiting(entry)
	}
	entry.Result = &MediaOrganizeResult{Status: entry.Status, Message: entry.Message, At: time.Now(), Target: entry.Target}
	if errors.Is(err, errMediaDuplicateDone) {
		entry.Result.Action, entry.Result.Message = "duplicate", "SHA1 一致，已删除来源重复文件"
	}
}
