package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"strm115/backend/internal/client/clouddrive2"
	"strm115/backend/internal/client/p115"
)

type mediaOrganizeDrive interface {
	OrganizeDrive
	ReadDirectory(context.Context, string, string) (p115.DriveDirectory, error)
}

// 去重完成回执只在当前调用中传递已核验的保留身份，不形成持久化操作账本。
type mediaDuplicateReceipt struct {
	fileID   string
	parentID string
	size     int64
	cd2      *clouddrive2.File
}

func (*mediaDuplicateReceipt) Error() string { return errMediaDuplicateDone.Error() }
func (*mediaDuplicateReceipt) Unwrap() error { return errMediaDuplicateDone }

const (
	mediaOrganizeScanDirectoryLimit = 5000
	mediaOrganizeScanDepthLimit     = 12
)

func mediaCheckScanDirectoryCount(count int, location string) error {
	if count > mediaOrganizeScanDirectoryLimit {
		return fmt.Errorf("单次最多扫描 %d 个目录（包含来源根目录），当前发现 %d 个：%s", mediaOrganizeScanDirectoryLimit, count, location)
	}
	return nil
}

// 根目录计入总数，各存储使用相同边界；重复目录不能形成完整扫描证明。
func mediaVisitScanDirectory(visited map[string]bool, id, location string, depth int) error {
	if visited[id] {
		return fmt.Errorf("扫描发现重复目录或目录循环：%s", location)
	}
	if depth > mediaOrganizeScanDepthLimit {
		return fmt.Errorf("扫描目录层级超过 %d 层（当前 %d 层）：%s", mediaOrganizeScanDepthLimit, depth, location)
	}
	if err := mediaCheckScanDirectoryCount(len(visited)+1, location); err != nil {
		return err
	}
	visited[id] = true
	return nil
}

type mediaOrganizeFiles struct {
	rule                 MediaOrganizeRule
	drive                OrganizeDrive
	cookie               string
	lists                map[string][]p115.ShareFile
	unrelated            []MediaOrganizeEntry
	directories          []MediaOrganizeEntry
	cleanupDirectories   map[string]MediaOrganizeEntry
	scanned              []MediaOrganizeEntry
	localSnapshots       map[string]os.FileInfo
	localSnapshotKeys    map[string]string
	settlingVideos       map[string]bool
	failureChecked       bool
	localReady           func(string, os.FileInfo) bool
	localPrune           func(map[string]bool, time.Time)
	localCandidates      []OrganizeFile
	localPending         map[string]bool
	cd2                  clouddrive2.Filesystem
	cd2Lease             *CD2Lease
	cd2Snapshots         map[string]clouddrive2.File
	cd2DirectoryChildren map[string]map[string]bool
	cd2Checkpoint        func(MediaOrganizeEntry, clouddrive2.File) error
	cd2Duplicate         func(MediaOrganizeEntry) error
	cd2ScanPath          string
	cd2Shallow           bool
	recognized           map[string]bool
	naming               map[string]preparedMediaNaming
	scan115ID            string
	scan115Relative      string
	shallow115           bool
	pending115           map[string]bool
	pending115Dirs       map[string]bool
	orphanDue            map[string]bool
	cloudEmpty           *media115EmptyState
}

func (s *MediaOrganizer) files(ctx context.Context, rule MediaOrganizeRule) (*mediaOrganizeFiles, error) {
	f := &mediaOrganizeFiles{rule: rule, drive: s.drive, lists: make(map[string][]p115.ShareFile)}
	if rule.Storage == "cd2" {
		if err := s.openCD2Files(ctx, f); err != nil {
			return nil, err
		}
	}
	if rule.Storage == "local" && rule.RealtimeEnabled {
		f.localReady = s.localTaskReadiness(rule)
		f.localPrune = func(seen map[string]bool, started time.Time) { s.pruneLocalObservations(rule, seen, started) }
	}
	if rule.Storage == "115" {
		drive, ok := s.drive.(mediaOrganizeDrive)
		if !ok || rule.AccountID == "" {
			return nil, fmt.Errorf("115 目录服务未配置")
		}
		a, err := s.accounts.Primary(rule.AccountID)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(a.Cookie) == "" {
			return nil, fmt.Errorf("115 主账号尚未登录")
		}
		f.cookie = a.Cookie
		for _, selected := range []struct{ id, path string }{{rule.SourceID, rule.Source}, {rule.TargetID, rule.Target}} {
			dir, err := drive.ReadDirectory(ctx, selected.id, f.cookie)
			if err != nil {
				return nil, fmt.Errorf("核验 115 目录: %w", &organizeSafeError{err: err, message: organizePublicError(err, f.cookie)})
			}
			if dir.CID != selected.id || dir.Path != selected.path {
				return nil, fmt.Errorf("115 目录已变化，请重新选择目录")
			}
		}
	}
	return f, nil
}

