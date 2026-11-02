package clouddrive2

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"

	"strm115/backend/internal/client/clouddrive2/pb"
)

type RenameOperation struct {
	Source string
	Name   string
}

// BatchFileResult 仅在目标身份与来源移除均已核实时返回 File；Err 非空时不得盲目重放。
type BatchFileResult struct {
	Source string
	Target string
	File   File
	Err    error
}

// BatchFilesystem 是可选能力，旧版适配器与单文件调用无需实现批量接口。
type BatchFilesystem interface {
	RenameMany(context.Context, []RenameOperation) ([]BatchFileResult, error)
	MoveMany(context.Context, []string, string) ([]BatchFileResult, error)
}

type batchDirectory struct {
	files map[string]File
	err   error
}

// batchSnapshot 在一次预检或结果核对中，每个父目录只读取一次。
type batchSnapshot struct {
	client      *Client
	directories map[string]batchDirectory
}

func (c *Client) newBatchSnapshot() *batchSnapshot {
	return &batchSnapshot{client: c, directories: make(map[string]batchDirectory)}
}

func (s *batchSnapshot) file(ctx context.Context, full string) (File, error) {
	if err := ctx.Err(); err != nil {
		return File{}, err
	}
	if full == "/" {
		return s.client.Stat(ctx, full, false)
	}
	directory := path.Dir(full)
	snapshot, ok := s.directories[directory]
	if !ok {
		files, err := s.client.List(ctx, directory, true)
		snapshot = batchDirectory{files: make(map[string]File, len(files)), err: err}
		for _, file := range files {
			snapshot.files[file.Path] = file
		}
		s.directories[directory] = snapshot
	}
	if snapshot.err != nil {
		return File{}, snapshot.err
	}
	file, exists := snapshot.files[full]
	if !exists {
		return File{}, ErrNotFound
	}
	return file, nil
}

func batchPaths(results []BatchFileResult) error {
	sources := make(map[string]bool, len(results))
	targets := make(map[string]bool, len(results))
	for _, result := range results {
		if result.Source == "/" || sources[result.Source] || targets[result.Target] {
			return fmt.Errorf("%w：批量来源或目标路径重复", ErrInput)
		}
		sources[result.Source], targets[result.Target] = true, true
	}
	for i, result := range results {
		if result.Source != result.Target && sources[result.Target] {
			return fmt.Errorf("%w：批量操作不能依赖同批来源改名", ErrInput)
		}
		for j := 0; j < i; j++ {
			if overlappingDirectories(result.Source, results[j].Source) {
				return fmt.Errorf("%w：批量来源路径不能互相包含", ErrInput)
			}
		}
	}
	return nil
}

// RenameMany 使用官方批量接口；返回逐文件核对结果，不把聚合成功当作全部成功。
func (c *Client) RenameMany(ctx context.Context, operations []RenameOperation) ([]BatchFileResult, error) {
	ctx = withDirectoryCache(ctx)
	if len(operations) == 0 || len(operations) > 100 {
		return nil, fmt.Errorf("%w：每批重命名 1 至 100 项", ErrInput)
	}
	results := make([]BatchFileResult, 0, len(operations))
	directories := make([]string, 0, len(operations))
	for _, operation := range operations {
		source, err := CleanPath(operation.Source)
		if err != nil {
			return nil, err
		}
		if err := cleanName(operation.Name); err != nil {
			return nil, err
		}
		results = append(results, BatchFileResult{Source: source, Target: path.Join(path.Dir(source), operation.Name)})
		directories = append(directories, path.Dir(source))
	}
	if err := batchPaths(results); err != nil {
		return nil, err
	}
	release, err := c.lockWrite(ctx, directories...)
	if err != nil {
		return nil, err
	}
	defer release()
	snapshot := c.newBatchSnapshot()
	before := make([]File, len(results))
	requests := make([]*pb.RenameFileRequest, 0, len(results))
	for i, result := range results {
		file, err := snapshot.file(ctx, result.Source)
		if err != nil {
			return nil, err
		}
		if file.ReadOnly {
			return nil, errors.New("CloudDrive2 来源不可写")
		}
		before[i] = file
		if result.Source == result.Target {
			results[i].File = file
			continue
		}
		if _, err := snapshot.file(ctx, result.Target); !errors.Is(err, ErrNotFound) {
			if err != nil {
				return nil, err
			}
			return nil, ErrExists
		}
		requests = append(requests, &pb.RenameFileRequest{TheFilePath: result.Source, NewName: path.Base(result.Target)})
	}
	if len(requests) == 0 {
		return results, nil
	}
	requestCtx, cancel := c.request(ctx)
	reply, rpcErr := c.rpc.RenameFiles(requestCtx, &pb.RenameFilesRequest{RenameFiles: requests})
	cancel()
	c.invalidateDirectories(directories...)
	if rpcErr != nil {
		rpcErr = c.wrap("批量重命名", rpcErr)
	} else {
		rpcErr = c.result("批量重命名", reply)
	}
	return c.verifyBatch(ctx, results, before, nil, rpcErr)
}

