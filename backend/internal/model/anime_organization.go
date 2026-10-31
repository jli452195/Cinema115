package model

import (
	"fmt"

	"strm115/backend/internal/naming"
)

// A value-only snapshot. Credentials remain in the task's pinned session.
type AnimeOrganizationSnapshot struct {
	Naming        naming.Snapshot     `json:"naming"`
	SchemaVersion int                 `json:"schemaVersion"`
	PolicyVersion int                 `json:"policyVersion"`
	PlanRevision  int64               `json:"planRevision"`
	Target        AnimeDownloadTarget `json:"target"`
}

func (s AnimeOrganizationSnapshot) Validate(download AnimeDownloadTarget) error {
	if s.SchemaVersion != 1 || s.PolicyVersion != 1 || s.PlanRevision < 1 || s.PlanRevision > AnimeMaxRevision || s.Target.Validate() != nil || s.Target.AccountID != download.AccountID || s.Target.ParentCID == download.ParentCID {
		return fmt.Errorf("invalid anime organization snapshot")
	}
	if s.Naming.Revision < 0 || s.Naming.Revision > AnimeMaxRevision || s.Naming.Template.Kind != naming.TV {
		return fmt.Errorf("invalid anime naming template snapshot")
	}
	if _, err := naming.Compile(s.Naming.Template); err != nil {
		return fmt.Errorf("invalid anime naming template: %w", err)
	}
	return nil
}

func FreezeAnimeOrganization(plan *AnimeNamingPlan, settings AnimeSettings, revision int64, download AnimeDownloadTarget, template naming.Snapshot) (*AnimeOrganizationSnapshot, error) {
	if plan == nil {
		return nil, nil
	}
	if plan.Validate() != nil || settings.AccountID == nil || settings.OrganizeCID == nil || settings.OrganizePath == nil || settings.DownloadCID == nil || settings.DownloadPath == nil || *settings.DownloadCID != download.ParentCID || *settings.DownloadPath != download.ParentPath || *settings.AccountID != download.AccountID {
		return nil, fmt.Errorf("organization target required")
	}
	target := download
	target.SettingsRevision = settings.Revision
	target.ParentCID, target.ParentPath = *settings.OrganizeCID, *settings.OrganizePath
	out := &AnimeOrganizationSnapshot{SchemaVersion: 1, PolicyVersion: 1, PlanRevision: revision, Target: target, Naming: naming.Snapshot{Revision: template.Revision, Template: template.Template.Clone()}}
	if err := out.Validate(download); err != nil {
		return nil, err
	}
	return out, nil
}
