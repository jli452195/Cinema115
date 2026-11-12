package service

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"strm115/backend/internal/client/p115"
)

type media115BatchKey struct{ source, directory, previousTarget string }

type media115RenameReceiptDrive interface {
	RenameEntriesWithReceipt(context.Context, []p115.RenameTarget, string) (bool, error)
}

// 同一来源与目标目录立即成组，每组视频确认完成后才放行字幕和附件。
func (s *MediaOrganizer) execute115Batches(ctx context.Context, files *mediaOrganizeFiles, run *MediaOrganizeRun, drive media115DirectoryDrive, failures map[string]bool) error {
	groups := make(map[media115BatchKey][]int)
	order := make([]media115BatchKey, 0)
	for i, entry := range run.Entries {
		if entry.Status == "done" || !mediaInRunTask(run, entry) || entry.File.MediaType == "cleanup" {
			continue
		}
		if entry.P115Move == nil && (failures[entry.File.ID] || entry.Relative == "" || entry.Status != "ready" && entry.Status != "error") {
			continue
		}
		key := media115BatchKey{source: entry.File.ParentID, directory: path.Dir(entry.Relative)}
		if entry.P115Move != nil {
			key.previousTarget = entry.P115Move.TargetCID
		}
		if _, exists := groups[key]; !exists {
			order = append(order, key)
		}
		groups[key] = append(groups[key], i)
	}
	for _, key := range order {
		for _, videos := range []bool{true, false} {
			done := make(map[string]bool, len(run.Entries))
			for _, entry := range run.Entries {
				done[entry.File.ID] = entry.Status == "done"
			}
			indices, singles := make([]int, 0), make([]int, 0)
			for _, index := range groups[key] {
				entry := &run.Entries[index]
				if entry.AttachmentKeep != nil {
					continue
				}
				if entry.Status == "done" || (entry.File.MediaType == "video") != videos {
					continue
				}
				if entry.VideoID != "" && !done[entry.VideoID] && entry.P115Move == nil {
					media115BatchResult(entry, fmt.Errorf("对应视频尚未整理成功，关联文件保留来源"), files.cookie)
					entry.ResultCode = "WAITING_MAIN_FILE"
					continue
				}
				mediaClearMainFileWaiting(entry)
				if entry.Failure != nil && entry.P115Move == nil {
					singles = append(singles, index)
					continue
				}
				indices = append(indices, index)
			}
			for len(indices) > 0 {
				count := min(len(indices), p115.DriveMutationBatchLimit)
				fallback, err := s.execute115Batch(ctx, files, run, drive, indices[:count])
				if err != nil {
					return err
				}
				singles = append(singles, fallback...)
				indices = indices[count:]
			}
			// 特殊项在批次预留释放后单独执行，保留既有去重及失败目录语义。
			for _, index := range singles {
				if err := s.executeMediaEntry(ctx, files, run, index); err != nil {
					return err
				}
			}
			if err := s.checkpoint(run); err != nil {
				return err
			}
		}
		// 此处没有持有批量写入预留，清理不会与移动锁嵌套。
		s.tryCleanup115EmptyDirectories(ctx, files, run)
	}
	return ctx.Err()
}

