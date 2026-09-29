package service

import (
	"context"
	"strings"
	"testing"

	"strm115/backend/internal/client/emby"
)

func adoptionFixture(t *testing.T) (*MemberPlaybackService, *memberTestRemote, string, string, *WebsiteAuth) {
	t.Helper()
	s, f, websiteID := memberFixture(t)
	id := strings.Repeat("d", 32)
	f.users[id] = emby.ManagedUser{ID: id, Name: "existing", Policy: map[string]any{"IsAdministrator": false, "IsDisabled": false, "EnableMediaPlayback": true, "EnableAllFolders": true, "EnableContentDownloading": true}}
	primary, err := s.accounts.AddPrimary("primary")
	if err != nil {
		t.Fatal(err)
	}
	cookie := "UID=777_A; SEID=primary"
	if err := s.accounts.Update(primary, 1, "primary", &cookie); err != nil {
		t.Fatal(err)
	}
	s.Cache = &PlaybackCache{Members: s, accounts: s.accounts, doc: PlaybackCacheStatus{Config: PlaybackCacheConfig{EmbyURL: "http://emby"}}}
	auth := NewWebsiteAuth(s.db, "admin", "password123", "secret")
	auth.Members = s
	return s, f, websiteID, id, auth
}

func TestMemberAdoptionExistingAndPreparedDrive(t *testing.T) {
	s, f, web, id, _ := adoptionFixture(t)
	ctx := context.Background()
	p, err := s.AdoptionPreview(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !p.CurrentDownload || p.NewDownload || len(p.CurrentLibraries) != 2 || len(p.NewLibraries) != 1 {
		t.Fatal("policy preview incorrect")
	}
	if err := s.LinkExistingUser(ctx, id, web, p.Fingerprint); err != nil {
		t.Fatal(err)
	}
	r := s.record(web)
	if r.EmbyID != id || !r.Adopted || !r.AllowPersonalSwitch || r.Source != "primary" || !r.Provisioned {
		t.Fatal("original identity or source lost")
	}
	if err := s.Cache.UpdateBinding("http://emby", PlaybackBinding{UserID: id}, "", true); err == nil {
		t.Fatal("generic binding can overwrite adopted source")
	}
	if err := s.SyncUser(ctx, web); err != nil {
		t.Fatal(err)
	}
	if f.created != 0 || f.passwordTarget != "" || s.record(web).Stage != "ready" {
		t.Fatal("adoption created or reset Emby")
	}
	if err := s.ChangePassword(ctx, web, "password-new", r.AccountRevision); err == nil {
		t.Fatal("adopted password mutation allowed")
	}
	if err := s.RenameAccount(ctx, web, "newname", r.AccountRevision); err == nil {
		t.Fatal("adopted rename allowed")
	}
	if err := s.Provision(ctx, web, "password-new"); err == nil {
		t.Fatal("adopted provision allowed")
	}
	memberScan(t, s, web)
	r = s.record(web)
	if r.Source != "primary" || r.Stage != "ready" || r.DirectoryID != "123" || f.dirs != 1 {
		t.Fatal("preparation changed playback or failed to prepare directory")
	}
	if err := s.UsePersonalDrive(ctx, web); err != nil {
		t.Fatal(err)
	}
	if s.record(web).Source != "personal" || s.record(web).Stage != "ready" || f.dirs != 1 {
		t.Fatal("switch failed")
	}
	if err := s.Suspend(ctx, web); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authorize(ctx, "http://emby", id); err == nil {
		t.Fatal("cleared personal drive fell back")
	}
	if s.record(web).Source != "personal" {
		t.Fatal("source changed on failure")
	}
}

func TestMemberAdoptionInviteAtomicAndSingleUse(t *testing.T) {
	s, _, _, id, auth := adoptionFixture(t)
	ctx := context.Background()
	p, err := s.AdoptionPreview(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	i, err := s.CreateAdoptionInvite(ctx, id, p.Fingerprint, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if source, err := auth.InviteSource(ctx, i.Code); err != nil || source != "existing" {
		t.Fatal(source, err)
	}
	if _, err := auth.Register(ctx, "reader", "password123", i.Code); err == nil {
		t.Fatal("duplicate website name accepted")
	}
	if err := auth.CheckInvite(ctx, i.Code); err != nil {
		t.Fatal("failed registration consumed invite", err)
	}
	u, err := auth.Register(ctx, "adopted", "password123", i.Code)
	if err != nil {
		t.Fatal(err)
	}
	if s.record(u.ID).EmbyID != id || s.record(u.ID).Source != "primary" {
		t.Fatal("invite failed to bind original ID")
	}
	if _, err := auth.Register(ctx, "second", "password123", i.Code); err == nil {
		t.Fatal("invite reused")
	}
	records, err := s.db.MemberPlaybackRecords(ctx)
	if err != nil || len(records) != 1 || records[0].UserID != u.ID {
		t.Fatal("association not atomic", err)
	}
}

func TestMemberAdoptionRejectsStaleAndForbiddenTargets(t *testing.T) {
	for _, kind := range []string{"admin", "disabled", "policy", "binding", "server", "revoked", "linked", "owner-used"} {
		t.Run(kind, func(t *testing.T) {
			s, f, web, id, auth := adoptionFixture(t)
			ctx := context.Background()
			p, err := s.AdoptionPreview(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			i, err := s.CreateAdoptionInvite(ctx, id, p.Fingerprint, "admin")
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "admin":
				f.users[id].Policy["IsAdministrator"] = true
			case "disabled":
				f.users[id].Policy["IsDisabled"] = true
			case "policy":
				f.users[id].Policy["EnableContentDownloading"] = false
			case "binding":
				s.Cache.doc.Config.Bindings = []PlaybackBinding{{UserID: id, AccountID: "new-account"}}
			case "server":
				s.emby.config.ServerID = "changed"
			case "revoked":
				if err := s.db.RevokeWebsiteInvite(ctx, i.ID); err != nil {
					t.Fatal(err)
				}
			case "linked":
				if err := s.LinkExistingUser(ctx, id, web, p.Fingerprint); err != nil {
					t.Fatal(err)
				}
			case "owner-used":
				memberScan(t, s, web)
				if err := s.LinkExistingUser(ctx, id, web, p.Fingerprint); err == nil {
					t.Fatal("personal owner association overwritten")
				}
				return
			}
			if _, err := auth.Register(ctx, "newreader", "password123", i.Code); err == nil {
				t.Fatal("invalid invitation accepted")
			}
			if _, err := s.db.WebsiteUserByName(ctx, "newreader"); err == nil {
				t.Fatal("failed claim left website account")
			}
		})
	}
}

func TestMemberPreparationFailureRetainsAssignedSource(t *testing.T) {
	s, f, web, id, _ := adoptionFixture(t)
	ctx := context.Background()
	target, err := s.accounts.Add("assigned")
	if err != nil {
		t.Fatal(err)
	}
	cookie := "UID=888_A; SEID=assigned"
	if err := s.accounts.Update(target, 1, "assigned", &cookie); err != nil {
		t.Fatal(err)
	}
	if err := s.accounts.UpdatePlaybackDirectory(target, 2, "123"); err != nil {
		t.Fatal(err)
	}
	s.Cache.doc.Config.Bindings = []PlaybackBinding{{UserID: id, AccountID: target}}
	p, err := s.AdoptionPreview(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.LinkExistingUser(ctx, id, web, p.Fingerprint); err != nil {
		t.Fatal(err)
	}
	if err := s.SyncUser(ctx, web); err != nil {
		t.Fatal(err)
	}
	f.failDirectory = true
	qr, err := s.CreateQR(ctx, web)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PollQR(ctx, web, qr.ID); err == nil {
		t.Fatal("uncertain directory accepted")
	}
	if err := s.UsePersonalDrive(ctx, web); err == nil {
		t.Fatal("switched to unprepared drive")
	}
	r := s.record(web)
	if r.Source != "assigned" || r.AssignedAccountID != target || !r.PolicyEnabled {
		t.Fatal("preparation interrupted assigned playback")
	}
	if err := s.Suspend(ctx, web); err != nil {
		t.Fatal(err)
	}
	if s.accounts.CookieFor(target) != cookie {
		t.Fatal("assigned cookie changed")
	}
	// Without an explicit grant, ordinary website users cannot switch sources.
	r.AllowPersonalSwitch = false
	if err := s.save(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err := s.UsePersonalDrive(ctx, web); err == nil {
		t.Fatal("ungranted switch allowed")
	}
}
