package service

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"strm115/backend/internal/client/clouddrive2"
	"strm115/backend/internal/client/p115"
)

var mediaAttachmentCopySuffix = regexp.MustCompile(`\s*[(（][1-9][0-9]{0,2}[)）]$`)

var errMediaAttachmentCheckpoint = errors.New("保存附件执行状态失败")

// 附件选择只在本次运行中缓存，来源与保留项快照仅用于当次身份核验。
type MediaAttachmentKeepIntent struct {
	Key                string                       `json:"key"`
	Phase              string                       `json:"phase"`
	KeeperID           string                       `json:"keeperId,omitempty"`
	Storage            string                       `json:"storage"`
	AccountID          string                       `json:"accountId,omitempty"`
	ConnectionID       string                       `json:"connectionId,omitempty"`
	ConnectionRevision uint64                       `json:"connectionRevision,omitempty"`
	SourceRoot         string                       `json:"sourceRoot"`
	TargetRoot         string                       `json:"targetRoot"`
	Relative           string                       `json:"relative"`
	Source             *MediaAttachmentFileSnapshot `json:"source,omitempty"`
	Keep               *MediaAttachmentFileSnapshot `json:"keep,omitempty"`
}

type MediaAttachmentFileSnapshot struct {
	Path     string            `json:"path"`
	ParentID string            `json:"parentId,omitempty"`
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Size     int64             `json:"size"`
	Modified int64             `json:"modified,omitempty"`
	SHA1     string            `json:"sha1,omitempty"`
	Hashes   map[uint32]string `json:"hashes,omitempty"`
}

func cloneMediaAttachmentKeep(source *MediaAttachmentKeepIntent) *MediaAttachmentKeepIntent {
	if source == nil {
		return nil
	}
	copy := *source
	for _, pair := range []struct {
		from *MediaAttachmentFileSnapshot
		to   **MediaAttachmentFileSnapshot
	}{{source.Source, &copy.Source}, {source.Keep, &copy.Keep}} {
		if pair.from != nil {
			file := *pair.from
			file.Hashes = maps.Clone(pair.from.Hashes)
			*pair.to = &file
		}
	}
	return &copy
}

func mediaSidecar(entry MediaOrganizeEntry) bool {
	return entry.File.MediaType == "subtitle" || entry.File.MediaType == "attachment"
}

func mediaAttachmentSameDestinationRole(entry MediaOrganizeEntry, name string) bool {
	if !mediaSidecar(entry) || entry.VideoID == "" || entry.Group == "" || entry.Relative == "" {
		return false
	}
	expected, actual := mediaAttachmentCanonicalName(path.Base(entry.Relative)), mediaAttachmentCanonicalName(name)
	return strings.EqualFold(path.Ext(expected), path.Ext(actual)) && mediaAttachmentTailKey(strings.TrimSuffix(expected, path.Ext(expected))) == mediaAttachmentTailKey(strings.TrimSuffix(actual, path.Ext(actual)))
}

func mediaAttachmentCanonicalName(name string) string {
	ext := path.Ext(name)
	return mediaAttachmentTrimCopy(strings.TrimSuffix(name, ext)) + strings.ToLower(ext)
}

func mediaAttachmentTrimCopy(stem string) string {
	for {
		clean := mediaAttachmentCopySuffix.ReplaceAllString(stem, "")
		if clean == stem {
			return stem
		}
		stem = clean
	}
}

func mediaAttachmentIsCopy(name string) bool {
	return mediaAttachmentCopySuffix.MatchString(strings.TrimSuffix(name, path.Ext(name)))
}

// 只在已有视频或同名附件支持时去掉复制后缀，视频本身始终保持原名。
func mediaAttachmentMatchFiles(files []OrganizeFile) []OrganizeFile {
	out := slices.Clone(files)
	for i, file := range out {
		out[i].Name = mediaAttachmentMatchName(file, files)
	}
	return out
}

