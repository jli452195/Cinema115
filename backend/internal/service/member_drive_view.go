package service

import (
	"context"
	"strings"
	"strm115/backend/internal/client/emby"
	"strm115/backend/internal/model"
)

// PersonalDriveView exposes ownership and credential presence, never credentials.
type PersonalDriveView struct {
	UID         string `json:"uid"`
	Connected   bool   `json:"connected"`
	DirectoryID string `json:"directoryId"`
}

type MemberPlaybackAdminView struct {
	model.MemberPlayback
	PersonalDrive PersonalDriveView `json:"personalDrive"`
	Username      string            `json:"username"`
	Disabled      bool              `json:"disabled"`
	Administrator bool              `json:"administrator"`
}

func (s *MemberPlaybackService) AdminMemberDirectory(ctx context.Context) ([]MemberPlaybackAdminView, error) {
	c := s.emby.snapshot()
	if c.ServerID == "" || c.APIKey == "" {
		return nil, memberError("请先配置 Emby")
	}
	if err := s.lock(ctx); err != nil {
		return nil, err
	}
	defer s.unlock()
	users, err := s.NewEmby(c.URL, c.APIKey).Users(ctx)
	if err != nil {
		return nil, err
	}
	for _, u := range users {
		if u.Policy.IsAdministrator || strings.HasPrefix(strings.ToLower(u.Name), "c115_") {
			continue
		}
		if _, err := s.observeUser(ctx, c, emby.ManagedUser{ID: u.ID, Name: u.Name}); err != nil {
			return nil, err
		}
	}
	out := s.AdminMembers()
	seen := make(map[string]bool, len(out))
	for i := range out {
		out[i].Disabled = true
		for _, u := range users {
			if u.ID == out[i].EmbyID && out[i].ServerID == c.ServerID {
				seen[u.ID] = true
				out[i].Administrator = u.Policy.IsAdministrator
				out[i].SkipLibrarySync = out[i].SkipLibrarySync || u.Policy.IsAdministrator
				out[i].Disabled = u.Policy.IsDisabled
				out[i].EmbyName = u.Name
			}
		}
	}
	for _, u := range users {
		if !u.Policy.IsAdministrator || seen[u.ID] {
			continue
		}
		out = append(out, MemberPlaybackAdminView{
			MemberPlayback: model.MemberPlayback{UserID: "admin:" + u.ID, EmbyID: u.ID, EmbyURL: c.URL, ServerID: c.ServerID, EmbyName: u.Name, Stage: "administrator", SkipLibrarySync: true},
			Username:       u.Name, Disabled: u.Policy.IsDisabled, Administrator: true,
		})
	}
	return out, nil
}

func (s *MemberPlaybackService) personalDrive(r model.MemberPlayback) PersonalDriveView {
	a, err := s.accounts.Get(r.AccountID)
	if err != nil || a.OwnerUserID != r.UserID || a.Ownership != "member" {
		return PersonalDriveView{}
	}
	return PersonalDriveView{UID: a.UID, Connected: a.Cookie != "", DirectoryID: r.DirectoryID}
}

func (s *MemberPlaybackService) AdminMembers() []MemberPlaybackAdminView {
	records := s.Records()
	out := make([]MemberPlaybackAdminView, 0, len(records))
	for _, r := range records {
		out = append(out, MemberPlaybackAdminView{MemberPlayback: r, PersonalDrive: s.personalDrive(r), Username: r.EmbyName})
	}
	return out
}
