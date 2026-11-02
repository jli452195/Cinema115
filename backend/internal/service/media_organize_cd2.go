package service

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"path"
	"slices"
	"strings"
	"time"

	"strm115/backend/internal/client/clouddrive2"
	"strm115/backend/internal/client/p115"
)

type mediaCD2Connections interface {
	Acquire(context.Context, string, uint64) (*CD2Lease, error)
}

type MediaCD2Location struct {
	Path     string `json:"path"`
	FileID   string `json:"fileId,omitempty"`
	Modified int64  `json:"modified,omitempty"`
}

func (s *MediaOrganizer) bindCD2Checkpoint(files *mediaOrganizeFiles, run *MediaOrganizeRun) {
	if run.Rule.Storage != "cd2" {
		return
	}
	files.cd2Checkpoint = func(entry MediaOrganizeEntry, current clouddrive2.File) error {
		for i := range run.Entries {
			if run.Entries[i].File.ID == entry.File.ID {
				location := &MediaCD2Location{Path: current.Path, FileID: current.ID}
				if !current.Modified.IsZero() {
					location.Modified = current.Modified.UnixNano()
				}
				run.Entries[i].CD2Current = location
				return s.checkpoint(run)
			}
		}
		return ErrMediaOrganizeInput
	}
}

// SetCD2 在启动时注入共用连接服务；CD2 不读取或关联 115 账号配置。
func (s *MediaOrganizer) SetCD2(connections *CD2Connections) {
	if connections != nil {
		s.cd2 = connections
	}
}

func mediaCD2RulePaths(rule *MediaOrganizeRule) error {
	for _, value := range []*string{&rule.Source, &rule.Target, &rule.Failure} {
		if value == &rule.Failure && *value == "" {
			continue
		}
		clean, err := clouddrive2.CleanPath(*value)
		if err != nil {
			return fmt.Errorf("%w：CD2 目录路径无效", ErrMediaOrganizeInput)
		}
		*value = clean
	}
	if mediaCloudWithin(rule.Source, rule.Target) || mediaCloudWithin(rule.Target, rule.Source) {
		return fmt.Errorf("%w：来源和目标目录不能重叠", ErrMediaOrganizeInput)
	}
	if rule.Failure != "" && (mediaCloudWithin(rule.Source, rule.Failure) || mediaCloudWithin(rule.Failure, rule.Source) || mediaCloudWithin(rule.Target, rule.Failure) || mediaCloudWithin(rule.Failure, rule.Target)) {
		return fmt.Errorf("%w：整理失败目录不能与来源、目标目录重叠", ErrMediaOrganizeInput)
	}
	rule.AccountID, rule.SourceID, rule.TargetID, rule.FailureID = "", "", "", ""
	return nil
}

func (s *MediaOrganizer) prepareCD2Rule(ctx context.Context, rule *MediaOrganizeRule) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := mediaCD2RulePaths(rule); err != nil {
		return err
	}
	f := &mediaOrganizeFiles{rule: *rule}
	if err := s.openCD2Files(ctx, f); err != nil {
		return err
	}
	defer f.close()
	return nil
}

func (s *MediaOrganizer) openCD2Files(ctx context.Context, f *mediaOrganizeFiles) error {
	if s.cd2 == nil || f.rule.ConnectionID == "" || f.rule.ConnectionRevision == 0 {
		return fmt.Errorf("%w：请先配置 CloudDrive2 并重新选择目录", ErrMediaOrganizeInput)
	}
	if err := mediaCD2RulePaths(&f.rule); err != nil {
		return err
	}
	lease, err := s.cd2.Acquire(ctx, f.rule.ConnectionID, f.rule.ConnectionRevision)
	if err != nil {
		return fmt.Errorf("获取 CD2 连接: %w", err)
	}
	f.cd2Lease, f.cd2 = lease, lease.Client
	for _, directory := range []string{f.rule.Source, f.rule.Target, f.rule.Failure} {
		if directory == "" {
			continue
		}
		if err := f.cd2CheckDirectory(ctx, directory); err != nil {
			f.close()
			return err
		}
	}
	return nil
}

func (f *mediaOrganizeFiles) close() {
	if f.cd2Lease != nil {
		_ = f.cd2Lease.Close()
		f.cd2Lease = nil
	}
}

