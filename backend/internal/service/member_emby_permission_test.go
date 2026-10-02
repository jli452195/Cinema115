package service

import (
	"context"
	"reflect"
	"strm115/backend/internal/client/emby"
	"testing"
)

func TestExternalEmbyPermissionBeforeProxySetupAndFirstPlaybackNeedsNoEnrollment(t *testing.T) {
	s, remote, _ := memberFixture(t)
	ctx := context.Background()
	memberTestPrimary(t, s)
	const embyID = "external-fresh"
	remote.users[embyID] = emby.ManagedUser{ID: embyID, Name: "fresh-viewer", Policy: map[string]any{"EnableMediaPlayback": true, "IsDisabled": false}}
	if _, err := s.Authorize(ctx, "http://emby", embyID); err != nil {
		t.Fatal("first request required enrollment", err)
	}
	id := embyProfileID("server", embyID)
	s.settings.EmbyURL = ""
	s.settings.ServerID = ""
	s.ProxyStatus = nil
	if err := setPlaybackForTest(ctx, s, id, false); err != nil {
		t.Fatal("Emby edit depended on proxy setup", err)
	}
	views, err := s.AdminMemberDirectory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range views {
		if v.UserID == id {
			if v.PlaybackAllowed == nil || *v.PlaybackAllowed {
				t.Fatal("directory did not expose live denial")
			}
			return
		}
	}
	t.Fatal("user missing")
}

func TestDisabledPersonalOwnerCannotSupplyVerificationCopies(t *testing.T) {
	s, remote, id := memberFixture(t)
	ctx := context.Background()
	memberScan(t, s, id)
	r := s.record(id)
	a, err := s.accounts.Get(r.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.authorizePersonalDonor(ctx, a); err != nil {
		t.Fatal(err)
	}
	remote.users[r.EmbyID].Policy["EnableMediaPlayback"] = false
	if err := s.authorizePersonalDonor(ctx, a); err == nil {
		t.Fatal("disabled owner supplied a copy")
	}
	remote.users[r.EmbyID].Policy["EnableMediaPlayback"] = true
	if err := s.Suspend(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := s.authorizePersonalDonor(ctx, a); err == nil {
		t.Fatal("unbound owner supplied a copy")
	}
}

func TestExternalEmbyPlaybackPreservesOtherPermissionsAndHasNoLocalGate(t *testing.T) {
	s, remote, id := memberFixture(t)
	ctx := context.Background()
	memberScan(t, s, id)
	r := s.record(id)
	remote.users[r.EmbyID].Policy["EnableAllDevices"] = false
	remote.users[r.EmbyID].Policy["EnabledDevices"] = []any{"tv"}
	remote.users[r.EmbyID].Policy["EnabledFolders"] = []any{"private"}
	remote.users[r.EmbyID].Policy["MaxParentalRating"] = float64(13)
	before := playbackRestrictions(remote.users[r.EmbyID].Policy)
	before["EnableMediaPlayback"] = false
	if err := setPlaybackForTest(ctx, s, id, false); err != nil {
		t.Fatal(err)
	}
	view, err := permissionForTest(ctx, s, id)
	if err != nil || view.PlaybackAllowed == nil || *view.PlaybackAllowed {
		t.Fatal(view, err)
	}
	if !reflect.DeepEqual(before, remote.users[r.EmbyID].Policy) {
		t.Fatal("unrelated permissions overwritten")
	}
	if !reflect.DeepEqual(r, s.record(id)) {
		t.Fatal("playback switch persisted as local authorization")
	}
	if err := s.SetPlaybackSettings(ctx, true, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authorize(ctx, r.EmbyURL, r.EmbyID); err == nil {
		t.Fatal("disabled playback authorized")
	}
	if _, err := s.AuthorizeMedia(ctx, r.EmbyURL, r.EmbyID, true); err != nil {
		t.Fatal("playback switch disabled download", err)
	}
	// Emby/other tools are authoritative immediately, with no local synchronization.
	remote.users[r.EmbyID].Policy["EnableMediaPlayback"] = true
	if _, err := s.Authorize(ctx, r.EmbyURL, r.EmbyID); err != nil {
		t.Fatal("external enable required sync", err)
	}
	remote.valid = false
	if _, err := s.Authorize(ctx, r.EmbyURL, r.EmbyID); err == nil {
		t.Fatal("invalid drive played")
	}
	if err := s.SyncUser(ctx, id); err != nil {
		t.Fatal(err)
	}
	if remote.users[r.EmbyID].Policy["EnableMediaPlayback"] != true {
		t.Fatal("drive failure overwrote Emby switch")
	}
	remote.valid = true
	if _, err := s.Authorize(ctx, r.EmbyURL, r.EmbyID); err != nil {
		t.Fatal("drive recovery required local grant", err)
	}
}

func TestRevokedChildCannotBeEnabledEvenWhenEmbyWasChanged(t *testing.T) {
	s, remote, owner, r := childFixture(t)
	ctx := context.Background()
	if err := s.ChildAction(ctx, owner, r.UserID, ChildActionInput{Action: "revoke", Revision: r.ChildRevision}); err != nil {
		t.Fatal(err)
	}
	remote.users[r.EmbyID].Policy["IsDisabled"] = false
	remote.users[r.EmbyID].Policy["EnableMediaPlayback"] = true

	if _, err := s.Authorize(ctx, r.EmbyURL, r.EmbyID); err == nil {
		t.Fatal("revoked child played")
	}
	if err := s.SyncUser(ctx, r.UserID); err != nil {
		t.Fatal(err)
	}
	if remote.users[r.EmbyID].Policy["IsDisabled"] != true {
		t.Fatal("revoked login not disabled")
	}
}

func TestRegisteredUserPlaybackSwitchRemainsOwnedByEmby(t *testing.T) {
	s, remote, _ := memberFixture(t)
	ctx := context.Background()
	memberTestPrimary(t, s)
	auth := NewPortalAuth(s.db, "admin", "password123", "secret")
	auth.Members = s
	invites, err := auth.GenerateInvites(ctx, "admin", "", 1, 7, "assigned")
	if err != nil {
		t.Fatal(err)
	}
	user, err := auth.Register(ctx, "registered-viewer", "password123", invites[0].Code)
	if err != nil {
		t.Fatal(err)
	}
	remote.users[user.EmbyID].Policy["EnableMediaPlayback"] = false
	if err := s.SyncUser(ctx, user.ID); err != nil {
		t.Fatal(err)
	}
	if remote.users[user.EmbyID].Policy["EnableMediaPlayback"] != false {
		t.Fatal("registered user policy overwritten")
	}
	remote.users[user.EmbyID].Policy["EnableMediaPlayback"] = true
	if _, err := s.Authorize(ctx, "http://emby", user.EmbyID); err != nil {
		t.Fatal(err)
	}
}
