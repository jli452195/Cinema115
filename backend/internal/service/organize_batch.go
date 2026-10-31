package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"strm115/backend/internal/client/p115"
)

type organizeBatch struct {
	source    string
	directory string
	indexes   []int
}

// Group only exact planned file IDs, preserving first-seen group and file order.
// Never move a whole source folder: it can contain excluded or newly added files.
func organizeBatches(items []OrganizeItem) []organizeBatch {
	type key struct{ source, directory string }
	positions := make(map[key]int)
	groups := make([]organizeBatch, 0)
	for i, item := range items {
		if item.Status == "done" {
			continue
		}
		k := key{item.File.ParentID, item.Directory}
		position, ok := positions[k]
		if !ok {
			position = len(groups)
			positions[k] = position
			groups = append(groups, organizeBatch{source: k.source, directory: k.directory})
		}
		groups[position].indexes = append(groups[position].indexes, i)
	}
	batches := make([]organizeBatch, 0, len(groups))
	for _, group := range groups {
		for start := 0; start < len(group.indexes); start += p115.DriveMutationBatchLimit {
			end := min(start+p115.DriveMutationBatchLimit, len(group.indexes))
			batches = append(batches, organizeBatch{source: group.source, directory: group.directory, indexes: group.indexes[start:end]})
		}
	}
	return batches
}

func (s *OrganizeEngine) runJob(ctx context.Context, job *OrganizeJob, cookie string) error {
	batches := organizeBatches(job.Items)
	if organizeStatsFrom(ctx) != &job.Stats {
		if job.Stats.StartedAt.IsZero() {
			job.Stats.StartedAt = time.Now()
		}
		ctx = withOrganizeStats(ctx, job)
	}
	job.Stats.BatchSize, job.Stats.BatchesTotal = p115.DriveMutationBatchLimit, job.Stats.BatchesCompleted+len(batches)
	job.Status = "running"
	if err := s.checkpoint(job); err != nil {
		return err
	}
	// Only directory IDs are reused across batches. Contents are always read
	// afresh for validation, preventing stale collision or source-file checks.
	directories := map[string]string{"": job.Config.TargetCID}
	for _, batch := range batches {
		if ctx.Err() != nil {
			job.Status, job.Message = "interrupted", "任务已中断，可在原任务详情中检查并继续"
			return s.checkpoint(job)
		}
		for _, i := range batch.indexes {
			job.Items[i].Status = "preparing"
		}
		// Persist batch intent before directory creation or file mutations.
		if err := s.checkpoint(job); err != nil {
			return err
		}
		ok, err := s.runBatch(ctx, job, batch, directories, cookie)
		if err != nil {
			return err
		} // A checkpoint failure stops the worker.
		if !ok {
			return nil
		} // The partial result has already been persisted.
		job.Stats.BatchesCompleted++
		if err := s.checkpoint(job); err != nil {
			return err
		}
	}
	job.Status, job.Message = "completed", "全部文件已整理完成"
	return s.checkpoint(job)
}

// Directory creation is covered by the persisted preparing intent. Resolve
// each path prefix only once and verify each newly created directory by ID.
func (s *OrganizeEngine) resolveDirectory(ctx context.Context, directory, accountID string, directories map[string]string, cookie string, create bool) (string, error) {
	cid, prefix := directories[""], ""
	if directory == "" {
		return cid, nil
	}
	for _, part := range strings.Split(directory, "/") {
		if prefix == "" {
			prefix = part
		} else {
			prefix += "/" + part
		}
		if cached, ok := directories[prefix]; ok {
			cid = cached
			continue
		}
		children, err := s.list(ctx, cid, cookie)
		if err != nil {
			return "", err
		}
		next, err := organizeFindDirectory(children, part)
		if err != nil {
			return "", err
		}
		if next == "" {
			if !create {
				return "", fmt.Errorf("原定目标目录不存在，未创建替代目录")
			}
			if _, err := s.account(accountID); err != nil {
				return "", err
			}
			started := time.Now()
			next, err = s.drive.CreateDirectory(ctx, cid, part, cookie)
			if stats := organizeStatsFrom(ctx); stats != nil {
				stats.DirectoryCreates++
				stats.MutationMS += time.Since(started).Milliseconds()
			}
			if err != nil {
				return "", fmt.Errorf("创建目录结果未确认，请检查目标目录后重新预览: %w", err)
			}
			children, err = s.list(ctx, cid, cookie)
			if err != nil {
				return "", err
			}
			verified, err := organizeFindDirectory(children, part)
			if err != nil || next == "" || verified != next {
				return "", fmt.Errorf("新建目录未确认或同名目录冲突，请检查目标目录")
			}
		}
		directories[prefix] = next
		cid = next
	}
	return cid, nil
}

