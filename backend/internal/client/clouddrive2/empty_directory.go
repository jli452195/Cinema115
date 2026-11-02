package clouddrive2

import (
	"context"
	"errors"
	"fmt"
	"path"

	"strm115/backend/internal/client/clouddrive2/pb"
)

// EmptyDirectoryFilesystem 在来源目录整理后提供空目录复核与清理能力。
type EmptyDirectoryFilesystem interface {
	RemoveEmptyDirectory(context.Context, string, string) (bool, error)
}

// RemoveEmptyDirectory 在目录写锁内绕过短期快照，完整刷新后才删除单个空目录。
// 官方删除接口没有原子的空目录条件，外部客户端仍可能在复核后写入内容。
func (c *Client) RemoveEmptyDirectory(ctx context.Context, full, expectedID string) (bool, error) {
	full, err := CleanPath(full)
	if err != nil {
		return false, err
	}
	if full == "/" {
		return false, fmt.Errorf("%w：不能删除 CD2 根目录", ErrInput)
	}
	ctx, cancel := c.request(ctx)
	defer cancel()
	parent := path.Dir(full)
	release, err := c.lockWrite(ctx, parent, full)
	if err != nil {
		return false, err
	}
	defer release()
	entries, err := c.list(ctx, parent, true)
	if errors.Is(err, ErrNotFound) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	var directory *File
	for i := range entries {
		if entries[i].Path == full {
			directory = &entries[i]
			break
		}
	}
	if directory == nil {
		return true, nil
	}
	if !directory.Directory || directory.ReadOnly || expectedID != "" && directory.ID != expectedID {
		return false, nil
	}
	children, err := c.list(ctx, full, true)
	if errors.Is(err, ErrNotFound) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if len(children) != 0 {
		return false, nil
	}
	result, err := c.rpc.DeleteFile(ctx, &pb.FileRequest{Path: full, ForceRefresh: boolValue(true)})
	c.invalidateDirectories(parent, full)
	if err != nil {
		return false, c.wrap("清理空目录", err)
	}
	if err := c.result("清理空目录", result); err != nil {
		return false, err
	}
	remaining, err := c.list(ctx, parent, true)
	if errors.Is(err, ErrNotFound) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("核对空目录删除结果: %w", err)
	}
	for _, entry := range remaining {
		if entry.Path == full {
			return false, errors.New("CloudDrive2 尚未确认空目录已删除")
		}
	}
	return true, nil
}
