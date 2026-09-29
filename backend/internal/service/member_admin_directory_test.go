package service

import (
	"context"
	"strings"
	"testing"

	"strm115/backend/internal/client/emby"
)

func TestDirectoryDisplaysAdministratorsWithoutCreatingPlaybackRecords(t *testing.T) {
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
			if !view.Administrator || !view.SkipLibrarySync {
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
	if s.record(embyProfileID("server", id)).UserID != "" {
		t.Fatal("administrator enrolled in playback")
	}
	if _, err := s.db.PortalUser(context.Background(), embyProfileID("server", id)); err == nil {
		t.Fatal("administrator profile persisted")
	}
}
