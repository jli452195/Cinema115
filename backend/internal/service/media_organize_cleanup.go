package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"slices"
	"strings"
	"time"

	"strm115/backend/internal/client/p115"
)

// Unsupported media is retained too. Archives and text documents deliberately
// are not on this list; the monitor never opens archives to look for subtitles.
func mediaKeepFile(name string) bool {
	if mediaFileType(name) != "" {
		return true
	}
	return mediaRetainedExtensions[strings.ToLower(path.Ext(name))]
}

var mediaRetainedExtensions = extensionSet("iso,vob,mts,m2v,mxf,flv,f4v,rm,ogv,divx,wtv,bif,ttf,otf,woff,woff2,mp3,flac,wav,m4a,mka,aac,ac3,eac3,dts,ogg,opus", "")

func mediaDiscardOrphanSidecars(f *mediaOrganizeFiles, run *MediaOrganizeRun, scanned []MediaOrganizeEntry) {
	original := mediaEntryFiles(scanned)
	mediaValidateAttachmentOwners(run, original)
	owners := make(map[string]string)
	for _, sub := range organizeSubtitles(original) {
		owners[sub.File.ID] = sub.VideoID
	}
	run.Entries = slices.DeleteFunc(run.Entries, func(e MediaOrganizeEntry) bool {
		if e.File.MediaType != "subtitle" && e.File.MediaType != "attachment" || f.settlingVideos[e.File.ParentID] {
			return false
		}
		if e.File.MediaType == "subtitle" && owners[e.File.ID] != "" {
			return false
		}
		if e.File.MediaType == "attachment" {
			if mediaAttachmentHasVideo(e.File, original) {
				return false
			}
			if parent, _, shared := mediaAttachmentFolder(e.File); shared {
				if f.settlingVideos[parent] {
					return false
				}
				for _, dir := range f.directories {
					if f.settlingVideos[dir.File.ID] && (parent == "." || dir.File.SourcePath == parent || strings.HasPrefix(dir.File.SourcePath, parent+"/")) {
						return false
					}
				}
			}
		}
		e.Group, e.VideoID, e.Relative, e.Target = "", "", "", ""
		f.unrelated = append(f.unrelated, e)
		return true
	})
	for i := range run.Entries {
		e := &run.Entries[i]
		if (e.File.MediaType == "subtitle" || e.File.MediaType == "attachment") && e.VideoID == "" {
			e.Status, e.Message = "retained", "对应视频本轮未处理、尚未稳定或归属待确认，留待后续监控"
		}
	}
}

func (f *mediaOrganizeFiles) checkLocalCleanup(e MediaOrganizeEntry) (string, error) {
	full, err := mediaLocalPath(f.rule.Source, e.File.SourcePath, false)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(full)
	if err != nil {
		return "", err
	}
	snapshot := f.localSnapshots[e.File.ID]
	if snapshot == nil || !os.SameFile(snapshot, info) {
		return "", fmt.Errorf("来源文件已变化，留待下轮监控")
	}
	if e.File.Directory {
		if !info.IsDir() {
			return "", fmt.Errorf("来源目录已变化")
		}
		dir, err := os.Open(full)
		if err != nil {
			return "", err
		}
		defer dir.Close()
		_, err = dir.Readdirnames(1)
		if !errors.Is(err, io.EOF) {
			return "", fmt.Errorf("目录非空，保留来源")
		}
	} else if !info.Mode().IsRegular() || info.Size() != e.File.Size || info.ModTime().UnixNano() != e.Modified {
		return "", fmt.Errorf("来源文件已变化，留待下轮监控")
	}
	return full, nil
}

// Cleanup uses only this auto-scan's exact file snapshots. Nothing here is
// replayed by Apply, and no nonempty directory is ever recursively deleted.
func (s *MediaOrganizer) cleanup(ctx context.Context, f *mediaOrganizeFiles, run *MediaOrganizeRun) error {
	if err := s.cleanupEntries(ctx, f, run, f.unrelated); err != nil {
		return err
	}
	// Children precede their parents; never delete the configured source root.
	for i := len(f.directories) - 1; i >= 0; i-- {
		e := f.directories[i]
		if run.Rule.Storage == "115" && len(f.lists[e.File.ID]) > 0 {
			continue
		}
		if err := s.cleanupEntries(ctx, f, run, []MediaOrganizeEntry{e}); err != nil {
			return err
		}
	}
	return nil
}

