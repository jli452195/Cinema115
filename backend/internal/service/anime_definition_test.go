package service

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"strm115/backend/internal/client/bangumi"
	"strm115/backend/internal/client/mikan"
	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
)

func TestAnimeDefinitionStoresRegularEpisodeCountForCompletion(t *testing.T) {
	for _, tc := range []struct {
		name     string
		regular  *int
		override *int
	}{
		{"12 regular plus OP and ED", bindingPtr(12), nil},
		{"unknown zero with 14 total chapters", bindingPtr(0), nil},
		{"unknown absent with 14 total chapters", nil, nil},
		{"manual override", bindingPtr(12), bindingPtr(10)},
		{"manual total when regular count unknown", nil, bindingPtr(10)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _, _, in := animeAdminFixture(t)
			ctx := context.Background()
			in.SettingsRevision = animeSaveReadySettings(t, s).Revision
			in.Config.Entry = AnimeEntryRef{BangumiID: bindingPtr("622288"), EditionKey: "original"}
			in.Config.Form = &model.AnimeForm{SchemaVersion: 1, TotalEpisodes: tc.override}
			s.Bindings.Subjects = &bindingSubjectsFake{result: &bangumi.SubjectResult{Subject: bangumi.Subject{
				Card: bangumi.Card{ID: "622288", Title: "FX战士久留美"}, Episodes: tc.regular, TotalEpisodes: bindingPtr(14),
			}}}
			receipt, err := s.SaveDefinition(ctx, "admin", "regular_episode_count", "", in)
			if err != nil {
				t.Fatal(err)
			}
			sub, err := s.Store.GetAnimeSubscription(ctx, receipt.ID, false)
			if err != nil {
				t.Fatal(err)
			}
			entry, err := s.Store.GetAnimeEntry(ctx, sub.EntryID)
			if err != nil {
				t.Fatal(err)
			}
			var want *int
			if tc.regular != nil && *tc.regular > 0 {
				want = tc.regular
			}
			if !reflect.DeepEqual(entry.Metadata.TotalEpisodes, want) || !reflect.DeepEqual(sub.Form.TotalEpisodes, tc.override) {
				t.Fatal("all chapters replaced regular count or manual override", entry.Metadata.TotalEpisodes, sub.Form.TotalEpisodes)
			}
			lifecycle, err := s.Store.AnimeLifecycle(ctx, sub, entry, time.Now().UnixMilli())
			if err != nil || (lifecycle.CompletionIssue == "TOTAL_UNKNOWN") != (want == nil && tc.override == nil) {
				t.Fatal("archive count disagrees with stored default and override", lifecycle, err)
			}
		})
	}
}

