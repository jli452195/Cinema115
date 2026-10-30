package service

import (
	"context"
	"fmt"
	"path"
	"strings"
	"time"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/naming"
)

// buildPlan is shared by actual cloud scans and pre-download website lists.
// It performs metadata/naming validation without touching 115.
func (s *OrganizeEngine) buildPlan(ctx context.Context, scan OrganizeScan, input OrganizePreviewInput) (OrganizeJob, error) {
	if (input.Kind != "movie" && input.Kind != "tv") || input.TMDBID < 1 || len(input.Files) == 0 || len(input.Files) > 1000 {
		return OrganizeJob{}, ErrOrganizeInput
	}
	p, err := (MediaNaming{Templates: s.templates, Metadata: s.metadata}).prepare(ctx, input.Kind, input.TMDBID, input.NamingRevision)
	if err != nil {
		return OrganizeJob{}, err
	}
	detail := p.detail
	if input.NumberingRevision != nil {
		revision := int64(0)
		if detail.Numbering != nil {
			revision = detail.Numbering.Revision
		}
		if *input.NumberingRevision != revision {
			return OrganizeJob{}, fmt.Errorf("%w：季集方案已变化，请重新选择作品并预览", ErrOrganizeConflict)
		}
	}
	job := OrganizeJob{Naming: &p.snapshot, Config: scan.Config, Revision: scan.Revision, Kind: input.Kind, TMDBID: input.TMDBID, Title: detail.Title, Status: "preview", CreatedAt: time.Now(), Items: make([]OrganizeItem, 0, len(input.Files))}
	job.Numbering = detail.Numbering
	files := make(map[string]OrganizeFile, len(scan.Files))
	for _, f := range scan.Files {
		files[f.ID] = f
	}
	seen := make(map[string]bool, len(input.Files))
	for _, selection := range input.Files {
		f, exists := files[selection.ID]
		if !exists || f.MediaType != "video" || seen[f.ID] {
			return OrganizeJob{}, fmt.Errorf("%w：请从扫描结果选择视频，每个文件只能出现一次", ErrOrganizeInput)
		}
		seen[f.ID] = true
		result, err := p.render(MediaNamingFile{OrganizeSelection: selection, Name: f.Name, TitleOnly: f.TitleOnly, NamingValues: f.NamingValues})
		if err != nil {
			return OrganizeJob{}, fmt.Errorf("%w：%s：%w", ErrOrganizeInput, f.Name, err)
		}
		job.Items = append(job.Items, OrganizeItem{File: f, Directory: result.Directory, Stem: result.Stem, Name: result.Name, TargetPath: path.Join(scan.Config.TargetPath, result.RelativePath), Status: "pending"})
	}
	if input.Subtitles {
		videos := make(map[string]OrganizeItem, len(job.Items))
		for _, item := range job.Items {
			videos[item.File.ID] = item
		}
		for _, sub := range organizeSubtitles(scan.Files) {
			video, selected := videos[sub.VideoID]
			if !selected {
				continue
			}
			result, err := naming.Companion(naming.Result{Directory: video.Directory, Stem: video.Stem}, sub.Tail, sub.File.Name)
			if err != nil {
				return OrganizeJob{}, fmt.Errorf("%w：%w", ErrOrganizeInput, err)
			}
			job.Items = append(job.Items, OrganizeItem{File: sub.File, Directory: result.Directory, Stem: result.Stem, Name: result.Name, TargetPath: path.Join(scan.Config.TargetPath, result.RelativePath), Status: "pending"})
		}
	}
	return job, nil
}

func (s *OrganizeEngine) checkPlan(ctx context.Context, job *OrganizeJob, cookie string, checkSource bool) error {
	// Cache complete listings only within this preview. Execution always reads
	// them again before mutating; changes in other tools invalidate the plan.
	cache := make(map[string][]p115.ShareFile)
	listing := func(cid string) ([]p115.ShareFile, error) {
		if files, exists := cache[cid]; exists {
			return files, nil
		}
		files, err := s.list(ctx, cid, cookie)
		if err == nil {
			cache[cid] = files
		}
		return files, err
	}
	targetNames := make(map[string][]int)
	for i := range job.Items {
		item := &job.Items[i]
		targetNames[strings.ToLower(item.TargetPath)] = append(targetNames[strings.ToLower(item.TargetPath)], i)
		cid := job.Config.TargetCID
		for _, part := range strings.Split(item.Directory, "/") {
			if part == "" {
				continue
			}
			children, err := listing(cid)
			if err != nil {
				return err
			}
			next, err := organizeFindDirectory(children, part)
			if err != nil {
				item.Conflict = err.Error()
				break
			}
			cid = next
			if cid == "" {
				break
			}
		}
		if item.Conflict == "" && cid != "" {
			children, err := listing(cid)
			if err != nil {
				return err
			}
			item.TargetCID = cid
			collision := organizeCollision(children, item.Name, item.File.ID)
			if item.File.TitleOnly {
				for _, child := range children {
					collision = collision || !child.Directory && strings.EqualFold(strings.TrimSuffix(child.Name, path.Ext(child.Name)), item.Name)
				}
			}
			if collision {
				item.Conflict = "目标已有同名条目，不能覆盖"
			}
		}
		if checkSource {
			source, err := listing(item.File.ParentID)
			if err != nil {
				return err
			}
			if !organizeSourceMatches(source, item.File, item.File.Name) {
				item.Conflict = "来源文件已变化，请重新扫描"
			}
			if item.Name != item.File.Name && organizeCollision(source, item.Name, item.File.ID) {
				item.Conflict = "来源目录已有目标名称，无法安全重命名"
			}
		}
	}
	for _, indexes := range targetNames {
		if len(indexes) > 1 {
			for _, i := range indexes {
				job.Items[i].Conflict = "本次多个文件的目标同名，请调整自定义标记或季集号"
			}
		}
	}
	return nil
}

func (s *OrganizeEngine) rememberPlan(job OrganizeJob) (OrganizeJob, error) {
	var err error
	job.ID, err = organizeToken()
	if err != nil {
		return OrganizeJob{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked()
	if len(s.plans) >= 8 { // Replace the oldest unsubmitted preview.
		oldest := ""
		var at time.Time
		for id, plan := range s.plans {
			if oldest == "" || plan.CreatedAt.Before(at) {
				oldest, at = id, plan.CreatedAt
			}
		}
		delete(s.plans, oldest)
	}
	s.plans[job.ID] = cloneOrganizeJob(job)
	return job, nil
}

func organizeFindDirectory(files []p115.ShareFile, name string) (string, error) {
	id := ""
	for _, file := range files {
		if strings.EqualFold(file.Name, name) {
			if !file.Directory || id != "" {
				return "", fmt.Errorf("目标目录名称冲突：%s", name)
			}
			id = file.ID
		}
	}
	return id, nil
}

func organizeCollision(files []p115.ShareFile, name, ownID string) bool {
	for _, file := range files {
		if file.ID != ownID && strings.EqualFold(file.Name, name) {
			return true
		}
	}
	return false
}

func organizeSourceMatches(files []p115.ShareFile, source OrganizeFile, name string) bool {
	for _, f := range files {
		if f.ID == source.ID {
			return !f.Directory && f.Name == name && f.Size == source.Size
		}
	}
	return false
}
