package service

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"unicode"

	"strm115/backend/internal/client/p115"
)

func (f *mediaOrganizeFiles) valid115EmptyCandidate(entry MediaOrganizeEntry, parent string) bool {
	relative := entry.File.SourcePath
	parentPath, known := f.cloudEmpty.paths[parent]
	return known && entry.File.Directory && entry.File.ParentID == parent && !f.protected115Directory(entry.File.ID) &&
		relative != "" && relative != "." && !path.IsAbs(relative) && path.Clean(relative) == relative && !strings.HasPrefix(relative, "../") &&
		entry.File.Name == path.Base(relative) && relative == path.Join(parentPath, entry.File.Name) &&
		!strings.ContainsAny(entry.File.Name, "/\\") && !strings.ContainsFunc(entry.File.Name, unicode.IsControl)
}

// 少量叶子只读一条，成批候选共用完整目录树；不完整快照永远不转为删除依据。
func (f *mediaOrganizeFiles) verify115EmptyBranches(ctx context.Context, drive media115EmptyDrive, parent string, candidates []MediaOrganizeEntry) ([]MediaOrganizeEntry, error) {
	state := f.cloudEmpty
	valid := make([]MediaOrganizeEntry, 0, len(candidates))
	pending := make([]MediaOrganizeEntry, 0, len(candidates))
	nested := false
	for _, entry := range candidates {
		if !f.valid115EmptyCandidate(entry, parent) {
			state.attempted[entry.File.ID] = true
			continue
		}
		if state.proven[entry.File.ID] {
			valid = append(valid, entry)
			continue
		}
		pending = append(pending, entry)
		nested = nested || len(state.children[entry.File.ID]) > 0
	}
	if len(pending) == 0 {
		return valid, ctx.Err()
	}
	if _, supported := f.drive.(media115TreeDrive); supported && (len(pending) >= 4 || nested) {
		root, relative := parent, state.paths[parent]
		if len(pending) == 1 {
			root, relative = pending[0].File.ID, pending[0].File.SourcePath
		}
		tree, err := f.read115EmptyTree(ctx, root, relative)
		if err == nil {
			for _, entry := range pending {
				if tree.paths[entry.File.ID] == entry.File.SourcePath && tree.empty(entry.File.ID, f.protected115Directory) {
					valid = append(valid, entry)
				} else {
					state.attempted[entry.File.ID] = true
				}
			}
			return valid, ctx.Err()
		}
		if !errors.Is(err, p115.ErrDriveTreeUnsupported) {
			return nil, err
		}
	}
	for _, entry := range pending {
		empty, err := f.verify115EmptyBranch(ctx, drive, entry.File.ID, entry.File.SourcePath, 0)
		if err != nil {
			return nil, err
		}
		if empty {
			valid = append(valid, entry)
		} else {
			state.attempted[entry.File.ID] = true
		}
	}
	return valid, ctx.Err()
}

func (f *mediaOrganizeFiles) verify115EmptyBranch(ctx context.Context, drive media115EmptyDrive, id, relative string, depth int) (bool, error) {
	if f.protected115Directory(id) || depth > 12 {
		return false, nil
	}
	if len(f.cloudEmpty.children[id]) == 0 {
		return f.probe115CleanupDirectory(ctx, drive, id, relative)
	}
	snapshot, err := f.read115CleanupDirectory(ctx, drive, id, relative)
	if err != nil {
		return false, err
	}
	if snapshot.tree != nil {
		return snapshot.tree.empty(id, f.protected115Directory), ctx.Err()
	}
	for _, child := range snapshot.children {
		if !child.Directory {
			return false, nil
		}
		empty, err := f.verify115EmptyBranch(ctx, drive, child.ID, path.Join(relative, child.Name), depth+1)
		if err != nil || !empty {
			return false, err
		}
	}
	return true, ctx.Err()
}

type media115DirectorySnapshot struct {
	children []p115.ShareFile
	tree     *media115EmptyTree
}

func (snapshot media115DirectorySnapshot) contains(id string) bool {
	if snapshot.tree != nil {
		_, exists := snapshot.tree.paths[id]
		return exists
	}
	for _, child := range snapshot.children {
		if child.ID == id {
			return true
		}
	}
	return false
}