func (s *MediaOrganizer) execute115Batch(ctx context.Context, files *mediaOrganizeFiles, run *MediaOrganizeRun, drive media115DirectoryDrive, indices []int) (fallback []int, resultErr error) {
	if err := s.checkOrganizeStop(run); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	valid := make([]int, 0, len(indices))
	for _, index := range indices {
		entry := &run.Entries[index]
		mediaClearMainFileWaiting(entry)
		if entry.P115Move != nil && !media115BatchIntentMatches(run.Rule, *entry) {
			s.reconcile115ChangedIntent(ctx, files, run, drive, index)
			continue
		}
		if !media115BatchValidEntry(*entry) || entry.Target != path.Join(run.Rule.Target, entry.Relative) {
			media115BatchResult(entry, ErrMediaOrganizeInput, files.cookie)
			continue
		}
		valid = append(valid, index)
	}
	if len(valid) == 0 {
		return nil, s.checkpoint(run)
	}
	valid, releaseEntries := s.beginMediaEntries(run, valid)
	if len(valid) == 0 {
		return nil, nil
	}
	defer releaseEntries()
	s.publishMediaEntryProgress(run, run.Entries[valid[0]].File.Name)
	defer s.publishMediaEntryProgress(run, "")
	active := valid
	defer func() {
		if resultErr == nil {
			return
		}
		// 尝试已因真实错误结束时，才把未完成意图记为失败，不追加保存重试。
		for _, index := range active {
			entry := &run.Entries[index]
			if entry.Status == "ready" && entry.P115Move != nil {
				media115BatchResult(entry, resultErr, files.cookie)
			}
		}
	}()
	work, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	if s.reserve != nil {
		release, err := s.reserve()
		if err != nil {
			for _, index := range valid {
				media115BatchResult(&run.Entries[index], err, files.cookie)
			}
			if err := s.checkpoint(run); err != nil {
				return nil, err
			}
			return nil, work.Err()
		}
		defer release()
	}
	first := run.Entries[valid[0]]
	sourcePath := path.Join(run.Rule.Source, path.Dir(first.File.SourcePath))
	targetPath := path.Join(run.Rule.Target, path.Dir(first.Relative))
	targetID := ""
	var target media115BatchSnapshot
	targetRead := false
	if first.P115Move != nil {
		targetID = first.P115Move.TargetCID
	}
	// 恢复先核对冻结的精确目标；成功后旧来源目录可能已回收，不能先读旧来源。
	if targetID != "" {
		var err error
		target, err = files.read115BatchDirectory(work, drive, targetID, targetPath)
		if err != nil {
			return nil, s.fail115Batch(work, run, valid, err, files.cookie)
		}
		targetRead = true
		remaining := make([]int, 0, len(valid))
		for _, index := range valid {
			entry := &run.Entries[index]
			name := path.Base(entry.Relative)
			if target.matches(*entry, name) && !target.collision(name, entry.File.ID) {
				media115BatchResult(entry, nil, files.cookie)
				files.forget115SourceEntry(*entry)
			} else if target.ids[entry.File.ID] {
				media115BatchResult(entry, fmt.Errorf("原文件位于目标范围但名称或身份已变化，保留原意图待核实"), files.cookie)
			} else {
				remaining = append(remaining, index)
			}
		}
		valid = remaining
		if err := s.checkpoint(run); err != nil {
			return nil, err
		}
		if len(valid) == 0 {
			return nil, work.Err()
		}
		first = run.Entries[valid[0]]
	}
	source, err := files.read115BatchDirectory(work, drive, first.File.ParentID, sourcePath)
	if err != nil {
		return nil, s.fail115Batch(work, run, valid, err, files.cookie)
	}
	if targetID == "" {
		targetID, err = files.cloudDestination(work, first.Relative, false)
		if err != nil {
			return nil, s.fail115Batch(work, run, valid, err, files.cookie)
		}
	}
	if targetID == "" {
		creatable := false
		for _, index := range valid {
			entry := &run.Entries[index]
			if source.matches(*entry, entry.File.Name) || source.matches(*entry, path.Base(entry.Relative)) {
				creatable = true
				if entry.P115Move == nil {
					entry.P115Move = media115BatchIntent(run.Rule, *entry, "")
				}
			}
		}
		if !creatable {
			return nil, s.fail115Batch(work, run, valid, fmt.Errorf("来源与原定目标中无法确认文件，未创建替代目录"), files.cookie)
		}
		// 目录创建也由精确目标路径的准备意图覆盖，创建结果不明时不再写文件。
		if err := s.checkpoint(run); err != nil {
			return nil, err
		}
		targetID, err = files.cloudDestination(work, first.Relative, true)
		if err != nil {
			return nil, s.fail115Batch(work, run, valid, err, files.cookie)
		}
	}
	if targetID == "" || targetID == first.File.ParentID || targetID == run.Rule.SourceID || targetID == run.Rule.FailureID {
		return nil, s.fail115Batch(work, run, valid, fmt.Errorf("115 批处理目标目录无效"), files.cookie)
	}
	if !targetRead {
		target, err = files.read115BatchDirectory(work, drive, targetID, targetPath)
		if err != nil {
			return nil, s.fail115Batch(work, run, valid, err, files.cookie)
		}
	}
	items := make([]int, 0, len(valid))
	renames := make([]p115.RenameTarget, 0, len(valid))
	occupied := make(map[string]bool, len(valid))
	videoDone := make(map[string]bool, len(run.Entries))
	for _, entry := range run.Entries {
		videoDone[entry.File.ID] = entry.Status == "done"
	}
	for _, index := range valid {
		entry := &run.Entries[index]
		name := path.Base(entry.Relative)
		if entry.File.ParentID != first.File.ParentID || path.Join(run.Rule.Source, path.Dir(entry.File.SourcePath)) != sourcePath || path.Join(run.Rule.Target, path.Dir(entry.Relative)) != targetPath ||
			entry.P115Move != nil && entry.P115Move.TargetCID != "" && entry.P115Move.TargetCID != targetID {
			media115BatchResult(entry, fmt.Errorf("115 批处理的精确来源或目标目录不一致"), files.cookie)
			continue
		}
		if target.matches(*entry, name) && !target.collision(name, entry.File.ID) {
			// 115 FID 是稳定文件身份；完整目标快照已核实，无需额外读取旧来源。
			media115BatchResult(entry, nil, files.cookie)
			files.forget115SourceEntry(*entry)
			continue
		}
		sourceValid := source.matches(*entry, entry.File.Name) || source.matches(*entry, name)
		if !sourceValid || target.ids[entry.File.ID] {
			media115BatchResult(entry, fmt.Errorf("来源与精确目标中未确认原文件身份，保留待核实意图"), files.cookie)
			continue
		}
		if entry.VideoID != "" && !videoDone[entry.VideoID] {
			media115BatchResult(entry, fmt.Errorf("对应视频尚未整理成功，关联文件保留来源"), files.cookie)
			entry.ResultCode = "WAITING_MAIN_FILE"
			continue
		}
		key := strings.ToLower(name)
		if target.collision(name, entry.File.ID) || source.collision(name, entry.File.ID) || occupied[key] {
			// 完整来源仍在且原目标未命中，旧意图未完成；交给精确单项去重或冲突处理。
			entry.P115Move = nil
			fallback = append(fallback, index)
			continue
		}
		occupied[key] = true
		if entry.P115Move == nil {
			entry.P115Move = media115BatchIntent(run.Rule, *entry, targetID)
		} else {
			copy := *entry.P115Move
			entry.P115Move = &copy
			entry.P115Move.TargetCID = targetID
		}
		entry.P115Move.Phase = "moving"
		if !source.matches(*entry, name) {
			entry.P115Move.Phase = "renaming"
			renames = append(renames, p115.RenameTarget{ID: entry.File.ID, Name: name})
		}
		// 预写只保存文件意图，当前尝试尚未结束，不能先发布失败回执。
		entry.Status, entry.Message, entry.ResultCode, entry.Result = "ready", "", "", nil
		items = append(items, index)
	}
	if err := s.checkpoint(run); err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return fallback, work.Err()
	}
	if err := work.Err(); err != nil {
		return nil, err
	}
	if len(renames) > 0 {
		confirmed := false
		var writeErr error
		if receipt, supported := files.drive.(media115RenameReceiptDrive); supported {
			confirmed, writeErr = receipt.RenameEntriesWithReceipt(work, renames, files.cookie)
		} else {
			writeErr = files.drive.RenameEntries(work, renames, files.cookie)
		}
		// 仅完整逐 ID 名称回执可复用批前已核实的文件身份，汇总成功或异常仍须回读。
		if !confirmed || writeErr != nil {
			current, readErr := files.read115BatchDirectory(work, drive, first.File.ParentID, sourcePath)
			ready := make([]int, 0, len(items))
			for _, index := range items {
				entry := &run.Entries[index]
				if readErr == nil && current.matches(*entry, path.Base(entry.Relative)) && !current.collision(path.Base(entry.Relative), entry.File.ID) {
					ready = append(ready, index)
				} else {
					cause := errors.Join(writeErr, readErr)
					if cause == nil {
						cause = fmt.Errorf("115 批量改名结果未确认，下次重试先核对原意图")
					}
					media115BatchResult(entry, cause, files.cookie)
				}
			}
			items = ready
		}
		if err := s.checkpoint(run); err != nil {
			return nil, err
		}
		if len(items) == 0 {
			return fallback, work.Err()
		}
		// 本批持续持有预留且只改来源，目标复用批前完整快照；移动后仍逐项新鲜核验。
	}
	moveIDs, moving := make([]string, 0, len(items)), make([]int, 0, len(items))
	for _, index := range items {
		entry := &run.Entries[index]
		name := path.Base(entry.Relative)
		if target.matches(*entry, name) && !target.collision(name, entry.File.ID) {
			media115BatchResult(entry, nil, files.cookie)
			files.forget115SourceEntry(*entry)
		} else if target.ids[entry.File.ID] || target.collision(name, entry.File.ID) {
			media115BatchResult(entry, fmt.Errorf("115 目标在改名后发生变化，移动前已停止"), files.cookie)
		} else {
			entry.P115Move.Phase = "moving"
			moving, moveIDs = append(moving, index), append(moveIDs, entry.File.ID)
		}
	}
	if err := s.checkpoint(run); err != nil {
		return nil, err
	}
	if len(moving) == 0 {
		return fallback, work.Err()
	}
	if err := work.Err(); err != nil {
		return nil, err
	}
	writeErr := files.drive.MoveEntriesBatch(work, moveIDs, targetID, files.cookie)
	final, readErr := files.read115BatchDirectory(work, drive, targetID, targetPath)
	for _, index := range moving {
		entry := &run.Entries[index]
		if readErr == nil && final.matches(*entry, path.Base(entry.Relative)) && !final.collision(path.Base(entry.Relative), entry.File.ID) {
			media115BatchResult(entry, nil, files.cookie)
			files.forget115SourceEntry(*entry)
		} else {
			cause := errors.Join(writeErr, readErr)
			if cause == nil {
				cause = fmt.Errorf("115 批量移动结果未确认，下次重试先核对原意图")
			}
			media115BatchResult(entry, cause, files.cookie)
		}
	}
	if err := s.checkpoint(run); err != nil {
		return nil, err
	}
	return fallback, work.Err()
}

func (s *MediaOrganizer) fail115Batch(ctx context.Context, run *MediaOrganizeRun, indices []int, err error, cookie string) error {
	for _, index := range indices {
		media115BatchResult(&run.Entries[index], err, cookie)
	}
	if err := s.checkpoint(run); err != nil {
		return err
	}
	return ctx.Err()
}

func (s *MediaOrganizer) reconcile115ChangedIntent(ctx context.Context, files *mediaOrganizeFiles, run *MediaOrganizeRun, drive media115DirectoryDrive, index int) {
	entry := &run.Entries[index]
	intent := entry.P115Move
	message := "115 原写入意图与当前方案不一致，请先核实原位置再修改方案"
	if intent.TargetCID != "" && intent.FileID == entry.File.ID && intent.AccountID == run.Rule.AccountID {
		_, err := files.read115BatchDirectory(ctx, drive, intent.TargetCID, intent.TargetDirectory)
		if err != nil {
			message += "：" + organizePublicError(err, files.cookie)
		}
	}
	media115BatchResult(entry, fmt.Errorf("%s", message), files.cookie)
}
