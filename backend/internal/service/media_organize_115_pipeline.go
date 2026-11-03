package service

import (
	"context"
	"fmt"
	"maps"
	"time"

	"strm115/backend/internal/client/p115"
)

type media115DirectoryDrive interface {
	ReadDirectoryFiles(context.Context, string, string) (p115.DriveDirectory, []p115.ShareFile, error)
}

type media115ScanDirectory struct {
	id       string
	relative string
	depth    int
}

// 当前目录完整读取并核对身份后立即整理，跨目录附件与删除证明等待整队列成功。
func (s *MediaOrganizer) process115Auto(ctx context.Context, files *mediaOrganizeFiles, run *MediaOrganizeRun) (processErr error) {
	run.Status, run.streaming = "scanning", true
	files.cloudEmpty = nil
	files.lists = make(map[string][]p115.ShareFile)
	files.naming = make(map[string]preparedMediaNaming)
	files.recognized = make(map[string]bool)
	files.pending115, files.pending115Dirs = s.pending115Tasks(run)
	defer func() {
		run.streaming = false
		files.scan115ID, files.scan115Relative, files.shallow115 = "", "", false
		if processErr != nil {
			if files.cloudEmpty != nil {
				files.cloudEmpty.complete = false
			}
			// 缺少新清理能力的适配器也不得从失败扫描的部分目录推导删除。
			files.directories, files.cleanupDirectories = nil, nil
		}
	}()
	if err := s.checkpoint(run); err != nil {
		return err
	}
	queue := []media115ScanDirectory{{id: run.Rule.SourceID}}
	visited := make(map[string]bool)
	deferred := make([]MediaOrganizeEntry, 0)
	unrelated := make([]MediaOrganizeEntry, 0)
	directories := make([]MediaOrganizeEntry, 0)
	allScanned := make([]MediaOrganizeEntry, 0)
	allCandidates := make([]OrganizeFile, 0)
	allSettling, allPending := make(map[string]bool), make(map[string]bool)
	fileCount := 0
	for len(queue) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		current := queue[0]
		queue = queue[1:]
		if visited[current.id] || len(visited) >= 300 || current.depth > 12 {
			return fmt.Errorf("115 目录循环或范围过大，请缩小扫描范围")
		}
		visited[current.id] = true
		files.scan115ID, files.scan115Relative, files.shallow115 = current.id, current.relative, true
		files.unrelated, files.directories = nil, nil
		entries, err := files.scan(ctx)
		if err != nil {
			return err
		}
		for _, file := range files.lists[current.id] {
			if !file.Directory {
				fileCount++
			}
		}
		if fileCount > 5000 {
			return fmt.Errorf("单次最多 5000 个文件，请缩小扫描范围")
		}
		for _, child := range files.directories {
			queue = append(queue, media115ScanDirectory{id: child.File.ID, relative: child.File.SourcePath, depth: current.depth + 1})
		}
		directories = append(directories, files.directories...)
		allCandidates = append(allCandidates, files.localCandidates...)
		allScanned = append(allScanned, entries...)
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
		if err := s.processMediaBatch(ctx, files, run, immediate, true); err != nil {
			return err
		}
		unrelated = append(unrelated, files.unrelated...)
	}
	files.unrelated, files.directories = unrelated, directories
	files.localCandidates, files.settlingVideos, files.localPending = allCandidates, allSettling, allPending
	files.scanned = allScanned
	files.scan115ID, files.scan115Relative, files.shallow115 = "", "", false
	if len(deferred) > 0 {
		deferred = files.holdLocalCompanions(deferred, allPending)
		if err := s.processMediaBatch(ctx, files, run, deferred, false); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	for i := range s.doc.Rules {
		if s.doc.Rules[i].ID == run.Rule.ID {
			s.doc.Rules[i].LastScan = time.Now()
		}
	}
	s.mu.Unlock()
	if files.cloudEmpty != nil {
		files.cloudEmpty.complete = true
	}
	if err := s.prepareOrphanCleanup(files, run); err != nil {
		return err
	}
	if len(files.unrelated) > 0 {
		if err := s.cleanup(ctx, files, run); err != nil {
			return err
		}
	}
	run.streaming = false
	mediaFinishRun(run)
	if len(run.Entries) == 0 {
		run.Message = "没有新的可处理文件"
		if len(allPending) > 0 {
			run.Message = fmt.Sprintf("有 %d 个临时文件尚未完成，留待后续扫描", len(allPending))
		}
	}
	return nil
}

// 旧任务的外部写入尚未确认时，仅显式重试可继续；自动扫描保护整个视频任务的来源。
func (s *MediaOrganizer) pending115Tasks(run *MediaOrganizeRun) (map[string]bool, map[string]bool) {
	pending, directories := make(map[string]bool), make(map[string]bool)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, previous := range s.doc.Runs {
		if previous.ID == run.ID || previous.Rule.Storage != "115" || previous.Rule.AccountID == "" || previous.Rule.AccountID != run.Rule.AccountID {
			continue
		}
		tasks := make(map[string]bool)
		for _, entry := range previous.Entries {
			if entry.P115Move == nil {
				continue
			}
			pending[entry.File.ID] = true
			if entry.File.ParentID != "" {
				directories[entry.File.ParentID] = true
			}
			if entry.File.MediaType == "video" {
				tasks[entry.File.ID] = true
			} else if entry.VideoID != "" {
				tasks[entry.VideoID] = true
			}
		}
		for _, entry := range previous.Entries {
			if entry.File.MediaType == "video" && tasks[entry.File.ID] && entry.File.ParentID != "" {
				directories[entry.File.ParentID] = true
			}
			if entry.Status != "done" && (tasks[entry.File.ID] || tasks[entry.VideoID]) {
				pending[entry.File.ID] = true
			}
		}
	}
	return pending, directories
}
