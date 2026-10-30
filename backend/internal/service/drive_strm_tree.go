package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"strings"
	"time"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
)

type driveSTRMExporter interface {
	StartDriveTreeExport(context.Context, string, string) (string, error)
	DriveTreeExportStatus(context.Context, string, string) (p115.DriveTreeExport, error)
	ReadDriveTreeExport(context.Context, p115.DriveTreeExport, string, string, func(string) error) error
	DeleteDriveTreeExport(context.Context, string, string) error
}

// Only the one scanner starts exports. The independent durable cleanup queue
// outlives mapping deletion and is never used with a different primary account.
func (s *DriveSTRMService) cleanDriveSTRMExports(ctx context.Context) error {
	client, ok := s.Client.(driveSTRMExporter)
	if !ok {
		return nil
	}
	jobs, err := s.Store.ListDriveSTRMExports(ctx)
	if err != nil {
		return err
	}
	var failures []error
	for id, accountID := range jobs {
		account, err := s.Accounts.Primary(accountID)
		if err != nil {
			continue
		}
		attempt, cancel := context.WithTimeout(ctx, 10*time.Second)
		err = client.DeleteDriveTreeExport(attempt, id, account.Cookie)
		cancel()
		if errors.Is(err, p115.ErrDriveTreeExportPending) {
			continue
		}
		if err == nil {
			err = s.Store.DeleteDriveSTRMExport(ctx, id)
		}
		if err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func (s *DriveSTRMService) scanDirectoryTree(ctx context.Context, value model.DriveSTRM, root p115.DriveDirectory, cookie, directory string, videos, metadata map[string]bool) (model.DriveSTRMStats, []string, error) {
	var stats model.DriveSTRMStats
	if value.SourceCID == "0" {
		return stats, nil, p115.ErrDriveTreeExportUnsupported
	}
	client, ok := s.Client.(driveSTRMExporter)
	if !ok {
		return stats, nil, p115.ErrDriveTreeExportUnsupported
	}
	jobs, err := s.Store.ListDriveSTRMExports(ctx)
	if err != nil {
		return stats, nil, err
	}
	for _, accountID := range jobs {
		if accountID == value.AccountID {
			return stats, nil, fmt.Errorf("上次目录树仍待清理，暂不重复提交导出")
		}
	}
	id, err := client.StartDriveTreeExport(ctx, value.SourceCID, cookie)
	if err != nil {
		return stats, nil, err
	}
	s.logDriveSTRM(value.ID, "INFO", fmt.Sprintf("目录「%s」正在导出目录树，等待 115 返回结果", value.Name))
	// Persist an accepted external job even if the request was just canceled.
	saveCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	err = s.Store.SaveDriveSTRMExport(saveCtx, value.AccountID, id)
	cancel()
	stored := err == nil
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if client.DeleteDriveTreeExport(cleanup, id, cookie) == nil && stored {
			_ = s.Store.DeleteDriveSTRMExport(cleanup, id)
		}
	}()
	if err != nil {
		return stats, nil, err
	}
	poll, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	var job p115.DriveTreeExport
	for {
		job, err = client.DriveTreeExportStatus(poll, id, cookie)
		if err != nil {
			return stats, nil, err
		}
		if job.FileID != "" {
			break
		}
		timer := time.NewTimer(2 * time.Second)
		select {
		case <-poll.Done():
			timer.Stop()
			return stats, nil, poll.Err()
		case <-timer.C:
		}
	}
	indexed, err := s.Store.ListDriveSTRMDirectories(ctx, value.ID)
	if err != nil {
		return stats, nil, err
	}
	directories := make(map[string]string, len(indexed)+1)
	directories[""] = value.SourceCID
	for _, item := range indexed {
		directories[item.Path] = item.FileID
	}
	wanted := make(map[string]bool)
	seen := make(map[string]bool)
	rootName := root.Name
	if rootName == "" {
		rootName = path.Base(root.Path)
	}
	err = client.ReadDriveTreeExport(ctx, job, rootName, cookie, func(relative string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		ext := strings.ToLower(path.Ext(relative))
		video := videos[ext]
		if !video && !metadata[ext] {
			return nil
		}
		output := driveSTRMOutputPath(relative, video)
		key := strings.ToLower(output)
		if seen[key] {
			return fmt.Errorf("%w：不同源文件映射到同一路径", p115.ErrDriveTreeExportUnsupported)
		}
		seen[key] = true
		if video {
			stats.Videos++
		}
		_, err := statDriveSTRMOutput(directory, output)
		if err == nil {
			stats.Unchanged++
			return nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		parent := path.Dir(output)
		for {
			if parent == "." {
				parent = ""
			}
			if cid, ok := directories[parent]; ok {
				wanted[cid] = true
				return nil
			}
			parent = path.Dir(parent)
		}
	})
	if err != nil {
		return stats, nil, err
	}
	cids := make([]string, 0, len(wanted))
	for cid := range wanted {
		cids = append(cids, cid)
	}
	s.logDriveSTRM(value.ID, "INFO", fmt.Sprintf("目录「%s」目录树核对完成：视频 %d，已有输出 %d，需补扫 %d 个目录", value.Name, stats.Videos, stats.Unchanged, len(cids)))
	return stats, cids, nil
}
