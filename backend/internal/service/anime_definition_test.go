package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"strm115/backend/internal/client/mikan"
	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
)

func TestAnimeDefinitionSaveWithoutPreviewAndWorkerInitialization(t *testing.T) {
	s, feed, old := newAnimeStartService(t)
	s.Previews = nil // Saving cannot depend on the UI preview service.
	in := AnimeSaveInput{Config: old.Config, SettingsRevision: old.SettingsRevision}
	reads := feed.reads
	ctx := context.Background()
	receipt, err := s.SaveDefinition(ctx, "admin", "definition_save_01", "", in)
	if err != nil || receipt.Status != "enabled" || receipt.RequiresRepreview || feed.reads != reads {
		t.Fatal("pure save failed or read RSS", receipt, err, feed.reads, reads)
	}
	sub, err := s.Store.GetAnimeSubscription(ctx, receipt.ID, false)
	if err != nil || sub.FirstEnabledAt == nil || sub.Sources[0].ActiveBaselineID != nil {
		t.Fatal("save created execution baseline", sub, err)
	}
	now := time.Now().Add(time.Second)
	runner := AnimeDiscoveryRunner{Admin: s, now: func() time.Time { return now }, Feeds: animeRSSFunc(func(_ context.Context, _ *AnimeSourceBudget, input AnimeSourceInput) (AnimeSourceResult, error) {
		if input.Kind != "history" || input.Take != 2000 {
			t.Fatal("first discovery did not read bounded history", input.Kind, input.Take)
		}
		page, err := feed.ReadSource(ctx, mikan.SourceRequest{Source: input.Source, Kind: input.Kind, Take: input.Take})
		return AnimeSourceResult{SourcePage: page}, err
	})}
	if err := runner.PollOnce(ctx); err != nil {
		t.Fatal(err)
	}
	after, err := s.Store.GetAnimeSubscription(ctx, receipt.ID, false)
	if err != nil || after.Sources[0].ActiveBaselineID == nil || after.Sources[0].CheckedAt == nil {
		t.Fatal("worker did not initialize", err)
	}
	page, err := s.Store.ListAnimeReleases(ctx, storage.AnimeReleaseFilter{SourceID: after.Sources[0].ID, Page: 1, PageSize: 20})
	if err != nil || len(page.Items) != 2 {
		t.Fatal(page, err)
	}
	for _, row := range page.Items {
		if row.Decision.Disposition != "selected" {
			t.Fatal("history not selected by saved policy", row.Decision)
		}
	}
	if err := runner.PollOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if feed.reads != reads+1 {
		t.Fatal("completed initialization repeated", feed.reads, reads)
	}
	// Lost save responses replay locally, without resetting the baseline.
	again, err := s.SaveDefinition(ctx, "admin", "definition_save_01", "", in)
	if err != nil || again.ID != receipt.ID || again.Revision != receipt.Revision || feed.reads != reads+1 {
		t.Fatal(again, err)
	}
}

func TestAnimeDefinitionInitializationFailurePauseAndEdit(t *testing.T) {
	s, _, old := newAnimeStartService(t)
	in := AnimeSaveInput{Config: old.Config, SettingsRevision: old.SettingsRevision}
	ctx := context.Background()
	receipt, err := s.SaveDefinition(ctx, "admin", "definition_failure_01", "", in)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Add(time.Second)
	reads := 0
	r := AnimeDiscoveryRunner{Admin: s, now: func() time.Time { return now }, Feeds: animeRSSFunc(func(context.Context, *AnimeSourceBudget, AnimeSourceInput) (AnimeSourceResult, error) {
		reads++
		return AnimeSourceResult{}, errors.New("temporary metadata failure")
	})}
	if err := r.PollOnce(ctx); err != nil {
		t.Fatal(err)
	}
	sub, err := s.Store.GetAnimeSubscription(ctx, receipt.ID, false)
	if err != nil || sub.Status != "enabled" || sub.Sources[0].NextCheckAt == nil || *sub.Sources[0].NextCheckAt <= now.UnixMilli() {
		t.Fatal("failure lost definition/backoff", sub, err)
	}
	if err := r.PollOnce(ctx); err != nil || reads != 1 {
		t.Fatal("retry ignored cooldown", reads, err)
	}
	paused, err := s.Store.ControlAnimeSubscription(ctx, sub.ID, "pause", sub.Revision, nil)
	if err != nil {
		t.Fatal(err)
	}
	source := animeSourceConfig(sub.Sources[0])
	source.Status = "paused"
	source.Rules.MinimumEpisode = bindingPtr(25)
	in.ExpectedRevision = paused.Revision
	in.Config.Sources = []model.AnimeSourceConfig{source}
	in.Config.Naming.Sources[0].SourceRef = source.ID
	out, err := s.SaveDefinition(ctx, "admin", "definition_edit_01", sub.ID, in)
	if err != nil || out.Status != "paused" {
		t.Fatal("editing resumed paused subscription", out, err)
	}
	if err := r.PollOnce(ctx); err != nil || reads != 1 {
		t.Fatal("paused edit read feed", reads, err)
	}
	if _, err := s.Store.ControlAnimeSubscription(ctx, sub.ID, "resume", out.Revision, nil); err != nil {
		t.Fatal("edited subscription cannot resume", err)
	}
}
