package service

import (
	"context"
	"fmt"
	"path"
	"strings"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/client/subshare"
	"strm115/backend/internal/utils/subtitle"
)

type subtitleLibrary interface {
	Files(context.Context, string) ([]subshare.File, error)
	Read(context.Context, subshare.File) ([]byte, error)
}
type DownloadSubtitleSelection struct {
	LocalID      string `json:"localId,omitempty"`
	SourceKey    string `json:"sourceKey,omitempty"`
	OriginalName string `json:"originalName,omitempty"`
	SHA256       string `json:"sha256,omitempty"`
	Size         int64  `json:"size,omitempty"`
	Version      string `json:"version,omitempty"`
	VideoID      string `json:"videoId"`
	Language     string `json:"language"`
	SHA          string `json:"sha"`
	VersionID    string `json:"versionId"`
	Path         string `json:"path"`
}

// VideoIndex 对应绑定前后固定的视频条目顺序，Name 保存已确认的字幕目标名。
type DownloadSubtitle struct {
	AttachmentID string        `json:"attachmentId,omitempty"`
	LocalID      string        `json:"localId,omitempty"`
	SHA256       string        `json:"sha256,omitempty"`
	Version      string        `json:"version,omitempty"`
	FileID       string        `json:"fileId,omitempty"`
	File         subshare.File `json:"file"`
	VideoIndex   int           `json:"videoIndex"`
	Language     string        `json:"language"`
	Name         string        `json:"name"`
	TargetPath   string        `json:"targetPath"`
	Status       string        `json:"status"`
	Message      string        `json:"message,omitempty"`
}

type downloadSubtitlePair struct {
	videoIndex             int
	local                  bool
	scope, revision        string
	sourceStem, targetStem string
}

func downloadSubtitlePairConflict(items []OrganizeItem, external []DownloadSubtitle) bool {
	if len(external) == 0 {
		return false
	}
	var moves []subtitle.Move
	for _, item := range items {
		if item.File.MediaType == "subtitle" && subtitle.PairStem(item.Name) != "" {
			moves = append(moves, subtitle.Move{SourceScope: "download:" + item.File.ParentID, SourceName: item.File.Name, Target: item.TargetPath})
		}
	}
	for _, sub := range external {
		if subtitle.PairStem(sub.Name) != "" && sub.VideoIndex >= 0 && sub.VideoIndex < len(items) {
			target := path.Join(path.Dir(items[sub.VideoIndex].TargetPath), sub.Name)
			moves = append(moves, subtitle.Move{SourceScope: "external", SourceName: sub.File.Name, Target: target})
		}
	}
	return len(subtitle.PairConflicts(moves)) > 0
}

func pendingDownloadSubtitles(job OrganizeJob) bool {
	if job.Download == nil || !downloadItemsDone(job) {
		return false
	}
	for _, sub := range job.Download.Subtitles {
		if sub.Status != "done" && sub.Status != "skipped" {
			return true
		}
	}
	return false
}