// 计数失配时改读完整新树；不支持目录树的实现只补一次严格列表，不放宽判定。
func (f *mediaOrganizeFiles) read115CleanupDirectory(ctx context.Context, drive media115EmptyDrive, id, relative string) (media115DirectorySnapshot, error) {
	directory, children, err := drive.ReadDirectoryFiles(ctx, id, f.cookie)
	if errors.Is(err, p115.ErrDriveDirectorySnapshotChanged) && ctx.Err() == nil {
		if _, supported := f.drive.(media115TreeDrive); supported {
			tree, err := f.read115EmptyTree(ctx, id, relative)
			if err != nil {
				return media115DirectorySnapshot{}, err
			}
			return media115DirectorySnapshot{children: tree.directChildren(id), tree: tree}, nil
		}
		directory, children, err = drive.ReadDirectoryFiles(ctx, id, f.cookie)
	}
	if err == nil {
		err = f.check115Directory(directory, id, relative)
	}
	return media115DirectorySnapshot{children: children}, err
}

func (f *mediaOrganizeFiles) probe115CleanupDirectory(ctx context.Context, drive media115EmptyDrive, id, relative string) (bool, error) {
	directory, empty, err := drive.ProbeEmptyDirectory(ctx, id, f.cookie)
	if errors.Is(err, p115.ErrDriveDirectorySnapshotChanged) && ctx.Err() == nil {
		if _, supported := f.drive.(media115TreeDrive); supported {
			tree, err := f.read115EmptyTree(ctx, id, relative)
			if err != nil {
				return false, err
			}
			return tree.empty(id, f.protected115Directory), ctx.Err()
		}
		directory, empty, err = drive.ProbeEmptyDirectory(ctx, id, f.cookie)
	}
	if err == nil {
		err = f.check115Directory(directory, id, relative)
	}
	return empty, err
}

type media115EmptyTree struct {
	paths    map[string]string
	children map[string][]p115.DriveTreeEntry
}

func (tree *media115EmptyTree) directChildren(id string) []p115.ShareFile {
	children := make([]p115.ShareFile, 0, len(tree.children[id]))
	for _, entry := range tree.children[id] {
		children = append(children, entry.ShareFile)
	}
	return children
}

func (tree *media115EmptyTree) empty(id string, protected func(string) bool) bool {
	if _, known := tree.paths[id]; !known || protected(id) {
		return false
	}
	for _, child := range tree.children[id] {
		if !child.Directory || !tree.empty(child.ID, protected) {
			return false
		}
	}
	return true
}

func (f *mediaOrganizeFiles) read115EmptyTree(ctx context.Context, root, relative string) (*media115EmptyTree, error) {
	directory, err := f.drive.(mediaOrganizeDrive).ReadDirectory(ctx, root, f.cookie)
	if err != nil {
		return nil, err
	}
	if err := f.check115Directory(directory, root, relative); err != nil {
		return nil, err
	}
	tree := &media115EmptyTree{paths: map[string]string{root: relative}, children: make(map[string][]p115.DriveTreeEntry)}
	entries := make(map[string]p115.DriveTreeEntry)
	delivered := false
	err = f.drive.(media115TreeDrive).WalkDriveFiles(ctx, root, f.cookie, func(batch []p115.DriveTreeEntry) error {
		delivered = true
		for _, entry := range batch {
			_, duplicate := entries[entry.ID]
			if entry.ID == "" || entry.ID == root || duplicate || entry.RelativePath == "" || path.IsAbs(entry.RelativePath) || path.Clean(entry.RelativePath) != entry.RelativePath || strings.HasPrefix(entry.RelativePath, "../") ||
				entry.Name == "" || entry.Name == "." || entry.Name == ".." || strings.ContainsAny(entry.Name, "/\\") || strings.ContainsFunc(entry.Name, unicode.IsControl) {
				return fmt.Errorf("115 完整目录树条目无效")
			}
			entries[entry.ID] = entry
			if entry.Directory {
				tree.paths[entry.ID] = path.Join(relative, entry.RelativePath)
			}
		}
		return ctx.Err()
	})
	if err != nil {
		if delivered && errors.Is(err, p115.ErrDriveTreeUnsupported) {
			return nil, fmt.Errorf("115 目录树返回部分内容后中断")
		}
		return nil, err
	}
	for _, entry := range entries {
		parentPath, known := tree.paths[entry.ParentCID]
		if !known || path.Join(relative, entry.RelativePath) != path.Join(parentPath, entry.Name) {
			return nil, fmt.Errorf("115 完整目录树归属已变化")
		}
		tree.children[entry.ParentCID] = append(tree.children[entry.ParentCID], entry)
	}
	return tree, ctx.Err()
}