func mediaLocalRoot(value string) (string, error) {
	if !filepath.IsAbs(value) {
		return "", fmt.Errorf("%w：请输入容器内可访问的绝对路径", ErrMediaOrganizeInput)
	}
	root, err := filepath.EvalSymlinks(filepath.Clean(value))
	if err != nil {
		return "", fmt.Errorf("检查目录: %w", err)
	}
	info, err := os.Stat(root)
	if err != nil {
		return "", fmt.Errorf("检查目录: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%w：路径不是目录", ErrMediaOrganizeInput)
	}
	return root, nil
}

func (f *mediaOrganizeFiles) rememberLocalSnapshot(full, id string, info os.FileInfo) error {
	identity, err := mediaLocalFileIdentityKey(full, info)
	if err != nil {
		return err
	}
	f.localSnapshots[id], f.localSnapshotKeys[id] = info, identity
	return nil
}

// Check every component, not only the final filename. Symlink traversal is not
// part of a monitored root; mounted directories remain ordinary filesystem paths.
func mediaLocalPath(root, relative string, create bool) (string, error) {
	currentRoot, err := mediaLocalRoot(root)
	if err != nil {
		return "", err
	}
	if currentRoot != root {
		return "", fmt.Errorf("目录挂载或链接已改变，请重新保存配置")
	}
	clean := filepath.Clean(filepath.FromSlash(relative))
	if clean == "." || filepath.IsAbs(clean) || !mediaWithin(root, filepath.Join(root, clean)) {
		return "", ErrMediaOrganizeInput
	}
	current := root
	for _, part := range strings.Split(clean, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, statErr := os.Lstat(current)
		if errors.Is(statErr, os.ErrNotExist) && create {
			if err := os.Mkdir(current, 0755); err != nil && !errors.Is(err, os.ErrExist) {
				return "", fmt.Errorf("创建整理目录: %w", err)
			}
			info, statErr = os.Lstat(current)
		}
		if statErr != nil {
			return "", fmt.Errorf("检查整理路径: %w", statErr)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("整理路径包含符号链接")
		}
	}
	return current, nil
}

func mediaFileType(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".sup", ".smi", ".sami", ".ttml", ".dfxp", ".scc":
		return "subtitle"
	}
	if strings.EqualFold(path.Ext(name), ".iso") {
		return ""
	}
	if strings.EqualFold(path.Ext(name), ".strm") {
		return "video"
	}
	if mediaAttachment(name) {
		return "attachment"
	}
	return organizeMedia(name)
}

