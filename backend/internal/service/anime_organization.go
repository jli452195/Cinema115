package service

import (
	"context"
	"encoding/json"
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

func (s *AnimeAdminService) JobOrganizationPreview(ctx context.Context, id string) (animeorganize.Plan, error) {
	job, err := s.DownloadJob(ctx, id)
	if err != nil {
		return animeorganize.Plan{}, err
	}
	return animeorganize.Build(job)
}
