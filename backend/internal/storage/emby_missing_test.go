package storage

import (
	"context"
	"testing"

	"strm115/backend/internal/model"
)

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