func (f *mediaOrganizeFiles) cleanupListing(ctx context.Context, parent, relative string) ([]p115.ShareFile, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if f.rule.Storage == "115" {
		dir, err := f.drive.(mediaOrganizeDrive).ReadDirectory(ctx, parent, f.cookie)
		if err != nil {
			return nil, err
		}
		if dir.CID != parent || dir.Path != path.Join(f.rule.Source, relative) {
			return nil, fmt.Errorf("来源子目录已变化，已停止清理")
		}
		files, err := listOrganizeFiles(ctx, f.drive, parent, f.cookie)
		if err == nil {
			f.lists[parent] = files
		}
		return files, err
	}
	full, err := mediaLocalRoot(f.rule.Source)
	if relative != "." {
		full, err = mediaLocalPath(f.rule.Source, relative, false)
	}
	if err != nil {
		return nil, err
	}
	if relative == "." && full != f.rule.Source {
		return nil, fmt.Errorf("来源目录已变化")
	}
	entries, err := os.ReadDir(full)
	if err != nil {
		return nil, err
	}
	files := make([]p115.ShareFile, 0, len(entries))
	for _, e := range entries {
		if e.Type()&os.ModeSymlink == 0 {
			files = append(files, p115.ShareFile{ID: path.Join(relative, e.Name()), Name: e.Name(), Directory: e.IsDir()})
		}
	}
	return files, nil
}

