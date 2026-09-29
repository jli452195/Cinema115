package service

import (
	"context"
	"reflect"
	"testing"
)

func TestLibraryExcludedGroupPreservesPolicyAndRejoins(t *testing.T) {
	ctx := context.Background()
	s, remote, id := memberFixture(t)
	memberScan(t, s, id)
	embyID := s.record(id).EmbyID
	if err := s.SetLibrarySyncGroup(ctx, id, true); err != nil {
		t.Fatal(err)
	}
	manual := []any{"private-library"}
	remote.users[embyID].Policy["EnabledFolders"] = manual
	remote.users[embyID].Policy["MaxParentalRating"] = float64(13)
	settings := s.Settings()
	settings.LibraryIDs = []string{"library2"}
	if err := s.SetSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	// Manual users do not depend on resolving the default media-library list.
	remote.failLibraries = true
	for _, all := range []bool{false, true} {
		remote.users[embyID].Policy["EnableAllFolders"] = all
		if err := s.SyncUser(ctx, id); err != nil {
			t.Fatal(err)
		}
		policy := remote.users[embyID].Policy
		if policy["EnableAllFolders"] != all || !reflect.DeepEqual(policy["EnabledFolders"], manual) || policy["MaxParentalRating"] != float64(13) {
			t.Fatal("manual library or rating policy overwritten", policy)
		}
		for _, key := range []string{"EnableVideoPlaybackTranscoding", "EnableAudioPlaybackTranscoding", "EnablePlaybackRemuxing"} {
			if policy[key] != false {
				t.Fatal("playback restriction lost", key)
			}
		}
		if _, err := s.Authorize(ctx, "http://emby", embyID); err != nil {
			t.Fatal(err)
		}
	}
	restored, err := NewMemberPlayback(ctx, s.db, s.accounts, s.emby, remote, remote, remote)
	if err != nil || !restored.record(id).SkipLibrarySync {
		t.Fatal("group not persisted", err)
	}
	blocked := true
	if err := s.AdminUpdate(ctx, id, &blocked, "", false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authorize(ctx, "http://emby", embyID); err == nil {
		t.Fatal("excluded group bypassed playback revocation")
	}
	remote.failLibraries = false
	if err := s.SetLibrarySyncGroup(ctx, id, false); err != nil {
		t.Fatal(err)
	}
	if err := s.SyncUser(ctx, id); err != nil {
		t.Fatal(err)
	}
	policy := remote.users[embyID].Policy
	if policy["EnableAllFolders"] != false || !reflect.DeepEqual(policy["EnabledFolders"], []string{"guid-tv"}) {
		t.Fatal("default group not restored", policy)
	}
}

func TestLibraryExcludedGroupFailureKeepsManualFolders(t *testing.T) {
	ctx := context.Background()
	s, remote, id := memberFixture(t)
	memberScan(t, s, id)
	if err := s.SetLibrarySyncGroup(ctx, id, true); err != nil {
		t.Fatal(err)
	}
	embyID := s.record(id).EmbyID
	manual := []any{"private-library"}
	remote.users[embyID].Policy["EnabledFolders"] = manual
	remote.ignoreFolders = true
	if err := s.SyncUser(ctx, id); err == nil {
		t.Fatal("unexpected library change accepted")
	}
	if !reflect.DeepEqual(remote.users[embyID].Policy["EnabledFolders"], manual) {
		t.Fatal("failure handler replaced manual folders")
	}
	if _, err := s.Authorize(ctx, "http://emby", embyID); err == nil {
		t.Fatal("uncertain policy allowed playback")
	}
	remote.users[embyID].Policy["IsAdministrator"] = true
	if err := s.SetLibrarySyncGroup(ctx, id, false); err == nil {
		t.Fatal("administrator enrolled in default group")
	}
	if err := s.SetLibrarySyncGroup(ctx, "unknown", true); err == nil {
		t.Fatal("unknown user accepted")
	}
}