func (f *mediaOrganizeFiles) cd2CheckDirectory(ctx context.Context, directory string) error {
	actual, err := f.cd2.Stat(ctx, directory, true)
	if err != nil {
		return fmt.Errorf("核验 CD2 目录: %w", err)
	}
	if !actual.Directory || actual.Path != directory {
		return fmt.Errorf("CD2 目录已变化，请重新选择目录")
	}
	if actual.ReadOnly {
		return fmt.Errorf("CD2 目录为只读，无法整理")
	}
	return nil
}

func mediaCD2ShareFile(file clouddrive2.File) p115.ShareFile {
	// 现有整理 DTO 的 ID 仅用于条目关联；这里使用 CD2 原生路径，不作 115 CID。
	return p115.ShareFile{ID: file.Path, Name: file.Name, Size: file.Size, Directory: file.Directory}
}

func (f *mediaOrganizeFiles) scanCD2(ctx context.Context, appendFile func(p115.ShareFile, string, string, int64)) (map[string]bool, error) {
	f.cd2Snapshots = make(map[string]clouddrive2.File)
	f.settlingVideos = make(map[string]bool)
	f.localCandidates = make([]OrganizeFile, 0, 128)
	pending := make(map[string]bool)
	f.localPending = pending
	visited := make(map[string]bool)
	fileCount := 0
	var walk func(string, string, int) error
	walk = func(directory, prefix string, depth int) error {
		if visited[directory] || depth > 12 || len(visited) >= 300 {
			return fmt.Errorf("CD2 目录循环或范围过大，请缩小扫描范围")
		}
		visited[directory] = true
		files, err := f.cd2.List(ctx, directory, true)
		if err != nil {
			return fmt.Errorf("读取 CD2 目录: %w", err)
		}
		for _, file := range files {
			if err := ctx.Err(); err != nil {
				return err
			}
			if file.Path != path.Join(directory, file.Name) || file.Name == "." || file.Name == ".." || strings.Contains(file.Name, "/") {
				return fmt.Errorf("CD2 返回了无法确认归属的文件路径")
			}
			if file.ReadOnly {
				continue
			}
			relative := path.Join(prefix, file.Name)
			if mediaTemporaryName(file.Name) {
				f.settlingVideos[prefix], f.settlingVideos[directory] = true, true
				if !file.Directory {
					base := mediaTemporaryBase(file.Name)
					candidate := OrganizeFile{ShareFile: p115.ShareFile{ID: path.Join(directory, base), Name: base}, ParentID: directory, SourcePath: path.Join(prefix, base), MediaType: mediaFileType(base)}
					if candidate.MediaType != "" {
						f.localCandidates = append(f.localCandidates, candidate)
						pending[candidate.ID] = true
					}
				}
				continue
			}
			f.cd2Snapshots[file.Path] = file
			if file.Directory {
				f.directories = append(f.directories, MediaOrganizeEntry{File: OrganizeFile{ShareFile: mediaCD2ShareFile(file), ParentID: directory, SourcePath: relative, MediaType: "cleanup"}, CD2FileID: file.ID})
				if err := walk(file.Path, relative, depth+1); err != nil {
					return err
				}
				continue
			}
			fileCount++
			if fileCount > 5000 {
				return fmt.Errorf("单次最多 5000 个文件，请缩小扫描范围")
			}
			candidate := OrganizeFile{ShareFile: mediaCD2ShareFile(file), ParentID: directory, SourcePath: relative, MediaType: mediaFileType(file.Name)}
			if candidate.MediaType != "" {
				f.localCandidates = append(f.localCandidates, candidate)
			}
			modified := int64(0)
			if !file.Modified.IsZero() {
				modified = file.Modified.UnixNano()
				if time.Since(file.Modified) < 30*time.Second {
					pending[file.Path] = true
					if candidate.MediaType == "video" {
						f.settlingVideos[prefix], f.settlingVideos[directory] = true, true
					}
					continue
				}
			}
			appendFile(mediaCD2ShareFile(file), directory, relative, modified)
		}
		return nil
	}
	if err := walk(f.rule.Source, ".", 0); err != nil {
		return nil, err
	}
	return pending, nil
}

