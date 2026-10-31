package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
	"strm115/backend/internal/strm"
)

func driveSTRMDigest(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func driveSTRMSignature(file p115.ShareFile) string {
	if len(file.SHA1) != 40 {
		return ""
	}
	return strings.ToUpper(file.SHA1) + ":" + strconv.FormatInt(file.Size, 10)
}

// readDriveSTRMOutput checks every parent, rejects links, and bounds reads.
func readDriveSTRMOutput(directory, relative string) ([]byte, error) {
	if _, err := statDriveSTRMOutput(directory, relative); err != nil {
		return nil, err
	}
	return os.ReadFile(filepath.Join(directory, filepath.FromSlash(relative)))
}

// Coordinate final checks with the existing share writer. Network attachment
// downloads happen before taking this lock, so they never block STRM writes.
func writeDriveSTRMOutput(ctx context.Context, directory string, record, previous model.DriveSTRMFile, data []byte) (bool, error) {
	generatedFileWriteMu.Lock()
	defer generatedFileWriteMu.Unlock()
	existing, err := readDriveSTRMOutput(directory, record.Path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	if err == nil {
		digest := driveSTRMDigest(existing)
		if digest == record.Digest {
			return false, nil
		}
		owned := driveSTRMOwnsOutput(record, previous)
		if !owned {
			return false, fmt.Errorf("目标含其他文件，已保留：%s", record.Path)
		}
	}
	if err := writeSTRM(ctx, filepath.Join(directory, filepath.FromSlash(record.Path)), string(data)); err != nil {
		return false, err
	}
	return true, nil
}

func (s *DriveSTRMService) progress(ctx context.Context, id string, stats model.DriveSTRMStats) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, err := s.Store.GetDriveSTRM(ctx, id)
	if err != nil {
		return err
	}
	value.Stats, value.UpdatedAt = stats, time.Now().UTC()
	return s.Store.SaveDriveSTRM(ctx, value)
}

type driveSTRMBulkReader interface {
	WalkDriveFiles(context.Context, string, string, func([]p115.DriveTreeEntry) error) error
}

type driveSTRMItem struct {
	file             p115.ShareFile
	record, previous model.DriveSTRMFile
}

type driveSTRMScan struct {
	service           *DriveSTRMService
	value             model.DriveSTRM
	cookie, directory string
	videos, metadata  map[string]bool
	stats             model.DriveSTRMStats
	failures          []error
	claimed           map[string]string
	blocked           map[string]bool
	lastProgress      time.Time
	lastLog           time.Time
}

func driveSTRMOutputPath(relative string, video bool) string {
	parts := strings.Split(relative, "/")
	for i := range parts {
		parts[i] = safeSTRMName(parts[i])
	}
	if video {
		parts[len(parts)-1] = safeSTRMName(strings.TrimSuffix(path.Base(relative), path.Ext(relative))) + ".strm"
	}
	return strings.Join(parts, "/")
}

func (r *driveSTRMScan) failure(err error) {
	r.stats.Failed++
	if len(r.failures) < 5 {
		r.failures = append(r.failures, err)
	}
}

func (r *driveSTRMScan) progress(ctx context.Context) error {
	if time.Since(r.lastProgress) < time.Second {
		return nil
	}
	r.lastProgress = time.Now()
	if err := r.service.progress(ctx, r.value.ID, r.stats); err != nil {
		return err
	}
	if time.Since(r.lastLog) >= 15*time.Second {
		r.lastLog = time.Now()
		r.service.logDriveSTRM(r.value.ID, "INFO", fmt.Sprintf("目录「%s」生成进度：%s", r.value.Name, driveSTRMLogStats(r.stats)))
	}
	return nil
}

// Inspect path types without repeatedly opening unchanged output contents.
func statDriveSTRMOutput(directory, relative string) (os.FileInfo, error) {
	if !filepath.IsLocal(filepath.FromSlash(relative)) {
		return nil, fmt.Errorf("输出路径无效")
	}
	destination := filepath.Join(directory, filepath.FromSlash(relative))
	if err := ensureSTRMDirectory(filepath.Dir(destination)); err != nil {
		return nil, err
	}
	info, err := os.Lstat(destination)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > p115.MaxMetadataSize {
		return nil, fmt.Errorf("输出不是有效的普通文件：%s", relative)
	}
	return info, nil
}

func driveSTRMOwnsOutput(record, previous model.DriveSTRMFile) bool {
	return previous.MappingID == record.MappingID && previous.FileID == record.FileID && driveSTRMSamePath(previous.Path, record.Path) && previous.Digest != ""
}

func driveSTRMAttachmentUnchanged(directory string, record, previous model.DriveSTRMFile, size int64) bool {
	info, err := statDriveSTRMOutput(directory, record.Path)
	return err == nil && driveSTRMOwnsOutput(record, previous) && previous.Signature != "" && previous.Signature == record.Signature && info.Size() == size
}

func (r *driveSTRMScan) write(ctx context.Context, item driveSTRMItem) (model.DriveSTRMFile, error) {
	file, record, previous := item.file, item.record, item.previous
	info, statErr := statDriveSTRMOutput(r.directory, record.Path)
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return record, statErr
	}
	link, err := strm.DriveURL(r.value.Settings.BaseURL, strm.Drive{PickCode: file.PickCode, FileID: file.ID, SHA1: file.SHA1, Size: file.Size, Name: file.Name})
	if err != nil {
		return record, err
	}
	data := []byte(link + "\n")
	record.Digest = driveSTRMDigest(data)
	if driveSTRMOwnsOutput(record, previous) && record.Digest == previous.Digest && statErr == nil && info.Size() == int64(len(data)) {
		r.stats.Unchanged++
		return record, nil
	}
	written, err := writeDriveSTRMOutput(ctx, r.directory, record, previous, data)
	if err != nil {
		return record, fmt.Errorf("保存 %s: %w", record.Path, err)
	}
	if !written {
		r.stats.Unchanged++
	} else {
		r.stats.Written++
	}
	return record, nil
}

