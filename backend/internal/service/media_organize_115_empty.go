package service

import (
	"context"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
	"time"

	"strm115/backend/internal/client/p115"
)

const media115EmptyBatchSize = p115.DriveMutationBatchLimit

type media115EmptyDrive interface {
	ProbeEmptyDirectory(context.Context, string, string) (p115.DriveDirectory, bool, error)
	ReadDirectoryFiles(context.Context, string, string) (p115.DriveDirectory, []p115.ShareFile, error)
	DeleteEntriesBatch(context.Context, string, []string, string) error
}

type media115TreeDrive interface {
	WalkDriveFiles(context.Context, string, string, func([]p115.DriveTreeEntry) error) error
}

type media115EmptyState struct {
	children   map[string]map[string]p115.ShareFile
	paths      map[string]string
	dirs       map[string]MediaOrganizeEntry
	attempted  map[string]bool
	removed    map[string]bool
	proven     map[string]bool
	reconciled bool
	cleaned    int
	summarized int
	complete   bool
	err        error
}

func (f *mediaOrganizeFiles) remember115Directory(cid, relative string, files []p115.ShareFile) {
	if f.rule.Storage != "115" {
		return
	}
	if f.cloudEmpty == nil {
		f.cloudEmpty = &media115EmptyState{
			children: make(map[string]map[string]p115.ShareFile), paths: make(map[string]string),
			dirs: make(map[string]MediaOrganizeEntry), attempted: make(map[string]bool), removed: make(map[string]bool),
		}
	}
	state := f.cloudEmpty
	state.paths[cid] = relative
	items := make(map[string]p115.ShareFile, len(files))
	for _, file := range files {
		items[file.ID] = file
		if file.Directory {
			state.dirs[file.ID] = MediaOrganizeEntry{File: OrganizeFile{ShareFile: file, ParentID: cid, SourcePath: path.Join(relative, file.Name), MediaType: "cleanup"}}
		}
	}
	state.children[cid] = items
}

// 只扣除已确认离开原来源的子项，移动前缓存和未完成写入都不能作为查空证明。
func (f *mediaOrganizeFiles) forget115SourceEntry(entry MediaOrganizeEntry) {
	if f.cloudEmpty == nil || entry.File.Directory || entry.Status != "done" && (entry.Failure == nil || entry.Failure.State != "stored") {
		return
	}
	delete(f.cloudEmpty.children[entry.File.ParentID], entry.File.ID)
}

func (f *mediaOrganizeFiles) protected115Directory(id string) bool {
	return id == "" || id == "0" || id == f.rule.SourceID || id == f.rule.TargetID || id == f.rule.FailureID
}

// 整棵已知分支不含任何文件时，可回收最上层目录；未知分支始终保留。
func (f *mediaOrganizeFiles) ready115EmptyBranches() []MediaOrganizeEntry {
	state := f.cloudEmpty
	if state == nil || !state.complete {
		return nil
	}
	empty := make(map[string]bool, len(state.dirs))
	checked := make(map[string]bool, len(state.dirs))
	var visit func(string) bool
	visit = func(id string) bool {
		if state.removed[id] {
			return true
		}
		if checked[id] {
			return empty[id]
		}
		checked[id] = true
		items, known := state.children[id]
		if !known || f.protected115Directory(id) || state.attempted[id] {
			return false
		}
		for _, child := range items {
			if !child.Directory || !visit(child.ID) {
				return false
			}
		}
		empty[id] = true
		return true
	}
	for id := range state.dirs {
		visit(id)
	}
	ready := make([]MediaOrganizeEntry, 0)
	for id, entry := range state.dirs {
		if !empty[id] || state.removed[id] || empty[entry.File.ParentID] {
			continue
		}
		ready = append(ready, entry)
	}
	slices.SortFunc(ready, func(a, b MediaOrganizeEntry) int {
		if a.File.ParentID != b.File.ParentID {
			return strings.Compare(a.File.ParentID, b.File.ParentID)
		}
		return strings.Compare(a.File.SourcePath, b.File.SourcePath)
	})
	return ready
}

// 即时清理失败不阻断其他媒体整理；本轮停止新的清理写入，收尾统一报告错误。
func (s *MediaOrganizer) tryCleanup115EmptyDirectories(ctx context.Context, files *mediaOrganizeFiles, run *MediaOrganizeRun) {
	if files.cloudEmpty == nil || files.cloudEmpty.err != nil {
		return
	}
	if err := s.cleanup115EmptyDirectories(ctx, files, run); err != nil {
		files.cloudEmpty.err = err
	}
}