func mediaCD2Relative(root, relative string) (string, error) {
	if relative == "" || relative == "." || path.IsAbs(relative) || strings.Contains(relative, "\\") {
		return "", ErrMediaOrganizeInput
	}
	for _, part := range strings.Split(relative, "/") {
		if part == ".." || part == "" {
			return "", ErrMediaOrganizeInput
		}
	}
	full := path.Join(root, relative)
	if full == root || !mediaCloudWithin(root, full) {
		return "", ErrMediaOrganizeInput
	}
	return full, nil
}

func (f *mediaOrganizeFiles) cd2Destination(ctx context.Context, relative string, create bool) (string, error) {
	full, err := mediaCD2Relative(f.rule.Target, relative)
	if err != nil {
		return "", err
	}
	parent := f.rule.Target
	for _, name := range strings.Split(path.Dir(relative), "/") {
		if name == "." {
			continue
		}
		next := path.Join(parent, name)
		info, statErr := f.cd2.Stat(ctx, next, true)
		if errors.Is(statErr, clouddrive2.ErrNotFound) {
			if !create {
				return "", nil
			}
			info, statErr = f.cd2.CreateDirectory(ctx, parent, name)
		}
		if statErr != nil {
			return "", fmt.Errorf("准备 CD2 目标目录: %w", statErr)
		}
		if !info.Directory || info.ReadOnly || info.Path != next {
			return "", fmt.Errorf("CD2 目标路径不是可写目录")
		}
		parent = next
	}
	return full, nil
}

func (f *mediaOrganizeFiles) cd2Conflict(ctx context.Context, e MediaOrganizeEntry) error {
	destination, err := f.cd2Destination(ctx, e.Relative, false)
	if err != nil || destination == "" {
		return err
	}
	files, err := f.cd2.List(ctx, path.Dir(destination), true)
	if err != nil {
		return err
	}
	for _, file := range files {
		if strings.EqualFold(file.Name, path.Base(destination)) {
			if e.CD2Current != nil && e.CD2Current.Path == destination {
				current := e
				current.Modified = e.CD2Current.Modified
				if mediaCD2Matches(current, file, e.CD2Current.FileID) {
					return nil
				}
			}
			return errMediaTargetExists
		}
	}
	return nil
}

func mediaCD2Matches(e MediaOrganizeEntry, file clouddrive2.File, expectedID string) bool {
	if file.Directory || file.ReadOnly || file.Size != e.File.Size {
		return false
	}
	matched := false
	for algorithm, expected := range e.CD2Hashes {
		if actual := file.Hashes[algorithm]; expected != "" && actual != "" {
			if !strings.EqualFold(expected, actual) {
				return false
			}
			matched = true
		}
	}
	if expectedID != "" {
		if file.ID != expectedID {
			return false
		}
		if !matched && e.Modified != 0 && !file.Modified.IsZero() && e.Modified != file.Modified.UnixNano() {
			return false
		}
		return true
	}
	return matched
}

