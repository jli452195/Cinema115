package clouddrive2

import (
	"context"
	"errors"
	"fmt"
	"path"
	"time"

	"strm115/backend/internal/client/clouddrive2/pb"
)

// EmptyDirectoryFilesystem 在来源目录整理后提供空目录复核与清理能力。
type EmptyDirectoryFilesystem interface {
	RemoveEmptyDirectory(context.Context, string, string) (bool, error)
}

// EmptyDirectoryRequest 只声明本轮已确认范围内的一个目录及其原生身份。
type EmptyDirectoryRequest struct {
	Path string
	ID   string
}

// EmptyDirectoryResult 保留逐目录的删除确认和错误，不能以批量回执推断成功。
type EmptyDirectoryResult struct {
	Path    string
	Removed bool
	Err     error
}

// BatchEmptyDirectoryFilesystem 允许同父目录共享实时身份读取和结果复核。
type BatchEmptyDirectoryFilesystem interface {
	RemoveEmptyDirectories(context.Context, []EmptyDirectoryRequest) []EmptyDirectoryResult
}

// RemoveEmptyDirectory 复用批量核验，并保留单目录调用遇到错误时不报告成功的语义。
func (c *Client) RemoveEmptyDirectory(ctx context.Context, full, expectedID string) (bool, error) {
	result := c.RemoveEmptyDirectories(ctx, []EmptyDirectoryRequest{{Path: full, ID: expectedID}})[0]
	return result.Removed && result.Err == nil, result.Err
}

// RemoveEmptyDirectories 每组最多复核二十个同父目录的候选，返回顺序与请求一致。
// 官方删除接口没有原子的空目录条件，外部客户端仍可能在复核后写入内容。
func (c *Client) RemoveEmptyDirectories(ctx context.Context, requests []EmptyDirectoryRequest) []EmptyDirectoryResult {
	results := make([]EmptyDirectoryResult, len(requests))
	groups := make(map[string][]int, len(requests))
	parents := make([]string, 0, len(requests))
	seen := make(map[string]bool, len(requests))
	for i, request := range requests {
		results[i].Path = request.Path
		full, err := CleanPath(request.Path)
		if err == nil && full == "/" {
			err = fmt.Errorf("%w：不能删除 CD2 根目录", ErrInput)
		}
		if err == nil && seen[full] {
			err = fmt.Errorf("%w：空目录清理请求重复", ErrInput)
		}
		if err != nil {
			results[i].Err = err
			continue
		}
		results[i].Path = full
		seen[full] = true
		parent := path.Dir(full)
		if _, ok := groups[parent]; !ok {
			parents = append(parents, parent)
		}
		groups[parent] = append(groups[parent], i)
	}
	for _, parent := range parents {
		indices := groups[parent]
		for len(indices) != 0 {
			count := min(len(indices), 20)
			c.removeEmptyDirectoryGroup(ctx, parent, requests, results, indices[:count])
			indices = indices[count:]
		}
	}
	return results
}

func (c *Client) removeEmptyDirectoryGroup(ctx context.Context, parent string, requests []EmptyDirectoryRequest, results []EmptyDirectoryResult, indices []int) {
	// 整组允许完成多次实时读取，每次列表和删除 RPC 仍受各自二十秒超时约束。
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	paths := make([]string, 1, len(indices)+1)
	paths[0] = parent
	for _, index := range indices {
		paths = append(paths, results[index].Path)
	}
	setErrors := func(indices []int, err error) {
		for _, index := range indices {
			results[index].Err = errors.Join(results[index].Err, err)
		}
	}
	release, err := c.lockWrite(ctx, paths...)
	if err != nil {
		setErrors(indices, err)
		return
	}
	defer release()
	entries, err := c.list(ctx, parent, true)
	if errors.Is(err, ErrNotFound) {
		for _, index := range indices {
			results[index].Removed = true
		}
		return
	}
	if err != nil {
		setErrors(indices, err)
		return
	}
	directories := make(map[string]File, len(entries))
	for _, entry := range entries {
		directories[entry.Path] = entry
	}
	candidates := make([]int, 0, len(indices))
	verify := make([]int, 0, len(indices))
	for _, index := range indices {
		if err := ctx.Err(); err != nil {
			results[index].Err = err
			continue
		}
		full := results[index].Path
		directory, exists := directories[full]
		if !exists {
			results[index].Removed = true
			continue
		}
		if !directory.Directory || directory.ReadOnly || requests[index].ID != "" && directory.ID != requests[index].ID {
			continue
		}
		children, err := c.list(ctx, full, true)
		if errors.Is(err, ErrNotFound) {
			// 列子项时路径消失或变为文件，统一从父目录重新确认，不能假定已删除。
			verify = append(verify, index)
			continue
		}
		if err != nil {
			results[index].Err = err
			continue
		}
		if len(children) == 0 {
			candidates = append(candidates, index)
		}
	}
	if len(candidates) != 0 {
		if err := ctx.Err(); err != nil {
			setErrors(candidates, err)
			setErrors(verify, err)
			return
		}
		deletions := make([]string, 0, len(candidates))
		for _, index := range candidates {
			deletions = append(deletions, results[index].Path)
		}
		result, err := func() (*pb.FileOperationResult, error) {
			work, cancel := c.request(ctx)
			defer cancel()
			if len(deletions) == 1 {
				return c.rpc.DeleteFile(work, &pb.FileRequest{Path: deletions[0], ForceRefresh: boolValue(true)})
			}
			return c.rpc.DeleteFiles(work, &pb.MultiFileRequest{Path: deletions})
		}()
		c.invalidateDirectories(append([]string{parent}, deletions...)...)
		if err != nil {
			err = c.wrap("清理空目录", err)
		} else {
			err = c.result("清理空目录", result)
		}
		setErrors(candidates, err)
		verify = append(verify, candidates...)
	}
	if len(verify) == 0 {
		return
	}
	// 无论批量回执是否成功，都只实时核验一次；不根据回执重放任何删除。
	remaining, err := c.list(ctx, parent, true)
	if err != nil && !errors.Is(err, ErrNotFound) {
		setErrors(verify, fmt.Errorf("核对空目录删除结果: %w", err))
		return
	}
	existing := make(map[string]bool, len(remaining))
	for _, entry := range remaining {
		existing[entry.Path] = true
	}
	for _, index := range verify {
		if !existing[results[index].Path] {
			results[index].Removed = true
			continue
		}
		results[index].Err = errors.Join(results[index].Err, errors.New("CloudDrive2 尚未确认空目录已删除"))
	}
}
