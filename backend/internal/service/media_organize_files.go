package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"strm115/backend/internal/client/p115"
)

type mediaOrganizeDrive interface {
	OrganizeDrive
	ReadDirectory(context.Context, string, string) (p115.DriveDirectory, error)
}

type mediaOrganizeFiles struct {
	rule           MediaOrganizeRule
	drive          OrganizeDrive
	cookie         string
	lists          map[string][]p115.ShareFile
	unrelated      []MediaOrganizeEntry
	directories    []MediaOrganizeEntry
	scanned        []MediaOrganizeEntry
	localSnapshots map[string]os.FileInfo
	settlingVideos map[string]bool
	failureChecked bool
}

func (s *MediaOrganizer) files(ctx context.Context, rule MediaOrganizeRule) (*mediaOrganizeFiles, error) {
	f := &mediaOrganizeFiles{rule: rule, drive: s.drive, lists: make(map[string][]p115.ShareFile)}
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
		typeName := mediaFileType(file.Name)
		if typeName == "" {
			if !mediaKeepFile(file.Name) {
				f.unrelated = append(f.unrelated, MediaOrganizeEntry{File: OrganizeFile{ShareFile: file, ParentID: parent, SourcePath: relative, MediaType: "cleanup"}, Modified: modified})
			}
			return
		}
		item := OrganizeFile{ShareFile: file, ParentID: parent, SourcePath: relative, MediaType: typeName, Season: 1}
		organizeFileNumbering(&item)
		item.NamingValues = mediaFileValues(file.Name, DownloadFileSpec{})
		out = append(out, MediaOrganizeEntry{File: item, Modified: modified, Status: "pending"})
	}
	if f.rule.Storage == "local" {
		f.localSnapshots = make(map[string]os.FileInfo)
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
		dirs := 0
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
			if entry.IsDir() {
				dirs++
				if dirs > 300 || strings.Count(rel, string(filepath.Separator)) > 12 {
					return fmt.Errorf("目录过大，请缩小扫描范围")
				}
				info, err := entry.Info()
				if err != nil {
					return err
				}
				id := filepath.ToSlash(rel)
				f.localSnapshots[id] = info
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
			if time.Since(info.ModTime()) < 30*time.Second {
				if mediaFileType(entry.Name()) == "video" {
					f.settlingVideos[filepath.ToSlash(filepath.Dir(rel))] = true
				}
				return nil
			} // Allow uploads/copies to settle.
			appendFile(p115.ShareFile{ID: filepath.ToSlash(rel), Name: entry.Name(), Size: info.Size()}, filepath.ToSlash(filepath.Dir(rel)), filepath.ToSlash(rel), info.ModTime().UnixNano())
			if !mediaKeepFile(entry.Name()) || mediaFileType(entry.Name()) == "subtitle" || mediaFileType(entry.Name()) == "attachment" {
				f.localSnapshots[filepath.ToSlash(rel)] = info
			}
			if len(out)+len(f.unrelated) > 5000 {
				return fmt.Errorf("单次最多 5000 个文件，请缩小扫描范围")
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("扫描本地目录: %w", err)
		}
	} else {
		visited := make(map[string]bool)
		var walk func(string, string, int) error
		walk = func(cid, prefix string, depth int) error {
			if cid == f.rule.TargetID || cid != "" && cid == f.rule.FailureID {
				return fmt.Errorf("目标或失败目录不能位于来源目录内")
			}
			if visited[cid] || depth > 12 || len(visited) >= 300 {
				return fmt.Errorf("目录循环或范围过大")
			}
			visited[cid] = true
			files, err := f.list(ctx, cid)
			if err != nil {
				return err
			}
			for _, file := range files {
				if err := ctx.Err(); err != nil {
					return err
				}
				if file.Directory {
					f.directories = append(f.directories, MediaOrganizeEntry{File: OrganizeFile{ShareFile: file, ParentID: cid, SourcePath: path.Join(prefix, file.Name), MediaType: "cleanup"}})
					if err := walk(file.ID, path.Join(prefix, file.Name), depth+1); err != nil {
						return err
					}
				} else {
					appendFile(file, cid, path.Join(prefix, file.Name), 0)
				}
				if len(out)+len(f.unrelated) > 5000 {
					return fmt.Errorf("单次最多 5000 个文件")
				}
			}
			return nil
		}
		if err := walk(f.rule.SourceID, "", 0); err != nil {
			return nil, err
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
	if e.Relative == "" {
		return nil
	}
	if f.rule.Storage == "local" {
		full := filepath.Join(f.rule.Target, filepath.FromSlash(e.Relative))
		if !mediaWithin(f.rule.Target, full) {
			return ErrMediaOrganizeInput
		}
		if _, err := os.Lstat(full); err == nil {
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
			return errMediaTargetExists
		}
	}
	return nil
}

func (f *mediaOrganizeFiles) move(ctx context.Context, e MediaOrganizeEntry) error {
	if e.Failure != nil && e.Failure.State == "stored" {
		// Keep the original file identity and subtitle associations in the plan.
		// Only the actual read location changes after quarantine.
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
			return errMediaTargetExists
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
	for _, other := range source {
		if other.ID != actual.ID && strings.EqualFold(other.Name, target) {
			return fmt.Errorf("来源目录中已有同名文件")
		}
	}
	if actual.Name != target {
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
