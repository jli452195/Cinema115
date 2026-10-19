package storage

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/animeresource"
	"strm115/backend/internal/client/animefeed"
	"strm115/backend/internal/model"
)

func activationKey(key string) AnimeWriteKey {
	return AnimeWriteKey{ActorKey: "admin-a", Operation: "enable", Key: key, Digest: strings.Repeat("a", 64)}
}

func activationFixture(t *testing.T) (*Store, model.AnimeSubscription, model.AnimeDownloadTarget, []AnimeActivationSource) {
	t.Helper()
	s, target := animeDownloadTargetFixture(t)
	e := createAnimeEntry(t, s, nil)
	c := animeConfig(e.ID)
	c.Sources[0].Rules.Include = []string{"hevc"}
	c.Sources[0].Provider, c.Sources[0].ItemID, c.Sources[0].GroupID = "garden", animePtr("123"), animePtr("dmhy_f45")
	backup := c.Sources[0]
	backup.Ref, backup.Role, backup.Position, backup.GroupID = "backup", "standby", 1, animePtr("mikan_f45")
	c.Sources = append(c.Sources, backup)
	sub := saveAnimeConfig(t, s, c)
	settings, err := s.GetAnimeSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	config := ActivationConfig(sub, e, settings)
	refs := make([]AnimeActivationSource, 0, 2)
	for _, source := range sub.Sources {
		now := time.Now().UnixMilli()
		page := animefeed.SourcePage{Kind: "history", Source: animefeed.NewSource(source.Provider, *source.ItemID, *source.GroupID), ObservedAt: time.UnixMilli(now - 1000), RawCount: 2, RequestedTake: 50, Completeness: "visible_end", Releases: []animefeed.SourceRelease{
			{PostKey: "rel_v1", Title: "[组] 动画 - 30 [1080p][HEVC]", MagnetHint: "magnet:?xt=urn:btih:" + strings.Repeat("a", 40)},
			{PostKey: "rel_v2", Title: "[组] 动画 - 30 [1080p][HEVC] v2", MagnetHint: "magnet:?xt=urn:btih:" + strings.Repeat("b", 40)},
		}}
		b, err := s.CreateAnimePreview(context.Background(), "admin-a", config, []AnimePreviewSeed{{SourceRef: source.ID, Page: &page}}, now)
		if err != nil {
			t.Fatal(err)
		}
		refs = append(refs, AnimeActivationSource{AnimePreviewRef: previewRef(b, 0), SelectedIDs: []string{b.Sources[0].Items[0].Release.ID, b.Sources[0].Items[1].Release.ID}, DismissedIDs: []string{}})
	}
	return s, sub, target, refs
}