func (s *DownloadOrganize) planSubtitles(ctx context.Context, jobs []OrganizeJob, resources []DownloadOrganizeInput) error {
	var total int64
	count := 0
	for _, resource := range resources {
		count += len(resource.Subtitles)
		if count > MaxSubtitleImportFiles {
			return fmt.Errorf("%w：所选字幕超过 %d 个", ErrOrganizeInput, MaxSubtitleImportFiles)
		}
	}
	versions := make(map[string][]subshare.File)
	targets := make(map[string]bool, count)
	for _, job := range jobs {
		for _, item := range job.Items {
			targets[strings.ToLower(item.TargetPath)] = true
		}
	}
	for i, resource := range resources {
		job := &jobs[i]
		idxPairs := make(map[downloadSubtitlePair]bool)
		subPairs := make(map[downloadSubtitlePair]bool)
		for _, selection := range resource.Subtitles {
			if err := ctx.Err(); err != nil {
				return err
			}
			var source *subshare.File
			attachment := ""
			if selection.LocalID != "" {
				if s.SubtitleAttachments == "" || !subtitleAttachmentID(selection.LocalID) || !subtitleDigest(selection.SHA256) || !subtitle.Supported(selection.OriginalName) || len(selection.OriginalName) > 2048 || strings.ContainsAny(selection.OriginalName, "\x00\r\n") || selection.VersionID != "" || selection.Path != "" || selection.SHA != "" {
					return fmt.Errorf("%w：本地字幕文件无效", ErrOrganizeInput)
				}
				if len(selection.SourceKey) > 128 || strings.ContainsAny(selection.SourceKey, "\x00\r\n") {
					return fmt.Errorf("%w：本地字幕来源无效", ErrOrganizeInput)
				}
				var err error
				attachment, err = organizeToken()
				if err != nil {
					return err
				}
				source = &subshare.File{Path: selection.LocalID, Name: selection.OriginalName, Size: selection.Size}
			} else {
				if s.Subtitles == nil {
					return fmt.Errorf("%w：字幕库不可用", ErrOrganizeInput)
				}
				files, ok := versions[selection.VersionID]
				if !ok {
					var err error
					files, err = s.Subtitles.Files(ctx, selection.VersionID)
					if err != nil {
						return err
					}
					versions[selection.VersionID] = files
				}
				for n := range files {
					if files[n].Path == selection.Path && files[n].SHA == selection.SHA {
						source = &files[n]
						break
					}
				}
			}
			videoIndex := -1
			for n, item := range job.Items {
				if item.File.ID == selection.VideoID && item.File.MediaType == "video" {
					videoIndex = n
					break
				}
			}
			if source == nil || videoIndex < 0 {
				return fmt.Errorf("%w：字幕或关联视频不在本次选择中", ErrOrganizeInput)
			}
			if source.Size < 1 {
				return fmt.Errorf("%w：字幕不能为空文件", ErrOrganizeInput)
			}
			if source.Size > p115.MaxSubtitleBytes {
				return fmt.Errorf("%w：单个字幕不能超过 16 MiB", ErrOrganizeInput)
			}
			video := job.Items[videoIndex]
			tail, err := subtitle.OutputTail(source.Name, selection.Language, selection.Version)
			if err != nil {
				return fmt.Errorf("%w：%w", ErrOrganizeInput, err)
			}
			name, err := subtitle.AppendTail(video.Stem, tail)
			if err != nil {
				return fmt.Errorf("%w：%w", ErrOrganizeInput, err)
			}
			target := path.Join(path.Dir(video.TargetPath), name)
			key := strings.ToLower(target)
			if targets[key] {
				return fmt.Errorf("%w：字幕目标同名，请更换语言或移除重复字幕：%s", ErrOrganizeConflict, name)
			}
			targets[key] = true
			total += source.Size
			if total > MaxSubtitleImportBytes {
				return fmt.Errorf("%w：所选字幕总大小超过 64 MiB", ErrOrganizeInput)
			}
			job.Download.Subtitles = append(job.Download.Subtitles, DownloadSubtitle{File: *source, VideoIndex: videoIndex, Language: selection.Language, Version: selection.Version, LocalID: selection.LocalID, SHA256: selection.SHA256, AttachmentID: attachment, Name: name, TargetPath: target, Status: "pending"})
			ext := path.Ext(name)
			if ext == ".idx" || ext == ".sub" {
				sourcePath, scope := source.Path, selection.VersionID
				local := selection.LocalID != ""
				if local {
					sourcePath, scope = source.Name, selection.SourceKey
				}
				sourcePath = strings.ReplaceAll(sourcePath, `\`, "/")
				pair := downloadSubtitlePair{
					videoIndex: videoIndex, local: local, scope: scope, revision: source.Revision,
					sourceStem: strings.ToLower(strings.TrimSuffix(sourcePath, path.Ext(sourcePath))),
					targetStem: strings.TrimSuffix(name, ext),
				}
				if ext == ".idx" {
					idxPairs[pair] = true
				} else {
					subPairs[pair] = true
				}
			}
		}
		for pair := range idxPairs {
			if !subPairs[pair] {
				return fmt.Errorf("%w：IDX 需要选择同来源的配套 SUB，并生成同名的字幕文件", ErrOrganizeInput)
			}
		}
		if downloadSubtitlePairConflict(job.Items, job.Download.Subtitles) {
			return fmt.Errorf("%w：资源附带字幕与已选字幕的 IDX/SUB 来源不同", ErrOrganizeInput)
		}
	}
	return nil
}

// 沿用任务固定凭据和目标目录写锁。恢复时核对每个目标目录，不重新提交磁力。
func (s *DownloadOrganize) uploadSubtitles(ctx context.Context, job *OrganizeJob, cookie string) error {
	if !pendingDownloadSubtitles(*job) {
		return nil
	}
	fail := func(sub *DownloadSubtitle, err error) error {
		sub.Message = organizePublicError(err, cookie)
		job.Status, job.Message = "partial", "视频已整理，字幕未完成；检查并继续可重试字幕："+sub.Message
		return s.Organize.checkpoint(job)
	}
	uploader, ok := s.Organize.drive.(subtitleUploader)
	if !ok {
		return fail(&job.Download.Subtitles[0], fmt.Errorf("字幕来源或上传服务不可用"))
	}
	cache := make(map[string][]p115.ShareFile)
	type pairMember struct {
		videoIndex int
		name       string
	}
	pairMembers := make(map[pairMember]*DownloadSubtitle)
	for i := range job.Download.Subtitles {
		sub := &job.Download.Subtitles[i]
		if ext := path.Ext(sub.Name); ext == ".idx" || ext == ".sub" {
			pairMembers[pairMember{sub.VideoIndex, sub.Name}] = sub
		}
	}
	for i := range job.Download.Subtitles {
		sub := &job.Download.Subtitles[i]
		if sub.Status == "done" || sub.Status == "skipped" {
			continue
		}
		if sub.VideoIndex < 0 || sub.VideoIndex >= len(job.Items) {
			return fail(sub, fmt.Errorf("字幕视频关联无效"))
		}
		video := job.Items[sub.VideoIndex]
		name := sub.Name
		if !subtitle.Supported(name) || strings.ContainsAny(name, "/\\\x00\r\n") {
			return fail(sub, fmt.Errorf("已确认的字幕目标名无效，请重新预览"))
		}
		sub.TargetPath = path.Join(path.Dir(video.TargetPath), name)
		files, loaded := cache[video.TargetCID]
		var err error
		if !loaded {
			files, err = s.Organize.list(ctx, video.TargetCID, cookie)
			if err != nil {
				return fail(sub, err)
			}
			cache[video.TargetCID] = files
		}
		if !organizeSourceMatches(files, OrganizeFile{ShareFile: video.File.ShareFile}, video.Name) {
			return fail(sub, fmt.Errorf("目标视频已移动或改名，请核对目录"))
		}
		var partner *DownloadSubtitle
		ext := path.Ext(name)
		if sub.Status == "pending" && (ext == ".idx" || ext == ".sub") {
			partnerExt := ".sub"
			if ext == ".sub" {
				partnerExt = ".idx"
			}
			partner = pairMembers[pairMember{sub.VideoIndex, strings.TrimSuffix(name, ext) + partnerExt}]
		}
		matches := 0
		partnerExists := false
		unplannedIndex := ""
		if sub.Status == "pending" && ext == ".sub" && partner == nil {
			unplannedIndex = subtitle.PairStem(name) + ".idx"
		}
		var existing p115.ShareFile
		for _, f := range files {
			if strings.EqualFold(f.Name, name) {
				matches++
				existing = f
			}
			partnerExists = partnerExists || (partner != nil && strings.EqualFold(f.Name, partner.Name))
			if unplannedIndex != "" && strings.EqualFold(f.Name, unplannedIndex) {
				return fail(sub, fmt.Errorf("目标目录已有同名 IDX，请核对来源或更换版本标记"))
			}
		}
		// 防止把已有的半组 VobSub 与新版本混用，已有本任务上传记录时可恢复缺少的配套文件。
		if partner != nil && (partner.Status == "pending" || partner.Status == "skipped") && (matches > 0) != partnerExists {
			return fail(sub, fmt.Errorf("目标目录已有不完整的 IDX/SUB 字幕，请先核对或更换语言标记"))
		}
		if matches > 0 {
			if matches != 1 || existing.Directory || existing.Size != sub.File.Size || !organizeID(existing.ID) {
				return fail(sub, fmt.Errorf("同名字幕无法唯一确认，请核对目标目录"))
			}
			sub.FileID = existing.ID
			if sub.Status == "uncertain" {
				sub.Status, sub.Message = "done", "已确认上次上传的字幕"
			} else {
				sub.Status, sub.Message = "skipped", "已有同名字幕，已跳过"
			}
			if err = s.Organize.checkpoint(job); err != nil {
				return err
			}
			continue
		}
		data, err := s.readDownloadSubtitle(ctx, *sub)
		if err != nil {
			return fail(sub, err)
		}
		sub.Status, sub.Message = "uncertain", "字幕上传结果待确认"
		if err = s.Organize.checkpoint(job); err != nil {
			return err
		}
		if _, err = s.Organize.account(job.Config.AccountID); err != nil {
			return fail(sub, err)
		}
		if err = uploader.UploadSubtitle(ctx, video.TargetCID, name, data, cookie); err != nil {
			return fail(sub, err)
		}
		sub.Message = "字幕已上传，等待核实"
		cache[video.TargetCID] = append(files, p115.ShareFile{Name: name, Size: sub.File.Size})
		if err = s.Organize.checkpoint(job); err != nil {
			return err
		}
	}
	// 每个目标目录只复查一次，保留 STRM 分享清单和任务恢复需要的文件标识。
	verified := make(map[string][]p115.ShareFile)
	for i := range job.Download.Subtitles {
		sub := &job.Download.Subtitles[i]
		if sub.Status != "uncertain" {
			continue
		}
		cid := job.Items[sub.VideoIndex].TargetCID
		files, ok := verified[cid]
		if !ok {
			var err error
			files, err = s.Organize.list(ctx, cid, cookie)
			if err != nil {
				return fail(sub, err)
			}
			verified[cid] = files
		}
		matches := 0
		for _, file := range files {
			if !file.Directory && file.Name == sub.Name && file.Size == sub.File.Size {
				matches++
				sub.FileID = file.ID
			}
		}
		if matches != 1 || !organizeID(sub.FileID) {
			return fail(sub, fmt.Errorf("上传后未能唯一确认字幕，请检查目标目录"))
		}
		sub.Status, sub.Message = "done", "字幕已上传并核实"
	}
	return s.Organize.checkpoint(job)
}
