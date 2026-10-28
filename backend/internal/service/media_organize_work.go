package service

import (
	"context"
	"errors"
	"fmt"
	"path"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"strm115/backend/internal/mediafacts"
	"strm115/backend/internal/naming"
)

func (s *MediaOrganizer) process(ctx context.Context, run *MediaOrganizeRun, action string) error {
	files, err := s.files(ctx, run.Rule)
	if err != nil {
		return err
	}
	if action == "scan" || action == "auto" {
		run.Status = "scanning"
		if err = s.checkpoint(run); err != nil {
			return err
		}
		entries, err := files.scan(ctx)
		if err != nil {
			return err
		}
		s.mu.Lock()
		seen := make(map[string]string, len(s.doc.Seen[run.Rule.ID]))
		for _, e := range entries {
			if value, ok := s.doc.Seen[run.Rule.ID][e.File.ID]; ok && value == e.Fingerprint {
				seen[e.File.ID] = value
			}
		}
		s.doc.Seen[run.Rule.ID] = seen
		s.mu.Unlock()
		indices := make(map[string]int)
		for _, e := range entries {
			if action == "auto" && seen[e.File.ID] == e.Fingerprint {
				continue
			}
			if e.File.MediaType != "video" {
				run.Entries = append(run.Entries, e)
				continue
			}
			parents := strings.Split(path.Dir(e.File.SourcePath), "/")
			reverse := make([]string, 0, 2)
			for i := len(parents) - 1; i >= 0 && len(reverse) < 2; i-- {
				if parents[i] != "." && parents[i] != "" {
					reverse = append(reverse, parents[i])
				}
			}
			identity := mediafacts.ParseIdentity(e.File.Name, reverse...)
			if e.File.Episode > 0 {
				identity.Kind = "tv"
				if e.File.Season != 0 && !organizeFileSeason.MatchString(e.File.Name) {
					e.File.Season = identity.Season
				}
			}
			key := mediafacts.NameKey(identity.Name) + ":" + identity.Year + ":" + identity.Kind + ":" + strconv.Itoa(identity.TMDBID) + ":" + strconv.Itoa(identity.Season)
			if identity.Name == "" && identity.TMDBID == 0 {
				key = e.File.ParentID + ":" + key
			}
			if _, ok := indices[key]; !ok {
				indices[key] = len(run.Groups)
				run.Groups = append(run.Groups, MediaOrganizeGroup{ID: strconv.Itoa(len(run.Groups) + 1), Identity: identity})
			}
			e.Group = run.Groups[indices[key]].ID
			run.Entries = append(run.Entries, e)
		}
		for i := range run.Groups {
			if err := ctx.Err(); err != nil {
				return err
			}
			g := &run.Groups[i]
			lookup, cancel := context.WithTimeout(ctx, 25*time.Second)
			detail, err := recognizeMedia(lookup, s.metadata, g.Identity)
			cancel()
			if err != nil {
				g.Message = "查询暂未完成：" + err.Error()
				continue
			}
			if detail == nil {
				g.Message = "未找到唯一匹配，请选择作品"
				continue
			}
			g.Kind, g.TMDBID, g.Title = detail.Kind, detail.ID, detail.Title
		}
		if err = s.render(ctx, files, run); err != nil {
			return err
		}
		s.mu.Lock()
		for i := range s.doc.Rules {
			if s.doc.Rules[i].ID == run.Rule.ID {
				s.doc.Rules[i].LastScan = time.Now()
			}
		}
		s.mu.Unlock()
		if action == "scan" {
			run.Status = "preview"
			run.Message = "请核对作品和目标路径，再确认整理"
			if !slices.ContainsFunc(run.Entries, func(e MediaOrganizeEntry) bool { return e.Status != "retained" }) {
				run.Status, run.Message = "completed", "没有新的可处理文件；新写入文件稳定 30 秒后再扫描"
			}
			return s.checkpoint(run)
		}
		mediaDiscardOrphanSidecars(files, run, entries)
	}
	if err = s.execute(ctx, files, run); err != nil {
		return err
	}
	if action == "auto" && len(files.unrelated)+len(files.directories) > 0 {
		status, message, count := run.Status, run.Message, len(run.Entries)
		run.Status = "running"
		if err = s.cleanup(ctx, files, run); err != nil {
			return err
		}
		run.Status, run.Message = status, message
		cleaned := 0
		for _, e := range run.Entries[count:] {
			if e.Status == "done" {
				cleaned++
			}
		}
		if cleaned > 0 {
			run.Message = fmt.Sprintf("已清理 %d 项（无关文件、未归属字幕与附件及空文件夹）", cleaned)
			if count > 0 {
				run.Message = message + "；" + run.Message
			}
		}
		for _, e := range run.Entries[count:] {
			if e.Result != nil && e.Result.Status == "error" {
				run.Status = "partial"
			}
		}
	}
	s.mu.Lock()
	if s.doc.Seen[run.Rule.ID] == nil {
		s.doc.Seen[run.Rule.ID] = make(map[string]string)
	}
	transient := mediaTransientGroups(run)
	for _, e := range run.Entries {
		if e.File.MediaType == "cleanup" {
			continue
		}
		if e.Status == "done" || e.Status == "conflict" || e.Status == "invalid" || e.Status == "needs_match" {
			// Upstream errors are transient, not negative recognition results.
			if !transient[e.Group] {
				s.doc.Seen[run.Rule.ID][e.File.ID] = e.Fingerprint
			}
		}
	}
	s.mu.Unlock()
	return s.checkpoint(run)
}

