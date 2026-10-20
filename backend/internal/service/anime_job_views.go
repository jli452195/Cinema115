package service

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"

	"strm115/backend/internal/animepreview"
	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
)

type AnimeJobFileView struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
	Size string `json:"size"`
}
type AnimeJobView struct {
	ID                  string                  `json:"id"`
	Title               string                  `json:"title"`
	GroupName           string                  `json:"groupName"`
	DownloadState       string                  `json:"downloadState"`
	FileState           string                  `json:"fileState"`
	WorkflowState       string                  `json:"workflowState"`
	OrganizationPlanned bool                    `json:"organizationPlanned"`
	Organization        *AnimeOrganizationView  `json:"organization,omitempty"`
	BlockCode           *string                 `json:"blockCode"`
	Revision            int64                   `json:"revision"`
	ExecutionPath       string                  `json:"executionPath"`
	DirectoryCID        *string                 `json:"directoryCid"`
	CreatedAt           int64                   `json:"createdAt"`
	UpdatedAt           int64                   `json:"updatedAt"`
	ObservedAt          int64                   `json:"observedAt"`
	AttemptedAt         int64                   `json:"attemptedAt"`
	NextRunAt           *int64                  `json:"nextRunAt"`
	Progress            *float64                `json:"progress"`
	Files               []AnimeJobFileView      `json:"files"`
	PermittedActions    storage.AnimeJobActions `json:"permittedActions"`
}

type AnimeOrganizationView struct {
	State      string `json:"state"`
	Total      int    `json:"total"`
	Renamed    int    `json:"renamed"`
	Moved      int    `json:"moved"`
	TargetPath string `json:"targetPath"`
	UpdatedAt  int64  `json:"updatedAt"`
}

func (s *AnimeAdminService) OrganizationAvailable() bool {
	return s.DownloadAvailable() && s.OrganizationGateway != nil
}

type AnimeJobPageView struct {
	Items       []AnimeJobView `json:"items"`
	Total       int            `json:"total"`
	Page        int            `json:"page"`
	PageSize    int            `json:"pageSize"`
	DataVersion string         `json:"dataVersion"`
}

func (s *AnimeAdminService) DownloadAvailable() bool {
	return s != nil && s.Store != nil && s.Previews != nil && s.Accounts != nil && s.Directories != nil && s.Bindings != nil && s.SubmitGateway != nil && s.StatusGateway != nil && s.FileGateway != nil && s.DownloadCoordinator != nil
}

func (s *AnimeAdminService) jobView(ctx context.Context, job model.AnimeDownloadJob) (AnimeJobView, error) {
	v := AnimeJobView{ID: job.ID, Title: job.Snapshot.Release.Title, GroupName: job.Snapshot.Source.GroupName, DownloadState: job.DownloadState, FileState: job.FileState, WorkflowState: job.WorkflowState, BlockCode: job.BlockCode, Revision: job.Revision,
		ExecutionPath: strings.TrimRight(job.Snapshot.Target.ParentPath, "/") + "/" + job.DirectoryName, DirectoryCID: job.DirectoryCID, CreatedAt: job.CreatedAt, UpdatedAt: job.UpdatedAt, NextRunAt: job.NextRunAt, Files: []AnimeJobFileView{}}
	if job.Tracking != nil {
		v.ObservedAt, v.AttemptedAt = job.Tracking.ObservedAt, job.Tracking.AttemptedAt
		if job.Tracking.Observation != nil {
			v.Progress = job.Tracking.Observation.Progress
		}
	}
	v.OrganizationPlanned = job.Snapshot.Organization != nil
	if v.OrganizationPlanned {
		v.Organization = &AnimeOrganizationView{State: "pending", TargetPath: job.Snapshot.Organization.Target.ParentPath}
		op, err := s.Store.GetAnimeOrganization(ctx, job.ID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return v, err
		}
		if err == nil {
			v.Organization.State, v.Organization.Total, v.Organization.UpdatedAt = op.Progress.State, len(op.Progress.Files), op.UpdatedAt
			for _, f := range op.Progress.Files {
				if f.Renamed {
					v.Organization.Renamed++
				}
				if f.Moved {
					v.Organization.Moved++
				}
			}
		}
	}
	if job.Files != nil {
		for _, file := range job.Files.Entries {
			v.Files = append(v.Files, AnimeJobFileView{ID: file.ID, Name: file.Name, Kind: file.Kind, Size: strconv.FormatInt(file.Size, 10)})
		}
	}
	actions, err := s.Store.AnimeDownloadJobActions(ctx, job)
	v.PermittedActions = actions
	return v, err
}
func (s *AnimeAdminService) JobPageView(ctx context.Context, id string, page, size int) (AnimeJobPageView, error) {
	raw, err := s.SubscriptionJobs(ctx, id, page, size)
	out := AnimeJobPageView{Items: make([]AnimeJobView, 0, len(raw.Items)), Total: raw.Total, Page: raw.Page, PageSize: raw.PageSize}
	if err != nil {
		return out, err
	}
	for _, job := range raw.Items {
		v, err := s.jobView(ctx, job)
		if err != nil {
			return out, err
		}
		out.Items = append(out.Items, v)
	}
	out.DataVersion, err = animepreview.Digest(out)
	return out, err
}
func (s *AnimeAdminService) JobView(ctx context.Context, id string) (AnimeJobView, error) {
	job, err := s.DownloadJob(ctx, id)
	if err != nil {
		return AnimeJobView{}, err
	}
	return s.jobView(ctx, job)
}
func (s *AnimeAdminService) JobAction(ctx context.Context, actor, key, id, action string, in AnimeControlInput) (out storage.AnimeJobActionReceipt, resultErr error) {
	k, err := animeRequestKey(actor, "POST /api/admin/anime/jobs/"+id+"/"+action, key, in)
	if err != nil {
		return out, err
	}
	defer s.replayAnimeConcurrentWrite(ctx, k, &out, &resultErr)
	defer func() {
		if resultErr == nil {
			s.wakeDownloads()
		}
	}()
	return s.Store.ScheduleAnimeJobAction(ctx, id, action, in.ExpectedRevision, k)
}
