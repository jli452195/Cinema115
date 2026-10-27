package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"strings"
	"time"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/utils/subtitle"
)

const MaxSubtitleImportBytes = 64 << 20

type subtitleUploader interface {
	UploadSubtitle(context.Context, string, string, []byte, string) error
}

type SubtitleVideo struct {
	p115.ShareFile
	ParentID string `json:"parentId"`
	Path     string `json:"path"`
}

type SubtitleSelection struct {
	LocalID      string `json:"localId"`
	OriginalName string `json:"originalName"`
	VideoID      string `json:"videoId"`
	Language     string `json:"language"`
	Version      string `json:"version"`
	Size         int64  `json:"size"`
	SHA256       string `json:"sha256"`
}

type SubtitlePreviewInput struct {
	AccountID string              `json:"accountId"`
	JobID     string              `json:"jobId"`
	Files     []SubtitleSelection `json:"files"`
}

type SubtitleImportItem struct {
	SubtitleSelection
	Video      SubtitleVideo `json:"video"`
	Name       string        `json:"name"`
	TargetPath string        `json:"targetPath"`
	Status     string        `json:"status"`
	Message    string        `json:"message,omitempty"`
}

type SubtitleImport struct {
	ID        string               `json:"id"`
	AccountID string               `json:"accountId"`
	JobID     string               `json:"jobId"`
	Revision  uint64               `json:"revision"`
	Status    string               `json:"status"`
	Message   string               `json:"message,omitempty"`
	CreatedAt time.Time            `json:"createdAt"`
	Items     []SubtitleImportItem `json:"items"`
}

func (s *ManualOrganize) SubtitleVideos(ctx context.Context, accountID, jobID string) ([]SubtitleVideo, error) {
	a, err := s.account(accountID)
	if err != nil {
		return nil, err
	}
	return s.subtitleVideos(ctx, accountID, jobID, a.Cookie)
}

func (s *ManualOrganize) subtitleVideos(ctx context.Context, accountID, jobID, cookie string) ([]SubtitleVideo, error) {
	var job *OrganizeJob
	for _, item := range s.History() {
		if item.ID == jobID && item.Config.AccountID == accountID {
			copy := item
			job = &copy
			break
		}
	}
	if job == nil || job.Status == "running" || job.Status == "queued" {
		return nil, fmt.Errorf("%w：请从已结束的整理记录选择作品", ErrOrganizeInput)
	}
	cache := make(map[string][]p115.ShareFile)
	videos := make([]SubtitleVideo, 0, len(job.Items))
	for _, item := range job.Items {
		if item.Status != "done" || item.File.MediaType != "video" || !organizeID(item.TargetCID) {
			continue
		}
		files, ok := cache[item.TargetCID]
		if !ok {
			var err error
			files, err = s.list(ctx, item.TargetCID, cookie)
			if err != nil {
				return nil, err
			}
			cache[item.TargetCID] = files
		}
		for _, file := range files {
			if file.ID == item.File.ID && !file.Directory && file.Size == item.File.Size && organizeMedia(file.Name) == "video" {
				videos = append(videos, SubtitleVideo{ShareFile: file, ParentID: item.TargetCID, Path: path.Join(path.Dir(item.TargetPath), file.Name)})
			}
		}
	}
	if len(videos) == 0 {
		return nil, fmt.Errorf("%w：未找到已整理的视频，可能已被移动，请检查目标目录", ErrOrganizeConflict)
	}
	return videos, nil
}

func subtitleExtension(name string) bool {
	return subtitle.Supported(name)
}

