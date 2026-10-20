package storage

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"strm115/backend/internal/animepreview"
	"strm115/backend/internal/model"
)

func animeStartFixture(t *testing.T) (*Store, model.AnimeSubscriptionConfig, AnimeConfigGuard, animepreview.Config, []AnimeActivationSource, model.AnimeDownloadTarget) {
	t.Helper()
	s, target := animeDownloadTargetFixture(t)
	expected := previewConfig(t, s)
	expected.Sources[0].RunWhenEnabled = true
	entry, err := s.GetAnimeEntry(context.Background(), expected.Chapter.ID)
	if err != nil {
		t.Fatal(err)
	}
	in := model.AnimeSubscriptionConfig{EntryID: entry.ID, Status: "paused", Form: model.AnimeForm{SchemaVersion: 1}, Sources: expected.Sources}
	guard := AnimeConfigGuard{Entry: entry, Settings: expected.Settings, RequireAccount: true}
	now := time.Now().UnixMilli()
	b := createPreview(t, s, expected, previewPage(2, now-1000), now)
	refs := []AnimeActivationSource{{AnimePreviewRef: previewRef(b, 0), SelectedIDs: []string{b.Sources[0].Items[0].Release.ID}, DismissedIDs: []string{b.Sources[0].Items[1].Release.ID}}}
	return s, in, guard, expected, refs, target
}

func TestAnimeStartAtomicChoicesReplayAndPause(t *testing.T) {
	s, in, guard, expected, refs, target := animeStartFixture(t)
	ctx := context.Background()
	key := activationKey("start_once_01")
	out, err := s.StartAnimeConfiguration(ctx, in, key, guard, expected, refs, target)
	if err != nil || out.Status != "enabled" || out.RequiresRepreview {
		t.Fatal(out, err)
	}
	sub, err := s.GetAnimeSubscription(ctx, out.ID, false)
	if err != nil || sub.FirstEnabledAt == nil || sub.Sources[0].NextCheckAt == nil {
		t.Fatal(sub, err)
	}
	for i, state := range []string{"selected", "dismissed"} {
		rid := refs[0].SelectedIDs[0]
		if i == 1 {
			rid = refs[0].DismissedIDs[0]
		}
		d, err := s.GetAnimeReleaseDecision(ctx, sub.Sources[0].ID, rid)
		if err != nil || d.Disposition != state {
			t.Fatal(d, err)
		}
	}
	again, err := s.StartAnimeConfiguration(ctx, in, key, guard, expected, refs, target)
	if err != nil || !reflect.DeepEqual(out, again) {
		t.Fatal(again, err)
	}
	if _, err := s.db.Exec(`UPDATE anime_settings SET revision=revision+1`); err != nil {
		t.Fatal(err)
	}
	if replay, err := s.StartAnimeConfiguration(ctx, in, key, guard, expected, refs, target); err != nil || !reflect.DeepEqual(replay, out) {
		t.Fatal("replay consulted changed settings", replay, err)
	}
	key.Digest = "b" + key.Digest[1:]
	if _, err := s.StartAnimeConfiguration(ctx, in, key, guard, expected, refs, target); !errors.Is(err, ErrAnimeIdempotency) {
		t.Fatal(err)
	}
	controlAnime(t, s, out.ID, "pause")
}

