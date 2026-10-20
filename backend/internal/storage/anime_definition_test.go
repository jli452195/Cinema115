package storage

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/model"
)

func editAnimeDefinition(t *testing.T, f *downloadClaimFixture, edit func(*model.AnimeSubscriptionConfig)) model.AnimeConfigReceipt {
	t.Helper()
	if _, err := f.store.db.Exec(`UPDATE anime_settings SET organize_cid='900',organize_path='/organized' WHERE organize_cid IS NULL`); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	sub, err := f.store.GetAnimeSubscription(ctx, f.sub.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := f.store.GetAnimeEntry(ctx, sub.EntryID)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := f.store.GetAnimeSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	c := model.AnimeSubscriptionConfig{ID: sub.ID, EntryID: sub.EntryID, ExpectedRevision: sub.Revision, Status: sub.Status, Naming: sub.Naming, Form: model.AnimeForm{SchemaVersion: 1}}
	for _, source := range sub.Sources {
		c.Sources = append(c.Sources, sourceEdit(source, source.ID))
	}
	edit(&c)
	key := animeAdminKey("definition_edit_" + string(rune('a'+sub.Revision)))
	out, err := f.store.SaveAnimeConfiguration(ctx, c, key, AnimeConfigGuard{Entry: entry, Settings: settings, RequireAccount: true, Runtime: true})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestAnimeDefinitionRulesReevaluateAndFenceOldPoll(t *testing.T) {
	f, now := discoveryFixture(t)
	old := discoveryPoll(t, f.store, now)
	otherID := f.sub.Sources[0].ID
	if otherID == old.Source.ID {
		otherID = f.sub.Sources[1].ID
	}
	other, err := f.store.GetAnimeSource(context.Background(), otherID)
	if err != nil {
		t.Fatal(err)
	}
	out := editAnimeDefinition(t, f, func(c *model.AnimeSubscriptionConfig) {
		for _, source := range c.Sources {
			if source.ID == old.Source.ID {
				c.Sources = []model.AnimeSourceConfig{source}
				break
			}
		}
		c.Sources[0].Rules.MinimumEpisode = animePtr(25)
	})
	if out.Status != "enabled" {
		t.Fatal(out)
	}
	unchanged, _ := f.store.GetAnimeSource(context.Background(), other.ID)
	if !reflect.DeepEqual(other, unchanged) {
		t.Fatal("unaffected source changed")
	}
	if _, err := f.store.db.Exec(`UPDATE anime_subscription_sources SET next_check_at=? WHERE id=?`, now+3600000, otherID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.CompleteAnimeSourcePoll(context.Background(), old, discoveryPage(old, now, discoveryRow("rel_old_worker", now)), now); !errors.Is(err, ErrAnimeConflict) {
		t.Fatal("old poll survived edit", err)
	}
	p := discoveryPoll(t, f.store, now+1000)
	if p.Source.ID != old.Source.ID || !p.Definition || !p.Initialize || p.T0 != old.T0 {
		t.Fatal("edit lost history boundary", p.Source.ID, p.T0, old.T0)
	}
	row := discoveryRow("rel_history", p.T0-100000)
	row.Title = "[字幕组] 动画 - 25 [1080p][HEVC]"
	stats, err := f.store.CompleteAnimeSourcePoll(context.Background(), p, discoveryPage(p, now+1000, row), now+1000)
	if err != nil || stats.Selected != 1 {
		t.Fatal("lowered minimum did not backfill", stats, err)
	}
}

func TestAnimeDefinitionReauthorizesSameQueuedResourceAndKeepsOtherSources(t *testing.T) {
	f, job := animeSubmitStoreFixture(t)
	if _, err := f.store.db.Exec(`UPDATE anime_settings SET revision=revision+1,organize_cid='900',organize_path='/organized'`); err != nil {
		t.Fatal(err)
	}
	// Give this test authoritative, initialized baselines.
	raw, _ := animeJSON(model.AnimeExecutionBaseline{SchemaVersion: 1, Target: f.target, RuleVersion: 1, ParserVersion: job.Snapshot.Release.ParserVersion, TimeVersion: job.Snapshot.Release.TimeVersion})
	if _, err := f.store.db.Exec(`UPDATE anime_source_baselines SET snapshot_json=?,t0=1`, raw); err != nil {
		t.Fatal(err)
	}
	editAnimeDefinition(t, f, func(c *model.AnimeSubscriptionConfig) {
		c.Sources = c.Sources[:1]
		c.Sources[0].Rules.MinimumEpisode = animePtr(25)
	})
	if _, err := f.store.PrepareAnimeSubmit(context.Background(), job.ID, job.Generation, f.target); !errors.Is(err, ErrAnimeWriteUnauthorized) {
		t.Fatal("stale queued task submitted", err)
	}
	p := discoveryPoll(t, f.store, job.CreatedAt+1000)
	row := discoveryRow("rel_dmhy_1", job.CreatedAt)
	row.Title = job.Snapshot.Release.Title
	row.OriginalGUID = "shared-guid"
	row.PublishedRaw = ""
	row.MagnetHint = "magnet:?xt=urn:btih:" + strings.Repeat("a", 40)
	newer := discoveryRow("rel_later_v2", job.CreatedAt)
	newer.Title = strings.Replace(row.Title, "- 30 ", "- 30v2 ", 1)
	if _, err := f.store.CompleteAnimeSourcePoll(context.Background(), p, discoveryPage(p, job.CreatedAt+1000, row, newer), job.CreatedAt+1000); err != nil {
		t.Fatal(err)
	}
	got, err := f.store.GetAnimeDownloadJob(context.Background(), job.ID)
	if err != nil || got.BlockCode != nil || got.Snapshot.Source.SourceRevision != p.Source.Revision || got.Snapshot.InfoHash != job.Snapshot.InfoHash || got.ParentCID != job.ParentCID {
		t.Fatal("queued resource not safely reauthorized", got.BlockCode, err)
	}
	if _, err := f.store.PrepareAnimeSubmit(context.Background(), job.ID, job.Generation, f.target); err != nil {
		t.Fatal("reevaluated intent cannot submit", err)
	}
	var newerState string
	if err := f.store.db.QueryRow(`SELECT d.disposition FROM anime_release_decisions d JOIN anime_releases r ON r.id=d.release_id WHERE d.source_id=? AND r.post_key='rel_later_v2'`, p.Source.ID).Scan(&newerState); err != nil || newerState != "filtered" {
		t.Fatal("later version replaced the chosen resource", newerState, err)
	}
}

func TestAnimeDefinitionHistoryRanksAfterTimeChecks(t *testing.T) {
	f, now := discoveryFixture(t)
	old := discoveryPoll(t, f.store, now)
	editAnimeDefinition(t, f, func(c *model.AnimeSubscriptionConfig) {
		for _, source := range c.Sources {
			if source.ID == old.Source.ID {
				c.Sources = []model.AnimeSourceConfig{source}
				break
			}
		}
		c.Sources[0].Rules.MinimumEpisode = animePtr(25)
	})
	if _, err := f.store.db.Exec(`UPDATE anime_subscription_sources SET next_check_at=? WHERE id<>?`, now+3600000, old.Source.ID); err != nil {
		t.Fatal(err)
	}
	p := discoveryPoll(t, f.store, now+1000)
	rows := []struct {
		key, title string
		published  int64
	}{
		{"rel_lower", "[字幕组] 动画 - 30 [1080p][HEVC]", p.T0 - 1000},
		{"rel_highest", "[字幕组] 动画 - 30v2 [1080p][HEVC]", p.T0 - 1000},
		{"rel_future_version", "[字幕组] 动画 - 30v3 [1080p][HEVC]", now + 3600000},
	}
	page := discoveryPage(p, now+1000)
	page.Kind, page.RequestedTake, page.Completeness, page.StopReason = "history", 2000, "visible_end", ""
	for _, row := range rows {
		r := discoveryRow(row.key, row.published)
		r.Title = row.title
		page.Releases = append(page.Releases, r)
	}
	page.RawCount = len(page.Releases)
	stats, err := f.store.CompleteAnimeSourcePoll(context.Background(), p, page, now+1000)
	if err != nil || stats.Selected != 1 || stats.Review != 1 {
		t.Fatal(stats, err)
	}
	want := map[string]string{"rel_lower": "filtered", "rel_highest": "selected", "rel_future_version": "needs_confirmation"}
	for key, disposition := range want {
		var got string
		if err := f.store.db.QueryRow(`SELECT d.disposition FROM anime_release_decisions d JOIN anime_releases r ON r.id=d.release_id WHERE d.source_id=? AND r.post_key=?`, p.Source.ID, key).Scan(&got); err != nil || got != disposition {
			t.Fatal(key, got, err)
		}
	}
	// Advancing a clock or restarting does not repeat this completed read.
	if _, found, err := f.store.ReserveAnimeSourcePoll(context.Background(), now+int64(time.Second/time.Millisecond)*2); err != nil || found {
		t.Fatal("initialized source was immediately repeated", found, err)
	}
}

func TestAnimeDefinitionAcceptedAndUnknownSnapshotsStayImmutable(t *testing.T) {
	for _, state := range []string{"confirmed", "unknown"} {
		t.Run(state, func(t *testing.T) {
			f, job := animeSubmitStoreFixture(t)
			op, err := f.store.PrepareAnimeSubmit(context.Background(), job.ID, job.Generation, f.target)
			if err != nil {
				t.Fatal(err)
			}
			if err := f.store.DispatchAnimeSubmit(context.Background(), op.ID); err != nil {
				t.Fatal(err)
			}
			observation := model.AnimeSubmitObservation{SchemaVersion: 1, Kind: "matched", TargetCID: "200"}
			code := ""
			if state == "unknown" {
				observation.Kind, observation.TargetCID, code = "missing", "", "TASK_MISSING"
			}
			if err := f.store.RecordAnimeSubmitOutcome(context.Background(), op.ID, state, code, observation); err != nil {
				t.Fatal(err)
			}
			before, _ := f.store.GetAnimeDownloadJob(context.Background(), job.ID)
			editAnimeDefinition(t, f, func(c *model.AnimeSubscriptionConfig) { c.Sources[0].Rules.Exclude = []string{"测试"} })
			after, _ := f.store.GetAnimeDownloadJob(context.Background(), job.ID)
			if !reflect.DeepEqual(before, after) {
				t.Fatal("edit mutated an issued job")
			}
			out := editAnimeDefinition(t, f, func(c *model.AnimeSubscriptionConfig) {
				for i, source := range c.Sources {
					if source.ID != job.Snapshot.Source.SourceID {
						continue
					}
					c.RemovedSources = []model.AnimeSourceRemoval{{ID: source.ID, ExpectedRevision: source.ExpectedRevision}}
					source.ID, source.Ref, source.ExpectedRevision = "", "replacement", 0
					source.GroupID, source.GroupName = animePtr("moe_p99"), animePtr("新字幕组")
					c.Sources[i] = source
				}
			})
			oldSource, _ := f.store.GetAnimeSource(context.Background(), job.Snapshot.Source.SourceID)
			newSource, err := f.store.GetAnimeSource(context.Background(), out.SourceIDs["replacement"])
			if err != nil || oldSource.Status != "removed" || oldSource.NamespaceID == newSource.NamespaceID || newSource.ActiveBaselineID != nil {
				t.Fatal("source replacement inherited old runtime", oldSource.Status, newSource.NamespaceID, err)
			}
			after, _ = f.store.GetAnimeDownloadJob(context.Background(), job.ID)
			if !reflect.DeepEqual(before, after) {
				t.Fatal("source replacement mutated issued task")
			}
			if err := f.store.DispatchAnimeSubmit(context.Background(), op.ID); !errors.Is(err, ErrAnimeConflict) {
				t.Fatal("issued submit replayed", err)
			}
			if state == "confirmed" {
				tx, err := f.store.db.BeginTx(context.Background(), nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				if err := checkAnimeJobAuthorizationTx(context.Background(), tx, after); err != nil {
					t.Fatal("accepted task cannot continue original organization", err)
				}
			}
		})
	}
}
