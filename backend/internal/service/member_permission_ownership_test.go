package service

import (
	"context"
	"reflect"
	"testing"

	"strm115/backend/internal/client/emby"
)

// Simulate an administrator changing the policy directly in Emby.
func setPlaybackForTest(ctx context.Context, s *MemberPlaybackService, id string, allowed bool) error {
	c := s.emby.snapshot()
	client := s.NewEmby(c.URL, c.APIKey)
	user, err := client.ManagedUser(ctx, s.record(id).EmbyID)
	if err != nil {
		return err
	}
	policy := make(map[string]any, len(user.Policy))
	for key, value := range user.Policy {
		policy[key] = value
	}
	policy["EnableMediaPlayback"] = allowed
	return client.SetManagedPolicy(ctx, user.ID, policy)
}
func permissionForTest(ctx context.Context, s *MemberPlaybackService, id string) (MemberPlaybackAdminView, error) {
	views, err := s.AdminMemberDirectory(ctx)
	if err != nil {
		return MemberPlaybackAdminView{}, err
	}
	for _, v := range views {
		if v.UserID == id {
			return v, nil
		}
	}
	return MemberPlaybackAdminView{}, ErrLogin
}

func TestExistingUserPermissionsRemainOwnedByEmby(t *testing.T) {
	ctx := context.Background()
	s, remote, id := memberFixture(t)
	memberScan(t, s, id)
	embyID := s.record(id).EmbyID
	manual := map[string]any{
		"EnableAllFolders": false, "EnabledFolders": []any{"private-library"},
		"MaxParentalRating": float64(13), "EnableAllDevices": false, "EnabledDevices": []any{"tv"},
		"EnableRemoteAccess": false, "RemoteClientBitrateLimit": float64(5000000),
		"IsHidden": false, "EnableContentDownloading": false, "EnableContentDeletion": true,
		"EnableSubtitleManagement": true, "EnableLiveTvAccess": true,
	}
	for k, v := range manual {
		remote.users[embyID].Policy[k] = v
	}
	if err := s.SetRegistrationLibraries(ctx, []string{"library2"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPlaybackSettings(ctx, true, ""); err != nil {
		t.Fatal(err)
	}
	remote.failLibraries = true
	if err := s.SetPlaybackSettings(ctx, true, "https://play.example.com"); err != nil {
		t.Fatal("playback form depended on registration libraries", err)
	}
	if !reflect.DeepEqual(s.Settings().LibraryIDs, []string{"library2"}) {
		t.Fatal("playback form replaced registration template")
	}
	for i := 0; i < 2; i++ {
		if err := s.SyncUser(ctx, id); err != nil {
			t.Fatal(err)
		}
		for k, v := range manual {
			if !reflect.DeepEqual(remote.users[embyID].Policy[k], v) {
				t.Fatalf("overwrote %s", k)
			}
		}
	}
	if _, err := s.Authorize(ctx, "http://emby", embyID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AuthorizeMedia(ctx, "http://emby", embyID, true); err == nil {
		t.Fatal("download switch expanded Emby permissions")
	}
	remote.users[embyID].Policy["EnableMediaPlayback"] = false
	if _, err := s.Authorize(ctx, "http://emby", embyID); err == nil {
		t.Fatal("live Emby disable ignored")
	}
	if err := s.SyncUser(ctx, id); err != nil {
		t.Fatal(err)
	}
	if remote.users[embyID].Policy["EnableMediaPlayback"] != false {
		t.Fatal("Emby playback disable overwritten")
	}
	remote.users[embyID].Policy["EnableMediaPlayback"] = true
	if err := s.SyncUser(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authorize(ctx, "http://emby", embyID); err != nil {
		t.Fatal("Emby recovery not respected", err)
	}
	writes := remote.policyWrites
	if err := s.SyncUser(ctx, id); err != nil {
		t.Fatal(err)
	}
	if remote.policyWrites != writes {
		t.Fatal("unchanged policy rewritten")
	}
}

func TestDirectoryDiscoveryDoesNotActivatePolicyManagement(t *testing.T) {
	ctx := context.Background()
	s, remote, _ := memberFixture(t)
	const embyID = "external-user"
	policy := map[string]any{"IsDisabled": false, "EnableMediaPlayback": true, "EnablePlaybackRemuxing": true, "EnabledFolders": []string{"private"}}
	remote.users[embyID] = emby.ManagedUser{ID: embyID, Name: "external", Policy: policy}
	if _, err := s.AdminMemberDirectory(ctx); err != nil {
		t.Fatal(err)
	}
	id := embyProfileID("server", embyID)
	writes := remote.policyWrites
	if _, err := s.AdminMemberDirectory(ctx); err != nil {
		t.Fatal(err)
	}
	if remote.policyWrites != writes {
		t.Fatal("reading directory took over user")
	}
	if err := s.AuthorizeLogin(ctx, "http://emby", embyID, "external"); err != nil {
		t.Fatal(err)
	}
	if err := s.SyncUser(ctx, id); err != nil {
		t.Fatal(err)
	}
	if remote.users[embyID].Policy["EnablePlaybackRemuxing"] != false {
		t.Fatal("mandatory restriction missing")
	}
	if !reflect.DeepEqual(remote.users[embyID].Policy["EnabledFolders"], policy["EnabledFolders"]) {
		t.Fatal("login changed libraries")
	}
}

func TestRegistrationTemplateAppliedOnlyAtCreation(t *testing.T) {
	ctx := context.Background()
	s, remote, _ := memberFixture(t)
	memberTestPrimary(t, s)
	auth := NewPortalAuth(s.db, "admin", "password123", "secret")
	auth.Members = s
	invites, err := auth.GenerateInvites(ctx, "admin", "", 2, 7, "assigned")
	if err != nil {
		t.Fatal(err)
	}
	first, err := auth.Register(ctx, "first-reader", "password123", invites[0].Code)
	if err != nil {
		t.Fatal(err)
	}
	if !s.record(first.ID).CreatedByCinema || !reflect.DeepEqual(remote.users[first.EmbyID].Policy["EnabledFolders"], []string{"guid-movies"}) {
		t.Fatal("creation template missing")
	}
	restored, err := NewMemberPlayback(ctx, s.db, s.accounts, s.emby, remote, remote, remote)
	if err != nil || !restored.record(first.ID).CreatedByCinema {
		t.Fatal("registration provenance not persisted", err)
	}
	manual := []any{"private-library"}
	remote.users[first.EmbyID].Policy["EnabledFolders"] = manual
	remote.users[first.EmbyID].Policy["EnableContentDownloading"] = false
	if err := s.SetRegistrationLibraries(ctx, []string{"library2"}); err != nil {
		t.Fatal(err)
	}
	second, err := auth.Register(ctx, "second-reader", "password123", invites[1].Code)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(remote.users[second.EmbyID].Policy["EnabledFolders"], []string{"guid-tv"}) {
		t.Fatal("new template not applied")
	}
	if err := s.SetRegistrationLibraries(ctx, nil); err != nil {
		t.Fatal(err)
	}
	remote.failLibraries = true
	if err := s.SyncUser(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(remote.users[first.EmbyID].Policy["EnabledFolders"], manual) || remote.users[first.EmbyID].Policy["EnableContentDownloading"] != false {
		t.Fatal("registered user manual permissions overwritten")
	}
	if _, err := s.Authorize(ctx, "http://emby", first.EmbyID); err != nil {
		t.Fatal("clearing template broke existing user", err)
	}
}

func TestUnconfirmedPlaybackRestrictionDeniesWithoutReplacingLibraries(t *testing.T) {
	ctx := context.Background()
	s, remote, id := memberFixture(t)
	memberScan(t, s, id)
	embyID := s.record(id).EmbyID
	manual := []any{"private-library"}
	remote.users[embyID].Policy["EnabledFolders"] = manual
	remote.users[embyID].Policy["EnablePlaybackRemuxing"] = true
	remote.ignoreRestrictions = true
	if err := s.SyncUser(ctx, id); err == nil {
		t.Fatal("unconfirmed restriction accepted")
	}
	if _, err := s.Authorize(ctx, "http://emby", embyID); err == nil {
		t.Fatal("unsafe playback allowed")
	}
	if !reflect.DeepEqual(remote.users[embyID].Policy["EnabledFolders"], manual) {
		t.Fatal("failure overwrote libraries")
	}
}
