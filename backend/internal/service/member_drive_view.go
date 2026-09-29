package service

import (
	"context"
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
	PersonalDrive   PersonalDriveView `json:"personalDrive"`
	WebsiteName     string            `json:"websiteName"`
	WebsiteDisabled bool              `json:"websiteDisabled"`
}

func (s *MemberPlaybackService) AdminMemberDirectory(ctx context.Context) ([]MemberPlaybackAdminView, error) {
	owners, err := s.db.MemberPlaybackOwners(ctx)
	if err != nil {
		return nil, err
	}
	out := s.AdminMembers()
	positions := make(map[string]int, len(out))
	for i := range out {
		positions[out[i].UserID] = i
	}
	for _, owner := range owners {
		if i, ok := positions[owner.UserID]; ok {
			out[i].WebsiteName, out[i].WebsiteDisabled = owner.Username, owner.Disabled
		}
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
		out = append(out, MemberPlaybackAdminView{MemberPlayback: r, PersonalDrive: s.personalDrive(r)})
	}
	return out
}