func TestAnimeActivationRealPreviewClaimVersionsAndResume(t *testing.T) {
	s, sub, target, refs := activationFixture(t)
	ctx := context.Background()
	key := activationKey("activation_01")
	receipt, err := s.EnableAnimeSubscription(ctx, sub.ID, sub.Revision, target, refs, key)
	if err != nil || receipt.Status != "enabled" || receipt.Revision != 2 || len(receipt.ResumedSourceIDs) != 2 {
		t.Fatal(receipt, err)
	}
	again, err := s.EnableAnimeSubscription(ctx, sub.ID, sub.Revision, target, refs, key)
	if err != nil || !reflect.DeepEqual(receipt, again) {
		t.Fatal("receipt changed", again, err)
	}
	var count int
	if err := s.db.QueryRow(`SELECT count(*) FROM anime_source_baselines`).Scan(&count); err != nil || count != 2 {
		t.Fatal(count, err)
	}
	sub, err = s.GetAnimeSubscription(ctx, sub.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	for i, source := range sub.Sources {
		b, err := s.GetAnimePreview(ctx, "admin-a", refs[i].BatchID)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range b.Sources[0].Items {
			d, err := s.GetAnimeReleaseDecision(ctx, source.ID, item.Release.ID)
			if err != nil {
				t.Fatal(err)
			}
			if d.Disposition != "selected" || d.SourceRevision != source.Revision || d.Snapshot.Selection.Kind != "initial_history" {
				t.Fatal(d)
			}
			v, err := animeresource.Verify(ctx, "garden", item.Observation, nil)
			if err != nil {
				t.Fatal(err)
			}
			result, err := s.ClaimAnimeDownload(ctx, source.ID, item.Release.ID, d.Generation, v, target)
			if err != nil {
				t.Fatal(err)
			}
			if result.Created != (i == 0) {
				t.Fatal("same hash duplicated across sources", result.Created)
			}
		}
	}
	jobs, err := s.ListAnimeDownloadJobs(ctx, sub.ID, 1, 20)
	if err != nil || jobs.Total != 2 {
		t.Fatal(jobs.Total, err)
	}
	stats, err := s.AnimeJobCountsBySource(ctx, sub.ID)
	if err != nil || stats[""].Total != 2 || stats[sub.Sources[0].ID].Total != 2 {
		t.Fatal(stats, err)
	}
	controlAnime(t, s, sub.ID, "pause")
	controlAnime(t, s, sub.ID, "resume")
	got, err := s.GetAnimeSubscription(ctx, sub.ID, false)
	if err != nil || got.Sources[0].ActiveBaselineID == nil || *got.Sources[0].ActiveBaselineID != *sub.Sources[0].ActiveBaselineID {
		t.Fatal(got, err)
	}
	if _, err := s.EnableAnimeSubscription(ctx, sub.ID, got.Revision, target, refs, activationKey("second_enable")); !errors.Is(err, ErrAnimeTransition) {
		t.Fatal(err)
	}
}

func TestAnimeActivationRejectsStaleOrImplicitSelectionsAtomically(t *testing.T) {
	for _, kind := range []string{"revision", "load", "expired", "actor", "missing_source", "extra_source", "unknown_release", "unselectable", "settings", "account", "failure"} {
		t.Run(kind, func(t *testing.T) {
			s, sub, target, refs := activationFixture(t)
			key := activationKey("activation_bad")
			revision := sub.Revision
			switch kind {
			case "revision":
				revision++
			case "load":
				refs[0].LoadRevision++
			case "expired":
				if _, err := s.db.Exec(`UPDATE anime_source_previews SET snapshot_json=json_set(snapshot_json,'$.expiresAt',1)`); err != nil {
					t.Fatal(err)
				}
			case "actor":
				key.ActorKey = "other-admin"
			case "missing_source":
				refs = refs[:1]
			case "extra_source":
				extra := refs[0]
				extra.SourceRef = "extra_source"
				refs = append(refs, extra)
			case "unknown_release":
				refs[0].SelectedIDs = []string{"not_loaded"}
			case "unselectable":
				if _, err := s.db.Exec(`UPDATE anime_preview_items SET snapshot_json=json_set(snapshot_json,'$.selectable',json('false'))`); err != nil {
					t.Fatal(err)
				}
			case "settings":
				if _, err := s.db.Exec(`UPDATE anime_settings SET revision=revision+1`); err != nil {
					t.Fatal(err)
				}
			case "account":
				if _, err := s.db.Exec(`UPDATE p115_accounts SET revision=revision+1 WHERE is_primary=1`); err != nil {
					t.Fatal(err)
				}
			case "failure":
				if _, err := s.db.Exec(`CREATE TRIGGER activation_receipt_failure BEFORE INSERT ON anime_request_keys BEGIN SELECT RAISE(ABORT,'simulated receipt failure'); END`); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.EnableAnimeSubscription(context.Background(), sub.ID, revision, target, refs, key); err == nil {
				t.Fatal("invalid activation accepted")
			}
			var baselines, decisions int
			if err := s.db.QueryRow(`SELECT count(*) FROM anime_source_baselines`).Scan(&baselines); err != nil {
				t.Fatal(err)
			}
			if err := s.db.QueryRow(`SELECT count(*) FROM anime_release_decisions`).Scan(&decisions); err != nil {
				t.Fatal(err)
			}
			got, err := s.GetAnimeSubscription(context.Background(), sub.ID, false)
			if err != nil || baselines != 0 || decisions != 0 || got.Status != "paused" || got.FirstEnabledAt != nil || got.Revision != sub.Revision {
				t.Fatal("partial activation escaped", got, baselines, decisions, err)
			}
		})
	}
}

func TestAnimeActivationSkippedHistoryNeverSelectedByPoll(t *testing.T) {
	s, sub, target, refs := activationFixture(t)
	ctx := context.Background()
	for i := range refs {
		refs[i].SelectedIDs = []string{}
	}
	if _, err := s.EnableAnimeSubscription(ctx, sub.ID, sub.Revision, target, refs, activationKey("activate_wait")); err != nil {
		t.Fatal(err)
	}
	poll := discoveryPoll(t, s, time.Now().UnixMilli()+int64(20*time.Minute/time.Millisecond))
	b, err := s.GetAnimePreview(ctx, "admin-a", refs[0].BatchID)
	if err != nil {
		t.Fatal(err)
	}
	rows := append([]animefeed.SourceRelease{}, b.Sources[0].Page.Releases...)
	stats, err := s.CompleteAnimeSourcePoll(ctx, poll, discoveryPage(poll, poll.T0+int64(time.Minute/time.Millisecond), rows...), time.Now().UnixMilli()+int64(20*time.Minute/time.Millisecond))
	if err != nil || stats.Selected != 0 {
		t.Fatal(stats, err)
	}
	counts, err := s.AnimeDecisionCountsBySource(ctx, sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, count := range counts {
		if count["history_skipped"] != 2 || count["selected"] != 0 {
			t.Fatal(count)
		}
	}
}
