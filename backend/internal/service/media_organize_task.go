package service

import (
	"context"
	"fmt"
	"slices"
	"time"

	"strm115/backend/internal/naming"
)

type MediaOrganizeCorrection struct {
	FileID  string `json:"fileId"`
	Kind    string `json:"kind,omitempty"`
	TMDBID  int    `json:"tmdbId,omitempty"`
	Season  *int   `json:"season,omitempty"`
	Episode *int   `json:"episode,omitempty"`
}

// Retry changes only one media task; the selection survives checkpoints/restarts.
func (s *MediaOrganizer) Retry(id string, input MediaOrganizeCorrection) (MediaOrganizeTask, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return s.RetryContext(ctx, id, input)
}

func (s *MediaOrganizer) RetryContext(ctx context.Context, id string, input MediaOrganizeCorrection) (MediaOrganizeTask, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if input.FileID == "" || input.TMDBID < 0 || input.TMDBID > 0 && input.Kind != "movie" && input.Kind != "tv" ||
		input.Season != nil && (*input.Season < 0 || *input.Season > 1000) ||
		input.Episode != nil && (*input.Episode < 0 || *input.Episode > 10000) {
		return MediaOrganizeTask{}, ErrMediaOrganizeInput
	}
	defer s.releaseHistoryLocked()
	if err := s.loadHistoryLocked(ctx, []string{id}, "", ""); err != nil {
		return MediaOrganizeTask{}, err
	}
	for i := range s.doc.Runs {
		run := &s.doc.Runs[i]
		if run.ID != id {
			continue
		}
		if s.busyRunLocked(id) || mediaRunActive(run.Status) {
			return MediaOrganizeTask{}, ErrMediaOrganizeBusy
		}
		pos := slices.IndexFunc(run.Entries, func(e MediaOrganizeEntry) bool { return e.File.MediaType == "video" && e.File.ID == input.FileID })
		if pos < 0 {
			return MediaOrganizeTask{}, ErrMediaOrganizeInput
		}
		video := &run.Entries[pos]
		unfinished := false
		for _, e := range run.Entries {
			if mediaInTask(e, input.FileID) && e.Status != "done" && e.Status != "retained" {
				unfinished = true
			}
		}
		if !unfinished {
			return MediaOrganizeTask{}, fmt.Errorf("%w：当前任务已完成", ErrMediaOrganizeInput)
		}
		if video.Status == "done" && (input.TMDBID > 0 || input.Season != nil || input.Episode != nil) {
			return MediaOrganizeTask{}, fmt.Errorf("%w：已完成的主文件不能修改，仅可重试失败附件", ErrMediaOrganizeInput)
		}
		if input.TMDBID > 0 || input.Season != nil || input.Episode != nil {
			for _, entry := range run.Entries {
				if !mediaInTask(entry, input.FileID) {
					continue
				}
				if entry.P115Move != nil {
					return MediaOrganizeTask{}, fmt.Errorf("%w：文件写入结果待核实，请先直接重试，核实后再修改作品或季集", ErrMediaOrganizeInput)
				}
			}
		}
		if err := run.prepareClassification(); err != nil {
			return MediaOrganizeTask{}, err
		}
		// 附件按当前文件事实重新判断，不重放上次尝试的回收选择。
		for j := range run.Entries {
			if mediaInTask(run.Entries[j], input.FileID) {
				run.Entries[j].AttachmentKeep = nil
			}
		}
		if video.Status != "done" {
			old := slices.IndexFunc(run.Groups, func(g MediaOrganizeGroup) bool { return g.ID == video.Group })
			if old < 0 {
				return MediaOrganizeTask{}, ErrMediaOrganizeInput
			}
			// 重试时补齐旧记录缺失的分辨率，保留已有命名标签及目录继承信息。
			if video.P115Move == nil && video.File.NamingValues[naming.Resolution] == "" {
				values := mediaFileValues(video.File.Name, DownloadFileSpec{})
				if resolution := values[naming.Resolution]; resolution != "" {
					if video.File.NamingValues == nil {
						video.File.NamingValues = values
					} else {
						video.File.NamingValues[naming.Resolution] = resolution
					}
				}
			}
			// 重试仅补解析缺失集数；未核实写入仍使用原计划，已有季数和人工修正保持不变。
			if video.P115Move == nil && video.File.Episode == 0 && !video.File.EpisodeExplicit && input.Episode == nil {
				inferred := video.File
				mediaOrganizeFileNumbering(&inferred)
				if mediaOrganizeHasNumbering(inferred) {
					video.File.Episode, video.File.EndEpisode = inferred.Episode, inferred.EndEpisode
					video.File.EpisodeExplicit = inferred.EpisodeExplicit
				}
			}
			group := run.Groups[old]
			// 未匹配的旧失败重试时，按当前词表重建片名；已选作品和未确认写入保持原计划。
			if group.TMDBID == 0 && group.Identity.Kind != "movie" && group.Identity.Conflict == "" && video.P115Move == nil &&
				(video.Failure == nil || video.Failure.State != "moving") && mediaIdentityDescriptionTag.MatchString(video.File.Name) {
				identities, contexts := mediaScanIdentities(run, run.Entries)
				inferred := video.File
				identity := identities[inferred.ID]
				if mediaOrganizeHasNumbering(inferred) {
					identity = mediaResolveEpisodeIdentity(identity, &inferred, contexts)
				}
				if identity.Name != group.Identity.Name && identity.Conflict == "" {
					group.Identity.Name, group.Identity.NameSource = identity.Name, identity.NameSource
					group.Identity.Kind, group.Identity.KindSource = identity.Kind, identity.KindSource
					if !video.File.EpisodeExplicit && input.Episode == nil {
						video.File.Episode, video.File.EndEpisode = inferred.Episode, inferred.EndEpisode
					}
				}
			}
			group.ID = "task:" + input.FileID
			if input.Season != nil {
				group.Identity.Season, group.Identity.SeasonExplicit = *input.Season, true
				group.MatchedSeason = mediaOptionalNumber(input.Season)
			}
			if input.TMDBID > 0 {
				group.Kind, group.TMDBID = input.Kind, input.TMDBID
				group.Title, group.Year, group.Message = "", "", ""
				group.ResultCode = "MATCHED"
			}
			gi := slices.IndexFunc(run.Groups, func(g MediaOrganizeGroup) bool { return g.ID == group.ID })
			if gi < 0 {
				run.Groups = append(run.Groups, group)
			} else {
				run.Groups[gi] = group
			}
			for j := range run.Entries {
				if mediaInTask(run.Entries[j], input.FileID) {
					run.Entries[j].Group = group.ID
				}
			}
		}
		if input.Season != nil {
			video.File.Season = *input.Season
		}
		if input.Episode != nil {
			video.File.Episode = *input.Episode
			video.File.EpisodeExplicit = true
		}
		if input.Episode != nil {
			video.File.EndEpisode = 0
		}
		if video.Status != "done" {
			video.Result, video.ResultCode = nil, ""
			video.UpdatedAt = time.Now()
		}
		run.TaskFileID, run.Status = input.FileID, "queued"
		run.UpdatedAt = s.localMonitorSettings().now()
		run.Message = "等待重新整理当前任务"
		if err := s.persistLocked(run.ID); err != nil {
			return MediaOrganizeTask{}, err
		}
		s.enqueueActionLocked(id, "retry")
		s.notify()
		var snapshot MediaOrganizeRun
		cloneMediaRun(*run, &snapshot)
		for _, task := range mediaTasks(snapshot) {
			if task.FileID == input.FileID {
				return task, nil
			}
		}
	}
	return MediaOrganizeTask{}, ErrMediaOrganizeInput
}