func mediaAttachmentMatchName(file OrganizeFile, files []OrganizeFile) string {
	if file.MediaType != "subtitle" && file.MediaType != "attachment" || !mediaAttachmentIsCopy(file.Name) {
		return file.Name
	}
	canonical := mediaAttachmentCanonicalName(file.Name)
	originalStem := strings.TrimSuffix(file.Name, path.Ext(file.Name))
	longest := -1
	for _, video := range files {
		if video.MediaType != "video" || video.ParentID != file.ParentID {
			continue
		}
		videoStem := strings.TrimSuffix(video.Name, path.Ext(video.Name))
		if strings.EqualFold(videoStem, originalStem) {
			return file.Name
		}
		if len(originalStem) < len(videoStem) || !strings.EqualFold(originalStem[:len(videoStem)], videoStem) {
			continue
		}
		tail := mediaAttachmentTrimCopy(originalStem[len(videoStem):])
		if len(videoStem) > longest && (tail == "" || strings.ContainsRune(".-", rune(tail[0]))) {
			canonical, longest = originalStem[:len(videoStem)]+tail+strings.ToLower(path.Ext(file.Name)), len(videoStem)
		}
	}
	candidate := file
	candidate.Name = canonical
	_, _, shared := mediaAttachmentFolder(candidate)
	if shared || longest >= 0 {
		return canonical
	}
	for _, other := range files {
		if other.ParentID == file.ParentID && other.ID != file.ID && strings.EqualFold(other.Name, canonical) {
			return canonical
		}
	}
	return file.Name
}

func mediaAttachmentTailKey(tail string) string {
	value := strings.ToLower(tail)
	value = strings.NewReplacer("zh-hans", "chs", "zh-cn", "chs", "zh_hans", "chs", "zh_cn", "chs", "zh-hant", "cht", "zh-tw", "cht", "zh_hant", "cht", "zh_tw", "cht").Replace(value)
	known, unknown := make([]string, 0, 4), make([]string, 0, 2)
	for _, token := range strings.FieldsFunc(value, func(r rune) bool { return strings.ContainsRune(". _-+&", r) }) {
		switch token {
		case "chs", "sc", "gb", "简", "简中", "简体", "简体中文", "簡體":
			token = "zh-Hans"
		case "cht", "tc", "big5", "繁", "繁中", "繁体", "繁體", "繁体中文":
			token = "zh-Hant"
		case "eng", "en", "英文", "英语", "英":
			token = "en"
		case "jpn", "ja", "日文", "日语", "日":
			token = "ja"
		case "forced", "sdh", "default":
		default:
			unknown = append(unknown, token)
			continue
		}
		known = append(known, token)
	}
	slices.Sort(known)
	known = slices.Compact(known)
	return strings.Join(known, "+") + "|" + strings.Join(unknown, ".")
}

func mediaAttachmentRoleName(run *MediaOrganizeRun, entry MediaOrganizeEntry, name string) string {
	ext := strings.ToLower(path.Ext(name))
	stem := strings.TrimSuffix(name, path.Ext(name))
	for _, video := range run.Entries {
		if video.File.ID != entry.VideoID || video.Relative == "" {
			continue
		}
		videoStem := strings.TrimSuffix(path.Base(video.Relative), path.Ext(video.Relative))
		if len(stem) >= len(videoStem) && strings.EqualFold(stem[:len(videoStem)], videoStem) {
			tail := mediaAttachmentTrimCopy(stem[len(videoStem):])
			if tail == "" || strings.ContainsRune(".-", rune(tail[0])) {
				if entry.File.MediaType == "subtitle" {
					return "subtitle|" + ext + "|" + mediaAttachmentTailKey(tail)
				}
				return "file|" + ext + "|" + strings.ToLower(tail)
			}
		}
	}
	name = mediaAttachmentCanonicalName(name)
	stem = strings.TrimSuffix(name, path.Ext(name))
	return "folder|" + ext + "|" + strings.ToLower(stem)
}

func mediaAttachmentCopyForEntry(run *MediaOrganizeRun, entry MediaOrganizeEntry, name string, target bool) bool {
	if !mediaAttachmentIsCopy(name) {
		return false
	}
	stem := strings.TrimSuffix(name, path.Ext(name))
	for _, video := range run.Entries {
		if video.File.ID != entry.VideoID {
			continue
		}
		videoName := video.File.Name
		if target {
			videoName = path.Base(video.Relative)
		}
		return !strings.EqualFold(stem, strings.TrimSuffix(videoName, path.Ext(videoName)))
	}
	return true
}

