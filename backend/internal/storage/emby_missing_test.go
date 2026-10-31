package storage

import (
	"context"
	"reflect"
	"sort"
	"testing"

	"strm115/backend/internal/model"
)

func TestEmbyMissingItemsReadSelectedIdentityAndIncompleteRows(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	items := []model.EmbyItem{
		{ID: "a", Type: "Series", TMDB: "10", LibraryID: "first"},
		{ID: "b", Type: "Series", TMDB: "10", LibraryID: "second"},
		{ID: "c", Type: "Series", TMDB: "20"},
		{ID: "a1", Type: "Episode", SeriesID: "a", Season: 1, Episode: 1},
		{ID: "a-broken", Type: "Episode", SeriesID: "a", Season: 1, Episode: -1},
		{ID: "b2", Type: "Episode", SeriesID: "b", Season: 1, Episode: 2},
		{ID: "c1", Type: "Episode", SeriesID: "c", Season: 1, Episode: 1},
		{ID: "orphan", Type: "Episode", SeriesID: "unknown", Season: 1, Episode: 2},
		{ID: "unassigned", Type: "Episode", Season: 1, Episode: -1},
		{ID: "bad-binding-a", Type: "Series", TMDB: "010"},
		{ID: "bad-binding-b", Type: "Series", TMDB: "010"},
		{ID: "movie", Type: "Movie"},
		{ID: "season", Type: "Season", SeriesID: "a", Season: 1},
	}
	if err := s.EmbyStage(ctx, "scope", "gen", items); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name      string
		selection []string
		want      []string
	}{
		{"same identity across libraries", []string{"a"}, []string{"a", "a-broken", "a1", "b", "b2"}},
		{"orphan source", []string{"unknown"}, []string{"orphan"}},
		{"unassigned source", []string{""}, []string{"unassigned"}},
		{"invalid TMDB is not shared identity", []string{"bad-binding-a"}, []string{"bad-binding-a"}},
		{"empty selection", []string{}, []string{}},
		{"initial full read", nil, []string{"a", "a-broken", "a1", "b", "b2", "bad-binding-a", "bad-binding-b", "c", "c1", "orphan", "unassigned"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows, err := s.EmbyMissingItems(ctx, "scope", "gen", tc.selection)
			if err != nil {
				t.Fatal(err)
			}
			got := make([]string, 0, len(rows))
			for _, item := range rows {
				got = append(got, item.ID)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("selected rows = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestEmbyMissingChangesTrackCommittedOldNewAndMetadata(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	if err := s.EmbyEnsure(ctx, "scope"); err != nil {
		t.Fatal(err)
	}
	if err := s.EmbyStage(ctx, "scope", "gen", []model.EmbyItem{
		{ID: "a", Type: "Series", TMDB: "10"},
		{ID: "b", Type: "Series", TMDB: "20"},
		{ID: "episode", Type: "Episode", SeriesID: "a", Season: 1, Episode: 2},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.EmbyPublish(ctx, "scope", "gen", 0); err != nil {
		t.Fatal(err)
	}
	event := func(id string) model.EmbyEvent {
		t.Helper()
		if err := s.EmbyEnqueue(ctx, "scope", id, "upsert"); err != nil {
			t.Fatal(err)
		}
		value, err := s.EmbyEventForItem(ctx, "scope", id)
		if err != nil || value == nil {
			t.Fatal(value, err)
		}
		return *value
	}
	revision, changed := s.EmbyMissingChanges("scope", "gen", 0)
	if revision != 0 || len(changed) != 0 {
		t.Fatal(revision, changed)
	}
	move := event("episode")
	if err := s.EmbyApplyEvent(ctx, "scope", "gen", move, []model.EmbyItem{{ID: "episode", Type: "Episode", SeriesID: "b", Season: 1, Episode: 2}}, false); err != nil {
		t.Fatal(err)
	}
	revision, changed = s.EmbyMissingChanges("scope", "gen", revision)
	sort.Strings(changed)
	if revision == 0 || !reflect.DeepEqual(changed, []string{"a", "b"}) {
		t.Fatal("episode reassignment lost old or new source", revision, changed)
	}
	// The consumed event cannot modify either rows or cache invalidation state.
	if err := s.EmbyApplyEvent(ctx, "scope", "gen", move, []model.EmbyItem{{ID: "episode", Type: "Episode", SeriesID: "a", Season: 1, Episode: 3}}, false); err != nil {
		t.Fatal(err)
	}
	if next, changed := s.EmbyMissingChanges("scope", "gen", revision); next != revision || len(changed) != 0 {
		t.Fatal("stale event dirtied report", next, changed)
	}
	// Fail after the index insert; rollback must not publish the source change.
	if _, err := s.db.ExecContext(ctx, `CREATE TRIGGER fail_missing_commit BEFORE UPDATE ON emby_state BEGIN SELECT RAISE(ABORT,'test rollback'); END`); err != nil {
		t.Fatal(err)
	}
	failed := event("episode")
	if err := s.EmbyApplyEvent(ctx, "scope", "gen", failed, []model.EmbyItem{{ID: "episode", Type: "Episode", SeriesID: "a", Season: 1, Episode: 4}}, false); err == nil {
		t.Fatal("failed index transaction unexpectedly succeeded")
	}
	if next, changed := s.EmbyMissingChanges("scope", "gen", revision); next != revision || len(changed) != 0 {
		t.Fatal("rolled-back change was published", next, changed)
	}
	if _, err := s.db.ExecContext(ctx, `DROP TRIGGER fail_missing_commit`); err != nil {
		t.Fatal(err)
	}
	rows, err := s.EmbyMissingItems(ctx, "scope", "gen", []string{"b"})
	if err != nil || len(rows) != 2 || rows[1].SeriesID != "b" || rows[1].Episode != 2 {
		t.Fatal("rollback changed published records", rows, err)
	}
	if err := s.EmbyApplyMetadataEvent(ctx, "scope", "gen", event("orphan"), []model.EmbyItem{{ID: "orphan", Type: "Episode", Season: 1, Episode: -1}}, "metadata incomplete"); err != nil {
		t.Fatal(err)
	}
	updated, changed := s.EmbyMissingChanges("scope", "gen", revision)
	if updated <= revision || !reflect.DeepEqual(changed, []string{""}) {
		t.Fatal("usable metadata update did not invalidate incomplete rows", updated, changed)
	}
	// Keeping the old cursor retains changes until the replacement report succeeds.
	if retry, changed := s.EmbyMissingChanges("scope", "gen", revision); retry != updated || !reflect.DeepEqual(changed, []string{""}) {
		t.Fatal("reading changes consumed them before report completion", retry, changed)
	}
	if reset, changed := s.EmbyMissingChanges("another-scope", "new-gen", updated); reset != 0 || len(changed) != 0 {
		t.Fatal("scope switch retained another report's change history", reset, changed)
	}
}

func TestEmbyMissingRemovalTracksAllDeletedSources(t *testing.T) {
	for _, tc := range []struct {
		name      string
		removedID string
	}{
		{"single episode", "episode"},
		{"season children", "season"},
		{"series children", "series"},
		{"whole library", "library"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := testStore(t)
			ctx := context.Background()
			if err := s.EmbyEnsure(ctx, "scope"); err != nil {
				t.Fatal(err)
			}
			items := []model.EmbyItem{
				{ID: "series", Type: "Series", LibraryID: "library"},
				{ID: "season", Type: "Season", SeriesID: "series", LibraryID: "library"},
				{ID: "episode", Type: "Episode", SeriesID: "series", ParentID: "season", LibraryID: "library", Season: 1, Episode: 2},
				{ID: "unrelated", Type: "Series", LibraryID: "other"},
				{ID: "other-episode", Type: "Episode", SeriesID: "unrelated", LibraryID: "other", Season: 1, Episode: 2},
			}
			if err := s.EmbyStage(ctx, "scope", "gen", items); err != nil {
				t.Fatal(err)
			}
			if err := s.EmbyPublish(ctx, "scope", "gen", 0); err != nil {
				t.Fatal(err)
			}
			s.EmbyMissingChanges("scope", "gen", 0)
			if err := s.EmbyEnqueue(ctx, "scope", tc.removedID, "delete"); err != nil {
				t.Fatal(err)
			}
			event, err := s.EmbyEventForItem(ctx, "scope", tc.removedID)
			if err != nil || event == nil {
				t.Fatal(event, err)
			}
			if err := s.EmbyApplyEvent(ctx, "scope", "gen", *event, nil, true); err != nil {
				t.Fatal(err)
			}
			revision, changed := s.EmbyMissingChanges("scope", "gen", 0)
			if revision == 0 || !reflect.DeepEqual(changed, []string{"series"}) {
				t.Fatal("removed row's owning series was lost", revision, changed)
			}
			rows, err := s.EmbyMissingItems(ctx, "scope", "gen", nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, item := range rows {
				if item.ID == "episode" {
					t.Fatal("expected episode was not deleted", item)
				}
			}
		})
	}
}

func TestEmbyMissingExclusionsAtomicAndServiceScoped(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	first := model.EmbyMissingExclusion{ID: "one", ExcludedAt: "2026-10-16T01:00:00Z", EmbyMissingSource: model.EmbyMissingSource{SeriesID: "series", TMDBID: "285479", Title: "凡人修仙传", EpisodeCount: 3}}
	if err := s.SaveEmbyMissingExclusions(ctx, "server-a", []model.EmbyMissingExclusion{first}); err != nil {
		t.Fatal(err)
	}
	second := first
	second.ID = "two"
	second.SeriesID = "another"
	if err := s.SaveEmbyMissingExclusions(ctx, "server-a", []model.EmbyMissingExclusion{second, first}); err == nil {
		t.Fatal("duplicate confirmation should fail")
	}
	items, err := s.EmbyMissingExclusions(ctx, "server-a")
	if err != nil || len(items) != 1 || items[0].ID != "one" {
		t.Fatal("partial exclusion transaction committed", items, err)
	}
	if removed, err := s.RestoreEmbyMissingExclusion(ctx, "server-b", "one"); err != nil || removed {
		t.Fatal("cross-service restore", removed, err)
	}
	// The same Series ID and old binding on another service is independent.
	second.SeriesID = "series"
	if err := s.SaveEmbyMissingExclusions(ctx, "server-b", []model.EmbyMissingExclusion{second}); err != nil {
		t.Fatal(err)
	}
	if removed, err := s.RestoreEmbyMissingExclusion(ctx, "server-a", "one"); err != nil || !removed {
		t.Fatal(removed, err)
	}
	items, err = s.EmbyMissingExclusions(ctx, "server-b")
	if err != nil || len(items) != 1 {
		t.Fatal(items, err)
	}
}
