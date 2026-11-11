package httpapi

import (
	"time"

	"strm115/backend/internal/naming"
	"strm115/backend/internal/service"
)

type mediaOrganizeTaskResponse struct {
	RunID           string                   `json:"runId"`
	FileID          string                   `json:"fileId"`
	Name            string                   `json:"name"`
	Title           string                   `json:"title"`
	Kind            string                   `json:"kind"`
	MediaType       string                   `json:"mediaType"`
	Season          int                      `json:"season"`
	Episode         int                      `json:"episode"`
	EpisodeExplicit bool                     `json:"episodeExplicit,omitempty"`
	Source          string                   `json:"source"`
	AttachmentCount int                      `json:"attachmentCount"`
	CanDelete       bool                     `json:"canDelete"`
	CanRetry        bool                     `json:"canRetry"`
	Status          string                   `json:"status"`
	Message         string                   `json:"message,omitempty"`
	At              time.Time                `json:"at"`
	Target          string                   `json:"target,omitempty"`
	Rule            mediaOrganizeTaskRule    `json:"rule"`
	Group           mediaOrganizeTaskGroup   `json:"group"`
	Entries         []mediaOrganizeTaskEntry `json:"entries"`
}

type mediaOrganizeTaskRule struct {
	Name    string `json:"name"`
	Source  string `json:"source"`
	Failure string `json:"failure,omitempty"`
}

type mediaOrganizeTaskGroup struct {
	ID       string `json:"id"`
	Identity struct {
		Name string `json:"name"`
	} `json:"identity"`
	Kind     string `json:"kind,omitempty"`
	TMDBID   int    `json:"tmdbId,omitempty"`
	Title    string `json:"title,omitempty"`
	Year     string `json:"year,omitempty"`
	Category string `json:"category,omitempty"`
	Message  string `json:"message,omitempty"`
}

type mediaOrganizeTaskEntry struct {
	File       mediaOrganizeTaskFile     `json:"file"`
	VideoID    string                    `json:"videoId,omitempty"`
	Target     string                    `json:"target,omitempty"`
	Status     string                    `json:"status"`
	Message    string                    `json:"message,omitempty"`
	ResultCode string                    `json:"resultCode,omitempty"`
	Failure    *mediaOrganizeTaskFailure `json:"failure,omitempty"`
	P115Move   *mediaOrganizeTaskMove    `json:"p115Move,omitempty"`
}

type mediaOrganizeTaskFile struct {
	ID              string                  `json:"id"`
	Name            string                  `json:"name"`
	SourcePath      string                  `json:"sourcePath"`
	MediaType       string                  `json:"mediaType"`
	Season          int                     `json:"season"`
	Episode         int                     `json:"episode"`
	EpisodeExplicit bool                    `json:"episodeExplicit,omitempty"`
	EndEpisode      int                     `json:"endEpisode,omitempty"`
	NamingValues    map[naming.Field]string `json:"namingValues,omitempty"`
}

type mediaOrganizeTaskFailure struct {
	State   string `json:"state"`
	Message string `json:"message,omitempty"`
}

type mediaOrganizeTaskMove struct {
	Phase string `json:"phase"`
}

// 详情只投影页面展示和修正所需资料，执行身份与恢复意图继续由服务内部保存。
func mediaOrganizeTaskOutput(task service.MediaOrganizeTask) mediaOrganizeTaskResponse {
	out := mediaOrganizeTaskResponse{
		RunID: task.RunID, FileID: task.FileID, Name: task.Name, Title: task.Title,
		Kind: task.Kind, MediaType: task.MediaType, Season: task.Season, Episode: task.Episode,
		EpisodeExplicit: task.EpisodeExplicit,
		Source:          task.Source, AttachmentCount: task.AttachmentCount, CanDelete: task.CanDelete,
		CanRetry: task.CanRetry, Status: task.Status, Message: task.Message, At: task.At, Target: task.Target,
		Rule: mediaOrganizeTaskRule{Name: task.Rule.Name, Source: task.Rule.Source, Failure: task.Rule.Failure},
		Group: mediaOrganizeTaskGroup{
			ID: task.Group.ID, Kind: task.Group.Kind, TMDBID: task.Group.TMDBID, Title: task.Group.Title,
			Year: task.Group.Year, Category: task.Group.Category, Message: task.Group.Message,
		},
		Entries: make([]mediaOrganizeTaskEntry, 0, len(task.Entries)),
	}
	out.Group.Identity.Name = task.Group.Identity.Name
	for _, entry := range task.Entries {
		row := mediaOrganizeTaskEntry{
			File: mediaOrganizeTaskFile{
				ID: entry.File.ID, Name: entry.File.Name, SourcePath: entry.File.SourcePath,
				MediaType: entry.File.MediaType, Season: entry.File.Season, Episode: entry.File.Episode,
				EpisodeExplicit: entry.File.EpisodeExplicit,
				EndEpisode:      entry.File.EndEpisode, NamingValues: entry.File.NamingValues,
			},
			VideoID: entry.VideoID, Target: entry.Target, Status: entry.Status,
			Message: entry.Message, ResultCode: entry.ResultCode,
		}
		if entry.Failure != nil {
			row.Failure = &mediaOrganizeTaskFailure{State: entry.Failure.State, Message: entry.Failure.Message}
		}
		if entry.P115Move != nil {
			row.P115Move = &mediaOrganizeTaskMove{Phase: entry.P115Move.Phase}
		}
		out.Entries = append(out.Entries, row)
	}
	return out
}
