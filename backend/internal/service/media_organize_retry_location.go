package service

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path"
	"slices"
	"strings"

	"strm115/backend/internal/client/clouddrive2"
	"strm115/backend/internal/client/p115"
)

// 旧失败仅可继承另一轮已确认的隔离位置；实际身份和来源缺失均须实时核实。
func (s *MediaOrganizer) restoreRetryLocations(ctx context.Context, files *mediaOrganizeFiles, run *MediaOrganizeRun) error {
	if run.Rule.Failure == "" {
		return nil
	}
	owners := make([]string, 0, len(run.Entries))
	indices := make([]int, 0, len(run.Entries))
	for i, entry := range run.Entries {
		if !mediaInRunTask(run, entry) || entry.Status == "done" || entry.File.MediaType == "cleanup" || entry.P115Move != nil || entry.CD2Current != nil ||
			entry.AttachmentKeep != nil && entry.AttachmentKeep.Phase != "retained" ||
			entry.Failure != nil && (entry.Failure.State == "stored" || entry.Failure.State == "moving") {
			continue
		}
		indices = append(indices, i)
		owners = append(owners, mediaEntryOwner(entry))
	}
	if len(indices) == 0 {
		return nil
	}
	slices.Sort(owners)
	owners = slices.Compact(owners)
	raws, err := s.db.LoadMediaOrganizeFailures(ctx, run.Rule.ID, owners)
	if err != nil {
		return fmt.Errorf("读取已确认的隔离位置: %w", err)
	}
	candidates := make(map[string][]MediaOrganizeEntry)
	for _, raw := range raws {
		other, err := mediaDecodeStoredRun(raw)
		if err != nil {
			return err
		}
		if other.ID == run.ID || mediaRunActive(other.Status) || !mediaOrphanBinding(other.Rule).matches(run.Rule) || other.Rule.Failure != run.Rule.Failure || other.Rule.FailureID != run.Rule.FailureID {
			continue
		}
		for _, entry := range other.Entries {
			if entry.Status != "done" && entry.P115Move == nil && (entry.AttachmentKeep == nil || entry.AttachmentKeep.Phase == "retained") && entry.Failure != nil && entry.Failure.State == "stored" {
				candidates[entry.File.ID] = append(candidates[entry.File.ID], entry)
			}
		}
	}
	indices, release := s.beginMediaEntries(run, indices)
	defer release()
	changed := false
	for _, index := range indices {
		if err := ctx.Err(); err != nil {
			return err
		}
		entry := &run.Entries[index]
		for _, candidate := range candidates[entry.File.ID] {
			if candidate.VideoID != entry.VideoID || candidate.File.MediaType != entry.File.MediaType || candidate.File.SourcePath != entry.File.SourcePath || !mediaResolvedSameSource(*entry, candidate, run.Rule.Storage) {
				continue
			}
			// 本地跨记录继承必须有原文件身份，旧记录缺少证据时不凭名称与大小认领。
			if run.Rule.Storage == "local" && (entry.LocalIdentity == "" || entry.LocalIdentity != candidate.LocalIdentity) {
				continue
			}
			verified, err := files.verifyRetryLocation(ctx, *entry, candidate)
			if err != nil {
				return fmt.Errorf("核验已确认的隔离位置: %w", err)
			}
			if !verified {
				continue
			}
			failure := *candidate.Failure
			entry.Failure = &failure
			if candidate.CD2Current != nil {
				current := *candidate.CD2Current
				entry.CD2Current = &current
			}
			entry.CD2Hashes = maps.Clone(candidate.CD2Hashes)
			changed = true
			break
		}
	}
	if changed {
		return s.checkpoint(run)
	}
	return nil
}