func TestAnimeStartRejectsInvalidPreviewWithoutPartialSubscription(t *testing.T) {
	for _, kind := range []string{"expired", "actor", "load", "scope", "rules", "selected", "duplicate", "missing", "settings", "write_failure"} {
		t.Run(kind, func(t *testing.T) {
			s, in, guard, expected, refs, target := animeStartFixture(t)
			key := activationKey("start_invalid_01")
			switch kind {
			case "expired":
				_, err := s.db.Exec(`UPDATE anime_source_previews SET snapshot_json=json_set(snapshot_json,'$.expiresAt',1)`)
				if err != nil {
					t.Fatal(err)
				}
			case "actor":
				key.ActorKey = "other-admin"
			case "load":
				refs[0].LoadRevision++
			case "scope":
				expected.Chapter.EditionKey = "other"
			case "rules":
				expected.Sources[0].Rules.MinimumEpisode = animePtr(100)
			case "selected":
				refs[0].SelectedIDs = []string{"foreign-release"}
			case "duplicate":
				refs = append(refs, refs[0])
			case "missing":
				refs = nil
			case "settings":
				if _, err := s.db.Exec(`UPDATE anime_settings SET revision=revision+1`); err != nil {
					t.Fatal(err)
				}
			case "write_failure":
				if _, err := s.db.Exec(`CREATE TRIGGER fail_start BEFORE INSERT ON anime_release_decisions BEGIN SELECT RAISE(ABORT,'synthetic failure'); END`); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.StartAnimeConfiguration(context.Background(), in, key, guard, expected, refs, target); err == nil {
				t.Fatal("invalid start committed")
			}
			for _, table := range []string{"anime_subscriptions", "anime_subscription_sources", "anime_source_baselines", "anime_release_decisions", "anime_request_keys"} {
				var count int
				// The fixture creates settings receipts, so only the start key is checked.
				query := `SELECT count(*) FROM ` + table
				if table == "anime_request_keys" {
					query += ` WHERE request_key='start_invalid_01'`
				}
				if err := s.db.QueryRow(query).Scan(&count); err != nil || count != 0 {
					t.Fatal(table, count, err)
				}
			}
		})
	}
}

func TestAnimeStartZeroHistoryAndIndependentVersions(t *testing.T) {
	for _, count := range []int{0, 2} {
		t.Run(string(rune('0'+count)), func(t *testing.T) {
			s, in, guard, expected, _, target := animeStartFixture(t)
			now := time.Now().UnixMilli()
			page := previewPage(count, now-1000)
			if count == 2 {
				page.Releases[1].Title = "[other group] anime [30][1080p][HEVC] v2"
			}
			b := createPreview(t, s, expected, page, now)
			ids := make([]string, 0, count)
			for _, item := range b.Sources[0].Items {
				ids = append(ids, item.Release.ID)
			}
			refs := []AnimeActivationSource{{AnimePreviewRef: previewRef(b, 0), SelectedIDs: ids, DismissedIDs: []string{}}}
			out, err := s.StartAnimeConfiguration(context.Background(), in, activationKey("start_history_01"), guard, expected, refs, target)
			if err != nil || out.Status != "enabled" {
				t.Fatal(out, err)
			}
			var selected int
			if err := s.db.QueryRow(`SELECT count(*) FROM anime_release_decisions WHERE disposition='selected'`).Scan(&selected); err != nil || selected != count {
				t.Fatal(selected, err)
			}
		})
	}
}

func TestAnimeStartMultipleSourcesReuseIndependentBatches(t *testing.T) {
	s, in, guard, expected, refs, target := animeStartFixture(t)
	standby := in.Sources[0]
	standby.Ref, standby.Role, standby.Position, standby.GroupID = "backup", "standby", 1, animePtr("46")
	in.Sources = append(in.Sources, standby)
	expected.Sources = in.Sources
	now := time.Now().UnixMilli()
	page := previewPage(2, now-1000)
	page.Source.GroupID = "46"
	b, err := s.CreateAnimePreview(context.Background(), "admin-a", expected, []AnimePreviewSeed{{SourceRef: "backup", Page: &page}}, now)
	if err != nil {
		t.Fatal(err)
	}
	refs = append(refs, AnimeActivationSource{AnimePreviewRef: previewRef(b, 0), SelectedIDs: []string{b.Sources[0].Items[0].Release.ID}, DismissedIDs: []string{}})
	out, err := s.StartAnimeConfiguration(context.Background(), in, activationKey("start_two_sources"), guard, expected, refs, target)
	if err != nil {
		t.Fatal(err)
	}
	sub, err := s.GetAnimeSubscription(context.Background(), out.ID, false)
	if err != nil || len(sub.Sources) != 2 {
		t.Fatal(sub, err)
	}
	for _, source := range sub.Sources {
		if source.Status != "enabled" || source.ActiveBaselineID == nil {
			t.Fatal(source)
		}
	}
}
