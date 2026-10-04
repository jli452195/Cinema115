package service

import (
	"context"
	"encoding/json"
	"strings"
	"strm115/backend/internal/model"
	"testing"
)

func childFixture(t *testing.T) (*MemberPlaybackService, *memberTestRemote, string, model.MemberPlayback) {
	t.Helper()
	s, f, owner := memberFixture(t)
	memberScan(t, s, owner)
	ctx := context.Background()
	if err := s.SetChildLimit(ctx, owner, 3); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateChild(ctx, owner, ChildCreateInput{RequestID: "request-child-0001", Name: "viewer-one", Password: "password123"}); err != nil {
		t.Fatal(err)
	}
	v, err := s.Children(ctx, owner)
	if err != nil || len(v.Items) != 1 {
		t.Fatal(v, err)
	}
	return s, f, owner, s.record(v.Items[0].UserID)
}

func TestPlaybackChildIdentityAndPersonalSource(t *testing.T) {
	s, f, owner, child := childFixture(t)
	ctx := context.Background()
	if !child.PlaybackOnly || child.OwnerUserID != owner || child.Source != "personal_shared" || child.AllowPersonalSwitch || child.AccountID != "" || child.Stage != "ready" {
		t.Fatal(child)
	}
	if len(s.accounts.List()) != 1 {
		t.Fatal("child copied personal credentials")
	}
	if _, err := s.Authorize(ctx, "http://emby", child.EmbyID); err != nil {
		t.Fatal(err)
	}
	if err := s.AuthorizeLogin(ctx, "http://emby", child.EmbyID, child.EmbyName); err != nil {
		t.Fatal(err)
	}
	auth := NewPortalAuth(s.db, "admin", "password123", "secret")
	auth.Members = s
	if _, _, err := auth.Login(ctx, child.EmbyName, "password123"); err == nil {
		t.Fatal("child logged into website")
	}
	u, err := s.db.PortalUser(ctx, child.UserID)
	if err != nil {
		t.Fatal(err)
	}
	oldToken, _, err := auth.sign(u)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Authenticate(ctx, oldToken); err == nil {
		t.Fatal("old website token accepted")
	}
	if _, err := s.CurrentView(ctx, child.UserID); err == nil {
		t.Fatal("personal center accessible")
	}
	if err := s.SetSource(ctx, child.UserID, "primary", ""); err == nil {
		t.Fatal("child changed to primary")
	}
	if err := s.SetChildLimit(ctx, child.UserID, 3); err == nil {
		t.Fatal("nested sharing allowed")
	}
	main := memberTestPrimary(t, s)
	if err := s.SetSource(ctx, owner, "primary", ""); err != nil {
		t.Fatal(err)
	}
	a, err := s.playbackAccount(s.record(child.UserID))
	if err != nil || a.ID == main || a.OwnerUserID != owner {
		t.Fatal("child followed primary", err)
	}
	bindings := s.Bindings(PlaybackCacheConfig{})
	found := false
	for _, b := range bindings.Bindings {
		if b.UserID == child.EmbyID {
			found = true
			if b.AccountID != a.ID {
				t.Fatal("wrong binding")
			}
		}
	}
	if !found {
		t.Fatal("missing child binding")
	}
	if _, err := s.Authorize(ctx, "http://emby", child.EmbyID); err != nil {
		t.Fatal(err)
	}
	if err := s.Suspend(ctx, owner); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authorize(ctx, "http://emby", child.EmbyID); err == nil {
		t.Fatal("owner unbound but child played")
	}
	if _, err := s.playbackAccount(s.record(child.UserID)); err == nil {
		t.Fatal("child fell back")
	}
	for _, k := range []string{"EnableVideoPlaybackTranscoding", "EnableAudioPlaybackTranscoding", "EnablePlaybackRemuxing", "IsAdministrator", "EnableContentDeletion"} {
		if f.users[child.EmbyID].Policy[k] != false {
			t.Fatal(k)
		}
	}
}

