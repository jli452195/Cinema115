package model

import (
	"strm115/backend/internal/naming"
	"testing"
)

func organizationPtr[T any](v T) *T { return &v }

func TestAnimeOrganizationFreezesDefaultTargetWithoutPreference(t *testing.T) {
	n := namingPlan()
	template, _ := naming.Default(naming.TV)
	snapshot := naming.Snapshot{Revision: 4, Template: template}
	download := AnimeDownloadTarget{SchemaVersion: 1, SettingsRevision: 2, AccountID: "main", ParentCID: "100", ParentPath: "/下载"}
	s := AnimeSettings{Revision: 3, AccountID: &download.AccountID, DownloadCID: &download.ParentCID, DownloadPath: &download.ParentPath, OrganizeCID: organizationPtr("900"), OrganizePath: organizationPtr("/整理")}
	o, err := FreezeAnimeOrganization(&n, s, 7, download, snapshot)
	if err != nil || o == nil || o.Target.ParentCID != "900" || o.Target.SettingsRevision != 3 || o.PlanRevision != 7 {
		t.Fatal(o, err)
	}
	*s.OrganizeCID = "901"
	if o.Target.ParentCID != "900" {
		t.Fatal("snapshot changed with current settings")
	}
	template.Filename[0].Prefix = "changed-"
	if o.Naming.Revision != 4 || o.Naming.Template.Filename[0].Prefix != "" {
		t.Fatal("task template changed with caller configuration")
	}
	for _, change := range []func(*AnimeSettings){
		func(s *AnimeSettings) { s.OrganizeCID = nil },
		func(s *AnimeSettings) { s.OrganizeCID = organizationPtr("100") },
		func(s *AnimeSettings) { s.AccountID = organizationPtr("other") },
		func(s *AnimeSettings) { s.DownloadCID = organizationPtr("101") },
	} {
		bad := s
		change(&bad)
		if _, err := FreezeAnimeOrganization(&n, bad, 7, download, snapshot); err == nil {
			t.Fatal("invalid target accepted")
		}
	}
	if o, err := FreezeAnimeOrganization(nil, AnimeSettings{}, 1, download, snapshot); err != nil || o != nil {
		t.Fatal(o, err)
	}
}