func (f *mediaOrganizeFiles) verifyRetryLocation(ctx context.Context, original, candidate MediaOrganizeEntry) (bool, error) {
	if _, err := f.failureFiles(ctx); err != nil {
		return false, err
	}
	switch f.rule.Storage {
	case "local":
		root, err := mediaLocalRoot(f.rule.Source)
		if err != nil || root != f.rule.Source {
			if err == nil {
				err = fmt.Errorf("整理来源目录已变化")
			}
			return false, err
		}
		_, err = mediaLocalPath(f.rule.Source, original.File.SourcePath, false)
		if err == nil {
			return false, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return false, err
		}
		full, err := mediaLocalPath(f.rule.Failure, candidate.File.SourcePath, false)
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		info, err := os.Lstat(full)
		if err != nil {
			return false, err
		}
		identity, err := mediaLocalFileIdentityKey(full, info)
		if err != nil {
			return false, err
		}
		return info.Mode().IsRegular() && candidate.Failure.LocalIdentity != "" && identity == candidate.Failure.LocalIdentity &&
			info.Size() == candidate.File.Size && candidate.Modified != 0 && info.ModTime().UnixNano() == candidate.Modified, nil
	case "115":
		return f.verify115RetryLocation(ctx, original, candidate)
	case "cd2":
		source, err := mediaCD2Relative(f.rule.Source, original.File.SourcePath)
		if err != nil {
			return false, err
		}
		if _, err := f.cd2.Stat(ctx, source, true); err == nil {
			return false, nil
		} else if !errors.Is(err, clouddrive2.ErrNotFound) {
			return false, err
		}
		destination, err := mediaCD2Relative(f.rule.Failure, candidate.File.SourcePath)
		if err != nil {
			return false, err
		}
		actual, err := f.cd2.Stat(ctx, destination, true)
		if errors.Is(err, clouddrive2.ErrNotFound) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		expected := candidate.CD2FileID
		if candidate.Failure.CD2FileID != "" {
			expected = candidate.Failure.CD2FileID
		}
		if candidate.CD2Current != nil {
			if candidate.CD2Current.Path != destination || candidate.CD2Current.FileID != expected {
				return false, nil
			}
			candidate.Modified = candidate.CD2Current.Modified
		}
		return expected != "" && actual.Path == destination && actual.Name == path.Base(destination) && mediaCD2Matches(candidate, actual, expected), nil
	}
	return false, ErrMediaOrganizeInput
}

func (f *mediaOrganizeFiles) verify115RetryLocation(ctx context.Context, original, candidate MediaOrganizeEntry) (bool, error) {
	if _, err := mediaCD2Relative(f.rule.Source, original.File.SourcePath); err != nil {
		return false, err
	}
	// 从冻结来源根逐层确认，父目录被正常清理也可证明原路径已不存在。
	current := *f
	current.rule.Target, current.rule.TargetID = f.rule.Source, f.rule.SourceID
	current.lists = make(map[string][]p115.ShareFile)
	parent, err := current.cloudDestination(ctx, original.File.SourcePath, false)
	if err != nil {
		return false, err
	}
	if parent != "" {
		children, err := current.list(ctx, parent)
		if err != nil {
			return false, err
		}
		for _, child := range children {
			if child.ID == original.File.ID || strings.EqualFold(child.Name, path.Base(original.File.SourcePath)) {
				return false, nil
			}
		}
	}
	failure := candidate.Failure
	if failure.ParentID == "" {
		return false, nil
	}
	directory, err := f.drive.ReadDirectory(ctx, failure.ParentID, f.cookie)
	if err != nil {
		return false, err
	}
	if directory.CID != failure.ParentID || directory.Path != path.Join(f.rule.Failure, path.Dir(candidate.File.SourcePath)) {
		return false, nil
	}
	delete(f.lists, failure.ParentID)
	children, err := f.list(ctx, failure.ParentID)
	if err != nil {
		return false, err
	}
	expected := candidate.File.ID
	if failure.FileID != "" {
		expected = failure.FileID
	}
	for _, child := range children {
		if child.ID != expected || child.Directory || child.Name != path.Base(candidate.File.SourcePath) || child.Size != candidate.File.Size {
			continue
		}
		if expected != candidate.File.ID {
			return media115SameSHA1(candidate, child), nil
		}
		return candidate.File.SHA1 == "" || child.SHA1 != "" && strings.EqualFold(child.SHA1, candidate.File.SHA1), nil
	}
	return false, nil
}
