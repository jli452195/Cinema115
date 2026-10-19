package storage

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"strm115/backend/internal/model"
)

func TestAnimeCatalogCrossSiteStatusSurvivesRestart(t *testing.T) {
	s, path := testStore(t)
	ctx := context.Background()
	entry := createAnimeEntry(t, s, animePtr("123"))
	sub := saveAnimeConfig(t, s, animeConfig(entry.ID))
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	for _, provider := range []string{"mikan", "anibt", "garden"} {
		if err := s.CacheAnimeCatalogLinks(ctx, provider, []AnimeCatalogLink{{"99", animePtr("123"), 100}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, provider := range []string{"mikan", "anibt", "garden"} {
		got, err := s.AnimeCatalogStatuses(ctx, provider, []string{"99", "99", "999"})
		if err != nil || len(got) != 2 || got[0].BangumiID == nil || *got[0].BangumiID != "123" || len(got[0].Subscriptions) != 1 || got[0].Subscriptions[0].ID != sub.ID || got[0].Subscriptions[0].Status != "paused" || len(got[1].Subscriptions) != 0 {
			t.Fatalf("%s: %+v %v", provider, got, err)
		}
	}
	unchanged, err := s.GetAnimeSubscription(ctx, sub.ID, false)
	if err != nil || !reflect.DeepEqual(unchanged, sub) {
		t.Fatal("mapping cache changed configuration", err)
	}
	// State is never persisted in the identity cache; changes/removal are live.
	if _, err := s.db.ExecContext(ctx, `UPDATE anime_subscriptions SET status='draft' WHERE id=?`, sub.ID); err != nil {
		t.Fatal(err)
	}
	got, err := s.AnimeCatalogStatuses(ctx, "garden", []string{"99"})
	if err != nil || got[0].Subscriptions[0].Status != "draft" {
		t.Fatal(got, err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE anime_subscriptions SET status='removed' WHERE id=?`, sub.ID); err != nil {
		t.Fatal(err)
	}
	got, err = s.AnimeCatalogStatuses(ctx, "mikan", []string{"99"})
	if err != nil || len(got[0].Subscriptions) != 0 || got[0].BangumiID == nil {
		t.Fatal(got, err)
	}
}

func TestAnimeCatalogMappingsReplaceOnlyOnNewEvidence(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	entry := createAnimeEntry(t, s, animePtr("123"))
	sub := saveAnimeConfig(t, s, animeConfig(entry.ID))
	cache := func(provider string, id *string, observed int64) {
		t.Helper()
		if err := s.CacheAnimeCatalogLinks(ctx, provider, []AnimeCatalogLink{{"99", id, observed}}); err != nil {
			t.Fatal(err)
		}
	}
	cache("mikan", animePtr("123"), 200)
	cache("garden", animePtr("456"), 300)
	cache("mikan", animePtr("456"), 100) // older cached response
	got, err := s.AnimeCatalogStatuses(ctx, "mikan", []string{"99"})
	if err != nil || len(got[0].Subscriptions) != 1 || got[0].Subscriptions[0].ID != sub.ID {
		t.Fatal(got, err)
	}
	got, err = s.AnimeCatalogStatuses(ctx, "garden", []string{"99"})
	if err != nil || len(got[0].Subscriptions) != 0 || *got[0].BangumiID != "456" {
		t.Fatal("provider collision", got, err)
	}
	cache("mikan", nil, 400) // a successful no-link or ambiguous-link read
	cache("mikan", animePtr("123"), 200)
	got, err = s.AnimeCatalogStatuses(ctx, "mikan", []string{"99"})
	if err != nil || got[0].BangumiID != nil || len(got[0].Subscriptions) != 0 {
		t.Fatal("stale link restored", got, err)
	}
	if err := s.CacheAnimeCatalogLinks(ctx, "mikan", []AnimeCatalogLink{{"99", animePtr("123"), 500}, {"bad", nil, 500}}); !errors.Is(err, ErrAnimeInvalid) {
		t.Fatal(err)
	}
	got, _ = s.AnimeCatalogStatuses(ctx, "mikan", []string{"99"})
	if got[0].BangumiID != nil {
		t.Fatal("invalid batch partially changed mapping")
	}
	for _, ids := range [][]string{nil, {"bad"}, make([]string, 201)} {
		if _, err := s.AnimeCatalogStatuses(ctx, "mikan", ids); !errors.Is(err, ErrAnimeInvalid) {
			t.Fatal(err)
		}
	}
	if _, err := s.AnimeCatalogStatuses(ctx, "other", []string{"99"}); !errors.Is(err, ErrAnimeInvalid) {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.AnimeCatalogStatuses(cancelled, "mikan", []string{"99"}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestAnimeCatalogExactSourceAndMultipleEditions(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	entry := createAnimeEntry(t, s, animePtr("123"))
	sub := saveAnimeConfig(t, s, animeConfig(entry.ID))
	got, err := s.AnimeCatalogStatuses(ctx, "mikan", []string{"2259"})
	if err != nil || len(got[0].Subscriptions) != 1 || got[0].BangumiID != nil {
		t.Fatal(got, err)
	}
	if err := s.CacheAnimeCatalogLinks(ctx, "mikan", []AnimeCatalogLink{{"2259", animePtr("999"), 200}}); err != nil {
		t.Fatal(err)
	}
	got, err = s.AnimeCatalogStatuses(ctx, "mikan", []string{"2259"})
	if err != nil || len(got[0].Subscriptions) != 0 {
		t.Fatal("old manual source overrides changed official identity", got, err)
	}
	other, err := s.SaveAnimeEntry(ctx, model.AnimeEntry{BangumiID: animePtr("123"), EditionKey: "alternate", Title: "另一版本", Metadata: model.AnimeMetadata{SchemaVersion: 1}})
	if err != nil {
		t.Fatal(err)
	}
	second := saveAnimeConfig(t, s, animeConfig(other.ID))
	if err := s.CacheAnimeCatalogLinks(ctx, "anibt", []AnimeCatalogLink{{"123", animePtr("123"), 200}}); err != nil {
		t.Fatal(err)
	}
	got, err = s.AnimeCatalogStatuses(ctx, "anibt", []string{"123"})
	if err != nil || len(got[0].Subscriptions) != 2 {
		t.Fatal(got, err)
	}
	page, err := s.ListAnimeSubscriptions(ctx, AnimeSubscriptionFilter{BangumiID: "123"})
	if err != nil || page.Total != 2 {
		t.Fatal(page, err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE anime_subscription_sources SET status='removed' WHERE subscription_id IN (?,?)`, sub.ID, second.ID); err != nil {
		t.Fatal(err)
	}
	got, err = s.AnimeCatalogStatuses(ctx, "mikan", []string{"2259"})
	if err != nil || len(got[0].Subscriptions) != 0 {
		t.Fatal("removed source matched", got, err)
	}
}