func mediaAttachmentChoiceKey(run *MediaOrganizeRun, entry MediaOrganizeEntry) string {
	if !mediaSidecar(entry) || entry.VideoID == "" || entry.Relative == "" {
		return ""
	}
	work := ""
	for _, group := range run.Groups {
		if group.ID == entry.Group && group.TMDBID > 0 && (group.Kind == "tv" || group.Kind == "movie") {
			work = group.Kind + ":" + strconv.Itoa(group.TMDBID)
			break
		}
	}
	if work == "" {
		return ""
	}
	role := mediaAttachmentRoleName(run, entry, path.Base(entry.Relative))
	version := ""
	if !strings.HasPrefix(role, "folder|") {
		version = entry.VideoID
	}
	return work + "|" + path.Dir(entry.Relative) + "|" + version + "|" + role
}

func mediaNewAttachmentKeep(rule MediaOrganizeRule, entry MediaOrganizeEntry, key string) *MediaAttachmentKeepIntent {
	return &MediaAttachmentKeepIntent{Key: key, Phase: "waiting", Storage: rule.Storage, AccountID: rule.AccountID,
		ConnectionID: rule.ConnectionID, ConnectionRevision: rule.ConnectionRevision, SourceRoot: rule.Source, TargetRoot: rule.Target, Relative: entry.Relative}
}

// 同角色同格式只选一份，正常原名优先；未识别的标签与不同视频身份仍各自独立。
func mediaPlanAttachmentChoices(run *MediaOrganizeRun) bool {
	changed := false
	groups := make(map[string][]int)
	for i, entry := range run.Entries {
		if entry.Status != "ready" && entry.Status != "error" || entry.P115Move != nil || entry.AttachmentKeep != nil || !mediaInRunTask(run, entry) {
			continue
		}
		if key := mediaAttachmentChoiceKey(run, entry); key != "" {
			groups[key] = append(groups[key], i)
		}
	}
	for key, indices := range groups {
		slices.SortFunc(indices, func(a, b int) int {
			left, right := run.Entries[a].File, run.Entries[b].File
			leftCopy, rightCopy := mediaAttachmentCopyForEntry(run, run.Entries[a], left.Name, false), mediaAttachmentCopyForEntry(run, run.Entries[b], right.Name, false)
			if leftCopy != rightCopy {
				if leftCopy {
					return 1
				}
				return -1
			}
			return cmp.Or(strings.Compare(path.Clean(filepath.ToSlash(left.SourcePath)), path.Clean(filepath.ToSlash(right.SourcePath))), strings.Compare(left.ID, right.ID))
		})
		for _, index := range indices[1:] {
			entry := &run.Entries[index]
			entry.AttachmentKeep = mediaNewAttachmentKeep(run.Rule, *entry, key)
			entry.AttachmentKeep.KeeperID = run.Entries[indices[0]].File.ID
			changed = true
		}
	}
	return changed
}

func mediaAttachmentSnapshotCD2(file clouddrive2.File) MediaAttachmentFileSnapshot {
	modified := int64(0)
	if !file.Modified.IsZero() {
		modified = file.Modified.UnixNano()
	}
	return MediaAttachmentFileSnapshot{Path: file.Path, ID: file.ID, Name: file.Name, Size: file.Size, Modified: modified, Hashes: maps.Clone(file.Hashes)}
}

func mediaAttachmentSnapshot115(file p115.ShareFile, parent, directory string) MediaAttachmentFileSnapshot {
	return MediaAttachmentFileSnapshot{Path: path.Join(directory, file.Name), ParentID: parent, ID: file.ID, Name: file.Name, Size: file.Size, SHA1: file.SHA1}
}

func mediaAttachmentSnapshotLocal(full string, info os.FileInfo) (MediaAttachmentFileSnapshot, error) {
	identity, err := mediaLocalFileIdentityKey(full, info)
	if err != nil {
		return MediaAttachmentFileSnapshot{}, err
	}
	return MediaAttachmentFileSnapshot{Path: filepath.ToSlash(full), ID: identity, Name: info.Name(), Size: info.Size(), Modified: info.ModTime().UnixNano()}, nil
}

