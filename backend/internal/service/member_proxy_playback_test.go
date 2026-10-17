package service

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestProxyPlaybackOnlyBlocksSelectedUsersPlayback(t *testing.T) {
	ctx := context.Background()
	s, remote, id := memberFixture(t)
	memberScan(t, s, id)
	if err := s.SetPlaybackSettings(ctx, true, "https://play.example.com"); err != nil {
		t.Fatal(err)
	}
	r := s.record(id)
	play, err := s.BeginMediaAuthorization(ctx, r.EmbyURL, r.EmbyID, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer play.Close()
	download, err := s.BeginMediaAuthorization(ctx, r.EmbyURL, r.EmbyID, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer download.Close()
	policy := remote.users[r.EmbyID].Policy
	writes := remote.policyWrites
	s.NewEmby = func(string, string) memberEmbyClient {
		t.Fatal("local switch contacted Emby")
		return remote
	}
	state, err := s.SetMemberProxyPlayback(ctx, id, false, 0)
	if err != nil || state.Enabled || state.Revision != 1 {
		t.Fatal(state, err)
	}
	// Cached-link and negotiation checks reject before any upstream work.
	if err := s.AuthorizeNegotiation(ctx, r.EmbyURL, r.EmbyID); err == nil {
		t.Fatal("paused proxy accepted negotiation")
	}
	if _, err := s.Authorize(ctx, r.EmbyURL, r.EmbyID); err == nil {
		t.Fatal("paused proxy accepted playback")
	}
	if err := play.CheckCurrent(ctx); err == nil {
		t.Fatal("old playback grant survived pause")
	}
	if err := download.CheckCurrent(ctx); err != nil {
		t.Fatal("playback pause affected existing download authorization", err)
	}
	s.NewEmby = func(string, string) memberEmbyClient { return remote }
	if _, err := s.AuthorizeMedia(ctx, r.EmbyURL, r.EmbyID, true); err != nil {
		t.Fatal("proxy playback pause blocked authorized download", err)
	}
	if _, err := s.portalUser(ctx, id); err != nil {
		t.Fatal("proxy playback pause blocked personal center", err)
	}
	if err := s.AuthorizeLogin(ctx, r.EmbyURL, r.EmbyID, r.EmbyName); err != nil {
		t.Fatal("proxy playback pause blocked Emby login", err)
	}
	if remote.policyWrites != writes || !reflect.DeepEqual(policy, remote.users[r.EmbyID].Policy) || remote.users[r.EmbyID].Policy["IsDisabled"] != false {
		t.Fatal("local gate changed Emby policy")
	}
	state, err = s.SetMemberProxyPlayback(ctx, id, false, 0)
	if err != nil || state.Revision != 1 {
		t.Fatal("repeated pause is not idempotent", state, err)
	}
	state, err = s.SetMemberProxyPlayback(ctx, id, true, 1)
	if err != nil || !state.Enabled || state.Revision != 2 {
		t.Fatal(state, err)
	}
	if err := play.CheckCurrent(ctx); !errors.Is(err, ErrAccountChanged) {
		t.Fatal("pause/resume revived an old request", err)
	}
	if err := download.CheckCurrent(ctx); err != nil {
		t.Fatal("pause/resume invalidated independent download", err)
	}
	if _, err := s.Authorize(ctx, r.EmbyURL, r.EmbyID); err != nil {
		t.Fatal("fresh playback did not recover", err)
	}
	want := r
	want.ProxyPlaybackRevision = 2
	if !reflect.DeepEqual(s.record(id), want) {
		t.Fatal("proxy switch changed identity, source or binding")
	}
}

func TestProxyPlaybackPersistsWithoutRemoteReadsAndKeepsEmbyDisabled(t *testing.T) {
	s, remote, id := memberFixture(t)
	r := s.record(id)
	remote.users[r.EmbyID].Policy["IsDisabled"] = true
	writes := remote.policyWrites
	s.NewEmby = func(string, string) memberEmbyClient {
		t.Fatal("local setting queried Emby")
		return remote
	}
	ctx := context.Background()
	if _, err := s.SetMemberProxyPlayback(ctx, id, false, 0); err != nil {
		t.Fatal(err)
	}
	state, err := s.ReadMemberProxyPlayback(ctx, id)
	if err != nil || state.Enabled || state.Revision != 1 {
		t.Fatal(state, err)
	}
	reloaded, err := NewMemberPlayback(ctx, s.db, s.accounts, s.emby, remote, remote, remote)
	if err != nil || !reloaded.record(id).ProxyPlaybackDisabled || reloaded.record(id).ProxyPlaybackRevision != 1 {
		t.Fatal("local setting did not survive reload", err)
	}
	if _, err := s.SetMemberProxyPlayback(ctx, id, true, 1); err != nil {
		t.Fatal(err)
	}
	if remote.users[r.EmbyID].Policy["IsDisabled"] != true || remote.policyWrites != writes {
		t.Fatal("enabling proxy re-enabled an Emby account")
	}
	s.NewEmby = func(string, string) memberEmbyClient { return remote }
	if _, err := s.Authorize(ctx, r.EmbyURL, r.EmbyID); err == nil {
		t.Fatal("local enable bypassed Emby disabled state")
	}
}

func TestProxyPlaybackPreservesOtherUsersAndEmbyPlaybackDenial(t *testing.T) {
	s, remote, owner, child := childFixture(t)
	ctx := context.Background()
	before := s.record(child.UserID)
	writes := remote.policyWrites
	if _, err := s.SetMemberProxyPlayback(ctx, owner, false, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authorize(ctx, "http://emby", child.EmbyID); err != nil {
		t.Fatal("owner's local switch cascaded to child", err)
	}
	if !reflect.DeepEqual(s.record(child.UserID), before) || remote.policyWrites != writes {
		t.Fatal("owner's local switch changed child identity or Emby policies")
	}
	if _, err := s.SetMemberProxyPlayback(ctx, owner, true, 1); err != nil {
		t.Fatal(err)
	}
	r := s.record(owner)
	remote.users[r.EmbyID].Policy["EnableMediaPlayback"] = false
	if _, err := s.Authorize(ctx, "http://emby", r.EmbyID); err == nil {
		t.Fatal("local enable expanded Emby playback permission")
	}
}

func TestProxyPlaybackRejectsProtectedIdentityAndStaleRevision(t *testing.T) {
	for _, scenario := range []string{"admin", "child", "incomplete", "foreign", "wrong_profile", "deleting", "reserved"} {
		t.Run(scenario, func(t *testing.T) {
			s, _, id := memberFixture(t)
			r := s.record(id)
			switch scenario {
			case "admin":
				r.Administrator = true
			case "child":
				r.PlaybackOnly = true
			case "incomplete":
				r.Provisioned = false
			case "foreign":
				r.ServerID = "other-server"
			case "wrong_profile":
				r.EmbyID = "other-user"
			case "reserved":
				r.EmbyName = "c115_pending"
			}
			if err := s.save(context.Background(), r); err != nil {
				t.Fatal(err)
			}
			if scenario == "deleting" {
				s.deleting = map[string]bool{id: true}
			}
			if _, err := s.SetMemberProxyPlayback(context.Background(), id, false, 0); err == nil || s.record(id).ProxyPlaybackDisabled {
				t.Fatal("protected identity accepted local switch", err)
			}
		})
	}
	s, _, id := memberFixture(t)
	ctx := context.Background()
	if _, err := s.SetMemberProxyPlayback(ctx, id, false, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetMemberProxyPlayback(ctx, id, true, 1); err != nil {
		t.Fatal(err)
	}
	state, err := s.SetMemberProxyPlayback(ctx, id, false, 0)
	if !errors.Is(err, ErrProxyPlaybackConflict) || !state.Enabled || state.Revision != 2 {
		t.Fatal("stale tab rewrote a pause/resume cycle", state, err)
	}
}

func TestProxyPlaybackFailedSavePreservesMemory(t *testing.T) {
	s, _, id := memberFixture(t)
	before := s.record(id)
	if err := s.db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetMemberProxyPlayback(context.Background(), id, false, 0); err == nil {
		t.Fatal("failed persistence reported success")
	}
	if !reflect.DeepEqual(s.record(id), before) {
		t.Fatal("failed persistence published local state")
	}
}
