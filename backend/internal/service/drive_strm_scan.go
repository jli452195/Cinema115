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
	return os.ReadFile(destination)
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
		owned := previous.MappingID == record.MappingID && previous.FileID == record.FileID && previous.Digest != "" && previous.Digest == digest
		if !owned {
			return false, fmt.Errorf("目标含其他文件或已手动修改，已保留：%s", record.Path)
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

func (s *DriveSTRMService) scan(ctx context.Context, value model.DriveSTRM) (model.DriveSTRMStats, error) {
	var stats model.DriveSTRMStats
	account, err := s.Accounts.Primary(value.AccountID)
	if err != nil {
		return stats, err
	}
	// This one snapshot is used for all pagination and attachments in this run.
	cookie := account.Cookie
	directory, err := ResolveOutputDirectory(s.Root, value.OutputDir)
	if err != nil {
		return stats, err
	}
	videos, err := driveSTRMExtensions(value.VideoExtensions)
	if err != nil {
		return stats, err
	}
	metadata, err := parseMetadataExtensions(value.MetadataExtensions, videos)
	if err != nil {
		return stats, err
	}
	files, err := s.Store.ListDriveSTRMFiles(ctx, value.ID)
	if err != nil {
		return stats, err
	}
	byID := make(map[string]model.DriveSTRMFile, len(files))
	byPath := make(map[string]model.DriveSTRMFile, len(files))
	for _, file := range files {
		byID[file.FileID] = file
		if !file.Directory {
			byPath[strings.ToLower(file.Path)] = file
		}
	}
	root, err := s.Client.ReadDirectory(ctx, value.SourceCID, cookie)
	if err != nil {
		return stats, fmt.Errorf("核验来源目录: %w", err)
	}
	if root.CID != value.SourceCID {
		return stats, generationInputError("来源目录身份已变化")
	}
	var failures []error
	recordFailure := func(err error) {
		stats.Failed++
		if len(failures) < 5 {
			failures = append(failures, err)
		}
	}
	type scope struct{ cid, relative string }
	scopes := []scope{{value.SourceCID, ""}}
	if value.Mode == "changes" && len(value.PendingDirectories) > 0 {
		scopes = make([]scope, 0, len(value.PendingDirectories))
		for _, cid := range value.PendingDirectories {
			if cid == value.SourceCID {
				scopes = []scope{{value.SourceCID, ""}}
				break
			}
			old, ok := byID[cid]
			if !ok || !old.Directory {
				continue
			}
			// Known IDs may have since moved outside the mapping. Re-check only
			// these event scopes, never perform a metadata query for each video.
			current, err := s.Client.ReadDirectory(ctx, cid, cookie)
			if err != nil {
				if ctx.Err() != nil {
					return stats, ctx.Err()
				}
				recordFailure(fmt.Errorf("核验变更目录 %s: %w", cid, err))
				continue
			}
			prefix := strings.TrimRight(root.Path, "/") + "/"
			if !strings.HasPrefix(current.Path, prefix) {
				continue
			}
			relative := strings.TrimPrefix(current.Path, prefix)
			parts := strings.Split(relative, "/")
			for i := range parts {
				parts[i] = safeSTRMName(parts[i])
			}
			scopes = append(scopes, scope{cid, strings.Join(parts, "/")})
		}
		sort.Slice(scopes, func(i, j int) bool { return len(scopes[i].relative) < len(scopes[j].relative) })
		kept := scopes[:0]
		for _, candidate := range scopes {
			covered := false
			for _, parent := range kept {
				if parent.cid == candidate.cid || parent.relative == "" || strings.HasPrefix(candidate.relative, parent.relative+"/") {
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
	visited := make(map[string]bool)
	claimed := make(map[string]string)
	seenFiles := make(map[string]bool)
	pages := 0

	type attachment struct {
		file   p115.ShareFile
		record model.DriveSTRMFile
	}
	attachments := make([]attachment, 0)
	writeEntry := func(file p115.ShareFile, record model.DriveSTRMFile, isVideo bool) error {
		relative := record.Path
		key := strings.ToLower(relative)
		existing, readErr := readDriveSTRMOutput(directory, relative)
		if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
			return fmt.Errorf("读取本地输出 %s: %w", relative, readErr)
		}
		old, owned := byPath[key]
		owned = owned && old.MappingID == value.ID && old.FileID == file.ID
		var data []byte
		if isVideo {
			link, err := strm.DriveURL(value.BaseURL, strm.Drive{PickCode: file.PickCode, FileID: file.ID, SHA1: file.SHA1, Size: file.Size, Name: file.Name})
			if err != nil {
				return fmt.Errorf("生成 %s: %w", relative, err)
			}
			data = []byte(link + "\n")
		} else {
			if file.Size <= 0 || file.Size > p115.MaxMetadataSize {
				return fmt.Errorf("配套文件大小无效或超过 32 MB：%s", relative)
			}
			if owned && old.Signature != "" && old.Signature == record.Signature && readErr == nil && old.Digest == driveSTRMDigest(existing) {
				stats.Unchanged++
				return nil
			}
			reader, ok := s.Client.(driveSTRMMetadataReader)
			if !ok {
				return fmt.Errorf("当前客户端不支持读取配套文件")
			}
			var err error
			data, err = reader.ReadDriveMetadata(ctx, file, cookie)
			if err != nil {
				return fmt.Errorf("读取配套文件 %s: %w", relative, err)
			}
			if int64(len(data)) != file.Size {
				return fmt.Errorf("配套文件下载不完整：%s", relative)
			}
		}
		record.Digest = driveSTRMDigest(data)
		written, err := writeDriveSTRMOutput(ctx, directory, record, old, data)
		if err != nil {
			return fmt.Errorf("保存 %s: %w", relative, err)
		}
		if !written {
			stats.Unchanged++
		} else {
			if isVideo {
				stats.Written++
			} else {
				stats.MetadataWritten++
			}
		}
		if err := s.Store.SaveDriveSTRMFile(ctx, record); err != nil {
			return err
		}
		byID[file.ID], byPath[key] = record, record
		return nil
	}
	lastProgress := time.Now()
	var walk func(string, string, int) error
	walk = func(cid, parent string, depth int) error {
		if depth > 64 || len(visited) >= 100000 {
			return fmt.Errorf("目录层级或数量超出扫描范围，请缩小来源目录")
		}
		if visited[cid] {
			return fmt.Errorf("目录出现重复或循环：%s", cid)
		}
		visited[cid] = true
		stats.Directories++
		seen := make(map[string]bool)
		total := -1
		for offset := 0; ; {
			if err := ctx.Err(); err != nil {
				return err
			}
			pages++
			if pages > 100000 {
				return fmt.Errorf("扫描分页过多，请缩小来源目录")
			}
			page, err := s.Client.DriveFiles(ctx, cid, offset, cookie)
			if err != nil {
				return fmt.Errorf("读取网盘目录 %s: %w", cid, err)
			}
			if page == nil || page.Count < 0 || page.Offset != offset || len(page.Files) == 0 && offset < page.Count || len(page.Files) > page.Count-offset || total >= 0 && page.Count != total {
				return fmt.Errorf("115 目录分页不完整，请重新检查")
			}
			if total < 0 {
				total = page.Count
			}
			for _, file := range page.Files {
				if err := ctx.Err(); err != nil {
					return err
				}
				if file.ID == "" || seen[file.ID] || seenFiles[file.ID] {
					return fmt.Errorf("115 目录包含重复或空条目，请重新检查")
				}
				seen[file.ID], seenFiles[file.ID] = true, true
				if len(seenFiles) > 1000000 {
					return fmt.Errorf("扫描条目过多，请缩小来源目录")
				}
				name := safeSTRMName(file.Name)
				ext := strings.ToLower(path.Ext(file.Name))
				isVideo, isMetadata := !file.Directory && videos[ext] && file.Size >= value.MinSizeMB*(1<<20), !file.Directory && metadata[ext]
				if !file.Directory && !isVideo && !isMetadata {
					continue
				}
				if isVideo {
					name = safeSTRMName(strings.TrimSuffix(file.Name, path.Ext(file.Name))) + ".strm"
				} else if isMetadata {
					name = safeSTRMName(strings.TrimSuffix(file.Name, path.Ext(file.Name))) + path.Ext(file.Name)
				}
				relative := path.Join(parent, name)
				key := strings.ToLower(relative)
				if previous, ok := claimed[key]; ok && previous != file.ID {
					recordFailure(fmt.Errorf("不同来源映射到同一输出：%s", relative))
					continue
				}
				claimed[key] = file.ID
				record := model.DriveSTRMFile{MappingID: value.ID, FileID: file.ID, ParentCID: cid, Path: relative, Directory: file.Directory, Signature: driveSTRMSignature(file)}
				if file.Directory {
					if err := s.Store.SaveDriveSTRMFile(ctx, record); err != nil {
						return err
					}
					byID[file.ID] = record
					if err := walk(file.ID, relative, depth+1); err != nil {
						if ctx.Err() != nil {
							return ctx.Err()
						}
						recordFailure(err)
					}
					continue
				}
				if isVideo {
					stats.Videos++
					if err := writeEntry(file, record, true); err != nil {
						if ctx.Err() != nil {
							return ctx.Err()
						}
						recordFailure(err)
					}
				} else {
					attachments = append(attachments, attachment{file, record})
				}
			}
			if time.Since(lastProgress) >= time.Second {
				if err := s.progress(ctx, value.ID, stats); err != nil {
					return err
				}
				lastProgress = time.Now()
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
				return stats, ctx.Err()
			}
			recordFailure(err)
		}
	}
	// Enumerate and write all video entries before downloading attachments.
	for _, item := range attachments {
		if err := ctx.Err(); err != nil {
			return stats, err
		}
		if err := writeEntry(item.file, item.record, false); err != nil {
			if ctx.Err() != nil {
				return stats, ctx.Err()
			}
			recordFailure(err)
		}
		if time.Since(lastProgress) >= time.Second {
			if err := s.progress(ctx, value.ID, stats); err != nil {
				return stats, err
			}
			lastProgress = time.Now()
		}
	}
	if len(failures) > 0 {
		if stats.Failed > len(failures) {
			failures = append(failures, fmt.Errorf("另有 %d 项失败", stats.Failed-len(failures)))
		}
		return stats, errors.Join(failures...)
	}
	return stats, nil
}
