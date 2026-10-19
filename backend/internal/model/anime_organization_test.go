package model

import "testing"

func organizationPtr[T any](v T) *T { return &v }

func TestAnimeOrganizationFreezesDefaultTargetWithoutPreference(t *testing.T) {
	n := namingPlan()
	download := AnimeDownloadTarget{SchemaVersion: 1, SettingsRevision: 2, AccountID: "main", AccountRevision: 2, UID: "123", ParentCID: "100", ParentPath: "/下载"}
	s := AnimeSettings{Revision: 3, AccountID: &download.AccountID, AccountRevision: &download.AccountRevision, UID: &download.UID, DownloadCID: &download.ParentCID, DownloadPath: &download.ParentPath, OrganizeCID: organizationPtr("900"), OrganizePath: organizationPtr("/整理")}
	o, err := FreezeAnimeOrganization(&n, s, 7, download)
	if err != nil || o == nil || o.Target.ParentCID != "900" || o.Target.SettingsRevision != 3 || o.PlanRevision != 7 {
		t.Fatal(o, err)
	}
	*s.OrganizeCID = "901"
	if o.Target.ParentCID != "900" {
		t.Fatal("snapshot changed with current settings")
	}
	for _, change := range []func(*AnimeSettings){
		func(s *AnimeSettings) { s.OrganizeCID = nil },
		func(s *AnimeSettings) { s.OrganizeCID = organizationPtr("100") },
		func(s *AnimeSettings) { s.AccountID = organizationPtr("other") },
		func(s *AnimeSettings) { s.UID = organizationPtr("999") },
		func(s *AnimeSettings) { s.DownloadCID = organizationPtr("101") },
	} {
		bad := s
		change(&bad)
		if _, err := FreezeAnimeOrganization(&n, bad, 7, download); err == nil {
			t.Fatal("invalid target accepted")
		}
	}
	if o, err := FreezeAnimeOrganization(nil, AnimeSettings{}, 1, download); err != nil || o != nil {
		t.Fatal(o, err)
	}
}
