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
				mediaCD2SetLocation(&run.Entries[i], current)
				return s.checkpoint(run)
			}
		}
		return ErrMediaOrganizeInput
	}
	files.cd2Duplicate = func(entry MediaOrganizeEntry) error {
		for i := range run.Entries {
			if run.Entries[i].File.ID == entry.File.ID {
				run.Entries[i].CD2Duplicate = true
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
	ctx = clouddrive2.WithDirectoryCache(ctx)
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
	if f.cd2Snapshots == nil || !f.cd2Shallow {
		f.cd2Snapshots = make(map[string]clouddrive2.File)
	}
	f.settlingVideos = make(map[string]bool)
	f.localCandidates = make([]OrganizeFile, 0, 128)
	pending := make(map[string]bool)
	f.localPending = pending
	visited := make(map[string]bool)
	fileCount := 0
	var walk func(string, string, int) error
	walk = func(directory, prefix string, depth int) error {
		if err := mediaVisitScanDirectory(visited, directory, directory, depth); err != nil {
			return fmt.Errorf("扫描 CD2 目录: %w", err)
		}
		files, err := f.cd2.List(ctx, directory, true)
		if err != nil {
			return fmt.Errorf("读取 CD2 目录: %w", err)
		}
		if err := f.rememberCD2Directory(directory, files); err != nil {
			return err
		}
		for _, file := range files {
			if err := ctx.Err(); err != nil {
				return err
			}
			if file.ReadOnly {
				continue
			}
			relative := path.Join(prefix, file.Name)
			if file.Directory {
				candidate := MediaOrganizeEntry{File: OrganizeFile{ShareFile: mediaCD2ShareFile(file), ParentID: directory, SourcePath: relative, MediaType: "cleanup"}, CD2FileID: file.ID}
				if f.cleanupDirectories == nil {
					f.cleanupDirectories = make(map[string]MediaOrganizeEntry)
				}
				f.cleanupDirectories[file.Path] = candidate
			}
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
				candidate := MediaOrganizeEntry{File: OrganizeFile{ShareFile: mediaCD2ShareFile(file), ParentID: directory, SourcePath: relative, MediaType: "cleanup"}, CD2FileID: file.ID}
				f.directories = append(f.directories, candidate)
				if !f.cd2Shallow {
					if err := walk(file.Path, relative, depth+1); err != nil {
						return err
					}
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
			}
			appendFile(mediaCD2ShareFile(file), directory, relative, modified)
		}
		return nil
	}
	root := f.cd2ScanPath
	if root == "" {
		root = f.rule.Source
	}
	if !mediaCloudWithin(f.rule.Source, root) {
		return nil, ErrMediaOrganizeInput
	}
	prefix := strings.TrimPrefix(strings.TrimPrefix(root, f.rule.Source), "/")
	if prefix == "" {
		prefix = "."
	}
	if err := walk(root, prefix, 0); err != nil {
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
	file, err := f.cd2Collision(ctx, e)
	if err != nil {
		return err
	}
	if file == nil || f.rule.DeleteDuplicates && mediaCD2SameSHA1(e, *file) || file != nil && mediaAttachmentSameDestinationRole(e, file.Name) {
		return nil
	}
	return errMediaTargetExists
}

func mediaCD2SameSHA1(e MediaOrganizeEntry, target clouddrive2.File) bool {
	// 仅比较 CD2 已提供的 SHA1，不补查、下载或增加大小判重条件。
	sourceHash, targetHash := strings.TrimSpace(e.CD2Hashes[2]), strings.TrimSpace(target.Hashes[2])
	return !target.Directory && !target.ReadOnly && sourceHash != "" && targetHash != "" && strings.EqualFold(sourceHash, targetHash)
}

func (f *mediaOrganizeFiles) cd2Collision(ctx context.Context, e MediaOrganizeEntry) (*clouddrive2.File, error) {
	destination, err := f.cd2Destination(ctx, e.Relative, false)
	if err != nil || destination == "" {
		return nil, err
	}
	files, err := f.cd2.List(ctx, path.Dir(destination), true)
	if err != nil {
		return nil, err
	}
	for _, file := range files {
		if strings.EqualFold(file.Name, path.Base(destination)) {
			if e.CD2Current != nil && e.CD2Current.Path == destination {
				current := e
				current.Modified = e.CD2Current.Modified
				if mediaCD2Matches(current, file, e.CD2Current.FileID) {
					return nil, nil
				}
			}
			return &file, nil
		}
	}
	return nil, nil
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
		atDestination := path.Dir(current.Path) == path.Dir(destination) && strings.EqualFold(path.Base(current.Path), path.Base(destination))
		if path.Dir(current.Path) != path.Dir(source) && current.Path != failurePath && !atDestination {
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
			if err == nil && (mediaCD2Matches(e, candidate, expectedID) || mediaCD2Matches(e, candidate, "")) {
				actual, source, sourceErr = candidate, renamed, nil
				expectedID = candidate.ID
			}
		}
	}
	if sourceErr != nil {
		recovering := e.Failure != nil && e.Failure.State == "moving" && f.rule.Target == f.rule.Failure
		if f.rule.DeleteDuplicates && (e.CD2Duplicate || recovering) {
			keeper, err := f.cd2Collision(ctx, e)
			if err != nil {
				return err
			}
			if keeper != nil && mediaCD2SameSHA1(e, *keeper) {
				return f.finishCD2Duplicate(ctx, e, source, expectedID, *keeper, true)
			}
		}
		// 仅核验已计划的精确目标；跨云身份变化且没有成功回执时交由人工核对。
		target, err := f.cd2.Stat(ctx, destination, true)
		if err != nil && !errors.Is(err, clouddrive2.ErrNotFound) {
			return err
		}
		if err == nil && (mediaCD2Matches(e, target, expectedID) || mediaCD2Matches(e, target, "")) {
			return nil
		}
		return fmt.Errorf("CD2 来源文件已变化或移动结果待核实，请重新扫描")
	}
	if !mediaCD2Matches(e, actual, expectedID) {
		return fmt.Errorf("CD2 来源文件身份或大小已变化，请重新扫描")
	}
	atDestination := path.Dir(source) == path.Dir(destination) && strings.EqualFold(path.Base(source), path.Base(destination))
	if atDestination && e.CD2Duplicate && mediaCD2SameSHA1(e, actual) {
		// 已保存的去重保留位置再次核验成功时，保留完成语义且不重复回收来源。
		actual.Hashes = maps.Clone(actual.Hashes)
		return &mediaDuplicateReceipt{fileID: actual.ID, size: actual.Size, cd2: &actual}
	}
	if source == destination {
		return nil
	}
	collision, err := f.cd2Collision(ctx, e)
	if err != nil {
		return err
	}
	if collision != nil {
		if !f.rule.DeleteDuplicates || !mediaCD2SameSHA1(e, *collision) {
			return errMediaTargetExists
		}
		return f.finishCD2Duplicate(ctx, e, source, expectedID, *collision, false)
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

// 去重只按已提供的 SHA1 判定；大小、原生 ID 和路径用于确认选中的同一份保留文件。
func (f *mediaOrganizeFiles) finishCD2Duplicate(ctx context.Context, entry MediaOrganizeEntry, source, expectedID string, keeper clouddrive2.File, missing bool) error {
	if !f.rule.DeleteDuplicates || keeper.ID == "" || keeper.Path == source || !mediaCD2SameSHA1(entry, keeper) {
		return errMediaTargetExists
	}
	verifyKeeper := func() (clouddrive2.File, error) {
		current, err := f.cd2.Stat(ctx, keeper.Path, true)
		if err != nil {
			return clouddrive2.File{}, err
		}
		if current.ID != keeper.ID || current.Path != keeper.Path || current.Name != keeper.Name || current.Name != path.Base(current.Path) ||
			current.Size != keeper.Size || !mediaCD2SameSHA1(entry, current) || !keeper.Modified.IsZero() && !current.Modified.Equal(keeper.Modified) {
			return clouddrive2.File{}, fmt.Errorf("CD2 去重保留文件身份或位置已变化")
		}
		return current, nil
	}
	verifyMissing := func() error {
		if _, err := f.cd2.Stat(ctx, source, true); !errors.Is(err, clouddrive2.ErrNotFound) {
			if err != nil {
				return err
			}
			return fmt.Errorf("CD2 来源重复文件仍存在，删除结果待核实")
		}
		children, err := f.cd2.List(ctx, path.Dir(source), true)
		if err != nil {
			return err
		}
		for _, file := range children {
			pathIdentity := expectedID == "" || strings.HasPrefix(expectedID, "/")
			if expectedID != "" && file.ID == expectedID || missing && pathIdentity && mediaCD2SameSHA1(entry, file) {
				return fmt.Errorf("CD2 来源目录仍有原身份或同内容文件，无法确认去重完成")
			}
		}
		return nil
	}
	if err := f.cd2CheckDirectory(ctx, path.Dir(keeper.Path)); err != nil {
		return err
	}
	if missing {
		if err := verifyMissing(); err != nil {
			return err
		}
		// 历史 moving 没有保留 ID 时，仅接受精确目录内唯一的同名候选。
		children, err := f.cd2.List(ctx, path.Dir(keeper.Path), true)
		if err != nil {
			return err
		}
		names := 0
		for _, file := range children {
			if strings.EqualFold(file.Name, keeper.Name) {
				names++
			}
		}
		if names != 1 {
			return fmt.Errorf("CD2 去重目标无法唯一确认")
		}
	} else {
		if f.cd2Duplicate != nil {
			if err := f.cd2Duplicate(entry); err != nil {
				return err
			}
		}
		actual, err := f.cd2.Stat(ctx, source, true)
		if err != nil {
			return err
		}
		if actual.Path != source || actual.Name != path.Base(source) || !mediaCD2Matches(entry, actual, expectedID) {
			return fmt.Errorf("CD2 来源重复文件身份已变化，保留来源")
		}
	}
	if _, err := verifyKeeper(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !missing {
		if err := f.cd2.Delete(ctx, []string{source}); err != nil {
			return err
		}
	}
	if err := verifyMissing(); err != nil {
		return err
	}
	current, err := verifyKeeper()
	if err != nil {
		return err
	}
	current.Hashes = maps.Clone(current.Hashes)
	return &mediaDuplicateReceipt{fileID: current.ID, size: current.Size, cd2: &current}
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
	if len(candidates) == 0 {
		return nil, nil
	}
	parent := path.Dir(candidates[0].File.ID)
	current := &mediaOrganizeFiles{rule: f.rule, cd2: f.cd2, cd2ScanPath: parent, cd2Shallow: true}
	for _, entry := range candidates {
		full, err := mediaCD2Relative(f.rule.Source, entry.File.SourcePath)
		if err != nil || full != entry.File.ID || path.Dir(full) != parent {
			return nil, fmt.Errorf("CD2 清理路径无法确认")
		}
		if scope, _, shared := mediaAttachmentFolder(entry.File); entry.File.MediaType == "attachment" && shared {
			current.cd2ScanPath, current.cd2Shallow = path.Join(f.rule.Source, scope), false
		}
	}
	// 一次读取同时核验本批文件身份和附件归属，不再逐文件扫描相同目录。
	if _, err := current.scanCD2(ctx, func(p115.ShareFile, string, string, int64) {}); err != nil {
		return nil, err
	}
	// 共享附件可能检查多个子目录，回收前再统一核对实际父目录中的文件身份。
	if !current.cd2Shallow {
		children, err := f.cd2.List(clouddrive2.WithDirectoryCache(ctx), parent, true)
		if err != nil {
			return nil, err
		}
		for _, entry := range candidates {
			delete(current.cd2Snapshots, entry.File.ID)
		}
		for _, file := range children {
			current.cd2Snapshots[file.Path] = file
		}
	}
	owners := mediaLocalCompanionOwners(current.localCandidates)
	valid := make([]MediaOrganizeEntry, 0, len(candidates))
	for _, entry := range candidates {
		if entry.File.Directory {
			continue
		}
		if entry.File.MediaType == "subtitle" || entry.File.MediaType == "attachment" {
			if !f.orphanDue[entry.File.ID] || len(owners[entry.File.ID]) > 0 {
				continue
			}
		}
		actual, present := current.cd2Snapshots[entry.File.ID]
		if !present {
			continue
		}
		if actual.Path != entry.File.ID || actual.Name != entry.File.Name || !mediaCD2Matches(entry, actual, entry.CD2FileID) {
			continue
		}
		if entry.Modified != 0 && actual.Modified.UnixNano() != entry.Modified {
			continue
		}
		valid = append(valid, entry)
	}
	return valid, nil
}

func (s *MediaOrganizer) cleanupCD2(ctx context.Context, f *mediaOrganizeFiles, run *MediaOrganizeRun) error {
	parents := make(map[string][]MediaOrganizeEntry)
	order := make([]string, 0)
	for _, entry := range f.unrelated {
		if entry.File.Directory {
			continue
		}
		parent := path.Dir(entry.File.ID)
		if _, seen := parents[parent]; !seen {
			order = append(order, parent)
		}
		parents[parent] = append(parents[parent], entry)
	}
	indices := make(map[string]int, len(run.Entries))
	for i, entry := range run.Entries {
		indices[entry.File.ID] = i
	}
	for _, parent := range order {
		candidates := parents[parent]
		for start := 0; start < len(candidates); start += 100 {
			if err := ctx.Err(); err != nil {
				return err
			}
			batch := candidates[start:min(start+100, len(candidates))]
			for _, entry := range batch {
				entry.Status, entry.Message = "error", "CD2 清理尚未完成，留待下轮扫描"
				entry.Result = &MediaOrganizeResult{Status: entry.Status, Message: entry.Message, At: time.Now()}
				index, found := indices[entry.File.ID]
				if !found {
					indices[entry.File.ID] = len(run.Entries)
					run.Entries = append(run.Entries, entry)
				} else {
					run.Entries[index] = entry
				}
			}
			if err := s.checkpoint(run); err != nil {
				return err
			}
			// 落盘后只做一次实时核验，同父文件共用扫描快照和删除后的目录读取。
			work := clouddrive2.WithDirectoryCache(ctx)
			valid, err := f.cd2CleanupCandidates(work, batch)
			if err != nil {
				return err
			}
			paths := make([]string, 0, len(valid))
			selected := make(map[string]bool, len(valid))
			for _, entry := range valid {
				paths = append(paths, entry.File.ID)
				selected[entry.File.ID] = true
			}
			remaining := make(map[string]bool)
			var writeErr, readErr error
			if len(paths) > 0 {
				writeErr = f.cd2.Delete(work, paths)
				if writeErr != nil {
					// 超时可能已应用部分删除；只读回本批父目录，按实际存在情况给结果。
					var children []clouddrive2.File
					children, readErr = f.cd2.List(clouddrive2.WithDirectoryCache(ctx), parent, true)
					for _, file := range children {
						remaining[file.Path] = true
					}
				}
			}
			var batchErr error
			for _, candidate := range batch {
				entry := &run.Entries[indices[candidate.File.ID]]
				if !selected[candidate.File.ID] {
					entry.Status, entry.Message, entry.Result = "retained", "文件或附件归属已变化，已保留来源", nil
					continue
				}
				if writeErr == nil || readErr == nil && !remaining[candidate.File.ID] {
					entry.Status, entry.Message = "done", "已通过 CD2 清理来源文件"
					if f.orphanDue[candidate.File.ID] {
						s.mu.Lock()
						s.forgetOrphanLocked(run.Rule, candidate.File.ID)
						s.mu.Unlock()
					}
				} else {
					batchErr = errors.Join(writeErr, readErr)
					entry.Message += "：" + batchErr.Error()
				}
				entry.Result = &MediaOrganizeResult{Status: entry.Status, Message: entry.Message, At: time.Now()}
			}
			if err := s.checkpoint(run); err != nil {
				return err
			}
			if batchErr != nil {
				return fmt.Errorf("CD2 清理已停止: %w", batchErr)
			}
		}
	}
	return nil
}
