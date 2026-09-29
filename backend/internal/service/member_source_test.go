package service

import (
	"context"
	"testing"
)

func TestMemberAdminSourcesAndNoFallback(t *testing.T) {
	s, f, id := memberFixture(t)
	ctx := context.Background()
	primary, err := s.accounts.AddPrimary("primary")
	if err != nil {
		t.Fatal(err)
	}
	cookie := "UID=777_A; SEID=primary"
	if err := s.accounts.Update(primary, 1, "primary", &cookie); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSource(ctx, id, "primary", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Provision(ctx, id, "password123"); err != nil {
		t.Fatal(err)
	}
	r := s.record(id)
	if r.Stage != "ready" || f.dirs != 0 || len(s.accounts.List()) != 1 {
		t.Fatal("friend requires personal drive", r)
	}
	if managed, err := s.Authorize(ctx, "http://emby", r.EmbyID); !managed || err != nil {
		t.Fatal(managed, err)
	}
	if len(s.Bindings(PlaybackCacheConfig{}).Bindings) != 0 || !s.SelectionMatches(r.EmbyID, nil) {
		t.Fatal("primary incorrectly selected as transfer target")
	}
	if _, err := s.CreateQR(ctx, id); err == nil {
		t.Fatal("friend can overwrite admin account")
	}
	if err := s.Suspend(ctx, id); err == nil {
		t.Fatal("friend can clear primary credentials")
	}
	a, _ := s.accounts.Get(primary)
	if a.Cookie != cookie {
		t.Fatal("primary cookie changed")
	}
	target, err := s.accounts.Add("assigned")
	if err != nil {
		t.Fatal(err)
	}
	targetCookie := "UID=888_A; SEID=assigned"
	if err := s.accounts.Update(target, 1, "assigned", &targetCookie); err != nil {
		t.Fatal(err)
	}
	a, _ = s.accounts.Get(target)
	if err := s.accounts.UpdatePlaybackDirectory(target, a.Revision, "123"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSource(ctx, id, "assigned", target); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authorize(ctx, "http://emby", r.EmbyID); err != nil {
		t.Fatal(err)
	}
	binding := s.Bindings(PlaybackCacheConfig{}).Bindings
	if len(binding) != 1 || binding[0].AccountID != target || s.SelectionMatches(r.EmbyID, nil) {
		t.Fatal("assigned fallback", binding)
	}
	if s.SelectionMatches(r.EmbyID, &PlaybackCacheSelection{binding: PlaybackBinding{AccountID: "other"}}) {
		t.Fatal("stale account selection accepted")
	}
	a, _ = s.accounts.Get(target)
	empty := ""
	if err := s.accounts.Update(target, a.Revision, a.Name, &empty); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authorize(ctx, "http://emby", r.EmbyID); err == nil {
		t.Fatal("missing assigned credential fell back")
	}
	if err := s.SyncUser(ctx, id); err != nil {
		t.Fatal(err)
	}
	if s.record(id).AssignedAccountID != target || s.record(id).PolicyEnabled {
		t.Fatal("binding lost")
	}
	if err := s.AuthorizeLogin(ctx, "http://emby", r.EmbyID, r.EmbyName); err != nil {
		t.Fatal("browse login lost with unavailable drive", err)
	}
	a, _ = s.accounts.Get(target)
	if err := s.accounts.Update(target, a.Revision, a.Name, &targetCookie); err != nil {
		t.Fatal(err)
	}
	a, _ = s.accounts.Get(target)
	if err := s.accounts.UpdatePlaybackDirectory(target, a.Revision, "123"); err != nil {
		t.Fatal(err)
	}
	if err := s.SyncUser(ctx, id); err != nil {
		t.Fatal(err)
	}
	if !s.record(id).PolicyEnabled {
		t.Fatal("assigned account did not recover")
	}
	if err := s.SetSource(ctx, id, "personal", primary); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authorize(ctx, "http://emby", r.EmbyID); err == nil {
		t.Fatal("personal pending fell back")
	}
	if err := s.SetSource(ctx, id, "assigned", primary); err == nil {
		t.Fatal("primary allowed as secondary")
	}
	blocked := true
	if err := s.AdminUpdate(ctx, id, &blocked, "", false); err != nil {
		t.Fatal(err)
	}
	if err := s.AuthorizeLogin(ctx, "http://emby", r.EmbyID, r.EmbyName); err == nil {
		t.Fatal("blocked user can log in")
	}
}

func TestInvitePlaybackSourcePersistsAndDefaultsSafely(t *testing.T) {
	s, _, _ := memberFixture(t)
	ctx := context.Background()
	auth := NewWebsiteAuth(s.db, "admin", "password123", "secret")
	for _, source := range []string{"primary", "personal"} {
		invites, err := auth.GenerateInvites(ctx, "admin", "test", 1, 7, source)
		if err != nil {
			t.Fatal(err)
		}
		got, err := auth.InviteSource(ctx, invites[0].Code)
		if err != nil || got != source {
			t.Fatal(got, err)
		}
		user, err := auth.Register(ctx, "reader_"+source, "password123", invites[0].Code)
		if err != nil {
			t.Fatal(err)
		}
		view, err := s.CurrentView(ctx, user.ID)
		if err != nil || view.Source != source {
			t.Fatal(view, err)
		}
		if len(s.accounts.List()) != 0 {
			t.Fatal("registration created unnecessary cloud account")
		}
		if _, err := auth.Register(ctx, "duplicate_"+source, "password123", invites[0].Code); err == nil {
			t.Fatal("invite reused")
		}
	}
	if _, err := auth.GenerateInvites(ctx, "admin", "test", 1, 7, "assigned"); err == nil {
		t.Fatal("invite allows assigned account")
	}
	records, err := s.db.MemberPlaybackRecords(ctx)
	if err != nil {
		t.Fatal(err)
	}
	primary := false
	for _, r := range records {
		if r.Source == "primary" {
			primary = true
		}
	}
	if !primary {
		t.Fatal("grant not persisted")
	}
}
