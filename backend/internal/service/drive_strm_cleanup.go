package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"runtime"
	"time"
	"unicode"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
)

func driveSTRMCleanupOutputPath(directory, relative string) (string, error) {
	full, err := mediaLocalPath(directory, relative, false)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(full)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("输出不是普通文件：%s", relative)
	}
	return full, nil
}

// Cleanup must never create a missing path. Keep reads bounded even if an
// external writer grows the file between the stat and the read.
func readDriveSTRMCleanupOutput(directory, relative string) ([]byte, string, error) {
	full, err := driveSTRMCleanupOutputPath(directory, relative)
	if err != nil {
		return nil, "", err
	}
	f, err := os.Open(full)
	if err != nil {
		return nil, "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, "", err
	}
	if !info.Mode().IsRegular() || info.Size() > p115.MaxMetadataSize {
		return nil, "", fmt.Errorf("旧输出不是有效的普通文件：%s", relative)
	}
	data, err := io.ReadAll(io.LimitReader(f, p115.MaxMetadataSize+1))
	if err != nil {
		return nil, "", err
	}
	if int64(len(data)) > p115.MaxMetadataSize {
		return nil, "", fmt.Errorf("旧输出大小已变化：%s", relative)
	}
	return data, full, nil
}

func pruneDriveSTRMParents(ctx context.Context, directory, relative string) error {
	for parent := path.Dir(relative); parent != "."; parent = path.Dir(parent) {
		if err := ctx.Err(); err != nil {
			return err
		}
		full, err := mediaLocalPath(directory, parent, false)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		dir, err := os.Open(full)
		if err != nil {
			return err
		}
		_, readErr := dir.Readdirnames(1)
		closeErr := dir.Close()
		if readErr == nil {
			return nil // An unrelated file or directory keeps its parent intact.
		}
		if !errors.Is(readErr, io.EOF) {
			return readErr
		}
		if closeErr != nil {
			return closeErr
		}
		if err := os.Remove(full); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

// SQLite NOCASE indexes cover ASCII only. On Windows, preserve paths whose
// extra case aliases cannot be ruled out by the indexed ownership query.
func driveSTRMCleanupAmbiguousCase(relative string) bool {
	if runtime.GOOS == "windows" {
		for _, r := range relative {
			if r > unicode.MaxASCII && unicode.SimpleFold(r) != r {
				return true
			}
		}
	}
	return false
}

func (s *DriveSTRMService) removeDriveSTRMOldOutput(ctx context.Context, directory string, old, current model.DriveSTRMFile) error {
	if driveSTRMSamePath(old.Path, current.Path) || driveSTRMCleanupAmbiguousCase(old.Path) {
		return nil
	}
	owners, err := s.Store.ListDriveSTRMFilesByPaths(ctx, old.MappingID, []string{old.Path})
	if err != nil {
		return err
	}
	for _, owner := range owners {
		if owner.FileID != old.FileID {
			return nil // This path was reused, possibly with identical bytes.
		}
	}
	generatedFileWriteMu.Lock()
	defer generatedFileWriteMu.Unlock()
	oldFull, err := driveSTRMCleanupOutputPath(directory, old.Path)
	if errors.Is(err, os.ErrNotExist) {
		return pruneDriveSTRMParents(ctx, directory, old.Path)
	}
	if err != nil {
		return err
	}
	_, err = driveSTRMCleanupOutputPath(directory, current.Path)
	if err != nil {
		return fmt.Errorf("新输出尚未通过核验，保留旧文件: %w", err)
	}
	if current.Digest == "" {
		return fmt.Errorf("新输出尚未保存成功索引，保留旧文件：%s", current.Path)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Remove(oldFull); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("清理旧输出: %w", err)
	}
	return pruneDriveSTRMParents(ctx, directory, old.Path)
}

// Reuse the tracked attachment worker for local cleanup, one file at a time.
// The control lock spans selection and deletion, so changing a mapping cannot
// redirect a previously selected job into another root. No cloud calls occur.
func (s *DriveSTRMService) cleanupDriveSTRMOutput(ctx context.Context) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	jobs, err := s.Store.ListDriveSTRMCleanup(ctx, 1)
	if err != nil || len(jobs) == 0 {
		return false, err
	}
	job := jobs[0]
	value, err := s.Store.GetDriveSTRM(ctx, job.Record.MappingID)
	if err != nil {
		return true, err
	}
	current, err := s.Store.ListDriveSTRMFilesByIDs(ctx, value.ID, []string{job.Record.FileID})
	if err != nil {
		return true, err
	}
	if len(current) == 0 || current[0].Directory || job.Record.Directory || job.Record.Digest == "" {
		return true, s.Store.DeleteDriveSTRMCleanup(ctx, job.ID)
	}
	directory, err := ResolveOutputDirectory(s.Root, value.OutputDir)
	if err == nil {
		_, err = s.Accounts.Primary(value.AccountID)
	}
	if err == nil {
		err = s.removeDriveSTRMOldOutput(ctx, directory, job.Record, current[0])
	}
	if ctx.Err() != nil {
		return true, ctx.Err()
	}
	if err != nil {
		// Local permission errors and an incomplete destination retry alone;
		// they must never turn into a remote full-scan failure.
		delay := driveSTRMRetryDelay(job.Attempts + 1)
		if retryErr := s.Store.RetryDriveSTRMCleanup(ctx, job, err.Error(), time.Now().UTC().Add(delay)); retryErr != nil {
			return true, retryErr
		}
		s.logDriveSTRM(value.ID, "WARNING", fmt.Sprintf("目录「%s」旧输出暂未清理：%s；将在 %s 后单独重试；原因：%s", value.Name, job.Record.Path, delay, err))
		return true, nil
	}
	return true, s.Store.DeleteDriveSTRMCleanup(ctx, job.ID)
}