func (f *mediaOrganizeFiles) scan(ctx context.Context) ([]MediaOrganizeEntry, error) {
	out := make([]MediaOrganizeEntry, 0, 128)
	appendFile := func(file p115.ShareFile, parent, relative string, modified int64) {
		cd2ID := f.cd2Snapshots[file.ID].ID
		cd2Hashes := maps.Clone(f.cd2Snapshots[file.ID].Hashes)
		typeName := mediaFileType(file.Name)
		if typeName == "" {
			if !mediaKeepFile(file.Name) {
				f.unrelated = append(f.unrelated, MediaOrganizeEntry{File: OrganizeFile{ShareFile: file, ParentID: parent, SourcePath: relative, MediaType: "cleanup"}, Modified: modified, CD2FileID: cd2ID, CD2Hashes: cd2Hashes})
			}
			return
		}
		item := OrganizeFile{ShareFile: file, ParentID: parent, SourcePath: relative, MediaType: typeName, Season: 1}
		mediaOrganizeFileNumbering(&item)
		item.NamingValues = mediaFileValues(file.Name, DownloadFileSpec{})
		out = append(out, MediaOrganizeEntry{File: item, Modified: modified, Status: "pending", CD2FileID: cd2ID, CD2Hashes: cd2Hashes})
	}
	if f.rule.Storage == "cd2" {
		pending, err := f.scanCD2(ctx, appendFile)
		if err != nil {
			return nil, err
		}
		return f.holdLocalCompanions(out, pending), nil
	}
	if f.rule.Storage == "local" {
		f.localSnapshots = make(map[string]os.FileInfo)
		f.localSnapshotKeys = make(map[string]string)
		f.settlingVideos = make(map[string]bool)
		root, err := mediaLocalRoot(f.rule.Source)
		if err != nil {
			return nil, err
		}
		if root != f.rule.Source {
			return nil, fmt.Errorf("来源目录已改变")
		}
		if _, err = mediaLocalRoot(f.rule.Target); err != nil {
			return nil, err
		}
		fileCount := 0
		visited := map[string]bool{root: true}
		seen := make(map[string]bool)
		started := time.Now()
		pending := make(map[string]bool)
		f.localPending = pending
		f.localCandidates = make([]OrganizeFile, 0, 128)
		err = filepath.WalkDir(root, func(full string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if full == root {
				return nil
			}
			rel, err := filepath.Rel(root, full)
			if err != nil {
				return err
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return nil
			}
			if !entry.IsDir() {
				fileCount++
				if fileCount > 5000 {
					return fmt.Errorf("单次最多 5000 个文件，请缩小扫描范围")
				}
				seen[full] = true
			}
			if mediaTemporaryName(entry.Name()) {
				f.settlingVideos[filepath.ToSlash(filepath.Dir(rel))] = true
				if entry.IsDir() {
					info, err := entry.Info()
					if err != nil {
						return err
					}
					id := filepath.ToSlash(rel)
					if err := f.rememberLocalSnapshot(full, id, info); err != nil {
						return err
					}
					f.directories = append(f.directories, MediaOrganizeEntry{File: OrganizeFile{ShareFile: p115.ShareFile{ID: id, Name: entry.Name(), Directory: true}, ParentID: filepath.ToSlash(filepath.Dir(rel)), SourcePath: id, MediaType: "cleanup"}})
					return filepath.SkipDir
				}
				candidate := mediaLocalCandidate(filepath.ToSlash(rel), mediaTemporaryBase(entry.Name()), 0)
				if candidate.MediaType != "" {
					f.localCandidates = append(f.localCandidates, candidate)
					pending[candidate.ID] = true
				}
				return nil
			}
			if entry.IsDir() {
				if err := mediaVisitScanDirectory(visited, full, full, strings.Count(rel, string(filepath.Separator))+1); err != nil {
					return err
				}
				info, err := entry.Info()
				if err != nil {
					return err
				}
				id := filepath.ToSlash(rel)
				if err := f.rememberLocalSnapshot(full, id, info); err != nil {
					return err
				}
				f.directories = append(f.directories, MediaOrganizeEntry{File: OrganizeFile{ShareFile: p115.ShareFile{ID: id, Name: entry.Name(), Directory: true}, ParentID: filepath.ToSlash(filepath.Dir(rel)), SourcePath: id, MediaType: "cleanup"}})
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return nil
			}
			id := filepath.ToSlash(rel)
			if typeName := mediaFileType(entry.Name()); !mediaKeepFile(entry.Name()) || typeName == "subtitle" || typeName == "attachment" {
				if err := f.rememberLocalSnapshot(full, id, info); err != nil {
					return err
				}
			} else {
				f.localSnapshots[id] = info
			}
			candidate := mediaLocalCandidate(filepath.ToSlash(rel), entry.Name(), info.Size())
			if candidate.MediaType != "" {
				f.localCandidates = append(f.localCandidates, candidate)
			}
			ready := time.Since(info.ModTime()) >= 30*time.Second
			if f.localReady != nil {
				ready = f.localReady(full, info)
			}
			if !ready || mediaLocalDownloadPending(full) {
				pending[candidate.ID] = true
				if mediaFileType(entry.Name()) == "video" {
					f.settlingVideos[filepath.ToSlash(filepath.Dir(rel))] = true
				}
				return nil
			} // Allow uploads/copies to settle.
			appendFile(p115.ShareFile{ID: filepath.ToSlash(rel), Name: entry.Name(), Size: info.Size()}, filepath.ToSlash(filepath.Dir(rel)), filepath.ToSlash(rel), info.ModTime().UnixNano())
			if len(out)+len(f.unrelated) > 5000 {
				return fmt.Errorf("单次最多 5000 个文件，请缩小扫描范围")
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("扫描本地目录: %w", err)
		}
		if f.localPrune != nil {
			f.localPrune(seen, started)
		}
		out = f.holdLocalCompanions(out, pending)
	} else {
		// 每次扫描重新建立来源账本，失败的再次扫描不能沿用上一次完整证明。
		if !f.shallow115 {
			f.cloudEmpty = nil
			f.lists = make(map[string][]p115.ShareFile)
		}
		f.localCandidates = make([]OrganizeFile, 0, 128)
		f.settlingVideos = make(map[string]bool)
		f.localPending = make(map[string]bool)
		var directoryDrive media115DirectoryDrive
		if f.shallow115 {
			var supported bool
			directoryDrive, supported = f.drive.(media115DirectoryDrive)
			if !supported {
				return nil, fmt.Errorf("115 完整目录读取服务未配置")
			}
		}
		visited := make(map[string]bool)
		var walk func(string, string, int) error
		walk = func(cid, prefix string, depth int) error {
			if cid == f.rule.TargetID || cid != "" && cid == f.rule.FailureID {
				return fmt.Errorf("目标或失败目录不能位于来源目录内")
			}
			if err := mediaVisitScanDirectory(visited, cid, path.Join(f.rule.Source, prefix), depth); err != nil {
				return err
			}
			if f.pending115Dirs[cid] {
				f.settlingVideos[cid] = true
			}
			var files []p115.ShareFile
			var err error
			if f.shallow115 {
				var directory p115.DriveDirectory
				directory, files, err = directoryDrive.ReadDirectoryFiles(ctx, cid, f.cookie)
				if err == nil {
					err = f.check115Directory(directory, cid, prefix)
				}
				if err == nil {
					f.lists[cid] = files
				}
			} else {
				files, err = f.list(ctx, cid)
			}
			if err != nil {
				return err
			}
			f.remember115Directory(cid, prefix, files)
			for _, file := range files {
				if err := ctx.Err(); err != nil {
					return err
				}
				if file.Directory {
					if file.ID == f.rule.TargetID || file.ID != "" && file.ID == f.rule.FailureID {
						return fmt.Errorf("目标或失败目录不能位于来源目录内")
					}
					f.directories = append(f.directories, MediaOrganizeEntry{File: OrganizeFile{ShareFile: file, ParentID: cid, SourcePath: path.Join(prefix, file.Name), MediaType: "cleanup"}})
					if !f.shallow115 {
						if err := walk(file.ID, path.Join(prefix, file.Name), depth+1); err != nil {
							return err
						}
					}
				} else {
					relative := path.Join(prefix, file.Name)
					candidate := OrganizeFile{ShareFile: file, ParentID: cid, SourcePath: relative, MediaType: mediaFileType(file.Name)}
					if f.pending115[file.ID] {
						f.localPending[file.ID], f.settlingVideos[cid] = true, true
						if candidate.MediaType != "" {
							f.localCandidates = append(f.localCandidates, candidate)
						}
						continue
					}
					if mediaTemporaryName(file.Name) {
						candidate.Name, candidate.MediaType = mediaTemporaryBase(file.Name), mediaFileType(mediaTemporaryBase(file.Name))
						f.settlingVideos[cid] = true
						f.localPending[file.ID] = true
						if candidate.MediaType != "" {
							f.localCandidates = append(f.localCandidates, candidate)
						}
						continue
					}
					if candidate.MediaType != "" {
						f.localCandidates = append(f.localCandidates, candidate)
					}
					appendFile(file, cid, relative, 0)
				}
				if len(out)+len(f.unrelated) > 5000 {
					return fmt.Errorf("单次最多 5000 个文件")
				}
			}
			return nil
		}
		root, relative := f.rule.SourceID, ""
		if f.shallow115 {
			root, relative = f.scan115ID, f.scan115Relative
		}
		if err := walk(root, relative, 0); err != nil {
			return nil, err
		}
		if !f.shallow115 && f.cloudEmpty != nil {
			f.cloudEmpty.complete = true
		}
		if f.shallow115 {
			out = f.holdLocalCompanions(out, f.localPending)
		}
	}
	return out, nil
}

func (f *mediaOrganizeFiles) list(ctx context.Context, cid string) ([]p115.ShareFile, error) {
	if list, ok := f.lists[cid]; ok {
		return list, nil
	}
	list, err := listOrganizeFiles(ctx, f.drive, cid, f.cookie)
	if err == nil {
		f.lists[cid] = list
	}
	return list, err
}

func (f *mediaOrganizeFiles) cloudDestination(ctx context.Context, relative string, create bool) (string, error) {
	cid := f.rule.TargetID
	for _, name := range strings.Split(path.Dir(relative), "/") {
		if name == "." || name == "" {
			continue
		}
		files, err := f.list(ctx, cid)
		if err != nil {
			return "", err
		}
		next, err := organizeFindDirectory(files, name)
		if err != nil {
			return "", err
		}
		if next == "" {
			if !create {
				return "", nil
			}
			next, err = f.drive.CreateDirectory(ctx, cid, name, f.cookie)
			if err != nil {
				return "", err
			}
			f.lists[cid] = append(f.lists[cid], p115.ShareFile{ID: next, Name: name, Directory: true})
		}
		cid = next
	}
	return cid, nil
}

func (f *mediaOrganizeFiles) conflict(ctx context.Context, e MediaOrganizeEntry) error {
	if e.AttachmentKeep != nil {
		return nil
	}
	if e.Relative == "" {
		return nil
	}
	if f.rule.Storage == "cd2" {
		return f.cd2Conflict(ctx, e)
	}
	if f.rule.Storage == "local" {
		full := filepath.Join(f.rule.Target, filepath.FromSlash(e.Relative))
		if !mediaWithin(f.rule.Target, full) {
			return ErrMediaOrganizeInput
		}
		if _, err := os.Lstat(full); err == nil {
			if mediaAttachmentSameDestinationRole(e, path.Base(e.Relative)) {
				return nil
			}
			return errMediaTargetExists
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	cid, err := f.cloudDestination(ctx, e.Relative, false)
	if err != nil || cid == "" {
		return err
	}
	files, err := f.list(ctx, cid)
	if err != nil {
		return err
	}
	for _, file := range files {
		if file.ID == e.File.ID && !file.Directory && file.Size == e.File.Size && file.Name == path.Base(e.Relative) {
			return nil
		}
		if strings.EqualFold(file.Name, path.Base(e.Relative)) {
			if mediaAttachmentSameDestinationRole(e, file.Name) {
				return nil
			}
			if f.rule.DeleteDuplicates && media115SameSHA1(e, file) {
				return nil
			}
			return errMediaTargetExists
		}
	}
	return nil
}

func (f *mediaOrganizeFiles) move(ctx context.Context, e MediaOrganizeEntry) error {
	if f.rule.Storage == "cd2" {
		return f.moveCD2(ctx, e)
	}
	if e.Failure != nil && e.Failure.State == "stored" {
		if f.rule.Storage == "115" && e.Failure.FileID != "" {
			return f.move115FailureDuplicate(ctx, e)
		}
		// 仅改变隔离后的读取位置，原任务键和附件关联保持不变。
		if _, err := f.failureFiles(ctx); err != nil {
			return err
		}
		current := *f
		current.rule.Source, current.rule.SourceID = f.rule.Failure, f.rule.FailureID
		e.File.ParentID = e.Failure.ParentID
		e.Failure = nil
		return current.move(ctx, e)
	}
	if f.rule.Storage == "local" {
		return f.moveLocal(ctx, e)
	}
	cid, err := f.cloudDestination(ctx, e.Relative, true)
	if err != nil {
		return err
	}
	target := path.Base(e.Relative)
	// Directory reservations are released between files; another organizer may
	// have written since the preview or the previous file. Reuse path traversal,
	// but refresh the two directories involved in the actual write.
	delete(f.lists, cid)
	delete(f.lists, e.File.ParentID)
	dest, err := f.list(ctx, cid)
	if err != nil {
		return err
	}
	for _, file := range dest {
		if file.ID == e.File.ID && file.Name == target && file.Size == e.File.Size && !file.Directory {
			return nil
		}
		if strings.EqualFold(file.Name, target) {
			return f.delete115Duplicate(ctx, e, cid, file)
		}
	}
	source, err := f.list(ctx, e.File.ParentID)
	if err != nil {
		return err
	}
	var actual *p115.ShareFile
	for i := range source {
		if source[i].ID == e.File.ID {
			actual = &source[i]
			break
		}
	}
	if actual == nil || actual.Directory || actual.Size != e.File.Size || (actual.Name != e.File.Name && actual.Name != target) {
		return fmt.Errorf("来源文件已变化，请重新扫描")
	}
	if actual.Name != target {
		for _, other := range source {
			if other.ID != actual.ID && strings.EqualFold(other.Name, target) {
				return fmt.Errorf("来源目录中已有同名文件")
			}
		}
		if err = f.drive.RenameEntries(ctx, []p115.RenameTarget{{ID: e.File.ID, Name: target}}, f.cookie); err != nil {
			delete(f.lists, e.File.ParentID)
			return err
		}
		actual.Name = target
	}
	if err = f.drive.MoveEntriesBatch(ctx, []string{e.File.ID}, cid, f.cookie); err != nil {
		delete(f.lists, cid)
		delete(f.lists, e.File.ParentID)
		return err
	}
	f.lists[cid] = append(f.lists[cid], *actual)
	for i, file := range f.lists[e.File.ParentID] {
		if file.ID == e.File.ID {
			f.lists[e.File.ParentID] = append(f.lists[e.File.ParentID][:i], f.lists[e.File.ParentID][i+1:]...)
			break
		}
	}
	return nil
}

func (f *mediaOrganizeFiles) move115FailureDuplicate(ctx context.Context, entry MediaOrganizeEntry) error {
	failure := entry.Failure
	if failure.FileID == entry.File.ID || failure.ParentID == "" {
		return fmt.Errorf("115 失败目录保留身份无效")
	}
	drive, ok := f.drive.(mediaOrganizeDrive)
	if !ok {
		return fmt.Errorf("115 目录核验服务不可用")
	}
	verifyTarget := func() (*mediaDuplicateReceipt, error) {
		cid, err := f.cloudDestination(ctx, entry.Relative, false)
		if err != nil || cid == "" {
			return nil, err
		}
		dir, err := drive.ReadDirectory(ctx, cid, f.cookie)
		if err != nil {
			return nil, err
		}
		if dir.CID != cid || dir.Path != path.Join(f.rule.Target, path.Dir(entry.Relative)) {
			return nil, fmt.Errorf("115 整理目标目录已变化")
		}
		delete(f.lists, cid)
		children, err := f.list(ctx, cid)
		if err != nil {
			return nil, err
		}
		var receipt *mediaDuplicateReceipt
		names := 0
		for _, file := range children {
			if strings.EqualFold(file.Name, path.Base(entry.Relative)) {
				names++
			}
			if file.ID == failure.FileID && file.Name == path.Base(entry.Relative) && file.Size == entry.File.Size && media115SameSHA1(entry, file) {
				receipt = &mediaDuplicateReceipt{fileID: file.ID, parentID: cid, size: file.Size}
			}
		}
		if receipt != nil && names != 1 {
			return nil, fmt.Errorf("115 整理目标存在重名文件，保留来源")
		}
		return receipt, nil
	}
	// 上次移动可能已经成功，先核对精确目标，避免依赖已经清理的旧目录。
	if receipt, err := verifyTarget(); err != nil {
		return err
	} else if receipt != nil {
		return receipt
	}
	if _, err := f.failureFiles(ctx); err != nil {
		return err
	}
	dir, err := drive.ReadDirectory(ctx, failure.ParentID, f.cookie)
	if err != nil {
		return err
	}
	if dir.CID != failure.ParentID || dir.Path != path.Join(f.rule.Failure, path.Dir(entry.File.SourcePath)) {
		return fmt.Errorf("115 失败文件所在目录已变化")
	}
	delete(f.lists, failure.ParentID)
	children, err := f.list(ctx, failure.ParentID)
	if err != nil {
		return err
	}
	var keeper *p115.ShareFile
	for i := range children {
		file := &children[i]
		if file.ID == failure.FileID {
			if file.Size != entry.File.Size || !media115SameSHA1(entry, *file) ||
				!strings.EqualFold(file.Name, path.Base(entry.File.SourcePath)) && file.Name != path.Base(entry.Relative) {
				return fmt.Errorf("115 失败目录保留文件已变化")
			}
			keeper = file
			break
		}
	}
	if keeper == nil {
		return fmt.Errorf("115 失败目录保留文件不存在，请重新扫描")
	}
	current := *f
	current.rule.Source, current.rule.SourceID = f.rule.Failure, f.rule.FailureID
	actual := entry
	actual.File.ShareFile, actual.File.ParentID = *keeper, failure.ParentID
	actual.File.SourcePath = path.Join(path.Dir(entry.File.SourcePath), keeper.Name)
	actual.Failure = nil
	if err := current.move(ctx, actual); err != nil {
		return err
	}
	receipt, err := verifyTarget()
	if err != nil {
		return err
	}
	if receipt == nil {
		return fmt.Errorf("115 失败目录保留文件移动结果待核实")
	}
	return receipt
}

func (f *mediaOrganizeFiles) moveLocal(ctx context.Context, e MediaOrganizeEntry) error {
	source, err := mediaLocalPath(f.rule.Source, e.File.ID, false)
	if err != nil {
		// A persisted quarantine intent may have completed before a restart.
		// Only reconcile its exact destination; never scan or overwrite files.
		if errors.Is(err, os.ErrNotExist) && e.Failure != nil && e.Failure.State == "moving" {
			target, checkErr := mediaLocalPath(f.rule.Target, e.Relative, false)
			if checkErr == nil {
				info, checkErr := os.Stat(target)
				if checkErr == nil && info.Mode().IsRegular() && info.Size() == e.File.Size && info.ModTime().UnixNano() == e.Modified {
					return nil
				}
			}
		}
		return err
	}
	info, err := os.Stat(source)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() != e.File.Size || info.ModTime().UnixNano() != e.Modified {
		return fmt.Errorf("来源文件已变化，请重新扫描")
	}
	parent, err := mediaLocalPath(f.rule.Target, filepath.ToSlash(filepath.Dir(filepath.FromSlash(e.Relative))), true)
	if path.Dir(e.Relative) == "." {
		parent, err = mediaLocalRoot(f.rule.Target)
		if err == nil && parent != f.rule.Target {
			return fmt.Errorf("目标目录已变化，请重新保存配置")
		}
	}
	if err != nil {
		return err
	}
	target := filepath.Join(parent, path.Base(e.Relative))
	if !mediaWithin(f.rule.Target, target) {
		return ErrMediaOrganizeInput
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Link+unlink is an atomic no-overwrite move on supporting filesystems.
	// Mounted/cloud filesystems fall back to an exclusive copy, never overwrite.
	if err = os.Link(source, target); err != nil {
		if _, statErr := os.Lstat(target); statErr == nil {
			return errMediaTargetExists
		}
		input, err := os.Open(source)
		if err != nil {
			return err
		}
		defer input.Close()
		opened, err := input.Stat()
		if err != nil {
			return err
		}
		if !os.SameFile(info, opened) {
			return fmt.Errorf("来源文件已变化")
		}
		output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
		if err != nil {
			return fmt.Errorf("创建目标文件: %w", err)
		}
		ok := false
		defer func() {
			_ = output.Close()
			if !ok {
				_ = os.Remove(target)
			}
		}()
		written, copyErr := io.Copy(output, &mediaContextReader{ctx: ctx, r: input})
		if copyErr != nil {
			return fmt.Errorf("复制文件: %w", copyErr)
		}
		if written != info.Size() {
			return fmt.Errorf("文件大小在复制中发生变化")
		}
		if err = output.Sync(); err != nil {
			return err
		}
		if err = output.Close(); err != nil {
			return err
		}
		current, err := os.Lstat(source)
		if err != nil || !os.SameFile(info, current) || current.Size() != info.Size() || current.ModTime() != info.ModTime() {
			return fmt.Errorf("复制时来源文件已变化，保留来源")
		}
		if err = os.Chtimes(target, info.ModTime(), info.ModTime()); err != nil {
			return err
		}
		ok = true
	}
	current, err := os.Lstat(source)
	if err != nil || !os.SameFile(info, current) || current.Size() != info.Size() || current.ModTime() != info.ModTime() {
		return fmt.Errorf("来源文件已变化，未删除来源")
	}
	if err = os.Remove(source); err != nil {
		return fmt.Errorf("目标已生成，来源删除失败: %w", err)
	}
	return nil
}

type mediaContextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r *mediaContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}