func (s *MediaOrganizer) cleanup115EmptyDirectories(ctx context.Context, files *mediaOrganizeFiles, run *MediaOrganizeRun) error {
	drive, supported := files.drive.(media115EmptyDrive)
	state := files.cloudEmpty
	if run.Rule.Storage != "115" || !supported || state == nil || !state.complete {
		return nil
	}
	if state.err != nil {
		return state.err
	}
	if !state.reconciled {
		for _, entry := range run.Entries {
			files.forget115SourceEntry(entry)
		}
		state.reconciled = true
	}
	state.proven = make(map[string]bool)
	defer func() { state.proven = nil }()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		ready := files.ready115EmptyBranches()
		if len(ready) == 0 {
			return nil
		}
		parent := ready[0].File.ParentID
		end := 1
		for end < len(ready) && end < media115EmptyBatchSize && ready[end].File.ParentID == parent {
			end++
		}
		if err := s.cleanup115EmptyBatch(ctx, files, run, drive, parent, ready[:end]); err != nil {
			state.err = err
			return err
		}
	}
}

func (f *mediaOrganizeFiles) check115Directory(directory p115.DriveDirectory, id, relative string) error {
	if directory.CID != id || directory.Path != path.Join(f.rule.Source, relative) {
		return fmt.Errorf("115 清理目录身份或路径已变化")
	}
	return nil
}

func (s *MediaOrganizer) cleanup115EmptyBatch(ctx context.Context, files *mediaOrganizeFiles, run *MediaOrganizeRun, drive media115EmptyDrive, parent string, candidates []MediaOrganizeEntry) error {
	work, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	if s.reserve != nil {
		release, err := s.reserve()
		if err != nil {
			return err
		}
		defer release()
	}
	valid, err := files.verify115EmptyBranches(work, drive, parent, candidates)
	if err != nil {
		return fmt.Errorf("核验 115 空目录: %w", err)
	}
	if len(valid) == 0 {
		return nil
	}
	indices := make([]int, 0, len(valid))
	ids := make([]string, 0, len(valid))
	for _, entry := range valid {
		files.cloudEmpty.attempted[entry.File.ID] = true
		entry.Status, entry.Message = "error", "115 空目录清理结果待核实；下轮监控重新扫描"
		entry.Result = &MediaOrganizeResult{Status: entry.Status, Message: entry.Message, At: time.Now()}
		indices = append(indices, len(run.Entries))
		ids = append(ids, entry.File.ID)
		run.Entries = append(run.Entries, entry)
	}
	if err := s.checkpoint(run); err != nil {
		return err
	}
	writeErr := drive.DeleteEntriesBatch(work, parent, ids, files.cookie)
	if err := work.Err(); err != nil {
		return errors.Join(writeErr, err)
	}
	snapshot, readErr := files.read115CleanupDirectory(work, drive, parent, files.cloudEmpty.paths[parent])
	var resultErr error
	for _, index := range indices {
		entry := &run.Entries[index]
		if readErr == nil && !snapshot.contains(entry.File.ID) {
			entry.Status, entry.Message = "done", "已清理空目录分支，已移入 115 回收站"
			files.remove115DirectoryBranch(entry.File.ID)
			files.cloudEmpty.cleaned++
		} else {
			cause := errors.Join(writeErr, readErr)
			if cause == nil {
				cause = fmt.Errorf("115 尚未确认该目录已删除")
			}
			entry.Message += "：" + organizePublicError(cause, files.cookie)
			if resultErr == nil {
				resultErr = cause
			}
		}
		entry.Result = &MediaOrganizeResult{Status: entry.Status, Message: entry.Message, At: time.Now()}
	}
	if readErr == nil {
		files.replace115DirectoryChildren(parent, snapshot.children)
	}
	if err := s.checkpoint(run); err != nil {
		return errors.Join(resultErr, err)
	}
	return resultErr
}

func (f *mediaOrganizeFiles) replace115DirectoryChildren(id string, files []p115.ShareFile) {
	items := make(map[string]p115.ShareFile, len(files))
	for _, file := range files {
		items[file.ID] = file
	}
	f.cloudEmpty.children[id] = items
	f.lists[id] = slices.Clone(files)
	if f.cloudEmpty.proven != nil {
		f.cloudEmpty.proven[id] = len(files) == 0
	}
}

func (f *mediaOrganizeFiles) remove115DirectoryBranch(id string) {
	state := f.cloudEmpty
	root := state.dirs[id]
	for child, entry := range state.dirs {
		if child == id || strings.HasPrefix(entry.File.SourcePath, root.File.SourcePath+"/") {
			state.removed[child] = true
			delete(state.children, child)
			delete(f.lists, child)
		}
	}
	delete(state.children[root.File.ParentID], id)
}