func (s *MediaOrganizer) render(ctx context.Context, files *mediaOrganizeFiles, run *MediaOrganizeRun) error {
	prepared := make(map[string]preparedMediaNaming)
	for i := range run.Groups {
		g := &run.Groups[i]
		if g.TMDBID < 1 {
			continue
		}
		p, err := (MediaNaming{Templates: s.templates, Metadata: s.metadata}).prepare(ctx, g.Kind, g.TMDBID, nil)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			g.Message = "查询暂未完成：" + err.Error()
			continue
		}
		prepared[g.ID] = p
		g.Title = p.detail.Title
		g.Category = ""
		g.Message = ""
		if run.Rule.Classify {
			g.Category = mediaCategory(p.detail)
		}
	}
	results := make(map[string]naming.Result)
	for i := range run.Entries {
		e := &run.Entries[i]
		if e.File.MediaType == "cleanup" {
			continue
		}
		if e.File.MediaType != "video" && e.Status != "done" {
			e.Group, e.VideoID, e.Relative, e.Target = "", "", "", ""
			e.Status, e.Message = "unmatched", "未找到唯一对应的视频，保留来源"
		}
		if e.Status == "done" && e.File.MediaType == "video" {
			name := path.Base(e.Relative)
			results[e.File.ID] = naming.Result{Directory: path.Dir(e.Relative), Name: name, Stem: strings.TrimSuffix(name, path.Ext(name)), Extension: path.Ext(name), RelativePath: e.Relative}
			continue
		}
		if e.File.MediaType != "video" {
			continue
		}
		e.Relative, e.Target, e.Message = "", "", ""
		p, ok := prepared[e.Group]
		if !ok {
			e.Status = "needs_match"
			e.Message = "请选择对应作品"
			continue
		}
		result, err := p.render(MediaNamingFile{OrganizeSelection: OrganizeSelection{ID: e.File.ID, Season: e.File.Season, Episode: e.File.Episode}, Name: e.File.Name, NamingValues: e.File.NamingValues})
		if err != nil {
			e.Status = "invalid"
			e.Message = err.Error()
			continue
		}
		for _, g := range run.Groups {
			if g.ID == e.Group && g.Category != "" {
				result.Directory = path.Join(g.Category, result.Directory)
				result.RelativePath = path.Join(g.Category, result.RelativePath)
				break
			}
		}
		results[e.File.ID] = result
		e.Relative = result.RelativePath
		e.Target = path.Join(run.Rule.Target, e.Relative)
		e.Status = "ready"
	}
	original := make([]OrganizeFile, 0, len(run.Entries))
	positions := make(map[string]int, len(run.Entries))
	for i, e := range run.Entries {
		original = append(original, e.File)
		positions[e.File.ID] = i
	}
	for _, sub := range organizeSubtitles(original) {
		i := positions[sub.File.ID]
		e := &run.Entries[i]
		if e.Status == "done" {
			continue
		}
		video, ok := results[sub.VideoID]
		if sub.VideoID != "" {
			e.Group = run.Entries[positions[sub.VideoID]].Group
			e.VideoID = sub.VideoID
		}
		if !ok {
			continue
		}
		result, err := naming.Companion(video, sub.Tail, sub.File.Name)
		if err != nil {
			e.Status = "invalid"
			e.Message = err.Error()
			continue
		}
		e.Group = run.Entries[positions[sub.VideoID]].Group
		e.Relative = result.RelativePath
		e.Target = path.Join(run.Rule.Target, e.Relative)
		e.Status = "ready"
		e.Message = ""
	}
	mediaOrganizeAttachments(run, results)
	duplicates := make(map[string][]int)
	for i, e := range run.Entries {
		if e.Status != "ready" {
			continue
		}
		key := strings.ToLower(e.Relative)
		duplicates[key] = append(duplicates[key], i)
	}
	for _, indices := range duplicates {
		if len(indices) > 1 {
			for _, i := range indices {
				run.Entries[i].Status = "conflict"
				run.Entries[i].Message = "本批次存在相同目标路径，请修正作品或季集"
			}
		}
	}
	for i := range run.Entries {
		e := &run.Entries[i]
		if e.Status != "ready" {
			continue
		}
		if err := files.conflict(ctx, *e); err != nil {
			e.Status = "error"
			if errors.Is(err, errMediaTargetExists) {
				e.Status = "conflict"
			}
			e.Message = err.Error()
		}
	}
	sort.SliceStable(run.Entries, func(i, j int) bool {
		if run.Entries[i].File.MediaType != run.Entries[j].File.MediaType {
			return run.Entries[i].File.MediaType == "video"
		}
		return run.Entries[i].File.SourcePath < run.Entries[j].File.SourcePath
	})
	return nil
}

