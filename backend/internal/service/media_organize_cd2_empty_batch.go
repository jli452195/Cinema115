package service

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"sync"
	"time"

	"strm115/backend/internal/client/clouddrive2"
)

type mediaCD2EmptyDirectoryJob struct {
	requests []clouddrive2.EmptyDirectoryRequest
	indices  []int
	results  []clouddrive2.EmptyDirectoryResult
}

// 每层先保存整批意图，再并行处理互不相关的父目录；结果汇总后才处理更浅的父目录。
func (s *MediaOrganizer) cleanupCD2EmptyDirectories(ctx context.Context, files *mediaOrganizeFiles, run *MediaOrganizeRun, ordered []MediaOrganizeEntry) error {
	remaining := files.cd2RemainingDirectoryChildren(run)
	var cleanupErr error
	for start := 0; start < len(ordered); {
		if err := ctx.Err(); err != nil {
			return errors.Join(cleanupErr, err)
		}
		end := start + 1
		depth := strings.Count(ordered[start].File.SourcePath, "/")
		for end < len(ordered) && strings.Count(ordered[end].File.SourcePath, "/") == depth {
			end++
		}
		jobs := make([]mediaCD2EmptyDirectoryJob, 0)
		parents := make(map[string]int)
		for _, entry := range ordered[start:end] {
			full, err := mediaCD2Relative(run.Rule.Source, entry.File.SourcePath)
			if err != nil || full != entry.File.ID || full == run.Rule.Target || full == run.Rule.Failure {
				cleanupErr = errors.Join(cleanupErr, fmt.Errorf("CD2 空目录路径无法确认"))
				continue
			}
			// 未完整扫描的分支保持未知，交给实时复核；已知仍有子项的目录不再请求网盘。
			if children, known := remaining[full]; known && len(children) > 0 {
				continue
			}
			parent := path.Dir(full)
			index, exists := parents[parent]
			if !exists {
				index = len(jobs)
				parents[parent] = index
				jobs = append(jobs, mediaCD2EmptyDirectoryJob{})
			}
			entry.Status, entry.Message = "error", "CD2 清理结果待核实；下轮监控会重新扫描"
			entry.Result = &MediaOrganizeResult{Status: entry.Status, Message: entry.Message, At: time.Now()}
			jobs[index].requests = append(jobs[index].requests, clouddrive2.EmptyDirectoryRequest{Path: full, ID: entry.CD2FileID})
			jobs[index].indices = append(jobs[index].indices, len(run.Entries))
			run.Entries = append(run.Entries, entry)
		}
		start = end
		if len(jobs) == 0 {
			continue
		}
		if err := s.checkpoint(run); err != nil {
			return errors.Join(cleanupErr, err)
		}
		files.removeCD2EmptyDirectoryJobs(ctx, jobs)
		for _, job := range jobs {
			results := make(map[string]clouddrive2.EmptyDirectoryResult, len(job.results))
			for _, result := range job.results {
				if _, exists := results[result.Path]; exists {
					result.Removed, result.Err = false, fmt.Errorf("CD2 返回重复的空目录清理结果")
				}
				results[result.Path] = result
			}
			for i, request := range job.requests {
				result, exists := results[request.Path]
				if !exists {
					result.Err = fmt.Errorf("CD2 未返回该空目录的清理结果")
				}
				entry := &run.Entries[job.indices[i]]
				if result.Removed {
					entry.Status, entry.Message = "done", "已通过 CD2 删除空文件夹"
					delete(remaining[path.Dir(request.Path)], request.Path)
				} else if result.Err == nil {
					entry.Status, entry.Message, entry.Result = "retained", "目录非空或身份已变化，已保留来源", nil
				}
				if result.Err != nil {
					entry.Message += "：" + result.Err.Error()
					cleanupErr = errors.Join(cleanupErr, result.Err)
				}
				if entry.Status != "retained" {
					entry.Result = &MediaOrganizeResult{Status: entry.Status, Message: entry.Message, At: time.Now()}
				}
			}
		}
		if err := s.checkpoint(run); err != nil {
			return errors.Join(cleanupErr, err)
		}
	}
	return errors.Join(cleanupErr, ctx.Err())
}

func (f *mediaOrganizeFiles) removeCD2EmptyDirectoryJobs(ctx context.Context, jobs []mediaCD2EmptyDirectoryJob) {
	bulk, batched := f.cd2.(clouddrive2.BatchEmptyDirectoryFilesystem)
	single, singleSupported := f.cd2.(clouddrive2.EmptyDirectoryFilesystem)
	process := func(index int) {
		job := &jobs[index]
		job.results = make([]clouddrive2.EmptyDirectoryResult, 0, len(job.requests))
		for start := 0; start < len(job.requests); start += mediaCD2BatchSize {
			requests := job.requests[start:min(start+mediaCD2BatchSize, len(job.requests))]
			work, cancel := context.WithTimeout(ctx, 2*time.Minute)
			if batched {
				job.results = append(job.results, bulk.RemoveEmptyDirectories(work, requests)...)
			} else {
				for _, request := range requests {
					result := clouddrive2.EmptyDirectoryResult{Path: request.Path}
					if singleSupported {
						result.Removed, result.Err = single.RemoveEmptyDirectory(work, request.Path, request.ID)
					} else {
						result.Err = fmt.Errorf("当前 CD2 连接不支持空目录复核清理")
					}
					job.results = append(job.results, result)
				}
			}
			cancel()
			if ctx.Err() != nil {
				return
			}
		}
	}
	// 旧连接的单目录能力保持串行；官方批量能力只在不同父目录间使用有界并发。
	if !batched || len(jobs) == 1 {
		for index := range jobs {
			process(index)
		}
		return
	}
	queue := make(chan int)
	var workers sync.WaitGroup
	for range min(4, len(jobs)) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range queue {
				process(index)
			}
		}()
	}
	for index := range jobs {
		select {
		case queue <- index:
		case <-ctx.Done():
			close(queue)
			workers.Wait()
			return
		}
	}
	close(queue)
	workers.Wait()
}