func (s *MediaOrganizer) cleanupEntries(ctx context.Context, f *mediaOrganizeFiles, run *MediaOrganizeRun, candidates []MediaOrganizeEntry) error {
	parents := make(map[string][]MediaOrganizeEntry)
	sharedScopes := make(map[string]bool)
	order := make([]string, 0)
	for _, e := range candidates {
		if _, ok := parents[e.File.ParentID]; !ok {
			order = append(order, e.File.ParentID)
		}
		parents[e.File.ParentID] = append(parents[e.File.ParentID], e)
	}
	for _, parent := range order {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := func() error {
			work, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			entries := parents[parent]
			var siblings []p115.ShareFile
			var recycler organizeRecycleDrive
			if run.Rule.Storage == "115" {
				var ok bool
				recycler, ok = f.drive.(organizeRecycleDrive)
				if !ok {
					return fmt.Errorf("当前网盘不支持移入回收站")
				}
				if s.reserve != nil {
					release, err := s.reserve()
					if err != nil {
						return err
					}
					defer release()
				}
				// One fresh listing per affected parent, independent of file count.
				files, err := f.cleanupListing(work, parent, path.Dir(entries[0].File.SourcePath))
				if err != nil {
					return err
				}
				siblings = files
				current := make(map[string]p115.ShareFile, len(files))
				for _, file := range files {
					current[file.ID] = file
				}
				valid := make([]MediaOrganizeEntry, 0, len(entries))
				for _, e := range entries {
					file, ok := current[e.File.ID]
					if ok && file.Directory == e.File.Directory && file.Name == e.File.Name && (file.Directory || file.Size == e.File.Size) && (file.Directory || !mediaKeepFile(file.Name) || e.File.MediaType == "subtitle" || e.File.MediaType == "attachment") {
						valid = append(valid, e)
					}
				}
				entries = valid
			}
			if slices.ContainsFunc(entries, func(e MediaOrganizeEntry) bool {
				return e.File.MediaType == "subtitle" || e.File.MediaType == "attachment"
			}) {
				if recycler == nil {
					files, err := f.cleanupListing(work, parent, path.Dir(entries[0].File.SourcePath))
					if err != nil {
						return err
					}
					siblings = files
				}
				videos := make([]OrganizeFile, 0, len(siblings))
				for _, file := range siblings {
					if !file.Directory && mediaFileType(file.Name) == "video" {
						videos = append(videos, OrganizeFile{ShareFile: file, ParentID: parent, SourcePath: path.Join(path.Dir(entries[0].File.SourcePath), file.Name), MediaType: "video"})
					}
				}
				for _, e := range entries {
					if e.File.MediaType != "attachment" {
						continue
					}
					scope, _, shared := mediaAttachmentFolder(e.File)
					if _, checked := sharedScopes[scope]; !shared || checked {
						continue
					}
					files := siblings
					if scope != path.Dir(e.File.SourcePath) {
						cid := scope
						if recycler != nil {
							cid = run.Rule.SourceID
							if scope != "." {
								cid = ""
								for _, dir := range f.directories {
									if dir.File.SourcePath == scope {
										cid = dir.File.ID
										break
									}
								}
								if cid == "" {
									return fmt.Errorf("附件所属来源目录无法确认")
								}
							}
						}
						var err error
						files, err = f.cleanupListing(work, cid, scope)
						if err != nil {
							return err
						}
					}
					sharedScopes[scope] = slices.ContainsFunc(files, func(file p115.ShareFile) bool {
						// A child folder may contain a video that arrived after the
						// scan. Reconsider shared artwork on the next full scan.
						return !file.Directory && mediaFileType(file.Name) == "video" || file.Directory && file.Name != ".actors" && file.Name != "extrafanart"
					})
				}
				matcher := organizeSubtitleMatcher(videos)
				entries = slices.DeleteFunc(entries, func(e MediaOrganizeEntry) bool {
					if e.File.MediaType == "attachment" {
						scope, _, shared := mediaAttachmentFolder(e.File)
						return mediaAttachmentHasVideo(e.File, videos) || shared && sharedScopes[scope]
					}
					if e.File.MediaType != "subtitle" {
						return false
					}
					owner, _ := organizeSubtitleOwner(e.File, videos, matcher)
					return owner != "" // A video may have arrived since the scan.
				})
			}
			const batchSize = 100
			for start := 0; start < len(entries); start += batchSize {
				if err := work.Err(); err != nil {
					return err
				}
				batch := entries[start:min(start+batchSize, len(entries))]
				if recycler == nil {
					valid := make([]MediaOrganizeEntry, 0, len(batch))
					for _, e := range batch {
						if _, err := f.checkLocalCleanup(e); err == nil {
							valid = append(valid, e)
						}
					}
					batch = valid // Changed files are reconsidered by a new scan.
				}
				if len(batch) == 0 {
					continue
				}
				indices := make([]int, 0, len(batch))
				ids := make([]string, 0, len(batch))
				for _, e := range batch {
					e.Status, e.Message = "error", "清理结果待核实；下轮自动监控会重新扫描"
					e.Result = &MediaOrganizeResult{Status: "error", Message: e.Message, At: time.Now()}
					indices = append(indices, len(run.Entries))
					ids = append(ids, e.File.ID)
					run.Entries = append(run.Entries, e)
				}
				// Keep an audit trail even if the provider applies a timed-out write.
				if err := s.checkpoint(run); err != nil {
					return err
				}
				var err error
				if recycler != nil {
					if batch[0].File.Directory {
						// Directory cleanup is one exact folder at a time. Recheck
						// after persistence, immediately before the provider write.
						children, err := listOrganizeFiles(work, f.drive, batch[0].File.ID, f.cookie)
						if err != nil {
							return err
						}
						if len(children) > 0 {
							e := &run.Entries[indices[0]]
							e.Status, e.Message, e.Result = "retained", "目录已有内容，已保留", nil
							if err := s.checkpoint(run); err != nil {
								return err
							}
							continue
						}
					}
					err = recycler.DeleteEntries(work, ids, f.cookie)
				}
				var batchErr error
				for _, i := range indices {
					e := &run.Entries[i]
					if recycler == nil {
						err = work.Err()
						if err == nil {
							// Persistence may take time; recheck before each unlink.
							var full string
							full, err = f.checkLocalCleanup(*e)
							if err == nil {
								err = os.Remove(full)
							}
						}
					}
					if err == nil {
						e.Status, e.Message = "done", "已从来源删除无关文件"
						if e.File.Directory {
							e.Message = "已删除空文件夹"
						} else if e.File.MediaType == "subtitle" {
							e.Message = "字幕未找到唯一对应视频，已从来源删除"
						} else if e.File.MediaType == "attachment" {
							e.Message = "附件未找到对应视频，已从来源删除"
						}
						if recycler != nil {
							e.Message += "，已移入 115 回收站"
							f.lists[parent] = slices.DeleteFunc(f.lists[parent], func(file p115.ShareFile) bool { return file.ID == e.File.ID })
						}
					} else {
						e.Message += "：" + organizePublicError(err, f.cookie)
						batchErr = err
					}
					e.Result = &MediaOrganizeResult{Status: e.Status, Message: e.Message, At: time.Now()}
					if err == nil && e.File.MediaType == "subtitle" {
						e.Result.Status = "error"
					}
				}
				if saveErr := s.checkpoint(run); saveErr != nil {
					return saveErr
				}
				if batchErr != nil {
					return batchErr
				}
			}
			return nil
		}()
		if err != nil {
			return fmt.Errorf("清理无关文件已停止: %w", &organizeSafeError{err: err, message: organizePublicError(err, f.cookie)})
		}
	}
	return nil
}