func TestAnimeDefinitionDerivesOneSourceRangeWithoutRSSReads(t *testing.T) {
	s, feed, in := newAnimeDefinitionService(t)
	in.Config.Form = &model.AnimeForm{SchemaVersion: 1, TotalEpisodes: bindingPtr(13)}
	in.Config.Sources[0].Rules.ChapterFirstEpisode = bindingPtr(0)
	in.Config.Sources[0].Rules.MinimumEpisode = bindingPtr(0)
	// Client range values are not a second independent configuration.
	in.Config.Sources[0].Binding.Scope = &model.AnimeScope{Kind: "range", First: bindingPtr(25), Last: bindingPtr(36)}
	reads := feed.reads
	out, err := s.SaveDefinition(context.Background(), "admin", "source_range_00_12", "", in)
	if err != nil {
		t.Fatal(err)
	}
	sub, err := s.Store.GetAnimeSubscription(context.Background(), out.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	scope := sub.Sources[0].Binding.Scope
	if scope.Kind != "range" || *scope.First != 0 || *scope.Last != 12 || feed.reads != reads {
		t.Fatal(scope, feed.reads, reads)
	}
}

func TestAnimeDefinitionArchivedEditAndNewSubtitleGroup(t *testing.T) {
	s, feed, in := newAnimeDefinitionService(t)
	ctx := context.Background()
	created, err := s.SaveDefinition(ctx, "admin", "archive_create_01", "", in)
	if err != nil {
		t.Fatal(err)
	}
	sub, _ := s.Store.GetAnimeSubscription(ctx, created.ID, false)
	archived, err := s.Store.ControlAnimeSubscription(ctx, sub.ID, "archive", sub.Revision, nil)
	if err != nil {
		t.Fatal(err)
	}
	main := animeSourceConfig(sub.Sources[0])
	in.ExpectedRevision = archived.Revision
	in.Config.Sources = []model.AnimeSourceConfig{main}
	in.Config.Naming.Sources[0].SourceRef = main.ID
	in.Config.Form = &model.AnimeForm{SchemaVersion: 1, TotalEpisodes: bindingPtr(12)}
	reads := feed.reads
	saved, err := s.SaveDefinition(ctx, "admin", "archive_edit_01", sub.ID, in)
	if err != nil || saved.Status != "archived" || feed.reads != reads {
		t.Fatal("ordinary edit resumed subscription or read RSS", saved, err)
	}
	unchanged, _ := s.Store.GetAnimeSource(ctx, main.ID)
	if !reflect.DeepEqual(unchanged, sub.Sources[0]) {
		t.Fatal("unchanged source lost execution state")
	}
	backup := main
	backup.ID, backup.Ref, backup.ExpectedRevision, backup.Role, backup.Position = "", "extra", 0, "standby", 1
	backup.GroupID = bindingPtr("28")
	in.Config.Sources = append(in.Config.Sources, backup)
	mapping := in.Config.Naming.Sources[0]
	mapping.SourceRef = backup.Ref
	in.Config.Naming.Sources = append(in.Config.Naming.Sources, mapping)
	in.ExpectedRevision = saved.Revision
	continued, err := s.SaveDefinition(ctx, "admin", "archive_new_group", sub.ID, in)
	if err != nil || continued.Status != "enabled" {
		t.Fatal("new group failed to resume atomically", continued, err)
	}
	current, _ := s.Store.GetAnimeSubscription(ctx, sub.ID, false)
	if len(current.Sources) != 2 || current.Sources[0].ID != main.ID {
		t.Fatal("sources were replaced or initialized during save", current)
	}
	again, err := s.SaveDefinition(ctx, "admin", "archive_new_group", sub.ID, in)
	if err != nil || !reflect.DeepEqual(again, continued) {
		t.Fatal("save replay changed sources", again, err)
	}
}

func TestAnimeDefinitionInitialReadUsesRealProviderAdapters(t *testing.T) {
	for _, tc := range []struct {
		provider, item, group string
		count                 int
	}{
		{"mikan", "2259", "45", 2},
		{"anibt", "630160", "lolihouse", 3},
		{"garden", "623249", "dmhy_f776", 2},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			s, _, directories, in := animeAdminFixture(t)
			in.SettingsRevision = animeSaveReadySettings(t, s).Revision
			anibt := NewAnibtService(&anibtFake{})
			anibt.spacing = 0
			garden := NewGardenService(&gardenFake{})
			garden.spacing = 0
			s.Bindings.Anibt, s.Bindings.Garden = anibt, garden
			source := &in.Config.Sources[0]
			source.Provider, source.ItemID, source.GroupID = tc.provider, &tc.item, &tc.group
			source.Binding.Scope = &model.AnimeScope{Kind: "feed"}
			source.Binding.ChapterCorrespondence = nil
			source.Binding.Review = &model.AnimeSourceReview{Method: "source_review", ItemID: tc.item, GroupID: tc.group}
			ctx := context.Background()
			receipt, err := s.SaveDefinition(ctx, "admin", "initial_provider_01", "", in)
			if err != nil {
				t.Fatal(err)
			}
			directoryReads := directories.calls
			reads := 0
			runner := AnimeDiscoveryRunner{Admin: s, Feeds: animeRSSFunc(func(ctx context.Context, budget *AnimeSourceBudget, input AnimeSourceInput) (AnimeSourceResult, error) {
				reads++
				return s.Bindings.ReadSource(ctx, budget, input)
			})}
			if err := runner.PollOnce(ctx); err != nil {
				t.Fatal(err)
			}
			sub, err := s.Store.GetAnimeSubscription(ctx, receipt.ID, false)
			if err != nil {
				t.Fatal(err)
			}
			if source := sub.Sources[0]; source.CheckedAt == nil || source.Health != "ok" || source.ErrorCode != nil {
				t.Fatalf("initial read failed: %+v", source)
			}
			page, err := s.Store.ListAnimeReleases(ctx, storage.AnimeReleaseFilter{SourceID: sub.Sources[0].ID, Page: 1, PageSize: 20})
			if err != nil || len(page.Items) != tc.count {
				t.Fatal("initial releases missing", len(page.Items), err)
			}
			if err := runner.PollOnce(ctx); err != nil || reads != 1 || directories.calls != directoryReads {
				t.Fatal("initialization repeated or read 115", reads, directories.calls, err)
			}
		})
	}
}