// stopBatch preserves done/renamed/uncertain states instead of assigning a
// batch response to every row. Other batches remain pending and are not run.
func (s *OrganizeEngine) stopBatch(job *OrganizeJob, batch organizeBatch, message string) (bool, error) {
	for _, i := range batch.indexes {
		item := &job.Items[i]
		if item.Status == "preparing" {
			item.Status, item.Message = "blocked", "本批未修改文件："+message
		}
		if item.Status == "renamed" {
			item.Message = "已完成命名，尚未移动；请核对来源目录"
		}
	}
	job.Status, job.Message = "partial", message+"；后续批次未执行，可在原任务详情中检查并继续"
	return false, s.checkpoint(job)
}

func (s *OrganizeEngine) listBatchResult(ctx context.Context, job *OrganizeJob, batch organizeBatch, cid, cookie string) ([]p115.ShareFile, error) {
	return s.listUntil(ctx, cid, cookie, func(files []p115.ShareFile) bool {
		for _, i := range batch.indexes {
			item := &job.Items[i]
			if !organizeSourceMatches(files, item.File, item.Name) || organizeCollision(files, item.Name, item.File.ID) {
				return false
			}
		}
		return true
	})
}

func (s *OrganizeEngine) runBatch(ctx context.Context, job *OrganizeJob, batch organizeBatch, directories map[string]string, cookie string) (bool, error) {
	if _, err := s.account(job.Config.AccountID); err != nil {
		return s.stopBatch(job, batch, organizePublicError(err, cookie))
	}
	source, err := s.list(ctx, batch.source, cookie)
	if err != nil {
		return s.stopBatch(job, batch, organizePublicError(err, cookie))
	}
	// A previous write may have succeeded before its response/checkpoint was
	// lost. Accept only the same file ID, size and original or planned name.
	create := true
	for _, i := range batch.indexes {
		item := &job.Items[i]
		if slicesContainFile(source, item.File.ID) && !organizeSourceMatches(source, item.File, item.File.Name) && !organizeSourceMatches(source, item.File, item.Name) {
			return s.stopBatch(job, batch, "来源文件已变化："+item.File.Name)
		}
		if item.TargetCID != "" || !slicesContainFile(source, item.File.ID) {
			create = false
		}
	}
	cid, err := s.resolveDirectory(ctx, batch.directory, job.Config.AccountID, directories, cookie, create)
	if err != nil {
		return s.stopBatch(job, batch, organizePublicError(err, cookie))
	}
	for _, i := range batch.indexes {
		if job.Items[i].TargetCID != "" && job.Items[i].TargetCID != cid {
			return s.stopBatch(job, batch, "原定目标目录已变化，未改用新的目录")
		}
		job.Items[i].TargetCID = cid
	}
	target := source
	if cid != batch.source {
		target, err = s.list(ctx, cid, cookie)
		if err != nil {
			return s.stopBatch(job, batch, organizePublicError(err, cookie))
		}
	}
	remaining := make([]int, 0, len(batch.indexes))
	seenNames := make(map[string]bool, len(batch.indexes))
	for _, i := range batch.indexes {
		item := &job.Items[i]
		if organizeCollision(target, item.Name, item.File.ID) {
			return s.stopBatch(job, batch, "目标已有同名条目："+item.Name)
		}
		if organizeSourceMatches(target, item.File, item.Name) {
			if cid != batch.source && slicesContainFile(source, item.File.ID) {
				return s.stopBatch(job, batch, "来源与目标同时返回同一文件，稍后再检查："+item.Name)
			}
			item.Status, item.Message = "done", "已核实原文件位于目标目录"
			continue
		}
		if !organizeSourceMatches(source, item.File, item.File.Name) && !organizeSourceMatches(source, item.File, item.Name) {
			return s.stopBatch(job, batch, "来源与原定目标中无法确认文件："+item.File.Name)
		}
		if organizeCollision(source, item.Name, item.File.ID) || seenNames[strings.ToLower(item.Name)] {
			return s.stopBatch(job, batch, "来源目录重命名将产生同名文件："+item.Name)
		}
		seenNames[strings.ToLower(item.Name)] = true
		item.Message = ""
		remaining = append(remaining, i)
	}
	batch.indexes = remaining
	if len(remaining) == 0 {
		return true, nil
	}
	renames := make([]p115.RenameTarget, 0, len(batch.indexes))
	for _, i := range batch.indexes {
		item := &job.Items[i]
		if !organizeSourceMatches(source, item.File, item.Name) {
			renames = append(renames, p115.RenameTarget{ID: item.File.ID, Name: item.Name})
			item.Status = "renaming"
		}
	}
	if len(renames) > 0 {
		if err := s.checkpoint(job); err != nil {
			return false, err
		}
		if _, err := s.account(job.Config.AccountID); err != nil {
			return s.stopBatch(job, batch, organizePublicError(err, cookie))
		}
		started := time.Now()
		writeErr := s.drive.RenameEntries(ctx, renames, cookie)
		job.Stats.RenameRequests++
		job.Stats.MutationMS += time.Since(started).Milliseconds()
		// Never infer full success/failure from the batch envelope. A timeout
		// or error can follow partial success, so reconcile every planned ID.
		files, readErr := s.listBatchResult(ctx, job, batch, batch.source, cookie)
		confirmed := true
		for _, i := range batch.indexes {
			item := &job.Items[i]
			if readErr == nil && organizeSourceMatches(files, item.File, item.Name) && !organizeCollision(files, item.Name, item.File.ID) {
				item.Status = "renamed"
				if cid == batch.source {
					item.Status = "done"
				}
			} else {
				confirmed = false
				item.Status, item.Message = "uncertain", "批量重命名结果未确认，请核对来源目录，勿重复执行"
				if writeErr != nil {
					item.Message += "：" + organizePublicError(writeErr, cookie)
				}
			}
		}
		if !confirmed {
			return s.stopBatch(job, batch, "本批重命名未全部确认")
		}
	} else {
		for _, i := range batch.indexes {
			job.Items[i].Status = "renamed"
		}
	}
	if cid == batch.source {
		for _, i := range batch.indexes {
			job.Items[i].Status = "done"
		}
		return true, nil
	}
	// Re-read after rename. With no rename, the just-validated target snapshot
	// is still current and there was no intervening upstream operation.
	if len(renames) > 0 {
		target, err = s.list(ctx, cid, cookie)
		if err != nil {
			return s.stopBatch(job, batch, "文件已命名，读取目标失败："+organizePublicError(err, cookie))
		}
	}
	for _, i := range batch.indexes {
		item := &job.Items[i]
		if organizeCollision(target, item.Name, item.File.ID) {
			return s.stopBatch(job, batch, "文件已命名，目标发生同名冲突："+item.Name)
		}
	}
	ids := make([]string, 0, len(batch.indexes))
	for _, i := range batch.indexes {
		job.Items[i].Status = "moving"
		ids = append(ids, job.Items[i].File.ID)
	}
	// This checkpoint includes all rename outcomes and precise move intent.
	if err := s.checkpoint(job); err != nil {
		return false, err
	}
	if _, err := s.account(job.Config.AccountID); err != nil {
		return s.stopBatch(job, batch, organizePublicError(err, cookie))
	}
	started := time.Now()
	writeErr := s.drive.MoveEntriesBatch(ctx, ids, cid, cookie)
	job.Stats.MoveRequests++
	job.Stats.MutationMS += time.Since(started).Milliseconds()
	files, readErr := s.listBatchResult(ctx, job, batch, cid, cookie)
	confirmed := true
	for _, i := range batch.indexes {
		item := &job.Items[i]
		if readErr == nil && organizeSourceMatches(files, item.File, item.Name) && !organizeCollision(files, item.Name, item.File.ID) {
			item.Status = "done"
		} else {
			confirmed = false
			item.Status, item.Message = "uncertain", "批量移动结果未确认，请核对来源和目标目录，勿重复执行"
			if writeErr != nil {
				item.Message += "：" + organizePublicError(writeErr, cookie)
			}
		}
	}
	if !confirmed {
		return s.stopBatch(job, batch, "本批移动未全部确认")
	}
	return true, nil
}

func slicesContainFile(files []p115.ShareFile, id string) bool {
	for _, file := range files {
		if file.ID == id {
			return true
		}
	}
	return false
}
