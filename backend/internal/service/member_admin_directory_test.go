package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"strm115/backend/internal/client/emby"
)

func TestDirectoryEnrollsAdministratorsWithoutDuplicatePlaybackRecords(t *testing.T) {
	s, remote, owner := memberFixture(t)
	id := strings.Repeat("f", 32)
	remote.users[id] = emby.ManagedUser{ID: id, Name: "administrator", Policy: map[string]any{"IsAdministrator": true, "IsDisabled": true}}
	promotedID := s.record(owner).EmbyID
	promoted := remote.users[promotedID]
	promoted.Policy["IsAdministrator"] = true
	remote.users[promotedID] = promoted
	for attempt := 0; attempt < 2; attempt++ {
		views, err := s.AdminMemberDirectory(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		counts := map[string]int{}
		for _, view := range views {
			if view.EmbyID != id && view.EmbyID != promotedID {
				continue
			}
			counts[view.EmbyID]++
			if !view.Administrator {
				t.Fatal("administrator classification lost")
			}
			if view.EmbyID == id && (!view.Disabled || view.EmbyName != "administrator") {
				t.Fatal("live administrator status lost")
			}
		}
		if counts[id] != 1 || counts[promotedID] != 1 {
			t.Fatal("missing or duplicate administrators", counts)
		}
	}
	if s.record(embyProfileID("server", id)).Source != "primary" {
		t.Fatal("administrator default source missing")
	}
	if _, err := s.db.PortalUser(context.Background(), embyProfileID("server", id)); err != nil {
		t.Fatal("administrator playback profile missing")
	}
}

type directoryAbsentError struct{ *deletionRemote }

func (f *directoryAbsentError) ManagedUserAbsent(context.Context, string) (bool, error) {
	return false, errors.New("lookup unavailable")
}

func TestDirectorySeparatesRemoteStateFromBusinessIdentity(t *testing.T) {
	for _, scenario := range []string{"active", "disabled", "deleted", "filtered", "lookup_error", "other_server", "same_name"} {
		t.Run(scenario, func(t *testing.T) {
			s, f, owner := memberFixture(t)
			remote := deletionClient(s, f)
			r := s.record(owner)
			original := f.users[r.EmbyID]
			want := MemberEmbyActive
			switch scenario {
			case "disabled":
				original.Policy["IsDisabled"] = true
				f.users[r.EmbyID] = original
				want = MemberEmbyDisabled
			case "deleted", "same_name":
				delete(f.users, r.EmbyID)
				want = MemberEmbyDeleted
				if scenario == "same_name" {
					original.ID = strings.Repeat("e", 32)
					f.users[original.ID] = original
				}
			case "filtered":
				remote.hideUser = true
				want = MemberEmbyMissing
			case "lookup_error":
				delete(f.users, r.EmbyID)
				s.NewEmby = func(string, string) memberEmbyClient { return &directoryAbsentError{remote} }
				want = MemberEmbyMissing
			case "other_server":
				r.ServerID = "previous-server"
				if err := s.save(context.Background(), r); err != nil {
					t.Fatal(err)
				}
				remote.hideUser = true
				want = MemberEmbyOtherServer
			}
			writes := f.policyWrites
			views, err := s.AdminMemberDirectory(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, view := range views {
				if view.UserID != owner {
					continue
				}
				found = true
				if view.EmbyState != want || view.Disabled != (want == MemberEmbyDisabled) || view.EmbyCheckedAt == "" {
					t.Fatalf("state = %s, disabled = %v, checked = %q; want %s", view.EmbyState, view.Disabled, view.EmbyCheckedAt, want)
				}
				if view.EmbyID != r.EmbyID {
					t.Fatal("old identity rebound")
				}
			}
			if !found || s.record(owner).UserID == "" {
				t.Fatal("local identity was discarded")
			}
			if f.policyWrites != writes || len(remote.deleted) != 0 {
				t.Fatal("directory read mutated remote users")
			}
			if scenario == "same_name" {
				newRecord := s.record(embyProfileID(r.ServerID, original.ID))
				if newRecord.UserID == owner || newRecord.AccountID != "" || newRecord.Source != "primary" {
					t.Fatal("same-name user inherited old association")
				}
			}
		})
	}
}

func TestDirectoryFailureDoesNotReportDeletion(t *testing.T) {
	s, f, owner := memberFixture(t)
	remote := deletionClient(s, f)
	remote.failList = true
	views, err := s.AdminMemberDirectory(context.Background())
	if err == nil || views != nil || s.record(owner).UserID == "" {
		t.Fatal("failed query produced a deletion observation")
	}
}