func (f *mediaOrganizeFiles) moveCD2(ctx context.Context, e MediaOrganizeEntry) error {
	root, expectedID := f.rule.Source, e.CD2FileID
	if e.Failure != nil && e.Failure.State == "stored" {
		root = f.rule.Failure
		if e.Failure.CD2FileID != "" {
			expectedID = e.Failure.CD2FileID
		}
	}
	source, err := mediaCD2Relative(root, e.File.SourcePath)
	if err != nil {
		return err
	}
	destination, err := f.cd2Destination(ctx, e.Relative, true)
	if err != nil {
		return err
	}
	if e.CD2Current != nil {
		current := e.CD2Current
		clean, err := clouddrive2.CleanPath(current.Path)
		if err != nil || clean != current.Path {
			return fmt.Errorf("CD2 已记录的文件位置无效")
		}
		failurePath := ""
		if f.rule.Failure != "" {
			failurePath, err = mediaCD2Relative(f.rule.Failure, e.File.SourcePath)
			if err != nil {
				return err
			}
		}
		if path.Dir(current.Path) != path.Dir(source) && current.Path != failurePath && current.Path != destination {
			return fmt.Errorf("CD2 已记录的文件位置不在本次整理计划内")
		}
		source, expectedID, e.Modified = current.Path, current.FileID, current.Modified
	}
	actual, sourceErr := f.cd2.Stat(ctx, source, true)
	if sourceErr != nil && !errors.Is(sourceErr, clouddrive2.ErrNotFound) {
		return sourceErr
	}
	if errors.Is(sourceErr, clouddrive2.ErrNotFound) {
		// 重命名已完成而移动未完成时，只接受原目录内身份完全匹配的文件。
		renamed := path.Join(path.Dir(source), path.Base(destination))
		if renamed != source {
			candidate, err := f.cd2.Stat(ctx, renamed, true)
			if err != nil && !errors.Is(err, clouddrive2.ErrNotFound) {
				return err
			}
			if err == nil && mediaCD2Matches(e, candidate, expectedID) {
				actual, source, sourceErr = candidate, renamed, nil
			}
		}
	}
	if sourceErr != nil {
		// 仅核验已计划的精确目标；跨云身份变化且没有成功回执时交由人工核对。
		target, err := f.cd2.Stat(ctx, destination, true)
		if err == nil && (mediaCD2Matches(e, target, expectedID) || mediaCD2Matches(e, target, "")) {
			return nil
		}
		return fmt.Errorf("CD2 来源文件已变化或移动结果待核实，请重新扫描")
	}
	if !mediaCD2Matches(e, actual, expectedID) {
		return fmt.Errorf("CD2 来源文件身份或大小已变化，请重新扫描")
	}
	if source == destination {
		return nil
	}
	if err := f.cd2Conflict(ctx, e); err != nil {
		return err
	}
	if actual.Name != path.Base(destination) {
		renamed, err := f.cd2.Rename(ctx, source, path.Base(destination))
		if err != nil {
			return err
		}
		if renamed != path.Join(path.Dir(source), path.Base(destination)) {
			return fmt.Errorf("CD2 重命名结果待核实")
		}
		source = renamed
		if err := f.recordCD2Location(ctx, e, source); err != nil {
			return err
		}
	}
	results, err := f.cd2.Move(ctx, []string{source}, path.Dir(destination))
	if err != nil {
		if errors.Is(err, clouddrive2.ErrExists) {
			return errMediaTargetExists
		}
		return err
	}
	if !slices.Contains(results, destination) {
		return fmt.Errorf("CD2 移动结果待核实")
	}
	return f.recordCD2Location(ctx, e, destination)
}

func (f *mediaOrganizeFiles) recordCD2Location(ctx context.Context, entry MediaOrganizeEntry, full string) error {
	if f.cd2Checkpoint == nil {
		return nil
	}
	current, err := f.cd2.Stat(ctx, full, true)
	if err != nil {
		return err
	}
	if current.Path != full || current.Directory || current.Size != entry.File.Size {
		return fmt.Errorf("CD2 写入后的文件位置未确认")
	}
	// 仅在原生写入回执已核验后记录新身份，路径型 ID 和云端新时间戳均可保留。
	return f.cd2Checkpoint(entry, current)
}