func (s *ManualOrganize) PreviewSubtitles(ctx context.Context, input SubtitlePreviewInput) (SubtitleImport, error) {
	if len(input.Files) == 0 || len(input.Files) > 100 {
		return SubtitleImport{}, ErrOrganizeInput
	}
	a, err := s.account(input.AccountID)
	if err != nil {
		return SubtitleImport{}, err
	}
	if _, ok := s.drive.(subtitleUploader); !ok {
		return SubtitleImport{}, fmt.Errorf("当前网盘不支持字幕上传")
	}
	if err := s.reserveCleanup(); err != nil {
		return SubtitleImport{}, err
	}
	defer s.releaseCleanup()
	videos, err := s.subtitleVideos(ctx, input.AccountID, input.JobID, a.Cookie)
	if err != nil {
		return SubtitleImport{}, err
	}
	byID := make(map[string]SubtitleVideo, len(videos))
	for _, v := range videos {
		byID[v.ID] = v
	}
	plan := SubtitleImport{AccountID: input.AccountID, JobID: input.JobID, Revision: a.Revision, CreatedAt: time.Now(), Status: "preview", Items: make([]SubtitleImportItem, 0, len(input.Files))}
	seen := make(map[string]bool, len(input.Files))
	names := make(map[string]bool, len(input.Files))
	cache := make(map[string][]p115.ShareFile)
	var total int64
	for _, file := range input.Files {
		video, ok := byID[file.VideoID]
		digest, hashErr := hex.DecodeString(file.SHA256)
		if !ok || !organizeID(file.LocalID) || seen[file.LocalID] || !subtitleExtension(file.OriginalName) || len(file.OriginalName) > 2048 || strings.ContainsAny(file.OriginalName, "\x00\r\n") || file.Size <= 0 || file.Size > p115.MaxSubtitleBytes || hashErr != nil || len(digest) != sha256.Size {
			return SubtitleImport{}, fmt.Errorf("%w：字幕文件、大小或视频匹配无效", ErrOrganizeInput)
		}
		seen[file.LocalID] = true
		total += file.Size
		if total > MaxSubtitleImportBytes {
			return SubtitleImport{}, fmt.Errorf("%w：每次字幕总大小不能超过 64 MiB", ErrOrganizeInput)
		}
		switch file.Language {
		case "", "zh-CN", "zh-TW", "en", "zh", "zh-en":
		default:
			return SubtitleImport{}, ErrOrganizeInput
		}
		if len([]rune(file.Version)) > 30 || organizeName(file.Version) != file.Version {
			return SubtitleImport{}, fmt.Errorf("%w：字幕版本标记无效", ErrOrganizeInput)
		}
		name, err := subtitle.Name(video.Name, file.OriginalName, file.Language, file.Version)
		if err != nil {
			return SubtitleImport{}, fmt.Errorf("%w：%s", ErrOrganizeInput, err)
		}
		key := video.ParentID + "/" + strings.ToLower(name)
		if names[key] {
			return SubtitleImport{}, fmt.Errorf("%w：多个字幕的目标名称相同，请修改语言、版本或取消勾选", ErrOrganizeInput)
		}
		names[key] = true
		files, ok := cache[video.ParentID]
		if !ok {
			files, err = s.list(ctx, video.ParentID, a.Cookie)
			if err != nil {
				return SubtitleImport{}, err
			}
			cache[video.ParentID] = files
		}
		item := SubtitleImportItem{SubtitleSelection: file, Video: video, Name: name, TargetPath: path.Join(path.Dir(video.Path), name), Status: "pending"}
		if organizeCollision(files, name, "") {
			item.Status, item.Message = "skipped", "已有同名条目，将跳过"
		}
		plan.Items = append(plan.Items, item)
	}
	// IDX is an index into a same-stem SUB payload. Never import it alone.
	for _, item := range plan.Items {
		if strings.EqualFold(path.Ext(item.Name), ".idx") && !names[item.Video.ParentID+"/"+strings.ToLower(strings.TrimSuffix(item.Name, path.Ext(item.Name))+".sub")] {
			return SubtitleImport{}, fmt.Errorf("%w：IDX 字幕需要勾选配套 SUB 并匹配同一视频、语言和版本", ErrOrganizeInput)
		}
	}
	for i := range plan.Items {
		if partner := subtitlePartner(plan.Items, i); partner >= 0 && (plan.Items[i].Status == "skipped" || plan.Items[partner].Status == "skipped") {
			plan.Items[i].Status, plan.Items[i].Message = "skipped", "IDX / SUB 组已有同名条目，整组跳过"
			plan.Items[partner].Status, plan.Items[partner].Message = "skipped", "IDX / SUB 组已有同名条目，整组跳过"
		}
	}
	plan.ID, err = organizeToken()
	if err != nil {
		return SubtitleImport{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.subtitlePlans == nil {
		s.subtitlePlans = make(map[string]SubtitleImport)
	}
	for id, old := range s.subtitlePlans {
		if time.Since(old.CreatedAt) > 10*time.Minute {
			delete(s.subtitlePlans, id)
		}
	}
	if len(s.subtitlePlans) >= 8 {
		oldest := ""
		for id, old := range s.subtitlePlans {
			if oldest == "" || old.CreatedAt.Before(s.subtitlePlans[oldest].CreatedAt) {
				oldest = id
			}
		}
		delete(s.subtitlePlans, oldest)
	}
	s.subtitlePlans[plan.ID] = cloneSubtitleImport(plan)
	return plan, nil
}

func cloneSubtitleImport(job SubtitleImport) SubtitleImport {
	job.Items = append([]SubtitleImportItem{}, job.Items...)
	return job
}

func subtitlePartner(items []SubtitleImportItem, index int) int {
	item := items[index]
	ext := strings.ToLower(path.Ext(item.Name))
	if ext != ".idx" && ext != ".sub" {
		return -1
	}
	stem := strings.TrimSuffix(item.Name, path.Ext(item.Name))
	for i, other := range items {
		otherExt := strings.ToLower(path.Ext(other.Name))
		if i != index && other.Video.ParentID == item.Video.ParentID && (otherExt == ".idx" || otherExt == ".sub") && otherExt != ext && strings.EqualFold(strings.TrimSuffix(other.Name, path.Ext(other.Name)), stem) {
			return i
		}
	}
	return -1
}

func (s *ManualOrganize) SubtitleHistory() []SubtitleImport {
	s.mu.Lock()
	defer s.mu.Unlock()
	jobs := make([]SubtitleImport, len(s.doc.SubtitleJobs))
	for i, job := range s.doc.SubtitleJobs {
		jobs[i] = cloneSubtitleImport(job)
	}
	return jobs
}

func (s *ManualOrganize) subtitleCheckpoint(job SubtitleImport) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc := s.doc
	doc.SubtitleJobs = append([]SubtitleImport{}, doc.SubtitleJobs...)
	found := false
	for i := range doc.SubtitleJobs {
		if doc.SubtitleJobs[i].ID == job.ID {
			doc.SubtitleJobs[i], found = cloneSubtitleImport(job), true
			break
		}
	}
	if !found {
		doc.SubtitleJobs = append([]SubtitleImport{cloneSubtitleImport(job)}, doc.SubtitleJobs...)
	}
	if len(doc.SubtitleJobs) > 10 {
		doc.SubtitleJobs = doc.SubtitleJobs[:10]
	}
	return s.saveLocked(doc)
}

func (s *ManualOrganize) ImportSubtitles(ctx context.Context, accountID, previewID string, files map[string][]byte) (SubtitleImport, error) {
	a, err := s.account(accountID)
	if err != nil {
		return SubtitleImport{}, err
	}
	for _, old := range s.SubtitleHistory() {
		if old.ID == previewID && old.AccountID == accountID {
			return old, nil
		}
	}
	if err := s.reserveCleanup(); err != nil {
		return SubtitleImport{}, err
	}
	defer s.releaseCleanup()
	s.mu.Lock()
	plan, ok := s.subtitlePlans[previewID]
	s.mu.Unlock()
	if !ok || plan.AccountID != accountID || plan.Revision != a.Revision || time.Since(plan.CreatedAt) > 10*time.Minute {
		return SubtitleImport{}, fmt.Errorf("%w：字幕预览已过期或账号已变化，请重新预览", ErrOrganizeConflict)
	}
	if len(files) != len(plan.Items) {
		return SubtitleImport{}, ErrOrganizeInput
	}
	for _, item := range plan.Items {
		data, ok := files[item.LocalID]
		hash := sha256.Sum256(data)
		if !ok || int64(len(data)) != item.Size || !strings.EqualFold(hex.EncodeToString(hash[:]), item.SHA256) {
			return SubtitleImport{}, fmt.Errorf("%w：字幕内容与预览不一致，请重新选择文件", ErrOrganizeInput)
		}
	}
	job := cloneSubtitleImport(plan)
	job.Status = "running"
	if err := s.subtitleCheckpoint(job); err != nil {
		return SubtitleImport{}, err
	}
	s.mu.Lock()
	delete(s.subtitlePlans, previewID)
	s.mu.Unlock()
	err = s.runSubtitleImport(ctx, &job, files, a.Cookie)
	job.Status, job.Message = "completed", "字幕导入完成"
	if err != nil {
		job.Status, job.Message = "partial", organizePublicError(err, a.Cookie)
	}
	if saveErr := s.subtitleCheckpoint(job); saveErr != nil {
		s.mu.Lock()
		for i := range s.doc.SubtitleJobs {
			if s.doc.SubtitleJobs[i].ID == job.ID {
				s.doc.SubtitleJobs[i].Status, s.doc.SubtitleJobs[i].Message = "interrupted", "结果保存失败，请核实目标目录"
			}
		}
		s.mu.Unlock()
		return SubtitleImport{}, saveErr
	}
	return job, nil
}

func (s *ManualOrganize) runSubtitleImport(ctx context.Context, job *SubtitleImport, data map[string][]byte, cookie string) error {
	uploader, ok := s.drive.(subtitleUploader)
	if !ok {
		return fmt.Errorf("当前网盘不支持字幕上传")
	}
	for i := range job.Items {
		item := &job.Items[i]
		if item.Status == "skipped" {
			continue
		}
		files, err := s.list(ctx, item.Video.ParentID, cookie)
		if err != nil {
			return err
		}
		if !organizeSourceMatches(files, OrganizeFile{ShareFile: item.Video.ShareFile}, item.Video.Name) {
			return fmt.Errorf("目标视频已移动或改名，已停止，请重新预览")
		}
		if partner := subtitlePartner(job.Items, i); partner >= 0 && job.Items[partner].Status == "pending" && (organizeCollision(files, job.Items[partner].Name, "") || organizeCollision(files, item.Name, "")) {
			item.Status, item.Message = "skipped", "IDX / SUB 组已有同名条目，整组跳过"
			job.Items[partner].Status, job.Items[partner].Message = "skipped", "IDX / SUB 组已有同名条目，整组跳过"
			continue
		}
		if organizeCollision(files, item.Name, "") {
			item.Status, item.Message = "skipped", "已有同名条目，已跳过"
			continue
		}
		item.Status, item.Message = "uncertain", "上传请求即将发送或已发送，结果待核实"
		if err := s.subtitleCheckpoint(*job); err != nil {
			return err
		}
		if err := uploader.UploadSubtitle(ctx, item.Video.ParentID, item.Name, data[item.LocalID], cookie); err != nil {
			return fmt.Errorf("字幕上传结果待核实，已停止后续上传: %w", err)
		}
		files, err = s.list(ctx, item.Video.ParentID, cookie)
		if err != nil {
			return err
		}
		matches := 0
		for _, file := range files {
			if file.Name == item.Name && !file.Directory && file.Size == item.Size {
				matches++
			}
		}
		if matches != 1 {
			return fmt.Errorf("上传后未能唯一核实字幕，已停止后续上传，请刷新目标目录")
		}
		item.Status, item.Message = "done", "已上传并核实"
		if err := s.subtitleCheckpoint(*job); err != nil {
			return err
		}
	}
	return nil
}