func (r *driveSTRMScan) consume(ctx context.Context, entries []p115.DriveTreeEntry) error {
	// A directory batch can be much larger than a file page. Bound both the SQL
	// transaction and the temporary record cache independently of library size.
	for offset := 0; offset < len(entries); offset += 1000 {
		batch := entries[offset:min(offset+1000, len(entries))]
		items := make([]driveSTRMItem, 0, len(batch))
		paths := make([]string, 0, len(batch))
		for _, entry := range batch {
			if err := ctx.Err(); err != nil {
				return err
			}
			ext := strings.ToLower(path.Ext(entry.Name))
			video := !entry.Directory && r.videos[ext] && entry.Size >= r.value.Settings.MinSizeMB*(1<<20)
			if !entry.Directory && !video && !r.metadata[ext] {
				continue
			}
			relative := driveSTRMOutputPath(entry.RelativePath, video)
			key := strings.ToLower(relative)
			parent := strings.ToLower(path.Dir(relative))
			if r.blocked[entry.ParentCID] || r.claimed[parent] != "" && r.claimed[parent] != entry.ParentCID {
				r.blocked[entry.ID] = true
				continue
			}
			if owner, ok := r.claimed[key]; ok && owner != entry.ID {
				r.failure(fmt.Errorf("不同来源映射到同一输出：%s", relative))
				r.blocked[entry.ID] = true
				continue
			}
			r.claimed[key] = entry.ID
			record := model.DriveSTRMFile{MappingID: r.value.ID, FileID: entry.ID, ParentCID: entry.ParentCID, Path: relative, Directory: entry.Directory, Signature: driveSTRMSignature(entry.ShareFile)}
			items = append(items, driveSTRMItem{file: entry.ShareFile, record: record})
			paths = append(paths, relative)
		}
		old, err := r.service.Store.ListDriveSTRMFilesByPaths(ctx, r.value.ID, paths)
		if err != nil {
			return err
		}
		byID := make(map[string]model.DriveSTRMFile, len(old))
		for _, record := range old {
			byID[record.FileID] = record
		}
		changed := make([]model.DriveSTRMFile, 0, len(items))
		attachments := make([]model.DriveSTRMAttachment, 0, len(items))
		for _, item := range items {
			previous := byID[item.record.FileID]
			if driveSTRMSamePath(previous.Path, item.record.Path) {
				item.previous = previous
			}
			if item.record.Directory {
				r.stats.Directories++
				if item.record != previous {
					changed = append(changed, item.record)
				}
				continue
			}
			if !r.videos[strings.ToLower(path.Ext(item.file.Name))] {
				if driveSTRMAttachmentUnchanged(r.directory, item.record, item.previous, item.file.Size) {
					r.stats.Unchanged++
					continue
				}
				attachments = append(attachments, model.DriveSTRMAttachment{
					Record: item.record, AccountID: r.value.AccountID,
					SourceCID: r.value.SourceCID, OutputDir: r.value.OutputDir, Settings: r.value.Settings,
					Name: item.file.Name, PickCode: item.file.PickCode, SHA1: item.file.SHA1, Size: item.file.Size,
				})
				continue
			}
			r.stats.Videos++
			record, err := r.write(ctx, item)
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err != nil {
				r.failure(err)
				continue
			}
			if record != previous {
				changed = append(changed, record)
			}
		}
		if err := r.service.Store.SaveDriveSTRMFiles(ctx, changed); err != nil {
			return err
		}
		if err := r.service.queueAttachments(ctx, r.value, attachments); err != nil {
			return err
		}
		if err := r.progress(ctx); err != nil {
			return err
		}
	}
	return nil
}