func (f *mediaOrganizeFiles) cd2CleanupCandidates(ctx context.Context, candidates []MediaOrganizeEntry) ([]MediaOrganizeEntry, error) {
	valid := make([]MediaOrganizeEntry, 0, len(candidates))
	for _, entry := range candidates {
		if entry.File.Directory {
			continue
		}
		full, err := mediaCD2Relative(f.rule.Source, entry.File.SourcePath)
		if err != nil || full != entry.File.ID {
			return nil, fmt.Errorf("CD2 清理路径无法确认")
		}
		actual, err := f.cd2.Stat(ctx, full, true)
		if errors.Is(err, clouddrive2.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if actual.ReadOnly || actual.Name != entry.File.Name || !mediaCD2Matches(entry, actual, entry.CD2FileID) {
			continue
		}
		if entry.Modified != 0 && actual.Modified.UnixNano() != entry.Modified {
			continue
		}
		valid = append(valid, entry)
	}
	return valid, nil
}

func (f *mediaOrganizeFiles) cd2SidecarStillOrphan(ctx context.Context, entry MediaOrganizeEntry) (bool, error) {
	if entry.File.MediaType != "subtitle" && entry.File.MediaType != "attachment" {
		return true, nil
	}
	files, err := f.cd2.List(ctx, path.Dir(entry.File.ID), true)
	if err != nil {
		return false, err
	}
	videos := make([]OrganizeFile, 0, len(files))
	for _, file := range files {
		if !file.Directory && (mediaFileType(file.Name) == "video" || mediaTemporaryName(file.Name)) {
			if mediaTemporaryName(file.Name) {
				return false, nil
			}
			videos = append(videos, OrganizeFile{ShareFile: mediaCD2ShareFile(file), ParentID: entry.File.ParentID, SourcePath: path.Join(path.Dir(entry.File.SourcePath), file.Name), MediaType: "video"})
		}
	}
	if entry.File.MediaType == "subtitle" {
		owner, _ := organizeSubtitleOwner(entry.File, videos, organizeSubtitleMatcher(videos))
		return owner == "", nil
	}
	if mediaAttachmentHasVideo(entry.File, videos) {
		return false, nil
	}
	scope, _, shared := mediaAttachmentFolder(entry.File)
	if !shared {
		return true, nil
	}
	full := f.rule.Source
	if scope != "." {
		full, err = mediaCD2Relative(f.rule.Source, scope)
		if err != nil {
			return false, err
		}
	}
	siblings, err := f.cd2.List(ctx, full, true)
	if err != nil {
		return false, err
	}
	return !slices.ContainsFunc(siblings, func(file clouddrive2.File) bool {
		return !file.Directory && (mediaFileType(file.Name) == "video" || mediaTemporaryName(file.Name)) || file.Directory && file.Name != ".actors" && file.Name != "extrafanart"
	}), nil
}

func (s *MediaOrganizer) cleanupCD2(ctx context.Context, f *mediaOrganizeFiles, run *MediaOrganizeRun) error {
	candidates := slices.Clone(f.unrelated)
	// CD2 没有仅删除空目录的原子接口，目录统一保留，避免递归删除并发新内容。
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return err
		}
		orphan, err := f.cd2SidecarStillOrphan(ctx, candidate)
		if err != nil {
			return fmt.Errorf("复核 CD2 附件: %w", err)
		}
		if !orphan {
			continue
		}
		valid, err := f.cd2CleanupCandidates(ctx, []MediaOrganizeEntry{candidate})
		if err != nil {
			return err
		}
		if len(valid) == 0 {
			continue
		}
		entry := candidate
		entry.CD2Hashes = maps.Clone(candidate.CD2Hashes)
		entry.Status, entry.Message = "error", "CD2 清理结果待核实；下轮监控会重新扫描"
		entry.Result = &MediaOrganizeResult{Status: "error", Message: entry.Message, At: time.Now()}
		index := len(run.Entries)
		run.Entries = append(run.Entries, entry)
		if err := s.checkpoint(run); err != nil {
			return err
		}
		// 检查点落盘后再次检查普通文件身份；目录始终不进入删除候选。
		valid, err = f.cd2CleanupCandidates(ctx, []MediaOrganizeEntry{candidate})
		if err != nil {
			return err
		}
		orphan, err = f.cd2SidecarStillOrphan(ctx, candidate)
		if err != nil {
			return err
		}
		if len(valid) == 0 || !orphan {
			run.Entries[index].Status, run.Entries[index].Message, run.Entries[index].Result = "retained", "文件或目录已变化，已保留来源", nil
			if err := s.checkpoint(run); err != nil {
				return err
			}
			continue
		}
		err = f.cd2.Delete(ctx, []string{entry.File.ID})
		result := &run.Entries[index]
		if err == nil {
			result.Status, result.Message = "done", "已通过 CD2 删除无关文件"
		} else {
			result.Message += "：" + err.Error()
		}
		result.Result = &MediaOrganizeResult{Status: result.Status, Message: result.Message, At: time.Now()}
		if err == nil && entry.File.MediaType == "subtitle" {
			result.Result.Status = "error"
		}
		if saveErr := s.checkpoint(run); saveErr != nil {
			return saveErr
		}
		if err != nil {
			return fmt.Errorf("CD2 清理已停止: %w", err)
		}
	}
	return nil
}
