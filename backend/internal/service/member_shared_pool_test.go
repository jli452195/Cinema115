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
		u := emby.ManagedUser{ID: id, Name: "viewer", Policy: map[string]any{"IsAdministrator": admin, "IsDisabled": false, "EnableAllFolders": false, "EnabledFolders": folders, "MaxParentalRating": float64(13), "EnableMediaPlayback": true, "EnableContentDownloading": true, "EnableContentDeletion": true}}
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

func TestSharedPoolDynamicSelectionAndNoPrimaryFallback(t *testing.T) {
	ctx := context.Background()
	s, _, owner := memberFixture(t)
	memberTestPrimary(t, s)
	pool := memberTestPool(t, s)
	if err := s.SetSource(ctx, owner, "assigned", pool); err == nil {
		t.Fatal("manual pool binding accepted")
	}
	if err := s.SetSource(ctx, owner, "assigned", ""); err != nil {
		t.Fatal(err)
	}
	request := WithPoolRequest(ctx, PoolRequest{Session: "authenticated-client", Item: "movie"})
	grant, err := s.BeginMediaAuthorization(request, "http://emby", s.record(owner).EmbyID, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if grant.account.ID != pool {
		t.Fatal("wrong pool target")
	}
	if err := grant.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := grant.Close(); err != nil {
		t.Fatal(err)
	}
	a, _ := s.accounts.Get(pool)
	if err := s.accounts.Delete(pool, a.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authorize(ctx, "http://emby", s.record(owner).EmbyID); err == nil {
		t.Fatal("empty pool fell back to primary")
	}
	if s.record(owner).Source != "assigned" {
		t.Fatal("source mutated")
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
	if err := s.SetSource(ctx, id, "primary", ""); err != nil {
		t.Fatal(err)
	}
	if s.accounts.CookieFor(r.AccountID) == "" {
		t.Fatal("source switch erased personal credentials")
	}
}
