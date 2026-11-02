package service

import (
	"fmt"
	"maps"
	"path"
	"strings"

	"strm115/backend/internal/client/clouddrive2"
)

// 保存完整直接子项，包含临时文件、只读项和不参与整理的文件；逐目录扫描不能覆盖先前分支。
func (f *mediaOrganizeFiles) rememberCD2Directory(directory string, children []clouddrive2.File) error {
	items := make(map[string]bool, len(children))
	for _, file := range children {
		if file.Path != path.Join(directory, file.Name) || file.Name == "." || file.Name == ".." || strings.Contains(file.Name, "/") {
			return fmt.Errorf("CD2 返回了无法确认归属的文件路径")
		}
		items[file.Path] = true
	}
	if f.cd2DirectoryChildren == nil {
		f.cd2DirectoryChildren = make(map[string]map[string]bool)
	}
	f.cd2DirectoryChildren[directory] = items
	return nil
}

// 树只筛选可能为空的目录，删除仍实时复核；同父改名与未确认写入不能减少子项。
func (f *mediaOrganizeFiles) cd2RemainingDirectoryChildren(run *MediaOrganizeRun) map[string]map[string]bool {
	remaining := make(map[string]map[string]bool, len(f.cd2DirectoryChildren))
	for directory, children := range f.cd2DirectoryChildren {
		remaining[directory] = maps.Clone(children)
	}
	for _, entry := range run.Entries {
		if entry.File.Directory || entry.CD2Reference {
			continue
		}
		removed := entry.Status == "done" || entry.Failure != nil && entry.Failure.State == "stored"
		if current := entry.CD2Current; current != nil && !mediaCloudWithin(run.Rule.Source, current.Path) {
			removed = true
		}
		if !removed {
			continue
		}
		full, err := mediaCD2Relative(run.Rule.Source, entry.File.SourcePath)
		if err == nil {
			delete(remaining[path.Dir(full)], full)
		}
	}
	return remaining
}