func (f *mediaOrganizeFiles) attachmentDirectory(ctx context.Context, parent, full string) ([]MediaAttachmentFileSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	files := make([]MediaAttachmentFileSnapshot, 0)
	switch f.rule.Storage {
	case "cd2":
		children, err := f.cd2.List(ctx, full, true)
		if err != nil {
			return nil, err
		}
		for _, child := range children {
			if !child.Directory && !child.ReadOnly && child.ID != "" && path.Dir(child.Path) == full && path.Base(child.Path) == child.Name {
				files = append(files, mediaAttachmentSnapshotCD2(child))
			}
		}
	case "115":
		var children []p115.ShareFile
		if drive, ok := f.drive.(media115DirectoryDrive); ok {
			snapshot, err := f.read115BatchDirectory(ctx, drive, parent, full)
			if err != nil {
				return nil, err
			}
			children = snapshot.files
		} else {
			drive, ok := f.drive.(mediaOrganizeDrive)
			if !ok {
				return nil, fmt.Errorf("115 附件目录核验服务不可用")
			}
			directory, err := drive.ReadDirectory(ctx, parent, f.cookie)
			if err != nil {
				return nil, err
			}
			if directory.CID != parent || directory.Path != full {
				return nil, fmt.Errorf("115 附件目录身份已变化")
			}
			children, err = listOrganizeFiles(ctx, f.drive, parent, f.cookie)
			if err != nil {
				return nil, err
			}
		}
		for _, child := range children {
			if !child.Directory && child.ID != "" {
				files = append(files, mediaAttachmentSnapshot115(child, parent, full))
			}
		}
	case "local":
		children, err := os.ReadDir(filepath.FromSlash(full))
		if err != nil {
			return nil, err
		}
		for _, child := range children {
			if child.Type()&os.ModeSymlink != 0 {
				continue
			}
			info, err := child.Info()
			if err != nil {
				return nil, err
			}
			if info.Mode().IsRegular() {
				file, err := mediaAttachmentSnapshotLocal(filepath.Join(filepath.FromSlash(full), child.Name()), info)
				if err != nil {
					return nil, err
				}
				files = append(files, file)
			}
		}
	default:
		return nil, ErrMediaOrganizeInput
	}
	return files, ctx.Err()
}

func (f *mediaOrganizeFiles) attachmentTargets(ctx context.Context, entry MediaOrganizeEntry) ([]MediaAttachmentFileSnapshot, error) {
	directory := path.Dir(entry.Relative)
	full, parent := path.Join(filepath.ToSlash(f.rule.Target), directory), ""
	if f.rule.Storage == "115" {
		var err error
		parent, err = f.cloudDestination(ctx, entry.Relative, false)
		if err != nil || parent == "" {
			return nil, err
		}
	} else if f.rule.Storage == "local" {
		var resolved string
		var err error
		if directory == "." {
			resolved, err = mediaLocalRoot(f.rule.Target)
			if err == nil && resolved != f.rule.Target {
				err = fmt.Errorf("整理目标目录已变化，请重新保存配置")
			}
		} else {
			resolved, err = mediaLocalPath(f.rule.Target, directory, false)
		}
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil, nil
			}
			return nil, err
		}
		full = filepath.ToSlash(resolved)
	}
	files, err := f.attachmentDirectory(ctx, parent, full)
	if errors.Is(err, clouddrive2.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return files, err
}

func mediaAttachmentTarget(run *MediaOrganizeRun, entry MediaOrganizeEntry, targets []MediaAttachmentFileSnapshot) *MediaAttachmentFileSnapshot {
	role := mediaAttachmentRoleName(run, entry, path.Base(entry.Relative))
	matches := make([]MediaAttachmentFileSnapshot, 0)
	for _, file := range targets {
		if file.Size <= 0 || mediaAttachmentRoleName(run, entry, file.Name) != role {
			continue
		}
		if run.Rule.Storage == "115" && file.ID == entry.File.ID || run.Rule.Storage == "cd2" && file.ID == entry.CD2FileID {
			continue
		}
		matches = append(matches, file)
	}
	slices.SortFunc(matches, func(a, b MediaAttachmentFileSnapshot) int {
		leftCopy, rightCopy := mediaAttachmentCopyForEntry(run, entry, a.Name, true), mediaAttachmentCopyForEntry(run, entry, b.Name, true)
		if leftCopy != rightCopy {
			if leftCopy {
				return 1
			}
			return -1
		}
		return cmp.Or(strings.Compare(a.Path, b.Path), strings.Compare(a.ID, b.ID))
	})
	if len(matches) == 0 {
		return nil
	}
	return &matches[0]
}

