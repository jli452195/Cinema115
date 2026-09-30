package service

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"strm115/backend/internal/client/emby"
)

func memberTestPrimary(t *testing.T, s *MemberPlaybackService) string {
	t.Helper()
	id, err := s.accounts.AddPrimary("main")
	if err != nil {
		t.Fatal(err)
	}
	cookie := "UID=888_A; SEID=main-secret"
	if err := s.accounts.Update(id, 1, "main", &cookie); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestDefaultPrimaryAndAdminPlaybackPreserveLibraries(t *testing.T) {
	ctx := context.Background()
	s, remote, _ := memberFixture(t)
	main := memberTestPrimary(t, s)
	for _, admin := range []bool{false, true} {
		id := strings.Repeat("d", 32)
		if admin {
			id = strings.Repeat("e", 32)
		}
		folders := []any{"private-library"}
		u := emby.ManagedUser{ID: id, Name: "viewer", Policy: map[string]any{"IsAdministrator": admin, "IsDisabled": false, "EnableAllFolders": false, "EnabledFolders": folders, "MaxParentalRating": float64(13), "EnableContentDeletion": true}}
		remote.users[id] = u
		if err := s.AuthorizeLogin(ctx, "http://emby", id, u.Name); err != nil {
			t.Fatal(err)
		}
		profile := embyProfileID("server", id)
		if s.record(profile).Source != "primary" {
			t.Fatal("default primary missing")
		}
		if err := s.SyncUser(ctx, profile); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Authorize(ctx, "http://emby", id); err != nil {
			t.Fatal(err)
		}
		if !s.SelectionMatches(id, nil) {
			t.Fatal("primary selection rejected")
		}
		if len(s.Bindings(PlaybackCacheConfig{}).Bindings) != 1 {
			t.Fatal("primary emitted a secondary binding")
		}
		if admin {
			policy := remote.users[id].Policy
			if !reflect.DeepEqual(policy["EnabledFolders"], folders) || policy["EnableAllFolders"] != false || policy["IsAdministrator"] != true || policy["EnableContentDeletion"] != true || policy["MaxParentalRating"] != float64(13) {
				t.Fatal("administrator permissions overwritten", policy)
			}
			for _, k := range []string{"EnableVideoPlaybackTranscoding", "EnableAudioPlaybackTranscoding", "EnablePlaybackRemuxing"} {
				if policy[k] != false {
					t.Fatal(k)
				}
			}
			if _, err := s.portalUser(ctx, profile); err == nil {
				t.Fatal("administrator entered ordinary portal")
			}
			if err := s.SetLibrarySyncGroup(ctx, profile, false); err == nil {
				t.Fatal("administrator library sync enabled")
			}
			if err := s.SetSource(ctx, profile, "personal", ""); err == nil {
				t.Fatal("administrator personal source allowed")
			}
		}
	}
	a, _ := s.accounts.Get(main)
	if err := s.accounts.Delete(main, a.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authorize(ctx, "http://emby", strings.Repeat("e", 32)); err == nil {
		t.Fatal("missing primary allowed playback")
	}
}

func TestSharedPoolLeastUsedAndFailureNeverFallsBack(t *testing.T) {
	ctx := context.Background()
	s, remote, owner := memberFixture(t)
	memberTestPrimary(t, s)
	pool := make([]string, 0, 2)
	for _, cookie := range []string{"UID=501_A; SEID=pool-one", "UID=502_A; SEID=pool-two"} {
		id, err := s.accounts.Add("pool")
		if err != nil {
			t.Fatal(err)
		}
		if err := s.accounts.Update(id, 1, "pool", &cookie); err != nil {
			t.Fatal(err)
		}
		if err := s.accounts.UpdatePlaybackDirectory(id, 2, "123"); err != nil {
			t.Fatal(err)
		}
		pool = append(pool, id)
	}
	if err := s.SetSource(ctx, owner, "assigned", pool[0]); err != nil {
		t.Fatal(err)
	}
	newID := strings.Repeat("f", 32)
	u := emby.ManagedUser{ID: newID, Name: "second", Policy: map[string]any{"IsDisabled": false}}
	remote.users[newID] = u
	second, err := s.ObserveUser(ctx, s.emby.snapshot(), u)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetSource(ctx, second.ID, "assigned", ""); err != nil {
		t.Fatal(err)
	}
	if s.record(second.ID).AssignedAccountID != pool[1] {
		t.Fatal("least-used selection failed")
	}
	if err := s.SetSource(ctx, second.ID, "assigned", pool[0]); err != nil {
		t.Fatal(err)
	}
	for _, option := range s.AccountOptions() {
		if option.ID == pool[0] && option.UserCount != 2 {
			t.Fatal("shared usage count wrong", option.UserCount)
		}
	}
	a, _ := s.accounts.Get(pool[0])
	empty := ""
	if err := s.accounts.Update(a.ID, a.Revision, a.Name, &empty); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{owner, second.ID} {
		if err := s.SyncUser(ctx, id); err != nil {
			t.Fatal(err)
		}
		r := s.record(id)
		if r.Source != "assigned" || r.AssignedAccountID != pool[0] {
			t.Fatal("failed binding changed")
		}
		if _, err := s.Authorize(ctx, "http://emby", r.EmbyID); err == nil {
			t.Fatal("failed shared account fell back")
		}
	}
	if err := s.SetSource(ctx, owner, "primary", ""); err != nil {
		t.Fatal(err)
	}
	if s.record(second.ID).AssignedAccountID != pool[0] {
		t.Fatal("other user's association removed")
	}
}

func TestAdminPersonalDriveStatusOnly(t *testing.T) {
	ctx := context.Background()
	s, _, id := memberFixture(t)
	memberScan(t, s, id)
	r := s.record(id)
	for _, option := range s.AccountOptions() {
		if option.ID == r.AccountID {
			t.Fatal("personal drive entered pool")
		}
	}
	for _, option := range s.accounts.ManagedList() {
		if option.ID == r.AccountID {
			t.Fatal("personal drive entered administrator list")
		}
	}
	for _, view := range s.AdminMembers() {
		if view.UserID != id {
			continue
		}
		if !view.PersonalDrive.Connected || view.PersonalDrive.UID != "" || view.PersonalDrive.DirectoryID != "" || view.AccountID != "" || view.DirectoryID != "" {
			t.Fatal("personal identifiers exposed")
		}
		raw, err := json.Marshal(view)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "SEID") || strings.Contains(strings.ToLower(string(raw)), "cookie") {
			t.Fatal("personal secret exposed")
		}
	}
	if err := s.AdminUpdate(ctx, id, nil, "123", false); err == nil {
		t.Fatal("administrator changed personal directory")
	}
	if err := s.SetSource(ctx, id, "primary", ""); err != nil {
		t.Fatal(err)
	}
	if s.accounts.CookieFor(r.AccountID) == "" {
		t.Fatal("source switch erased personal credentials")
	}
}
