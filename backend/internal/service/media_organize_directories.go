package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

const (
	mediaLocalDirectoryLimit     = 500
	mediaLocalDirectoryScanLimit = 20000
	mediaLocalDirectoryBatch     = 256
)

type MediaLocalDirectory struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type MediaLocalDirectories struct {
	Path        string                `json:"path"`
	ParentPath  *string               `json:"parentPath"`
	Directories []MediaLocalDirectory `json:"directories"`
	Truncated   bool                  `json:"truncated"`
}

// LocalDirectories browses paths visible to the server, without changing rule
// configuration or creating directories. Empty paths open the filesystem root
// on Unix and a virtual drive list on Windows.
func (s *MediaOrganizer) LocalDirectories(ctx context.Context, value string) (MediaLocalDirectories, error) {
	out := MediaLocalDirectories{Directories: make([]MediaLocalDirectory, 0, mediaLocalDirectoryLimit)}
	if err := ctx.Err(); err != nil {
		return out, fmt.Errorf("读取本地目录: %w", err)
	}
	if value == "" {
		if runtime.GOOS == "windows" {
			drives, err := mediaLocalDirectoryDrives(ctx)
			if err != nil {
				return out, err
			}
			out.Directories = drives
			return out, nil
		}
		value = string(filepath.Separator)
	}
	root, err := mediaLocalRoot(value)
	if err != nil {
		return out, mediaLocalDirectoryError(err)
	}
	out.Path = root
	parent := filepath.Dir(root)
	if parent != root {
		out.ParentPath = &parent
	} else if runtime.GOOS == "windows" {
		parent = ""
		out.ParentPath = &parent
	}
	directory, err := os.Open(root)
	if err != nil {
		return out, mediaLocalDirectoryError(err)
	}
	defer directory.Close()
	scanned := 0
	for {
		if err := ctx.Err(); err != nil {
			return out, fmt.Errorf("读取本地目录: %w", err)
		}
		entries, readErr := directory.ReadDir(mediaLocalDirectoryBatch)
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return out, mediaLocalDirectoryError(readErr)
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return out, fmt.Errorf("读取本地目录: %w", err)
			}
			if scanned == mediaLocalDirectoryScanLimit {
				out.Truncated = true
				break
			}
			scanned++
			child := filepath.Join(root, entry.Name())
			isDirectory := entry.IsDir()
			if entry.Type()&os.ModeSymlink != 0 {
				info, statErr := os.Stat(child)
				isDirectory = statErr == nil && info.IsDir()
			}
			if !isDirectory {
				continue
			}
			if len(out.Directories) == mediaLocalDirectoryLimit {
				out.Truncated = true
				break
			}
			out.Directories = append(out.Directories, MediaLocalDirectory{Name: entry.Name(), Path: child})
		}
		if out.Truncated || errors.Is(readErr, io.EOF) || len(entries) == 0 {
			break
		}
	}
	sort.Slice(out.Directories, func(i, j int) bool {
		left, right := strings.ToLower(out.Directories[i].Name), strings.ToLower(out.Directories[j].Name)
		if left == right {
			return out.Directories[i].Name < out.Directories[j].Name
		}
		return left < right
	})
	return out, nil
}

func mediaLocalDirectoryError(err error) error {
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("目录不存在或未挂载，请检查服务器或容器内的路径: %w", err)
	}
	if errors.Is(err, os.ErrPermission) {
		return fmt.Errorf("没有权限读取此目录，请检查服务器目录权限: %w", err)
	}
	return fmt.Errorf("读取本地目录: %w", err)
}