// 每次重试重新选择，只列已命名附件的精确目标目录。
func (s *MediaOrganizer) prepareAttachmentChoices(ctx context.Context, files *mediaOrganizeFiles, run *MediaOrganizeRun) (choiceErr error) {
	clear := func() {
		for i := range run.Entries {
			if run.Entries[i].Status != "done" && mediaInRunTask(run, run.Entries[i]) {
				run.Entries[i].AttachmentKeep = nil
			}
		}
	}
	clear()
	defer func() {
		if choiceErr != nil {
			clear()
		}
	}()
	mediaPlanAttachmentChoices(run)
	targets := make(map[string][]MediaAttachmentFileSnapshot)
	for i := range run.Entries {
		entry := &run.Entries[i]
		key := mediaAttachmentChoiceKey(run, *entry)
		if key == "" || entry.Status == "done" || entry.P115Move != nil || !mediaInRunTask(run, *entry) {
			continue
		}
		directory := path.Dir(entry.Relative)
		children, read := targets[directory]
		if !read {
			var err error
			children, err = files.attachmentTargets(ctx, *entry)
			if err != nil {
				return fmt.Errorf("核验附件保留目标: %w", err)
			}
			targets[directory] = children
		}
		if keep := mediaAttachmentTarget(run, *entry, children); keep != nil {
			if entry.AttachmentKeep != nil && entry.AttachmentKeep.Keep != nil && mediaAttachmentIntentMatches(run, *entry) && mediaAttachmentSnapshotEqual(*entry.AttachmentKeep.Keep, *keep) {
				continue
			}
			entry.AttachmentKeep = mediaNewAttachmentKeep(run.Rule, *entry, key)
			entry.AttachmentKeep.Keep = keep
			entry.AttachmentKeep.Phase = "selected"
		}
	}
	return nil
}

func mediaAttachmentSnapshotEqual(expected, actual MediaAttachmentFileSnapshot) bool {
	if expected.Path != actual.Path || expected.ParentID != actual.ParentID || expected.ID == "" || expected.ID != actual.ID || expected.Name != actual.Name || expected.Size != actual.Size || expected.Modified != actual.Modified || !strings.EqualFold(expected.SHA1, actual.SHA1) {
		return false
	}
	for algorithm, hash := range expected.Hashes {
		if hash != "" && !strings.EqualFold(actual.Hashes[algorithm], hash) {
			return false
		}
	}
	return true
}