func TestAnimeDefinitionSaveWithoutPreviewAndWorkerInitialization(t *testing.T) {
	s, feed, old := newAnimeDefinitionService(t)
	s.Previews = nil // Saving cannot depend on the UI preview service.
	in := AnimeSaveInput{Config: old.Config, SettingsRevision: old.SettingsRevision}
	reads := feed.reads
	ctx := context.Background()
	receipt, err := s.SaveDefinition(ctx, "admin", "definition_save_01", "", in)
	if err != nil || receipt.Status != "enabled" || receipt.RequiresRepreview || feed.reads != reads {
		t.Fatal("pure save failed or read RSS", receipt, err, feed.reads, reads)
	}
	sub, err := s.Store.GetAnimeSubscription(ctx, receipt.ID, false)
	if err != nil || sub.FirstEnabledAt == nil {
		t.Fatal("save created execution baseline", sub, err)
	}
	now := time.Now().Add(time.Second)
	checks := 0
	runner := AnimeDiscoveryRunner{Admin: s, now: func() time.Time { return now }, Feeds: animeRSSFunc(func(_ context.Context, _ *AnimeSourceBudget, input AnimeSourceInput) (AnimeSourceResult, error) {
		if checks == 0 && (input.Kind != "history" || input.Take != 2000) || checks > 0 && input.Kind != "rss" {
			t.Fatal("history must initialize once, then poll RSS", checks, input.Kind, input.Take)
		}
		page, err := feed.ReadSource(ctx, mikan.SourceRequest{Source: input.Source, Kind: input.Kind, Take: input.Take})
		page.ObservedAt = now
		if checks > 0 {
			page.Completeness, page.StopReason = "partial", "rss_window"
			page.Releases = append(page.Releases, mikan.SourceRelease{PostKey: "0000000000000000000000000000000000000003", Title: "[组] 动画 [32][1080p]", PublishedRaw: now.Add(-time.Second).Format(time.RFC3339Nano)})
			page.RawCount++
		}
		checks++
		return AnimeSourceResult{SourcePage: page}, err
	})}
	if err := runner.PollOnce(ctx); err != nil {
		t.Fatal(err)
	}
	after, err := s.Store.GetAnimeSubscription(ctx, receipt.ID, false)
	if err != nil || after.Sources[0].CheckedAt == nil {
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
	// A fresh worker has no in-memory initialization state. It must still
	// resume the persisted RSS schedule and discover only the new episode.
	runner = AnimeDiscoveryRunner{Admin: s, now: func() time.Time { return now }, Feeds: runner.Feeds}
	now = now.Add(16 * time.Minute)
	if err := runner.PollOnce(ctx); err != nil {
		t.Fatal(err)
	}
	page, err = s.Store.ListAnimeReleases(ctx, storage.AnimeReleaseFilter{SourceID: after.Sources[0].ID, Page: 1, PageSize: 20})
	if err != nil || len(page.Items) != 3 || checks != 2 {
		t.Fatal("worker restart lost or repeated history", len(page.Items), checks, err)
	}
	for _, row := range page.Items {
		if row.Decision.Disposition != "selected" || row.Decision.Generation != 1 {
			t.Fatal("new release was missed or an existing selection repeated", row.Decision)
		}
	}
}

func TestAnimeDefinitionInitializationFailurePauseAndEdit(t *testing.T) {
	s, _, old := newAnimeDefinitionService(t)
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
