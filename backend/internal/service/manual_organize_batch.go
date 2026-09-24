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

func (s *ManualOrganize) runJob(ctx context.Context, job *OrganizeJob, cookie string) error {
	batches := organizeBatches(job.Items)
	job.Stats = OrganizeStats{BatchSize: p115.DriveMutationBatchLimit, BatchesTotal: len(batches), StartedAt: time.Now()}
	ctx = context.WithValue(ctx, organizeStatsKey{}, &job.Stats)
	ctx = p115.WithRequestTiming(ctx, func(t p115.RequestTiming) {
		job.Stats.HTTPRequests += t.Requests
		job.Stats.HTTPMS += t.HTTP.Milliseconds()
		job.Stats.WaitMS += t.Wait.Milliseconds()
	})
	job.Status = "running"
	if err := s.checkpoint(job); err != nil {
		return err
	}
	// Only directory IDs are reused across batches. Contents are always read
	// afresh for validation, preventing stale collision or source-file checks.
	directories := map[string]string{"": job.Config.TargetCID}
	for _, batch := range batches {
		if ctx.Err() != nil {
			job.Status, job.Message = "interrupted", "任务已中断，请核对文件当前位置后重新扫描"
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
func (s *ManualOrganize) resolveDirectory(ctx context.Context, directory string, directories map[string]string, cookie string) (string, error) {
	cid, prefix := directories[""], ""
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
func (s *ManualOrganize) stopBatch(job *OrganizeJob, batch organizeBatch, message string) (bool, error) {
	for _, i := range batch.indexes {
		item := &job.Items[i]
		if item.Status == "preparing" {
			item.Status, item.Message = "blocked", "本批未修改文件："+message
		}
		if item.Status == "renamed" {
			item.Message = "已完成命名，尚未移动；请核对来源目录"
		}
	}
	job.Status, job.Message = "partial", message+"；后续批次未执行，请核对记录后重新扫描"
	return false, s.checkpoint(job)
}

func (s *ManualOrganize) runBatch(ctx context.Context, job *OrganizeJob, batch organizeBatch, directories map[string]string, cookie string) (bool, error) {
	source, err := s.list(ctx, batch.source, cookie)
	if err != nil {
		return s.stopBatch(job, batch, organizePublicError(err, cookie))
	}
	// Validate every row before changing anything in this batch. Even two
	// planned files cannot temporarily rename onto one another's source names.
	seenNames := make(map[string]bool, len(batch.indexes))
	for _, i := range batch.indexes {
		item := &job.Items[i]
		if !organizeSourceMatches(source, item.File, item.File.Name) {
			return s.stopBatch(job, batch, "来源文件已变化："+item.File.Name)
		}
		if organizeCollision(source, item.Name, item.File.ID) || seenNames[strings.ToLower(item.Name)] {
			return s.stopBatch(job, batch, "来源目录重命名将产生同名文件："+item.Name)
		}
		seenNames[strings.ToLower(item.Name)] = true
	}
	cid, err := s.resolveDirectory(ctx, batch.directory, directories, cookie)
	if err != nil {
		return s.stopBatch(job, batch, organizePublicError(err, cookie))
	}
	for _, i := range batch.indexes {
		job.Items[i].TargetCID = cid
	}
	target := source
	if cid != batch.source {
		target, err = s.list(ctx, cid, cookie)
		if err != nil {
			return s.stopBatch(job, batch, organizePublicError(err, cookie))
		}
	}
	for _, i := range batch.indexes {
		item := &job.Items[i]
		if organizeCollision(target, item.Name, item.File.ID) {
			return s.stopBatch(job, batch, "目标已有同名条目："+item.Name)
		}
	}
	renames := make([]p115.RenameTarget, 0, len(batch.indexes))
	for _, i := range batch.indexes {
		item := &job.Items[i]
		if item.Name != item.File.Name {
			renames = append(renames, p115.RenameTarget{ID: item.File.ID, Name: item.Name})
			item.Status = "renaming"
		}
	}
	if len(renames) > 0 {
		if err := s.checkpoint(job); err != nil {
			return false, err
		}
		started := time.Now()
		writeErr := s.drive.RenameEntries(ctx, renames, cookie)
		job.Stats.RenameRequests++
		job.Stats.MutationMS += time.Since(started).Milliseconds()
		// Never infer full success/failure from the batch envelope. A timeout
		// or error can follow partial success, so reconcile every planned ID.
		files, readErr := s.list(ctx, batch.source, cookie)
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
	started := time.Now()
	writeErr := s.drive.MoveEntriesBatch(ctx, ids, cid, cookie)
	job.Stats.MoveRequests++
	job.Stats.MutationMS += time.Since(started).Milliseconds()
	files, readErr := s.list(ctx, cid, cookie)
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