// MoveMany 将同一目标目录的文件一次提交，并在聚合失败后仍核对已完成项。
func (c *Client) MoveMany(ctx context.Context, sources []string, destination string) ([]BatchFileResult, error) {
	ctx = withDirectoryCache(ctx)
	destination, err := CleanPath(destination)
	if err != nil {
		return nil, err
	}
	if len(sources) == 0 || len(sources) > 100 {
		return nil, fmt.Errorf("%w：每批移动 1 至 100 项", ErrInput)
	}
	results := make([]BatchFileResult, 0, len(sources))
	directories := make([]string, 0, len(sources)+1)
	directories = append(directories, destination)
	for _, source := range sources {
		source, err = CleanPath(source)
		if err != nil {
			return nil, err
		}
		target := path.Join(destination, path.Base(source))
		if source == target {
			return nil, ErrExists
		}
		if destination == source || strings.HasPrefix(destination, source+"/") {
			return nil, fmt.Errorf("%w：移动目标不能位于来源之内", ErrInput)
		}
		results = append(results, BatchFileResult{Source: source, Target: target})
		directories = append(directories, path.Dir(source))
	}
	if err := batchPaths(results); err != nil {
		return nil, err
	}
	release, err := c.lockWrite(ctx, directories...)
	if err != nil {
		return nil, err
	}
	defer release()
	snapshot := c.newBatchSnapshot()
	dest, err := snapshot.file(ctx, destination)
	if err != nil {
		return nil, err
	}
	if !dest.Directory || dest.ReadOnly {
		return nil, errors.New("CloudDrive2 目标目录不可写")
	}
	before := make([]File, 0, len(results))
	paths := make([]string, 0, len(results))
	for _, result := range results {
		if _, err := snapshot.file(ctx, result.Target); !errors.Is(err, ErrNotFound) {
			if err != nil {
				return nil, err
			}
			return nil, ErrExists
		}
		file, err := snapshot.file(ctx, result.Source)
		if err != nil {
			return nil, err
		}
		if file.ReadOnly {
			return nil, errors.New("CloudDrive2 来源不可写")
		}
		across := file.CloudName != dest.CloudName || file.CloudAccount != dest.CloudAccount
		if across && (file.Directory || !hasHash(file)) {
			return nil, errors.New("CD2 跨存储移动需要可核验的文件哈希，请选择同一存储内的目录")
		}
		before, paths = append(before, file), append(paths, result.Source)
	}
	requestCtx, cancel := c.request(ctx)
	policy := pb.MoveFileRequest_Skip
	reply, rpcErr := c.rpc.MoveFile(requestCtx, &pb.MoveFileRequest{TheFilePaths: paths, DestPath: destination, ConflictPolicy: &policy, MoveAcrossClouds: boolValue(true)})
	cancel()
	c.invalidateDirectories(directories...)
	if rpcErr != nil {
		rpcErr = c.wrap("批量移动", rpcErr)
	} else {
		rpcErr = c.result("批量移动", reply)
	}
	return c.verifyBatch(ctx, results, before, &dest, rpcErr)
}

func (c *Client) verifyBatch(ctx context.Context, results []BatchFileResult, before []File, destination *File, rpcErr error) ([]BatchFileResult, error) {
	// 提交后已使目录版本失效，核对所得新快照仍供本次整理后续步骤复用。
	ctx = withDirectoryCache(ctx)
	snapshot := c.newBatchSnapshot()
	failures := make([]error, 0, len(results)+1)
	for i := range results {
		result := &results[i]
		if result.Source == result.Target {
			continue
		}
		after, err := snapshot.file(ctx, result.Target)
		if err == nil {
			across := destination != nil && (before[i].CloudName != destination.CloudName || before[i].CloudAccount != destination.CloudAccount)
			if !sameFile(before[i], after, across) {
				err = errors.New("CloudDrive2 批量操作目标身份未确认")
			}
		}
		if err == nil {
			_, sourceErr := snapshot.file(ctx, result.Source)
			if sourceErr == nil {
				err = errors.New("CloudDrive2 尚未确认来源路径已移除")
			} else if !errors.Is(sourceErr, ErrNotFound) {
				err = sourceErr
			}
		}
		if err != nil {
			result.Err = fmt.Errorf("CloudDrive2 文件结果待核对: %w", err)
			failures = append(failures, result.Err)
			continue
		}
		result.File = after
	}
	if len(failures) == 0 {
		return results, nil
	}
	if rpcErr != nil {
		failures = append(failures, rpcErr)
	}
	return results, errors.Join(failures...)
}