func TestPlaybackChildLifecycleAndRevocationSurviveDiscovery(t *testing.T) {
	s, f, owner, child := childFixture(t)
	ctx := context.Background()
	action := func(name string) {
		t.Helper()
		r := s.record(child.UserID)
		if err := s.ChildAction(ctx, owner, r.UserID, ChildActionInput{Action: name, Revision: r.ChildRevision, Password: "changed-password"}); err != nil {
			t.Fatal(name, err)
		}
	}
	action("pause")
	if _, err := s.Authorize(ctx, "http://emby", child.EmbyID); err == nil {
		t.Fatal("paused playback accepted")
	}
	if err := s.AuthorizeLogin(ctx, "http://emby", child.EmbyID, child.EmbyName); err != nil {
		t.Fatal("pause prevented browsing", err)
	}
	action("resume")
	action("disable")
	if err := s.AuthorizeLogin(ctx, "http://emby", child.EmbyID, child.EmbyName); err == nil {
		t.Fatal("disabled login accepted")
	}
	action("enable")
	if _, err := s.Authorize(ctx, "http://emby", child.EmbyID); err != nil {
		t.Fatal("enable failed", err)
	}
	action("password")
	if f.passwordTarget != child.EmbyID {
		t.Fatal("wrong password target")
	}
	blocked := true
	if err := setPlaybackForTest(ctx, s, child.UserID, !blocked); err != nil {
		t.Fatal(err)
	}
	r := s.record(child.UserID)
	if err := s.ChildAction(ctx, owner, r.UserID, ChildActionInput{Action: "enable", Revision: r.ChildRevision}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authorize(ctx, "http://emby", child.EmbyID); err == nil {
		t.Fatal("owner action bypassed Emby playback denial")
	}
	blocked = false
	if err := setPlaybackForTest(ctx, s, child.UserID, !blocked); err != nil {
		t.Fatal(err)
	}
	action("revoke")
	if _, err := s.ObserveUser(ctx, s.emby.snapshot(), f.users[child.EmbyID]); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AdminMemberDirectory(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.SyncUser(ctx, child.UserID); err != nil {
		t.Fatal(err)
	}
	r = s.record(child.UserID)
	if !r.PlaybackOnly || !r.ChildRevoked || r.Source != "personal_shared" {
		t.Fatal("discovery erased tombstone", r)
	}
	if _, err := s.Authorize(ctx, "http://emby", child.EmbyID); err == nil {
		t.Fatal("revoked playback accepted")
	}
	if _, err := s.portalUser(ctx, child.UserID); err == nil {
		t.Fatal("revoked child became website user")
	}
	view, err := s.Children(ctx, owner)
	if err != nil || view.Used != 0 {
		t.Fatal(view, err)
	}
	reloaded, err := NewMemberPlayback(ctx, s.db, s.accounts, s.emby, f, f, f)
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.record(child.UserID).ChildRevoked {
		t.Fatal("revoke not durable")
	}
}

func TestChildCreationUncertainRetryQuotaAndNoPasswords(t *testing.T) {
	s, f, owner := memberFixture(t)
	memberScan(t, s, owner)
	ctx := context.Background()
	if err := s.SetChildLimit(ctx, owner, 1); err != nil {
		t.Fatal(err)
	}
	input := ChildCreateInput{RequestID: "request-child-uncertain", Name: "viewer-one", Password: "never-persist-password"}
	f.failCreate = true
	f.failEnable = true
	if err := s.CreateChild(ctx, owner, input); err == nil {
		t.Fatal("unconfirmed activation accepted")
	}
	if f.created != 1 {
		t.Fatal(f.created)
	}
	if err := s.CreateChild(ctx, owner, ChildCreateInput{RequestID: "request-child-second", Name: "viewer-two", Password: "password123"}); err == nil {
		t.Fatal("uncertain job did not occupy quota")
	}
	jobs, err := s.db.ChildCreations(ctx, owner)
	if err != nil || len(jobs) != 1 {
		t.Fatal(jobs, err)
	}
	raw, err := json.Marshal(jobs)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), input.Password) {
		t.Fatal("password persisted")
	}
	f.failCreate = false
	f.failEnable = false
	if err := s.CreateChild(ctx, owner, input); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateChild(ctx, owner, input); err != nil {
		t.Fatal(err)
	}
	if f.created != 1 {
		t.Fatal("retry created duplicate")
	}
	v, err := s.Children(ctx, owner)
	if err != nil || v.Used != 1 || !v.Items[0].Completed {
		t.Fatal(v, err)
	}
	if err := s.SetChildLimit(ctx, owner, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authorize(ctx, "http://emby", s.record(v.Items[0].UserID).EmbyID); err != nil {
		t.Fatal("quota decrease stopped child", err)
	}
}

func TestChildExistingNameDoesNotReserveQuota(t *testing.T) {
	s, _, owner := memberFixture(t)
	memberScan(t, s, owner)
	ctx := context.Background()
	if err := s.SetChildLimit(ctx, owner, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateChild(ctx, owner, ChildCreateInput{RequestID: "existing-name-request", Name: "reader", Password: "password123"}); err == nil {
		t.Fatal("existing name accepted")
	}
	view, err := s.Children(ctx, owner)
	if err != nil || view.Used != 0 {
		t.Fatal("name conflict occupied quota", view, err)
	}
}

func TestChildUnbindAndUIDReplacementNeverCopyCredentials(t *testing.T) {
	s, f, owner, child := childFixture(t)
	ctx := context.Background()
	before, err := s.playbackAccount(child)
	if err != nil {
		t.Fatal(err)
	}
	f.cookie = "UID=987_A; SEID=new-personal"
	qr, err := s.CreateQR(ctx, owner, memberQRInput(s, owner, true))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PollQR(ctx, owner, qr.ID); err != nil {
		t.Fatal(err)
	}
	pending, err := s.playbackAccount(child)
	if err != nil || pending.ID != before.ID {
		t.Fatal("pending replacement changed child", err)
	}
	if err := s.SetPersonalDirectory(ctx, owner, memberDirectoryInput(s, owner, "456", false)); err != nil {
		t.Fatal(err)
	}
	after, err := s.playbackAccount(child)
	if err != nil || after.ID == before.ID || after.UID != "987" || after.PlaybackParentCID != "456" {
		t.Fatal("replacement not resolved", err)
	}
	if before.UID != "123" || before.PlaybackParentCID != "123" {
		t.Fatal("in-flight snapshot changed")
	}
	if s.record(child.UserID).AccountID != "" {
		t.Fatal("child persisted personal credentials reference")
	}
	r := s.record(child.UserID)
	if err := s.ChildAction(ctx, owner, r.UserID, ChildActionInput{Action: "disable", Revision: r.ChildRevision}); err != nil {
		t.Fatal(err)
	}
	if !s.ChildSessionDenied("http://emby", r.EmbyID) {
		t.Fatal("existing disabled session allowed")
	}
}

func TestChildAdministratorPromotionAndForeignOwnerDenied(t *testing.T) {
	s, f, owner, child := childFixture(t)
	ctx := context.Background()
	if err := s.ChildAction(ctx, child.UserID, child.UserID, ChildActionInput{Action: "password", Revision: child.ChildRevision, Password: "password123"}); err == nil {
		t.Fatal("child managed itself")
	}
	u := f.users[child.EmbyID]
	u.Policy["IsAdministrator"] = true
	f.users[child.EmbyID] = u
	if err := s.ChildAction(ctx, owner, child.UserID, ChildActionInput{Action: "password", Revision: child.ChildRevision, Password: "password123"}); err == nil {
		t.Fatal("owner reset administrator")
	}
	if err := s.SyncUser(ctx, child.UserID); err == nil {
		t.Fatal("administrator still managed")
	}
	if f.users[child.EmbyID].Policy["IsAdministrator"] != true {
		t.Fatal("changed administrator privileges")
	}
	if _, err := s.Authorize(ctx, "http://emby", child.EmbyID); err == nil {
		t.Fatal("promoted child played")
	}
}