func (s *MediaOrganizer) execute(ctx context.Context, files *mediaOrganizeFiles, run *MediaOrganizeRun) error {
	mediaValidateAttachmentOwners(run, mediaEntryFiles(run.Entries))
	run.Status = "running"
	if err := s.checkpoint(run); err != nil {
		return err
	}
	if err := s.quarantine(ctx, files, run, true); err != nil {
		return err
	}
	failures := mediaFailureEntries(run)
	// A short reservation protects 115 directory writes without reserving cloud
	// work while scanning, waiting for TMDB, or processing local files.
	for i := range run.Entries {
		e := &run.Entries[i]
		if failures[e.File.ID] {
			continue
		}
		if e.Status != "ready" && e.Status != "error" {
			continue
		}
		if e.Relative == "" {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if e.VideoID != "" {
			videoDone := false
			for _, video := range run.Entries {
				if video.File.ID == e.VideoID {
					videoDone = video.Status == "done"
					break
				}
			}
			if !videoDone {
				e.Status, e.Message = "error", "对应视频尚未整理成功，关联文件保留来源"
				e.Result = &MediaOrganizeResult{Status: e.Status, Message: e.Message, At: time.Now()}
				continue
			}
		}
		var release func()
		var err error
		if run.Rule.Storage == "115" && s.reserve != nil {
			release, err = s.reserve()
			if err != nil {
				e.Status = "error"
				e.Message = "115 目录忙，请稍后继续"
				e.Result = &MediaOrganizeResult{Status: e.Status, Message: e.Message, At: time.Now()}
				continue
			}
		}
		err = files.move(ctx, *e)
		if release != nil {
			release()
		}
		if err != nil {
			e.Status = "error"
			if errors.Is(err, errMediaTargetExists) {
				e.Status = "conflict"
			}
			e.Message = organizePublicError(err, files.cookie)
		} else {
			e.Status = "done"
			e.Message = ""
			e.Failure = nil
		}
		e.Result = &MediaOrganizeResult{Status: e.Status, Message: e.Message, At: time.Now(), Target: e.Target}
		if err := s.checkpoint(run); err != nil {
			return err
		}
	}
	if err := s.quarantine(ctx, files, run, false); err != nil {
		return err
	}
	run.Status = "completed"
	run.Message = "整理完成"
	for _, e := range run.Entries {
		if e.Status != "done" && e.Status != "retained" {
			run.Status = "partial"
			run.Message = "已处理可整理文件，其余条目请核对"
			break
		}
	}
	if len(run.Entries) == 0 {
		run.Message = "没有新的可处理文件；新写入文件稳定 30 秒后再扫描"
	}
	return nil
}

type MediaOrganizeCorrection struct {
	Groups []struct {
		ID     string `json:"id"`
		Kind   string `json:"kind"`
		TMDBID int    `json:"tmdbId"`
	} `json:"groups"`
	Files []OrganizeSelection `json:"files"`
}

func (s *MediaOrganizer) Correct(ctx context.Context, id string, input MediaOrganizeCorrection) (MediaOrganizeRun, error) {
	s.mu.Lock()
	if s.active != "" || len(s.actions) > 0 {
		s.mu.Unlock()
		return MediaOrganizeRun{}, ErrMediaOrganizeBusy
	}
	var run MediaOrganizeRun
	for _, r := range s.doc.Runs {
		if r.ID == id {
			cloneMediaRun(r, &run)
			break
		}
	}
	if run.ID == "" {
		s.mu.Unlock()
		return run, ErrMediaOrganizeInput
	}
	s.active = id
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.active = ""; s.mu.Unlock(); s.notify() }()
	for _, change := range input.Groups {
		if change.TMDBID < 1 || (change.Kind != "movie" && change.Kind != "tv") {
			return run, ErrMediaOrganizeInput
		}
		for i := range run.Groups {
			if run.Groups[i].ID == change.ID {
				for _, e := range run.Entries {
					if e.Group == change.ID && e.Status == "done" && (run.Groups[i].TMDBID != change.TMDBID || run.Groups[i].Kind != change.Kind) {
						return run, fmt.Errorf("%w：已整理作品不能更换，请重新扫描来源目录", ErrMediaOrganizeInput)
					}
				}
				run.Groups[i].TMDBID = change.TMDBID
				run.Groups[i].Kind = change.Kind
			}
		}
	}
	for _, change := range input.Files {
		if change.Season < 0 || change.Season > 1000 || change.Episode < 0 || change.Episode > 10000 {
			return run, ErrMediaOrganizeInput
		}
		for i := range run.Entries {
			if run.Entries[i].File.ID == change.ID && run.Entries[i].Status != "done" {
				run.Entries[i].File.Season, run.Entries[i].File.Episode = change.Season, change.Episode
			}
		}
	}
	f, err := s.files(ctx, run.Rule)
	if err != nil {
		return run, err
	}
	if err = s.render(ctx, f, &run); err != nil {
		return run, err
	}
	run.Status = "preview"
	run.Message = "预览已更新"
	return run, s.checkpoint(&run)
}

func (s *MediaOrganizer) Apply(id string) (MediaOrganizeRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active != "" || len(s.actions) > 0 {
		return MediaOrganizeRun{}, ErrMediaOrganizeBusy
	}
	for i := range s.doc.Runs {
		r := &s.doc.Runs[i]
		if r.ID != id {
			continue
		}
		mediaRunCounts(r)
		if r.ReadyCount == 0 && (r.Status == "preview" || r.FailureCount == 0) {
			return MediaOrganizeRun{}, fmt.Errorf("%w：没有可执行条目，请先完成匹配", ErrMediaOrganizeInput)
		}
		r.Status = "queued"
		if err := s.persistLocked(); err != nil {
			return MediaOrganizeRun{}, err
		}
		s.actions[id] = "execute"
		s.notify()
		var copy MediaOrganizeRun
		cloneMediaRun(*r, &copy)
		return copy, nil
	}
	return MediaOrganizeRun{}, ErrMediaOrganizeInput
}