func (f *mediaOrganizeFiles) attachmentReadSnapshot(ctx context.Context, expected MediaAttachmentFileSnapshot) (*MediaAttachmentFileSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if f.rule.Storage == "cd2" {
		actual, err := f.cd2.Stat(ctx, expected.Path, true)
		if errors.Is(err, clouddrive2.ErrNotFound) {
			return nil, f.attachmentMissingSnapshot(ctx, expected)
		}
		if err != nil {
			return nil, err
		}
		file := mediaAttachmentSnapshotCD2(actual)
		if actual.Directory || actual.ReadOnly || !mediaAttachmentSnapshotEqual(expected, file) {
			return nil, fmt.Errorf("附件身份已变化，保留来源等待核对")
		}
		return &file, nil
	}
	if f.rule.Storage == "local" {
		resolved := ""
		for _, root := range []string{f.rule.Source, f.rule.Target, f.rule.Failure} {
			if root == "" || !mediaWithin(root, filepath.FromSlash(expected.Path)) {
				continue
			}
			relative, err := filepath.Rel(root, filepath.FromSlash(expected.Path))
			if err != nil {
				return nil, err
			}
			resolved, err = mediaLocalPath(root, relative, false)
			if errors.Is(err, os.ErrNotExist) {
				return nil, f.attachmentMissingSnapshot(ctx, expected)
			}
			if err != nil {
				return nil, err
			}
			break
		}
		if resolved == "" {
			return nil, ErrMediaOrganizeInput
		}
		info, err := os.Lstat(resolved)
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		file, err := mediaAttachmentSnapshotLocal(resolved, info)
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() || !mediaAttachmentSnapshotEqual(expected, file) {
			return nil, fmt.Errorf("附件身份已变化，保留来源等待核对")
		}
		return &file, nil
	}
	files, err := f.attachmentDirectory(ctx, expected.ParentID, path.Dir(expected.Path))
	if errors.Is(err, clouddrive2.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	for _, file := range files {
		if file.ID == expected.ID {
			if !mediaAttachmentSnapshotEqual(expected, file) {
				return nil, fmt.Errorf("附件身份已变化，保留来源等待核对")
			}
			return &file, nil
		}
	}
	return nil, nil
}

// 原路径消失时核对同目录身份，来源改名不能冒充已经回收。
func (f *mediaOrganizeFiles) attachmentMissingSnapshot(ctx context.Context, expected MediaAttachmentFileSnapshot) error {
	children, err := f.attachmentDirectory(ctx, expected.ParentID, path.Dir(expected.Path))
	if err != nil {
		return err
	}
	for _, child := range children {
		if child.ID == expected.ID {
			return fmt.Errorf("附件位置或身份已变化，保留来源等待核对")
		}
	}
	return nil
}

func (f *mediaOrganizeFiles) attachmentSource(ctx context.Context, entry MediaOrganizeEntry) (*MediaAttachmentFileSnapshot, error) {
	root, parent := f.rule.Source, entry.File.ParentID
	if entry.Failure != nil && entry.Failure.State == "stored" {
		root, parent = f.rule.Failure, entry.Failure.ParentID
	}
	full := path.Join(filepath.ToSlash(root), entry.File.SourcePath)
	expectedID, modified := entry.File.ID, entry.Modified
	if f.rule.Storage == "115" && entry.Failure != nil && entry.Failure.State == "stored" && entry.Failure.FileID != "" {
		expectedID = entry.Failure.FileID
	}
	if f.rule.Storage == "cd2" {
		expectedID = entry.CD2FileID
		if entry.Failure != nil && entry.Failure.State == "stored" && entry.Failure.CD2FileID != "" {
			expectedID = entry.Failure.CD2FileID
		}
		if entry.CD2Current != nil {
			full, expectedID, modified = entry.CD2Current.Path, entry.CD2Current.FileID, entry.CD2Current.Modified
		}
	}
	if !mediaCloudWithin(filepath.ToSlash(root), full) {
		return nil, ErrMediaOrganizeInput
	}
	if f.rule.Storage != "local" && expectedID == "" {
		return nil, fmt.Errorf("来源附件缺少文件身份，保留来源")
	}
	missing := func() (*MediaAttachmentFileSnapshot, error) {
		identity := expectedID
		if f.rule.Storage == "local" {
			identity = f.localSnapshotKeys[entry.File.ID]
		}
		if identity == "" {
			return nil, fmt.Errorf("来源附件已不存在，无法核对原文件身份")
		}
		return nil, f.attachmentMissingSnapshot(ctx, MediaAttachmentFileSnapshot{Path: full, ParentID: parent, ID: identity, Name: path.Base(full)})
	}
	if f.rule.Storage == "local" {
		resolved, err := mediaLocalPath(root, entry.File.SourcePath, false)
		if errors.Is(err, os.ErrNotExist) {
			return missing()
		}
		if err != nil {
			return nil, err
		}
		full = filepath.ToSlash(resolved)
		info, err := os.Lstat(resolved)
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() || f.localSnapshots[entry.File.ID] != nil && !os.SameFile(f.localSnapshots[entry.File.ID], info) {
			return nil, fmt.Errorf("待处理附件的来源身份已变化")
		}
	}
	var children []MediaAttachmentFileSnapshot
	var err error
	if f.rule.Storage == "cd2" {
		var actual clouddrive2.File
		actual, err = f.cd2.Stat(ctx, full, true)
		if err == nil {
			if actual.Directory || actual.ReadOnly || actual.Path != full || actual.Name != path.Base(full) {
				return nil, fmt.Errorf("来源附件位置或属性已变化，保留来源")
			}
			children = []MediaAttachmentFileSnapshot{mediaAttachmentSnapshotCD2(actual)}
		}
	} else {
		children, err = f.attachmentDirectory(ctx, parent, path.Dir(full))
	}
	if errors.Is(err, clouddrive2.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
		return missing()
	}
	if err != nil {
		return nil, err
	}
	for _, file := range children {
		// 115 同目录允许重名，必须先找原 ID，不能选中同名替身。
		if f.rule.Storage == "115" && file.ID != expectedID {
			continue
		}
		if file.Path != full {
			if f.rule.Storage == "115" {
				// 隔离去重允许原名大小写差异，仍须核对同一冻结目录及已保存的保留 ID。
				adopted := entry.Failure != nil && entry.Failure.State == "stored" && entry.Failure.FileID != ""
				if !adopted || path.Dir(file.Path) != path.Dir(full) || !strings.EqualFold(path.Base(file.Path), path.Base(full)) {
					return nil, fmt.Errorf("待处理附件的来源位置已变化")
				}
			} else {
				continue
			}
		}
		valid := file.Size == entry.File.Size
		if f.rule.Storage == "local" {
			valid = valid && file.Modified == modified
			if identity := f.localSnapshotKeys[entry.File.ID]; identity != "" {
				valid = valid && file.ID == identity
			}
		} else {
			valid = valid && expectedID != "" && file.ID == expectedID
			if f.rule.Storage == "115" {
				valid = valid && strings.EqualFold(file.SHA1, entry.File.SHA1)
			} else {
				for algorithm, hash := range entry.CD2Hashes {
					if hash != "" && !strings.EqualFold(file.Hashes[algorithm], hash) {
						valid = false
					}
				}
				if modified != 0 && file.Modified != 0 && file.Modified != modified {
					valid = false
				}
			}
		}
		if !valid {
			return nil, fmt.Errorf("待处理附件的来源身份已变化")
		}
		return &file, nil
	}
	return nil, nil
}

func mediaAttachmentIntentMatches(run *MediaOrganizeRun, entry MediaOrganizeEntry) bool {
	intent := entry.AttachmentKeep
	return intent != nil && mediaSidecar(entry) && intent.Key == mediaAttachmentChoiceKey(run, entry) && intent.Storage == run.Rule.Storage &&
		intent.AccountID == run.Rule.AccountID && intent.ConnectionID == run.Rule.ConnectionID && intent.ConnectionRevision == run.Rule.ConnectionRevision &&
		intent.SourceRoot == run.Rule.Source && intent.TargetRoot == run.Rule.Target && intent.Relative == entry.Relative
}

func (s *MediaOrganizer) executeAttachmentChoice(ctx context.Context, files *mediaOrganizeFiles, run *MediaOrganizeRun, index int) (chosen bool, choiceErr error) {
	entry := &run.Entries[index]
	defer func() {
		if choiceErr != nil {
			entry.AttachmentKeep = nil
		}
	}()
	if err := ctx.Err(); err != nil {
		return true, err
	}
	if !mediaSidecar(*entry) || entry.P115Move != nil || mediaAttachmentChoiceKey(run, *entry) == "" {
		return false, nil
	}
	var verifiedKeep *MediaAttachmentFileSnapshot
	if entry.AttachmentKeep == nil {
		targets, err := files.attachmentTargets(ctx, *entry)
		if err != nil {
			return true, err
		}
		keep := mediaAttachmentTarget(run, *entry, targets)
		if keep == nil {
			return false, nil
		}
		entry.AttachmentKeep = mediaNewAttachmentKeep(run.Rule, *entry, mediaAttachmentChoiceKey(run, *entry))
		entry.AttachmentKeep.Keep = keep
		verifiedKeep = keep
	}
	if !mediaAttachmentIntentMatches(run, *entry) {
		return true, fmt.Errorf("附件保留计划与当前归属不一致，请先核对文件位置")
	}
	intent := entry.AttachmentKeep
	if intent.Keep == nil {
		for _, keeper := range run.Entries {
			if keeper.File.ID == intent.KeeperID && keeper.Status != "done" {
				return true, fmt.Errorf("等待同角色附件先完成，来源暂时保留")
			}
		}
		targets, err := files.attachmentTargets(ctx, *entry)
		if err != nil {
			return true, err
		}
		intent.Keep = mediaAttachmentTarget(run, *entry, targets)
		if intent.Keep == nil {
			return true, fmt.Errorf("保留的同角色附件尚未在目标确认，来源暂时保留")
		}
		verifiedKeep = intent.Keep
	}
	if !mediaCloudWithin(filepath.ToSlash(run.Rule.Target), intent.Keep.Path) || intent.Keep.Size <= 0 {
		return true, ErrMediaOrganizeInput
	}
	// 同次列目录可复用，删除前仍重新核对保留项。
	keep := verifiedKeep
	var err error
	if keep == nil {
		keep, err = files.attachmentReadSnapshot(ctx, *intent.Keep)
		if err != nil {
			return true, fmt.Errorf("核验保留附件: %w", err)
		}
	}
	if keep == nil {
		return true, fmt.Errorf("保留的同角色附件已不存在，来源暂时保留")
	}
	source, err := files.attachmentSource(ctx, *entry)
	if err != nil {
		return true, fmt.Errorf("核验来源附件: %w", err)
	}
	if source == nil {
		return true, ctx.Err()
	}
	intent.Source, intent.Keep = source, keep
	if err := s.checkpoint(run); err != nil {
		return true, fmt.Errorf("%w: %w", errMediaAttachmentCheckpoint, err)
	}
	// 外部删除之前再次核对两边身份，读取异常绝不解释为文件不存在。
	if current, err := files.attachmentReadSnapshot(ctx, *keep); err != nil || current == nil {
		if err == nil {
			err = fmt.Errorf("保留附件已不存在")
		}
		return true, err
	}
	if current, err := files.attachmentReadSnapshot(ctx, *source); err != nil || current == nil {
		if err == nil {
			err = fmt.Errorf("来源附件在回收前发生变化")
		}
		return true, err
	}
	switch run.Rule.Storage {
	case "cd2":
		err = files.cd2.Delete(ctx, []string{source.Path})
	case "115":
		recycle, ok := files.drive.(organizeRecycleDrive)
		if !ok {
			return true, fmt.Errorf("115 附件回收服务不可用")
		}
		err = recycle.DeleteEntries(ctx, []string{source.ID}, files.cookie)
	case "local":
		err = os.Remove(filepath.FromSlash(source.Path))
	}
	if err != nil {
		return true, fmt.Errorf("同角色附件回收失败: %w", err)
	}
	remaining, err := files.attachmentReadSnapshot(ctx, *source)
	if err != nil {
		return true, fmt.Errorf("核验来源附件回收结果: %w", err)
	}
	if remaining != nil {
		return true, fmt.Errorf("来源附件仍存在，本次回收未完成")
	}
	if current, err := files.attachmentReadSnapshot(ctx, *keep); err != nil || current == nil {
		if err == nil {
			err = fmt.Errorf("保留附件无法确认")
		}
		return true, err
	}
	return true, ctx.Err()
}

func (s *MediaOrganizer) executePendingAttachmentChoices(ctx context.Context, files *mediaOrganizeFiles, run *MediaOrganizeRun, candidates map[string]bool) error {
	for i, entry := range run.Entries {
		if entry.AttachmentKeep == nil || entry.Status == "done" || !mediaInRunTask(run, entry) || candidates != nil && !candidates[entry.File.ID] {
			continue
		}
		if err := s.executeMediaEntry(ctx, files, run, i); err != nil {
			return err
		}
	}
	return nil
}

func mediaAttachmentChoiceResult(entry *MediaOrganizeEntry) {
	entry.Status, entry.Message, entry.Failure = "done", "", nil
	entry.Target = entry.AttachmentKeep.Keep.Path
	entry.AttachmentKeep = nil
	entry.Result = &MediaOrganizeResult{Status: "done", Action: "attachment_kept", Message: "已保留另一份同角色附件", Target: entry.Target, At: time.Now()}
}