func driveSTRMSamePath(a, b string) bool {
	return a == b || runtime.GOOS == "windows" && strings.EqualFold(a, b)
}

func (s *DriveSTRMService) scan(ctx context.Context, value model.DriveSTRM) (model.DriveSTRMStats, error) {
	account, err := s.Accounts.Primary(value.AccountID)
	if err != nil {
		return model.DriveSTRMStats{}, err
	}
	directory, err := ResolveOutputDirectory(s.Root, value.OutputDir)
	if err != nil {
		return model.DriveSTRMStats{}, err
	}
	videos, err := driveSTRMExtensions(value.Settings.VideoExtensions)
	if err != nil {
		return model.DriveSTRMStats{}, err
	}
	metadata, err := parseMetadataExtensions(value.Settings.MetadataExtensions, videos)
	if err != nil {
		return model.DriveSTRMStats{}, err
	}
	r := driveSTRMScan{service: s, value: value, cookie: account.Cookie, directory: directory, videos: videos, metadata: metadata, claimed: make(map[string]string), blocked: make(map[string]bool), lastProgress: time.Now(), lastLog: time.Now()}
	root, err := s.Client.ReadDirectory(ctx, value.SourceCID, account.Cookie)
	if err != nil {
		return r.stats, fmt.Errorf("核验来源目录: %w", err)
	}
	if root.CID != value.SourceCID {
		return r.stats, generationInputError("来源目录身份已变化")
	}
	treeRepair := value.Mode == "tree"
	if treeRepair {
		stats, cids, treeErr := s.scanDirectoryTree(ctx, value, root, account.Cookie, directory, videos, metadata)
		if treeErr == nil {
			if len(cids) == 0 {
				return stats, nil
			}
			value.Mode, value.PendingDirectories = "changes", cids
		} else if errors.Is(treeErr, p115.ErrDriveTreeExportUnsupported) {
			value.Mode = "full"
			s.logDriveSTRM(value.ID, "WARNING", "当前目录不支持目录树核对，改用完整扫描")
			if err := s.setScanMode(ctx, value.ID, "full"); err != nil {
				return r.stats, err
			}
		} else {
			return stats, treeErr
		}
	}
	if reader, ok := s.Client.(driveSTRMBulkReader); ok && value.Mode != "changes" {
		r.stats.Directories = 1
		started := false
		err = reader.WalkDriveFiles(ctx, value.SourceCID, account.Cookie, func(entries []p115.DriveTreeEntry) error {
			started = true
			return r.consume(ctx, entries)
		})
		if !errors.Is(err, p115.ErrDriveTreeUnsupported) || started {
			if err != nil {
				return r.stats, err
			}
			return r.stats, r.result()
		}
	}
	type scope struct{ cid, relative string }
	scopes := []scope{{value.SourceCID, ""}}
	if value.Mode == "changes" && len(value.PendingDirectories) > 0 {
		indexed, err := s.Store.ListDriveSTRMFilesByIDs(ctx, value.ID, value.PendingDirectories)
		if err != nil {
			return r.stats, err
		}
		known := make(map[string]model.DriveSTRMFile, len(indexed))
		for _, item := range indexed {
			known[item.FileID] = item
		}
		scopes = nil
		for _, cid := range value.PendingDirectories {
			if cid == value.SourceCID {
				scopes = []scope{{value.SourceCID, ""}}
				break
			}
			if !known[cid].Directory {
				continue
			}
			current, err := s.Client.ReadDirectory(ctx, cid, account.Cookie)
			if err != nil {
				if ctx.Err() != nil {
					return r.stats, ctx.Err()
				}
				if treeRepair {
					scopes = []scope{{value.SourceCID, ""}}
					break
				}
				r.failure(fmt.Errorf("核验变更目录 %s: %w", cid, err))
				continue
			}
			prefix := strings.TrimRight(root.Path, "/") + "/"
			if !strings.HasPrefix(current.Path, prefix) {
				if treeRepair {
					scopes = []scope{{value.SourceCID, ""}}
					break
				}
				continue
			}
			relative := strings.TrimPrefix(current.Path, prefix)
			if treeRepair && driveSTRMOutputPath(relative, false) != known[cid].Path {
				// A renamed old directory cannot stand in for a new directory
				// that now occupies its former path in the exported tree.
				scopes = []scope{{value.SourceCID, ""}}
				break
			}
			scopes = append(scopes, scope{cid, relative})
		}
		paths := make([]string, 0, len(scopes))
		for _, scope := range scopes {
			paths = append(paths, driveSTRMOutputPath(scope.relative, false))
		}
		owners, err := s.Store.ListDriveSTRMFilesByPaths(ctx, value.ID, paths)
		if err != nil {
			return r.stats, err
		}
		byPath := make(map[string]string, len(owners))
		for _, owner := range owners {
			key := strings.ToLower(owner.Path)
			if _, exists := byPath[key]; exists {
				byPath[key] = "" // Multiple recorded owners require a tree check.
			} else {
				byPath[key] = owner.FileID
			}
		}
		for _, candidate := range scopes {
			if owner, exists := byPath[strings.ToLower(driveSTRMOutputPath(candidate.relative, false))]; exists && owner != candidate.cid {
				// A scope may have moved into another sanitized directory.
				// Recheck the tree together rather than mixing their files.
				scopes = []scope{{value.SourceCID, ""}}
				break
			}
		}
		sort.Slice(scopes, func(i, j int) bool { return len(scopes[i].relative) < len(scopes[j].relative) })
		kept := scopes[:0]
		for _, candidate := range scopes {
			covered := false
			for _, parent := range kept {
				if parent.relative == "" || parent.cid == candidate.cid || strings.HasPrefix(candidate.relative, parent.relative+"/") {
					covered = true
					break
				}
			}
			if !covered {
				kept = append(kept, candidate)
			}
		}
		scopes = kept
	}
	r.stats.Directories = len(scopes)
	visited, seen := make(map[string]bool), make(map[string]bool)
	pages := 0
	var walk func(string, string, int) error
	walk = func(cid, parent string, depth int) error {
		if depth > 64 || len(visited) >= 100000 || visited[cid] {
			return fmt.Errorf("目录层级过深、重复或循环：%s", cid)
		}
		visited[cid] = true
		total := -1
		for offset := 0; ; {
			if err := ctx.Err(); err != nil {
				return err
			}
			pages++
			if pages > 100000 {
				return fmt.Errorf("扫描分页过多，请缩小来源目录")
			}
			page, err := s.Client.DriveFiles(ctx, cid, offset, account.Cookie)
			if err != nil {
				return fmt.Errorf("读取网盘目录 %s: %w", cid, err)
			}
			if page == nil || page.Count < 0 || page.Offset != offset || len(page.Files) == 0 && offset < page.Count || len(page.Files) > page.Count-offset || total >= 0 && page.Count != total {
				return fmt.Errorf("115 目录分页不完整，请重新检查")
			}
			total = page.Count
			entries := make([]p115.DriveTreeEntry, 0, len(page.Files))
			for _, file := range page.Files {
				if file.ID == "" || seen[file.ID] {
					return fmt.Errorf("115 目录包含重复或空条目，请重新检查")
				}
				seen[file.ID] = true
				if len(seen) > 1000000 {
					return fmt.Errorf("扫描条目过多，请缩小来源目录")
				}
				entries = append(entries, p115.DriveTreeEntry{ShareFile: file, ParentCID: cid, RelativePath: path.Join(parent, file.Name)})
			}
			if err := r.consume(ctx, entries); err != nil {
				return err
			}
			for _, entry := range entries {
				if !entry.Directory || r.claimed[strings.ToLower(driveSTRMOutputPath(entry.RelativePath, false))] != entry.ID {
					continue
				}
				if err := walk(entry.ID, entry.RelativePath, depth+1); err != nil {
					if ctx.Err() != nil {
						return ctx.Err()
					}
					r.failure(err)
				}
			}
			offset += len(page.Files)
			if offset >= page.Count {
				break
			}
		}
		return nil
	}
	for _, scope := range scopes {
		if err := walk(scope.cid, scope.relative, 0); err != nil {
			if ctx.Err() != nil {
				return r.stats, ctx.Err()
			}
			r.failure(err)
		}
	}
	return r.stats, r.result()
}

func (s *DriveSTRMService) setScanMode(ctx context.Context, id, mode string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, err := s.Store.GetDriveSTRM(ctx, id)
	if err != nil {
		return err
	}
	value.Mode = mode
	return s.Store.SaveDriveSTRM(ctx, value)
}

func (r *driveSTRMScan) result() error {
	if r.stats.Failed > len(r.failures) {
		r.failures = append(r.failures, fmt.Errorf("另有 %d 项失败", r.stats.Failed-len(r.failures)))
	}
	return errors.Join(r.failures...)
}
