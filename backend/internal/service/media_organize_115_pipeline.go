package service

import (
	"context"
	"strm115/backend/internal/client/p115"
)

type media115DirectoryDrive interface {
	ReadDirectoryFiles(context.Context, string, string) (p115.DriveDirectory, []p115.ShareFile, error)
}

// 完整读取当前目录后立即整理，作品分支完成后收尾。
func (s *MediaOrganizer) process115Auto(ctx context.Context, files *mediaOrganizeFiles, run *MediaOrganizeRun) error {
	return s.processCloudAuto(ctx, files, run)
}

// 未完成主文件的写入需要先核对位置；附件不再保护整个视频任务。
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
			if entry.File.MediaType != "video" || entry.Status == "done" || entry.P115Move == nil {
				continue
			}
			tasks[entry.File.ID] = true
			if entry.File.ParentID != "" {
				directories[entry.File.ParentID] = true
			}
		}
		for _, entry := range previous.Entries {
			if entry.Status != "done" && (tasks[entry.File.ID] || tasks[entry.VideoID]) {
				pending[entry.File.ID] = true
			}
		}
	}
	return pending, directories
}
