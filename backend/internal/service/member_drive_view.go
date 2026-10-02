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
	DriveStatus DriveHealthView `json:"driveStatus"`
	ChildUsed   *int            `json:"childUsed,omitempty"`
	OwnerName   string          `json:"ownerName,omitempty"`
	ImageTag    string          `json:"imageTag,omitempty"`
	model.MemberPlayback
	PersonalDrive   PersonalDriveView `json:"personalDrive"`
	Username        string            `json:"username"`
	Disabled        bool              `json:"disabled"`
	PlaybackAllowed *bool             `json:"playbackAllowed,omitempty"`
	DownloadAllowed *bool             `json:"downloadAllowed,omitempty"`
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
		if strings.HasPrefix(strings.ToLower(u.Name), "c115_") {
			continue
		}
		if _, err := s.observeUser(ctx, c, emby.ManagedUser{ID: u.ID, Name: u.Name, Policy: map[string]any{"IsAdministrator": u.Policy.IsAdministrator}}); err != nil {
			return nil, err
		}
	}
	out := s.AdminMembers()
	counts, err := s.db.ChildUsage(ctx)
	if err != nil {
		return nil, err
	}
	for i := range out {
		count := counts[out[i].UserID]
		out[i].ChildUsed = &count
		out[i].Disabled = true
		for _, u := range users {
			if u.ID == out[i].EmbyID && out[i].ServerID == c.ServerID {
				out[i].Administrator = u.Policy.IsAdministrator
				out[i].Disabled = u.Policy.IsDisabled
				out[i].PlaybackAllowed = u.Policy.EnableMediaPlayback
				out[i].DownloadAllowed = u.Policy.EnableContentDownloading
				out[i].EmbyName = u.Name
				out[i].ImageTag = u.PrimaryImageTag
			}
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
		ownerName := ""
		if r.PlaybackOnly {
			ownerName = s.record(r.OwnerUserID).EmbyName
		}
		out = append(out, MemberPlaybackAdminView{DriveStatus: s.DriveHealth(r), OwnerName: ownerName, MemberPlayback: adminPlaybackRecord(r), PersonalDrive: PersonalDriveView{Connected: s.personalDrive(r).Connected}, Username: r.EmbyName})
	}
	return out
}

// Administrative views expose personal-drive status, never identifiers or credentials.
func adminPlaybackRecord(r model.MemberPlayback) model.MemberPlayback {
	r.AccountID = ""
	r.PendingAccountID = ""
	r.DirectoryID = ""
	r.DirectoryStarted = false
	r.PendingDirectoryStarted = false
	r.UncertainDirectories = nil
	return r
}
