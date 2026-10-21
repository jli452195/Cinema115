package storage

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/model"
)

func lifecycleFixture(t *testing.T) (*downloadClaimFixture, int64) {
	t.Helper()
	f := newDownloadClaimFixture(t)
	now := time.Now().UnixMilli()
	if _, err := f.store.db.Exec(`UPDATE anime_entries SET metadata_json=json_set(metadata_json,'$.totalEpisodes',12); UPDATE anime_subscription_sources SET health='ok',checked_at=?`, now); err != nil {
		t.Fatal(err)
	}
	for _, source := range f.sub.Sources {
		if _, err := f.store.db.Exec(`INSERT INTO anime_source_activity VALUES(?,?,?,?)`, source.ID, source.Revision, now, now); err != nil {
			t.Fatal(err)
		}
	}
	return f, now
}

func evaluateLifecycle(t *testing.T, f *downloadClaimFixture, now int64) model.AnimeSubscription {
	t.Helper()
	tx, err := f.store.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := evaluateAnimeLifecycleTx(context.Background(), tx, f.sub.ID, now); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	sub, err := f.store.GetAnimeSubscription(context.Background(), f.sub.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	return sub
}

func TestAnimeInactivityRequiresAllHealthySourcesAndSettledWork(t *testing.T) {
	for _, mode := range []string{"59 days", "60 days", "one updating", "source error", "reading", "new source", "failed job", "pending metadata", "disabled", "90 days"} {
		t.Run(mode, func(t *testing.T) {
			f, now := lifecycleFixture(t)
			quiet := now - 60*animeDay
			if mode == "59 days" {
				quiet = now - 59*animeDay
			}
			if _, err := f.store.db.Exec(`UPDATE anime_source_activity SET quiet_since=?`, quiet); err != nil {
				t.Fatal(err)
			}
			sourceID := f.sub.Sources[1].ID
			switch mode {
			case "one updating":
				_, err := f.store.db.Exec(`UPDATE anime_source_activity SET quiet_since=? WHERE source_id=?`, now, sourceID)
				if err != nil {
					t.Fatal(err)
				}
			case "source error":
				_, err := f.store.db.Exec(`UPDATE anime_subscription_sources SET health='error' WHERE id=?`, sourceID)
				if err != nil {
					t.Fatal(err)
				}
			case "reading":
				_, err := f.store.db.Exec(`UPDATE anime_subscription_sources SET check_token='inflight' WHERE id=?`, sourceID)
				if err != nil {
					t.Fatal(err)
				}
			case "new source":
				_, err := f.store.db.Exec(`UPDATE anime_subscription_sources SET status='enabled',health='unknown',checked_at=NULL WHERE id=?`, sourceID)
				if err != nil {
					t.Fatal(err)
				}
			case "failed job", "pending metadata":
				r, v := f.selectRelease(t, 0, "rel_lifecycle_pending", strings.Repeat("d", 40))
				if mode == "failed job" {
					job := f.claim(t, 0, r, v).Job
					if _, err := f.store.db.Exec(`UPDATE anime_download_jobs SET download_state='failed' WHERE id=?`, job.ID); err != nil {
						t.Fatal(err)
					}
				}
			case "disabled":
				_, err := f.store.db.Exec(`UPDATE anime_rss_settings SET inactive_archive_days=0`)
				if err != nil {
					t.Fatal(err)
				}
			case "90 days":
				_, err := f.store.db.Exec(`UPDATE anime_rss_settings SET inactive_archive_days=90`)
				if err != nil {
					t.Fatal(err)
				}
			}
			sub := evaluateLifecycle(t, f, now)
			want := "enabled"
			if mode == "60 days" {
				want = "archived"
			}
			if sub.Status != want {
				t.Fatal(mode, sub.Status, want)
			}
			if want == "archived" {
				entry, _ := f.store.GetAnimeEntry(context.Background(), sub.EntryID)
				info, err := f.store.AnimeLifecycle(context.Background(), sub, entry, now)
				if err != nil || info.ArchiveReason != "inactive" || info.ArchivedAt == nil {
					t.Fatal(info, err)
				}
				_, err = f.store.ControlAnimeSubscription(context.Background(), sub.ID, "unarchive", sub.Revision, nil)
				if err != nil {
					t.Fatal(err)
				}
				var count int
				if err := f.store.db.QueryRow(`SELECT count(*) FROM anime_source_activity`).Scan(&count); err != nil || count != 0 {
					t.Fatal("restore retained stale clock", count, err)
				}
			}
		})
	}
}

func TestAnimeCompletionRequiresEveryEpisodeAndSubtitleGroup(t *testing.T) {
	f, now := lifecycleFixture(t)
	for group := 0; group < 2; group++ {
		for episode := 30; episode <= 36; episode++ {
			if group == 1 && episode == 35 {
				continue
			}
			r, v := f.selectTitle(t, group, fmt.Sprintf("rel_complete_%d_%d", group, episode), fmt.Sprintf("%040x", group*100+episode), fmt.Sprintf("[字幕组] 测试 - %02d [1080p][HEVC]", episode))
			job := f.claim(t, group, r, v).Job
			if _, err := f.store.db.Exec(`UPDATE anime_download_jobs SET download_state='completed',file_state='verified',workflow_state='organized' WHERE id=?`, job.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	if got := evaluateLifecycle(t, f, now); got.Status != "enabled" {
		t.Fatal("hole in standby was counted complete", got.Status)
	}
	r, v := f.selectTitle(t, 1, "rel_complete_missing", strings.Repeat("e", 40), "[字幕组] 测试 - 35 [1080p][HEVC]")
	job := f.claim(t, 1, r, v).Job
	if got := evaluateLifecycle(t, f, now); got.Status != "enabled" {
		t.Fatal("queued episode counted as organized")
	}
	if _, err := f.store.db.Exec(`UPDATE anime_download_jobs SET download_state='completed',file_state='verified',workflow_state='organized' WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.db.Exec(`UPDATE anime_rss_settings SET auto_archive=0`); err != nil {
		t.Fatal(err)
	}
	if got := evaluateLifecycle(t, f, now); got.Status != "enabled" {
		t.Fatal("completion switch ignored")
	}
	if _, err := f.store.db.Exec(`UPDATE anime_rss_settings SET auto_archive=1`); err != nil {
		t.Fatal(err)
	}
	sub := evaluateLifecycle(t, f, now)
	if sub.Status != "archived" || sub.Revision != f.sub.Revision+1 {
		t.Fatal(sub.Status, sub.Revision)
	}
	var reason string
	if err := f.store.db.QueryRow(`SELECT reason FROM anime_subscription_archives WHERE subscription_id=?`, sub.ID).Scan(&reason); err != nil || reason != "complete" {
		t.Fatal(reason, err)
	}
}

func TestAnimeCompletionRangeNeverInfersChapterFromTMDBSeasonLength(t *testing.T) {
	source := animeMainSource("main", "45")
	s := model.AnimeSource{ID: "source", Binding: source.Binding, Rules: source.Rules}
	sub := model.AnimeSubscription{}
	first, last, ok := animeCompletionRange(sub, s, 12)
	if !ok || first != 30 || last != 36 {
		t.Fatal(first, last, ok)
	}
	s.Binding.ChapterCorrespondence = nil
	s.Binding.Scope = &model.AnimeScope{Kind: "feed"}
	sub.Naming = &model.AnimeNamingPlan{Sources: []model.AnimeSourceMapping{{SourceRef: s.ID, Segments: []model.AnimeMappingSegment{{First: 25, SourceBase: 25, TargetBase: 1, TargetSeason: 2}}}}}
	if _, _, ok := animeCompletionRange(sub, s, 12); ok {
		t.Fatal("TMDB mapping was mistaken for chapter start")
	}
	s.Rules.ChapterFirstEpisode = animePtr(25)
	if first, last, ok := animeCompletionRange(sub, s, 12); !ok || first != 30 || last != 36 {
		t.Fatal(first, last, ok)
	}
}

func TestAnimeSourceActivityUsesNewMatchesAndResetsAfterFailuresAndOfflineGaps(t *testing.T) {
	f, now := discoveryFixture(t)
	p := discoveryPoll(t, f.store, now)
	row := discoveryRow("rel_activity", 1)
	if _, err := f.store.CompleteAnimeSourcePoll(context.Background(), p, discoveryPage(p, now, row), now); err != nil {
		t.Fatal(err)
	}
	var quiet int64
	if err := f.store.db.QueryRow(`SELECT quiet_since FROM anime_source_activity WHERE source_id=?`, p.Source.ID).Scan(&quiet); err != nil || quiet != now {
		t.Fatal(quiet, err)
	}
	// A successful unchanged poll lowers the frequency only after 30 quiet days.
	if _, err := f.store.db.Exec(`UPDATE anime_source_activity SET quiet_since=?,last_success_at=? WHERE source_id=?`, now-31*animeDay, now, p.Source.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.db.Exec(`UPDATE anime_subscription_sources SET next_check_at=? WHERE id=?`, now+1000, p.Source.ID); err != nil {
		t.Fatal(err)
	}
	// Keep the independent standby out of this reservation.
	if _, err := f.store.db.Exec(`UPDATE anime_subscription_sources SET next_check_at=? WHERE id<>?`, now+2*animeDay, p.Source.ID); err != nil {
		t.Fatal(err)
	}
	p = discoveryPoll(t, f.store, now+1000)
	if _, err := f.store.CompleteAnimeSourcePoll(context.Background(), p, discoveryPage(p, now+1000, row), now+1000); err != nil {
		t.Fatal(err)
	}
	var next int64
	if err := f.store.db.QueryRow(`SELECT next_check_at FROM anime_subscription_sources WHERE id=?`, p.Source.ID).Scan(&next); err != nil || next < now+1000+animeDay {
		t.Fatal(next, err)
	}
	if _, err := f.store.db.Exec(`UPDATE anime_subscription_sources SET next_check_at=? WHERE id=?`, now+2000, p.Source.ID); err != nil {
		t.Fatal(err)
	}
	p = discoveryPoll(t, f.store, now+2000)
	if err := f.store.FailAnimeSourcePoll(context.Background(), p, "SOURCE_READ_FAILED", 0, false, now+2000); err != nil {
		t.Fatal(err)
	}
	if err := f.store.db.QueryRow(`SELECT quiet_since FROM anime_source_activity WHERE source_id=?`, p.Source.ID).Scan(&quiet); err != nil || quiet != now+2000 {
		t.Fatal("failure counted as quiet", quiet, err)
	}
	tx, err := f.store.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	quiet, err = recordAnimeSourceActivityTx(context.Background(), tx, p.Source, false, now+4*animeDay)
	if err != nil || quiet != now+4*animeDay {
		t.Fatal("offline gap counted as quiet", quiet, err)
	}
}

func TestAnimeRSSSettingsIndependentRevisionReplayAndValidation(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	before, err := s.GetAnimeSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	in := model.AnimeRSSSettings{Revision: 1, PollMinutes: 30, AutoArchive: false, InactiveArchiveDays: 90}
	key := AnimeWriteKey{ActorKey: "admin", Operation: "rss-settings", Key: "rss-settings-test", Digest: strings.Repeat("a", 64)}
	out, err := s.SaveAnimeRSSSettings(ctx, in, key)
	if err != nil || out.Revision != 2 {
		t.Fatal(out, err)
	}
	replay, err := s.SaveAnimeRSSSettings(ctx, in, key)
	if err != nil || replay != out {
		t.Fatal(replay, err)
	}
	key.Key = "rss-settings-conflict"
	if _, err := s.SaveAnimeRSSSettings(ctx, in, key); !errors.Is(err, ErrAnimeConflict) {
		t.Fatal(err)
	}
	in.Revision = 2
	in.PollMinutes = 0
	if _, err := s.SaveAnimeRSSSettings(ctx, in, key); !errors.Is(err, ErrAnimeInvalid) {
		t.Fatal(err)
	}
	after, err := s.GetAnimeSettings(ctx)
	if err != nil || after.Revision != before.Revision {
		t.Fatal("RSS policy changed drive settings", after, err)
	}
}

func TestAnimeLifecycleDevelopmentRebuildPreservesDirectories(t *testing.T) {
	f, _ := lifecycleFixture(t)
	ctx := context.Background()
	before, err := f.store.GetAnimeSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var path string
	if err := f.store.db.QueryRow(`SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&path); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{`DROP TABLE anime_subscription_archives`, `DROP TABLE anime_source_activity`, `DROP TABLE anime_rss_settings`, `PRAGMA user_version=53`} {
		if _, err := f.store.db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		s, err := Open(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		assertAnimeReset(t, s)
		after, err := s.GetAnimeSettings(ctx)
		if err != nil || !reflect.DeepEqual(after, before) {
			t.Fatal("upgrade changed directories", after, err)
		}
		rss, err := s.GetAnimeRSSSettings(ctx)
		if err != nil || rss != (model.AnimeRSSSettings{Revision: 1, PollMinutes: 15, AutoArchive: true, InactiveArchiveDays: 60}) {
			t.Fatal(rss, err)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAnimeCompletionUsesSourceRules(t *testing.T) {
	f, now := lifecycleFixture(t)
	source := &f.sub.Sources[0]
	source.Rules.Match = []string{`(?i)1080p`}
	raw, err := animeJSON(source.Rules)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.db.Exec(`UPDATE anime_subscription_sources SET rules_json=? WHERE id=?`, raw, source.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.db.Exec(`UPDATE anime_subscription_sources SET run_when_enabled=0,status='paused' WHERE id=?`, f.sub.Sources[1].ID); err != nil {
		t.Fatal(err)
	}
	for episode := 30; episode <= 36; episode++ {
		r, v := f.selectTitle(t, 0, fmt.Sprintf("rel_rule_%d", episode), fmt.Sprintf("%040x", episode), fmt.Sprintf("[group] Anime - %02d [1080p][HEVC]", episode))
		job := f.claim(t, 0, r, v).Job
		if _, err := f.store.db.Exec(`UPDATE anime_download_jobs SET download_state='completed',file_state='verified',workflow_state='organized' WHERE id=?`, job.ID); err != nil {
			t.Fatal(err)
		}
		sub := evaluateLifecycle(t, f, now)
		if episode < 36 && sub.Status != "enabled" || episode == 36 && sub.Status != "archived" {
			t.Fatal("source coverage", episode, sub.Status)
		}
	}
}

func TestAnimeLastOrganizedCheckpointArchivesInSameTransaction(t *testing.T) {
	f, job := animeOrganizeStoreFixture(t)
	ctx := context.Background()
	now := time.Now().UnixMilli()
	if _, err := f.store.db.Exec(`UPDATE anime_entries SET metadata_json=json_set(metadata_json,'$.totalEpisodes',1)`); err != nil {
		t.Fatal(err)
	}
	for i, source := range f.sub.Sources {
		if i != 0 {
			if _, err := f.store.db.Exec(`UPDATE anime_subscription_sources SET status='paused',run_when_enabled=0 WHERE id=?`, source.ID); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if _, err := f.store.db.Exec(`UPDATE anime_subscription_sources SET rules_json=json_set(rules_json,'$.chapterFirstEpisode',30),health='ok' WHERE id=?`, source.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := f.store.db.Exec(`INSERT INTO anime_source_activity VALUES(?,?,?,?)`, source.ID, source.Revision, now, now); err != nil {
			t.Fatal(err)
		}
	}
	op, err := f.store.PrepareAnimeOrganization(ctx, job, "")
	if err != nil {
		t.Fatal(err)
	}
	// Model a successful move and an already dispatched cleanup. No cloud I/O.
	for i := range op.Progress.Files {
		op.Progress.Files[i].Renamed, op.Progress.Files[i].Moved = true, true
	}
	op.Progress.Step = &model.AnimeOrganizeStep{Kind: "cleanup", CID: *job.DirectoryCID}
	raw, err := animeJSON(op.Progress)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.db.Exec(`UPDATE anime_drive_operations SET result_json=?,authorized_at=? WHERE id=?`, raw, now, op.ID); err != nil {
		t.Fatal(err)
	}
	next := op.Progress
	next.State, next.Cleanup, next.Step = "done", "recycled", nil
	if _, err := f.store.CheckpointAnimeOrganization(ctx, op, next, false, ""); err != nil {
		t.Fatal(err)
	}
	sub, err := f.store.GetAnimeSubscription(ctx, f.sub.ID, false)
	if err != nil || sub.Status != "archived" {
		t.Fatal("last completion waited for another poll", sub.Status, err)
	}
}
