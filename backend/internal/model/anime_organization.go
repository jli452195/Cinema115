package model

import "fmt"

// A value-only snapshot. Credentials remain in the task's pinned session.
type AnimeOrganizationSnapshot struct {
	SchemaVersion int                 `json:"schemaVersion"`
	PolicyVersion int                 `json:"policyVersion"`
	PlanRevision  int64               `json:"planRevision"`
	Target        AnimeDownloadTarget `json:"target"`
}

func (s AnimeOrganizationSnapshot) Validate(download AnimeDownloadTarget) error {
	if s.SchemaVersion != 1 || s.PolicyVersion != 1 || s.PlanRevision < 1 || s.PlanRevision > AnimeMaxRevision || s.Target.Validate() != nil || s.Target.AccountID != download.AccountID || s.Target.AccountRevision != download.AccountRevision || s.Target.UID != download.UID || s.Target.ParentCID == download.ParentCID {
		return fmt.Errorf("invalid anime organization snapshot")
	}
	return nil
}

func FreezeAnimeOrganization(naming *AnimeNamingPlan, settings AnimeSettings, revision int64, download AnimeDownloadTarget) (*AnimeOrganizationSnapshot, error) {
	if naming == nil {
		return nil, nil
	}
	if naming.Validate() != nil || settings.AccountID == nil || settings.AccountRevision == nil || settings.UID == nil || settings.OrganizeCID == nil || settings.OrganizePath == nil || settings.DownloadCID == nil || settings.DownloadPath == nil || *settings.DownloadCID != download.ParentCID || *settings.DownloadPath != download.ParentPath || *settings.AccountID != download.AccountID || *settings.AccountRevision != download.AccountRevision || *settings.UID != download.UID {
		return nil, fmt.Errorf("organization target required")
	}
	target := download
	target.SettingsRevision = settings.Revision
	target.ParentCID, target.ParentPath = *settings.OrganizeCID, *settings.OrganizePath
	out := &AnimeOrganizationSnapshot{SchemaVersion: 1, PolicyVersion: 1, PlanRevision: revision, Target: target}
	if err := out.Validate(download); err != nil {
		return nil, err
	}
	return out, nil
}
