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

// VideoIndex refers to the immutable item order, including after cloud binding.
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
	versions := make(map[string][]subshare.File)
	targets := make(map[string]bool)
	for _, job := range jobs {
		for _, item := range job.Items {
			targets[strings.ToLower(item.TargetPath)] = true
		}
	}
	for i, resource := range resources {
		job := &jobs[i]
		for _, selection := range resource.Subtitles {
			count++
			if count > 100 {
				return fmt.Errorf("%w：所选字幕超过 100 个", ErrOrganizeInput)
			}
			var source *subshare.File
			attachment := ""
			if selection.LocalID != "" {
				if s.SubtitleAttachments == "" || !subtitleAttachmentID(selection.LocalID) || !subtitleDigest(selection.SHA256) || selection.Size < 1 || selection.Size > p115.MaxSubtitleBytes || !subtitle.Supported(selection.OriginalName) || len(selection.OriginalName) > 2048 || strings.ContainsAny(selection.OriginalName, "\x00\r\n") || selection.VersionID != "" || selection.Path != "" || selection.SHA != "" {
					return fmt.Errorf("%w：本地字幕文件或大小无效", ErrOrganizeInput)
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
			video := job.Items[videoIndex]
			videoName := video.Name
			if video.File.TitleOnly {
				videoName = video.Stem + ".mkv"
			}
			if organizeName(selection.Version) != selection.Version {
				return fmt.Errorf("%w：字幕版本标记无效", ErrOrganizeInput)
			}
			name, err := subtitle.Name(videoName, source.Name, selection.Language, selection.Version)
			if err != nil {
				return fmt.Errorf("%w：%s", ErrOrganizeInput, err)
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
		}
		for _, sub := range job.Download.Subtitles {
			if !strings.EqualFold(path.Ext(sub.Name), ".idx") {
				continue
			}
			paired := false
			for _, other := range job.Download.Subtitles {
				if other.VideoIndex == sub.VideoIndex && other.Language == sub.Language && other.Version == sub.Version && other.File.Revision == sub.File.Revision && ((sub.LocalID != "" && other.LocalID != "" && strings.EqualFold(other.File.Name, strings.TrimSuffix(sub.File.Name, path.Ext(sub.File.Name))+".sub")) || (sub.LocalID == "" && other.LocalID == "" && strings.EqualFold(other.File.Path, strings.TrimSuffix(sub.File.Path, path.Ext(sub.File.Path))+".sub"))) {
					paired = true
					break
				}
			}
			if !paired {
				return fmt.Errorf("%w：IDX 需要选择同版本配套 SUB 并关联相同视频与语言", ErrOrganizeInput)
			}
		}
	}
	return nil
}

// Executed under the existing directory writer reservation, with pinned task
// credentials. Recovery lists each destination once, never resubmits a magnet.
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
	for i := range job.Download.Subtitles {
		sub := &job.Download.Subtitles[i]
		if sub.Status == "done" || sub.Status == "skipped" {
			continue
		}
		if sub.VideoIndex < 0 || sub.VideoIndex >= len(job.Items) {
			return fail(sub, fmt.Errorf("字幕视频关联无效"))
		}
		video := job.Items[sub.VideoIndex]
		name, err := subtitle.Name(video.Name, sub.File.Name, sub.Language, sub.Version)
		if err != nil {
			return fail(sub, err)
		}
		sub.Name, sub.TargetPath = name, path.Join(path.Dir(video.TargetPath), name)
		files, loaded := cache[video.TargetCID]
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
		// Do not combine half of an existing VobSub pair with a new version.
		// A saved upload intent, however, can safely finish its missing partner.
		if sub.Status == "pending" && (strings.HasSuffix(name, ".idx") || strings.HasSuffix(name, ".sub")) {
			partnerName := strings.TrimSuffix(name, path.Ext(name)) + ".sub"
			if strings.HasSuffix(name, ".sub") {
				partnerName = strings.TrimSuffix(name, path.Ext(name)) + ".idx"
			}
			for _, partner := range job.Download.Subtitles {
				if partner.VideoIndex != sub.VideoIndex || partner.Name != partnerName || partner.Status != "pending" {
					continue
				}
				currentExists, partnerExists := false, false
				for _, f := range files {
					currentExists = currentExists || strings.EqualFold(f.Name, name)
					partnerExists = partnerExists || strings.EqualFold(f.Name, partner.Name)
				}
				if currentExists != partnerExists {
					return fail(sub, fmt.Errorf("目标目录已有不完整的 IDX/SUB 字幕，请先核对或更换语言标记"))
				}
			}
		}
		matches := 0
		var existing p115.ShareFile
		for _, f := range files {
			if strings.EqualFold(f.Name, name) {
				matches++
				existing = f
			}
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
		if err = uploader.UploadSubtitle(ctx, video.TargetCID, name, data, cookie); err != nil {
			return fail(sub, err)
		}
		sub.Message = "字幕已上传，等待核实"
		cache[video.TargetCID] = append(files, p115.ShareFile{Name: name, Size: sub.File.Size})
		if err = s.Organize.checkpoint(job); err != nil {
			return err
		}
	}
	// Verify all successful writes with one read per destination, retaining real
	// file identities for the exact STRM-share manifest and restart recovery.
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
