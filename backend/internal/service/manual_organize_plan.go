package service

import (
	"context"
	"fmt"
	"path"
	"strings"
	"time"

	"strm115/backend/internal/client/p115"
)

func (s *ManualOrganize) Preview(ctx context.Context, accountID string, input OrganizePreviewInput) (OrganizeJob, error) {
	if (input.Kind != "movie" && input.Kind != "tv") || input.TMDBID < 1 || len(input.Files) == 0 || len(input.Files) > 1000 {
		return OrganizeJob{}, ErrOrganizeInput
	}
	s.mu.Lock()
	s.pruneLocked()
	scan, ok := s.scans[input.ScanID]
	s.mu.Unlock()
	if !ok || scan.Config.AccountID != accountID {
		return OrganizeJob{}, fmt.Errorf("%w：扫描已过期，请重新选择文件", ErrOrganizeConflict)
	}
	a, err := s.account(accountID)
	if err != nil {
		return OrganizeJob{}, err
	}
	if a.Revision != scan.Revision {
		return OrganizeJob{}, fmt.Errorf("%w：账号配置已修改，请重新扫描", ErrOrganizeConflict)
	}
	detail, err := s.metadata.Details(ctx, input.Kind, input.TMDBID)
	if err != nil {
		return OrganizeJob{}, fmt.Errorf("读取 TMDB 作品: %w", err)
	}
	if detail == nil || detail.ID != input.TMDBID || detail.Kind != input.Kind || detail.Title == "" {
		return OrganizeJob{}, fmt.Errorf("TMDB 返回了无效的作品信息")
	}
	title := organizeName(detail.Title)
	base := title
	if len(detail.Date) >= 4 {
		base += " (" + detail.Date[:4] + ")"
	}
	folder := fmt.Sprintf("%s {tmdb-%d}", base, input.TMDBID)
	if title == "" || len([]rune(folder)) > 180 {
		return OrganizeJob{}, fmt.Errorf("%w：作品名称过长或为空", ErrOrganizeInput)
	}
	job := OrganizeJob{Config: scan.Config, Revision: a.Revision, Kind: input.Kind, TMDBID: input.TMDBID, Title: detail.Title, Folder: folder, Status: "preview", CreatedAt: time.Now(), Items: []OrganizeItem{}}
	files := make(map[string]OrganizeFile, len(scan.Files))
	for _, f := range scan.Files {
		files[f.ID] = f
	}
	seen := make(map[string]bool, len(input.Files))
	episodes := make(map[int]map[int]bool)
	for _, selection := range input.Files {
		f, exists := files[selection.ID]
		if !exists || f.MediaType != "video" || seen[f.ID] {
			return OrganizeJob{}, fmt.Errorf("%w：请从扫描结果选择视频，每个文件只能出现一次", ErrOrganizeInput)
		}
		seen[f.ID] = true
		name, directory := base, folder
		if input.Kind == "tv" {
			if selection.Season < 0 || selection.Season > 1000 || selection.Episode < 1 || selection.Episode > 10000 || (selection.EndEpisode != 0 && (selection.EndEpisode < selection.Episode || selection.EndEpisode > 10000)) {
				return OrganizeJob{}, fmt.Errorf("%w：请为每个视频填写有效季集号", ErrOrganizeInput)
			}
			if episodes[selection.Season] == nil {
				season, err := s.metadata.Season(ctx, input.TMDBID, selection.Season)
				if err != nil {
					return OrganizeJob{}, fmt.Errorf("读取 TMDB 季信息: %w", err)
				}
				if season == nil {
					return OrganizeJob{}, fmt.Errorf("TMDB 未返回季信息")
				}
				episodes[selection.Season] = make(map[int]bool, len(season.Episodes))
				for _, ep := range season.Episodes {
					episodes[selection.Season][ep.Number] = true
				}
			}
			end := selection.EndEpisode
			if end == 0 {
				end = selection.Episode
			}
			for ep := selection.Episode; ep <= end; ep++ {
				if !episodes[selection.Season][ep] {
					return OrganizeJob{}, fmt.Errorf("%w：TMDB 中不存在 S%02dE%02d，请校正季集", ErrOrganizeInput, selection.Season, ep)
				}
			}
			name = fmt.Sprintf("%s - S%02dE%02d", title, selection.Season, selection.Episode)
			if end > selection.Episode {
				name += fmt.Sprintf("-E%02d", end)
			}
			directory = path.Join(folder, fmt.Sprintf("Season %02d", selection.Season))
		}
		if suffix := organizeName(strings.TrimSpace(selection.Suffix)); suffix != "" {
			name += " - " + suffix
		}
		name += strings.ToLower(path.Ext(f.Name))
		if len([]rune(name)) > 200 {
			return OrganizeJob{}, fmt.Errorf("%w：目标文件名过长，请缩短版本标记", ErrOrganizeInput)
		}
		job.Items = append(job.Items, OrganizeItem{File: f, Directory: directory, Name: name, TargetPath: path.Join(scan.Config.TargetPath, directory, name), Status: "pending"})
	}
	if input.Subtitles {
		videos := append([]OrganizeItem{}, job.Items...)
		for _, sub := range scan.Files {
			if sub.MediaType != "subtitle" {
				continue
			}
			// Match against all scanned videos so excluding a video cannot cause
			// its subtitles to be attached to another, shorter prefix.
			bestID, tail := organizeSubtitleOwner(sub, scan.Files)
			for _, video := range videos {
				if video.File.ID != bestID {
					continue
				}
				name := strings.TrimSuffix(video.Name, path.Ext(video.Name))
				if tail != "" {
					name += "." + organizeName(tail)
				}
				name += strings.ToLower(path.Ext(sub.Name))
				if len([]rune(name)) > 200 {
					return OrganizeJob{}, fmt.Errorf("%w：字幕文件名过长", ErrOrganizeInput)
				}
				job.Items = append(job.Items, OrganizeItem{File: sub, Directory: video.Directory, Name: name, TargetPath: path.Join(scan.Config.TargetPath, video.Directory, name), Status: "pending"})
			}
		}
	}
	// Cache complete listings only within this preview. Execution always reads
	// them again before mutating; changes in other tools invalidate the plan.
	cache := make(map[string][]p115.ShareFile)
	listing := func(cid string) ([]p115.ShareFile, error) {
		if files, exists := cache[cid]; exists {
			return files, nil
		}
		files, err := s.list(ctx, cid, a.Cookie)
		if err == nil {
			cache[cid] = files
		}
		return files, err
	}
	targetNames := make(map[string][]int)
	for i := range job.Items {
		item := &job.Items[i]
		targetNames[strings.ToLower(item.TargetPath)] = append(targetNames[strings.ToLower(item.TargetPath)], i)
		cid := scan.Config.TargetCID
		for _, part := range strings.Split(item.Directory, "/") {
			children, err := listing(cid)
			if err != nil {
				return OrganizeJob{}, err
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
				return OrganizeJob{}, err
			}
			item.TargetCID = cid
			if organizeCollision(children, item.Name, item.File.ID) {
				item.Conflict = "目标已有同名条目，不能覆盖"
			}
		}
		source, err := listing(item.File.ParentID)
		if err != nil {
			return OrganizeJob{}, err
		}
		if !organizeSourceMatches(source, item.File, item.File.Name) {
			item.Conflict = "来源文件已变化，请重新扫描"
		}
		if item.Name != item.File.Name && organizeCollision(source, item.Name, item.File.ID) {
			item.Conflict = "来源目录已有目标名称，无法安全重命名"
		}
	}
	for _, indexes := range targetNames {
		if len(indexes) > 1 {
			for _, i := range indexes {
				job.Items[i].Conflict = "本次多个文件的目标同名，请调整版本标记或季集号"
			}
		}
	}
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
	s.plans[job.ID] = job
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
