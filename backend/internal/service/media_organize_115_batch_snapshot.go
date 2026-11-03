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

// 只保存文件身份与精确目录，不保存凭据；未确认意图在显式重试时先定位再决定剩余写入。
type Media115MoveIntent struct {
	Phase           string `json:"phase"`
	FileID          string `json:"fileId"`
	AccountID       string `json:"accountId"`
	SourceCID       string `json:"sourceCid"`
	SourceDirectory string `json:"sourceDirectory"`
	SourcePath      string `json:"sourcePath"`
	SourceName      string `json:"sourceName"`
	TargetCID       string `json:"targetCid,omitempty"`
	TargetDirectory string `json:"targetDirectory"`
	TargetName      string `json:"targetName"`
	Relative        string `json:"relative"`
	Size            int64  `json:"size"`
	SHA1            string `json:"sha1,omitempty"`
}

type media115BatchSnapshot struct {
	files []p115.ShareFile
	byID  map[string]p115.ShareFile
	names map[string][]p115.ShareFile
	ids   map[string]bool
}

func (f *mediaOrganizeFiles) read115BatchDirectory(ctx context.Context, drive media115DirectoryDrive, id, expectedPath string) (media115BatchSnapshot, error) {
	directory, children, err := drive.ReadDirectoryFiles(ctx, id, f.cookie)
	var tree *media115EmptyTree
	if errors.Is(err, p115.ErrDriveDirectorySnapshotChanged) && ctx.Err() == nil {
		if _, supported := f.drive.(media115TreeDrive); supported {
			current := *f
			current.rule.Source, current.rule.SourceID = expectedPath, id
			tree, err = current.read115EmptyTree(ctx, id, "")
			if err == nil {
				children = tree.directChildren(id)
			}
		} else {
			directory, children, err = drive.ReadDirectoryFiles(ctx, id, f.cookie)
		}
	}
	if err != nil {
		return media115BatchSnapshot{}, err
	}
	if tree == nil && (directory.CID != id || directory.Path != expectedPath) {
		return media115BatchSnapshot{}, fmt.Errorf("115 批处理目录身份或路径已变化")
	}
	snapshot := media115BatchSnapshot{files: children, byID: make(map[string]p115.ShareFile), names: make(map[string][]p115.ShareFile), ids: make(map[string]bool)}
	for _, child := range children {
		if child.ID == "" || snapshot.ids[child.ID] {
			return media115BatchSnapshot{}, fmt.Errorf("115 批处理目录条目身份无效")
		}
		snapshot.byID[child.ID], snapshot.ids[child.ID] = child, true
		key := strings.ToLower(child.Name)
		snapshot.names[key] = append(snapshot.names[key], child)
	}
	if tree != nil {
		for _, descendants := range tree.children {
			for _, child := range descendants {
				snapshot.ids[child.ID] = true
			}
		}
	}
	f.lists[id] = slices.Clone(children)
	if f.cloudEmpty != nil {
		if _, source := f.cloudEmpty.paths[id]; source {
			f.replace115DirectoryChildren(id, children)
		}
	}
	return snapshot, ctx.Err()
}

func media115BatchMatches(entry MediaOrganizeEntry, file p115.ShareFile, name string) bool {
	return !file.Directory && file.ID == entry.File.ID && file.Name == name && file.Size == entry.File.Size &&
		(entry.File.SHA1 == "" || strings.EqualFold(file.SHA1, entry.File.SHA1))
}

func (snapshot media115BatchSnapshot) matches(entry MediaOrganizeEntry, name string) bool {
	file, exists := snapshot.byID[entry.File.ID]
	return exists && media115BatchMatches(entry, file, name)
}

func (snapshot media115BatchSnapshot) collision(name, id string) bool {
	for _, file := range snapshot.names[strings.ToLower(name)] {
		if file.ID != id {
			return true
		}
	}
	return false
}

func media115BatchValidEntry(entry MediaOrganizeEntry) bool {
	return !entry.File.Directory && entry.File.ID != "" && entry.File.ParentID != "" && entry.File.Size >= 0 &&
		entry.File.SourcePath != "" && !path.IsAbs(entry.File.SourcePath) && path.Clean(entry.File.SourcePath) == entry.File.SourcePath &&
		!strings.HasPrefix(entry.File.SourcePath, "../") && entry.File.SourcePath != ".." && path.Base(entry.File.SourcePath) == entry.File.Name &&
		entry.Relative != "" && !path.IsAbs(entry.Relative) && path.Clean(entry.Relative) == entry.Relative && !strings.HasPrefix(entry.Relative, "../") && entry.Relative != ".."
}

func media115BatchIntent(rule MediaOrganizeRule, entry MediaOrganizeEntry, targetID string) *Media115MoveIntent {
	return &Media115MoveIntent{Phase: "preparing", FileID: entry.File.ID, AccountID: rule.AccountID,
		SourceCID: entry.File.ParentID, SourceDirectory: path.Join(rule.Source, path.Dir(entry.File.SourcePath)), SourcePath: entry.File.SourcePath, SourceName: entry.File.Name,
		TargetCID: targetID, TargetDirectory: path.Join(rule.Target, path.Dir(entry.Relative)), TargetName: path.Base(entry.Relative), Relative: entry.Relative, Size: entry.File.Size, SHA1: entry.File.SHA1}
}

func media115BatchIntentMatches(rule MediaOrganizeRule, entry MediaOrganizeEntry) bool {
	intent := entry.P115Move
	if intent == nil || !media115BatchValidEntry(entry) {
		return false
	}
	wanted := media115BatchIntent(rule, entry, intent.TargetCID)
	return intent.FileID == wanted.FileID && intent.AccountID == wanted.AccountID && intent.SourceCID == wanted.SourceCID && intent.SourceDirectory == wanted.SourceDirectory &&
		intent.SourcePath == wanted.SourcePath && intent.SourceName == wanted.SourceName && intent.TargetDirectory == wanted.TargetDirectory && intent.TargetName == wanted.TargetName &&
		intent.Relative == wanted.Relative && intent.Size == wanted.Size && strings.EqualFold(intent.SHA1, wanted.SHA1)
}

func media115BatchResult(entry *MediaOrganizeEntry, err error, cookie string) {
	entry.Status, entry.Message = "done", ""
	if err != nil {
		entry.Status, entry.Message = "error", organizePublicError(err, cookie)
	}
	entry.Result = &MediaOrganizeResult{Status: entry.Status, Message: entry.Message, Target: entry.Target, At: time.Now()}
	if err == nil {
		entry.P115Move, entry.Failure = nil, nil
	}
}
