package service

import (
	"context"
	"encoding/json"
	"path"
	"time"

	"strm115/backend/internal/animeorganize"
	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
)

type AnimeOrganizeDirectoryInput struct {
	ExpectedRevision int64   `json:"expectedRevision"`
	CID              *string `json:"cid"`
}

// Changing directory 2 verifies only that directory with the fixed main
// credential. It does not re-read directory 1 or inspect any files.
func (s *AnimeAdminService) SaveOrganizeDirectory(ctx context.Context, actor, key string, in AnimeOrganizeDirectoryInput) (out model.AnimeSettings, resultErr error) {
	k, err := animeRequestKey(actor, "PATCH /api/admin/anime/settings/organize-directory", key, in)
	if err != nil {
		return out, err
	}
	if r, e := s.Store.LookupAnimeReceipt(ctx, k); e != nil {
		return out, e
	} else if r != nil {
		return out, json.Unmarshal(r.Body, &out)
	}
	defer s.replayAnimeConcurrentWrite(ctx, k, &out, &resultErr)
	if in.CID != nil && *in.CID != "0" && !model.ValidAnimeExternalID(*in.CID) {
		return out, storage.ErrAnimeInvalid
	}
	session, err := s.PrepareDownloadTarget(ctx, in.ExpectedRevision)
	if err != nil {
		return out, err
	}
	out, err = s.Store.GetAnimeSettings(ctx)
	if err != nil {
		return out, err
	}
	if out.Revision != in.ExpectedRevision {
		return out, storage.ErrAnimeSettingsChanged
	}
	out.OrganizeCID, out.OrganizePath = nil, nil
	if in.CID != nil {
		if *in.CID == session.target.ParentCID {
			return out, storage.ErrAnimeInvalid
		}
		ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		d, err := s.Directories.ReadDirectory(ctx, *in.CID, session.cookie)
		if err != nil {
			return out, animeDownloadDirectoryError(ctx, err)
		}
		target := session.target
		target.ParentCID, target.ParentPath = d.CID, d.Path
		if d.CID != *in.CID || target.Validate() != nil {
			return out, &AnimeConfigError{Code: "UPSTREAM_INVALID"}
		}
		out.OrganizeCID, out.OrganizePath = &d.CID, &d.Path
	}
	if err := session.Validate(ctx); err != nil {
		return out, err
	}
	return s.Store.SaveAnimeSettings(ctx, out, k)
}

type AnimeOrganizationPreview struct {
	Mode                 string               `json:"mode"`
	Revision             int64                `json:"revision"`
	SettingsRevision     int64                `json:"settingsRevision"`
	TargetPath           string               `json:"targetPath"`
	Items                []animeorganize.Item `json:"items"`
	Example              bool                 `json:"example"`
	CloudConflictChecked bool                 `json:"cloudConflictChecked"`
}

func (s *AnimeAdminService) OrganizationPreview(ctx context.Context, id string) (AnimeOrganizationPreview, error) {
	out := AnimeOrganizationPreview{Mode: "original", Example: true, Items: []animeorganize.Item{}}
	sub, err := s.Store.GetAnimeSubscription(ctx, id, false)
	if err != nil {
		return out, err
	}
	settings, err := s.Store.GetAnimeSettings(ctx)
	if err != nil {
		return out, err
	}
	out.Revision, out.SettingsRevision = sub.Revision, settings.Revision
	if settings.OrganizePath != nil {
		out.TargetPath = *settings.OrganizePath
	}
	if sub.Naming == nil {
		return out, nil
	}
	out.Mode = "standard"
	for _, source := range sub.Sources {
		if source.GroupName == nil || source.Status == "removed" {
			continue
		}
		for _, mapping := range sub.Naming.Sources {
			if mapping.SourceRef != source.ID || len(mapping.Segments) == 0 {
				continue
			}
			n := mapping.Segments[0].First
			p := sub.Naming.Project(source.ID, model.AnimeReleaseParse{Kind: "episode", Episode: &n}, *source.GroupName)
			item := animeorganize.Item{ID: source.ID, Kind: "example", OriginalPath: *source.GroupName, Status: "needs_review", Reason: p.Status}
			if p.Status == "mapped" && out.TargetPath != "" {
				item.TargetPath = path.Join(out.TargetPath, p.PathStem)
				item.Status = "ready"
			} else if p.Status == "mapped" {
				item.Reason = "target_directory_required"
			}
			out.Items = append(out.Items, item)
		}
	}
	return out, nil
}
func (s *AnimeAdminService) JobOrganizationPreview(ctx context.Context, id string) (animeorganize.Plan, error) {
	job, err := s.DownloadJob(ctx, id)
	if err != nil {
		return animeorganize.Plan{}, err
	}
	return animeorganize.Build(job)
}
