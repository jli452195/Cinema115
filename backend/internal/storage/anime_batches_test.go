package storage

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"strm115/backend/internal/btresource"
	"strm115/backend/internal/client/animefeed"
	"strm115/backend/internal/model"
	"strm115/backend/internal/utils/torrentmeta"
)

func batchClaimFixture(t *testing.T) *downloadClaimFixture {
	t.Helper()
	f := progressFixture(t)
	naming := organizationNaming(f.sub.Sources[0].ID)
	naming.Sources = nil
	for _, source := range f.sub.Sources {
		naming.Sources = append(naming.Sources, model.AnimeSourceMapping{SourceRef: source.ID, Segments: []model.AnimeMappingSegment{{First: 1, SourceBase: 1, TargetSeason: 1, TargetBase: 1}}})
	}
	raw, err := animeJSON(naming)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.db.Exec(`UPDATE anime_subscriptions SET naming_json=? WHERE id=?`, raw, f.sub.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.db.Exec(`UPDATE anime_settings SET organize_cid='900',organize_path='/整理'`); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestAnimeBatchDiscoveryPrefersVerifiedCoverageAndReleasesFailedCollections(t *testing.T) {
	for _, mode := range []string{"partial coverage", "no matching files", "read failed"} {
		t.Run(mode, func(t *testing.T) {
			f := batchClaimFixture(t)
			now := time.Now().UnixMilli()
			if _, err := f.store.db.Exec(`UPDATE anime_subscription_sources SET next_check_at=?`, now-1); err != nil {
				t.Fatal(err)
			}
			if _, err := f.store.db.Exec(`UPDATE anime_subscription_sources SET status='paused' WHERE id=?`, f.sub.Sources[1].ID); err != nil {
				t.Fatal(err)
			}
			poll := discoveryPoll(t, f.store, now+60001)
			rows := []animefeed.SourceRelease{discoveryRow("rel_single_1", now), discoveryRow("rel_single_2", now), discoveryRow("rel_batch", now)}
			rows[0].Title, rows[1].Title, rows[2].Title = "Anime - 01 [HEVC]", "Anime - 02 [HEVC]", "Anime 01-02 合集 [HEVC]"
			if _, err := f.store.CompleteAnimeSourcePoll(context.Background(), poll, discoveryPage(poll, now+60001, rows...), now+60001); err != nil {
				t.Fatal(err)
			}
			work, found, err := f.store.ReserveAnimeDiscovery(context.Background(), now+60002)
			if err != nil || !found || work.Decision.Snapshot.Parse.Kind != "batch" {
				t.Fatal("collection was not verified first", work, found, err)
			}
			if _, found, err := f.store.ReserveAnimeDiscovery(context.Background(), now+60003); err != nil || found {
				t.Fatal("concurrent single verification overtook collection", found, err)
			}
			code, retryable := "", false
			if mode == "read failed" {
				code, retryable = "RESOURCE_READ_FAILED", true
			} else {
				codec := "HEVC"
				if mode == "no matching files" {
					codec = "AVC"
				}
				verified, err := btresource.Verify(context.Background(), "garden", work.Observation, nil)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := f.store.ClaimAnimeDownload(context.Background(), work.Decision.SourceID, work.Decision.ReleaseID, work.Decision.Generation, verified, f.target, []torrentmeta.File{{Path: "Anime - 01 [" + codec + "].mkv", Size: 1}}); err != nil {
					t.Fatal(err)
				}
			}
			if err := f.store.CompleteAnimeDiscovery(context.Background(), work, code, retryable, now+60004); err != nil {
				t.Fatal(err)
			}
			states, _, err := f.store.AnimePreviewStates(context.Background(), poll.Source.ID, []string{work.Decision.ReleaseID})
			if err != nil || mode == "no matching files" && states[work.Decision.ReleaseID] != "BATCH_NO_MATCHING_FILES" {
				t.Fatal("lost collection explanation", states, err)
			}
			pending, found, err := f.store.ReserveAnimeDiscovery(context.Background(), now+60005)
			if err != nil || !found || pending.Decision.Snapshot.Parse.Kind != "episode" || mode == "partial coverage" && *pending.Decision.Snapshot.Parse.Episode != 2 {
				t.Fatal("uncovered single did not continue", pending, found, err)
			}
			wantCovered := 0
			if mode == "partial coverage" {
				wantCovered = 1
			}
			var count int
			if err := f.store.db.QueryRow(`SELECT count(*) FROM anime_release_decisions WHERE source_id=? AND disposition='filtered' AND reason_code='EPISODE_ALREADY_COVERED' AND next_verify_at IS NULL AND json_type(snapshot_json,'$.selection') IS NULL`, poll.Source.ID).Scan(&count); err != nil || count != wantCovered {
				t.Fatal("coverage skipped wrong singles", count, wantCovered, err)
			}
		})
	}
}

func TestAnimeBatchCannotReplaceQueuedOrVerifyingSingle(t *testing.T) {
	for _, mode := range []string{"queued", "verifying"} {
		t.Run(mode, func(t *testing.T) {
			f := batchClaimFixture(t)
			single, proof := f.selectTitle(t, 0, "rel_single", fmt.Sprintf("%040x", 7000), "Anime - 01 [HEVC]")
			if mode == "queued" {
				f.claim(t, 0, single, proof)
			} else if _, err := f.store.db.Exec(`UPDATE anime_release_decisions SET lease_token=?,lease_until=? WHERE release_id=?`, single.ID, time.Now().UnixMilli()+60000, single.ID); err != nil {
				t.Fatal(err)
			}
			batch, proof := f.selectTitle(t, 0, "rel_batch", fmt.Sprintf("%040x", 7001), "Anime 01-02 合集 [HEVC]")
			out := claimBatch(t, f, 0, batch, proof, []torrentmeta.File{{Path: "Anime - 01 [HEVC].mkv"}, {Path: "Anime - 02 [HEVC].mkv"}})
			if !out.Created || len(out.Job.Snapshot.BatchFiles) != 1 || *out.Job.Snapshot.BatchFiles[0].Parse.Episode != 2 {
				t.Fatal("collection replaced work already in flight", out.Job.Snapshot.BatchFiles)
			}
		})
	}
}

func TestAnimeBatchPublisherLabelsSurviveSubmitAndCompletion(t *testing.T) {
	f := batchClaimFixture(t)
	if _, err := f.store.db.Exec(`UPDATE anime_subscription_sources SET rules_json=json_set(rules_json,'$.match',json('["(?i)1080p","外挂"]')) WHERE id=?`, f.sub.Sources[0].ID); err != nil {
		t.Fatal(err)
	}
	var err error
	f.sub, err = f.store.GetAnimeSubscription(context.Background(), f.sub.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	files := make([]torrentmeta.File, 0, 12)
	for n := 1; n <= 12; n++ {
		files = append(files, torrentmeta.File{Path: fmt.Sprintf("[MMSUB] Kono Oto Tomare - %02d [WebRip 1080p AVC-8bit AAC].mkv", n), Size: 1024})
	}
	r, v := f.selectTitle(t, 0, "rel_mmsub", fmt.Sprintf("%040x", 7002), "Anime 01-12 合集 [1080p][简繁外挂字幕]")
	job := claimBatch(t, f, 0, r, v, files).Job
	if len(job.Snapshot.BatchFiles) != 12 {
		t.Fatal(job.Snapshot.BatchFiles)
	}
	if _, err := f.store.db.Exec(`UPDATE anime_download_jobs SET directory_cid='200' WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.PrepareAnimeSubmit(context.Background(), job.ID, job.Generation, f.target); err != nil {
		t.Fatal("contextual match lost its submit authorization", err)
	}
	if _, err := f.store.db.Exec(`UPDATE anime_download_jobs SET download_state='completed',file_state='verified',workflow_state='organized' WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	if p := refreshProgress(t, f)[f.sub.Sources[0].ID]; p.State != "complete" || p.OrganizedCount != 12 || p.MissingCount != 0 {
		t.Fatal("completed collection was filtered again by bare file names", p)
	}
}

func TestAnimeBatchOverlappingTailContributesToArchiveOnlyWithKnownTotal(t *testing.T) {
	for _, known := range []bool{true, false} {
		t.Run(fmt.Sprint("known total ", known), func(t *testing.T) {
			f := batchClaimFixture(t)
			files := make([]torrentmeta.File, 12)
			for i := range files {
				files[i] = torrentmeta.File{Path: fmt.Sprintf("Anime - %02d [HEVC].mkv", i+1), Size: 1}
			}
			first, proof := f.selectTitle(t, 0, "rel_batch_first", fmt.Sprintf("%040x", 5000), "Anime 01-09 合集")
			one := claimBatch(t, f, 0, first, proof, files[:9])
			second, proof := f.selectTitle(t, 0, "rel_batch_tail", fmt.Sprintf("%040x", 5001), "Anime 08-12 合集")
			two := claimBatch(t, f, 0, second, proof, files[7:])
			if len(one.Job.Snapshot.BatchFiles) != 9 || len(two.Job.Snapshot.BatchFiles) != 3 || *two.Job.Snapshot.BatchFiles[0].Parse.Episode != 10 {
				t.Fatal("overlapping collection failed to reserve only the new tail", two.Job.Snapshot.BatchFiles)
			}
			if _, err := f.store.db.Exec(`UPDATE anime_download_jobs SET download_state='completed',file_state='verified',workflow_state='organized'`); err != nil {
				t.Fatal(err)
			}
			if !known {
				if _, err := f.store.db.Exec(`UPDATE anime_entries SET metadata_json=json_remove(metadata_json,'$.totalEpisodes')`); err != nil {
					t.Fatal(err)
				}
			}
			now := time.Now().UnixMilli()
			if _, err := f.store.db.Exec(`UPDATE anime_subscription_sources SET status='paused' WHERE id=?`, f.sub.Sources[1].ID); err != nil {
				t.Fatal(err)
			}
			if _, err := f.store.db.Exec(`UPDATE anime_subscription_sources SET health='ok' WHERE id=?`, f.sub.Sources[0].ID); err != nil {
				t.Fatal(err)
			}
			if _, err := f.store.db.Exec(`INSERT INTO anime_source_activity VALUES(?,?,?,?)`, f.sub.Sources[0].ID, f.sub.Sources[0].Revision, now, now); err != nil {
				t.Fatal(err)
			}
			progress := refreshProgress(t, f)[f.sub.Sources[0].ID]
			want := "ongoing"
			if known {
				want = "complete"
			}
			if progress.State != want || progress.OrganizedCount != 12 || progress.MissingCount != 0 {
				t.Fatal("incorrect completion from batch receipts", progress)
			}
			want = "enabled"
			if known {
				want = "archived"
			}
			if sub := evaluateLifecycle(t, f, now); sub.Status != want {
				t.Fatal("batch changed completion/archive semantics", sub.Status, want)
			}
		})
	}
}

func TestAnimeBatchQueuedFilesCannotBypassEditedRules(t *testing.T) {
	f := batchClaimFixture(t)
	r, v := f.selectTitle(t, 0, "rel_batch_edit", fmt.Sprintf("%040x", 6000), "Anime 01-12 合集")
	job := claimBatch(t, f, 0, r, v, []torrentmeta.File{{Path: "Anime - 01 [HEVC].mkv", Size: 1}}).Job
	editAnimeDefinition(t, f, func(c *model.AnimeSubscriptionConfig) {
		c.Sources[0].Rules.Exclude = []string{"(?i)HEVC"}
	})
	poll := discoveryPoll(t, f.store, job.CreatedAt+1000)
	row := discoveryRow(r.PostKey, job.CreatedAt)
	row.Title, row.OriginalGUID, row.PublishedRaw = r.Title, "shared-guid", ""
	row.MagnetHint = "magnet:?xt=urn:btih:" + v.Hash()
	if _, err := f.store.CompleteAnimeSourcePoll(context.Background(), poll, discoveryPage(poll, job.CreatedAt+1000, row), job.CreatedAt+1000); err != nil {
		t.Fatal(err)
	}
	current, err := f.store.GetAnimeDownloadJob(context.Background(), job.ID)
	if err != nil || current.BlockCode == nil || *current.BlockCode != "CONFIGURATION_CHANGED" {
		t.Fatal("changed rules reactivated an excluded batch", current.BlockCode, err)
	}
	if _, err := f.store.PrepareAnimeSubmit(context.Background(), job.ID, job.Generation, f.target); err == nil {
		t.Fatal("excluded frozen files could still be submitted")
	}
}

func claimBatch(t *testing.T, f *downloadClaimFixture, group int, release model.AnimeRelease, verified btresource.Verified, files []torrentmeta.File) model.AnimeDownloadClaimResult {
	t.Helper()
	d, err := f.store.GetAnimeReleaseDecision(context.Background(), f.sub.Sources[group].ID, release.ID)
	if err != nil {
		t.Fatal(err)
	}
	out, err := f.store.ClaimAnimeDownload(context.Background(), d.SourceID, d.ReleaseID, d.Generation, verified, f.target, files)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestAnimeBatchClaimsOnlyUnclaimedMatchingEpisodes(t *testing.T) {
	f := batchClaimFixture(t)
	for n := 1; n <= 7; n++ {
		progressEpisode(t, f, 0, n, true)
	}
	progressEpisode(t, f, 0, 9, true)
	progressEpisode(t, f, 0, 11, false) // A candidate does not reserve a download.
	files := make([]torrentmeta.File, 0, 12)
	for n := 1; n <= 12; n++ {
		codec := "HEVC"
		if n == 10 {
			codec = "AVC"
		}
		files = append(files, torrentmeta.File{Path: fmt.Sprintf("合集/Anime - %02d [1080p][%s].mkv", n, codec), Size: 1024})
	}
	release, verified := f.selectTitle(t, 0, "rel_batch_1", fmt.Sprintf("%040x", 3000), "[组] Anime 01-12 合集 [HEVC/AVC]")
	out := claimBatch(t, f, 0, release, verified, files)
	if !out.Created || out.Skipped || out.Job.Snapshot.Organization == nil || len(out.Job.Snapshot.BatchFiles) != 3 {
		t.Fatal(out)
	}
	for i, n := range []int{8, 11, 12} {
		if *out.Job.Snapshot.BatchFiles[i].Parse.Episode != n {
			t.Fatal("organized, pending or nonmatching episode was selected", out.Job.Snapshot.BatchFiles)
		}
	}
	// Verification cache survives storage serialization and preserves the entire
	// manifest, while an idempotent claim keeps precisely the original subset.
	cached, found, err := f.store.FindAnimeVerifiedRelease(context.Background(), release.ID, release.ContentFingerprint)
	if err != nil || !found || !reflect.DeepEqual(cached.Files(), files) {
		t.Fatal("manifest cache", found, err)
	}
	again := claimBatch(t, f, 0, release, cached, cached.Files())
	if again.Created || again.Job.ID != out.Job.ID || !reflect.DeepEqual(again.Job.Snapshot.BatchFiles, out.Job.Snapshot.BatchFiles) {
		t.Fatal("relink changed the selected subset", again)
	}
	// A different collection cannot take the same episodes, and later ordinary
	// posts must not download an episode already reserved by this collection.
	repeat, proof := f.selectTitle(t, 0, "rel_batch_2", fmt.Sprintf("%040x", 3001), "[组] Anime 01-12 合集")
	if result := claimBatch(t, f, 0, repeat, proof, files); !result.Skipped || result.Created {
		t.Fatal("overlapping collection created another download", result)
	}
	single, proof := f.selectTitle(t, 0, "rel_later_single", fmt.Sprintf("%040x", 3002), "Anime - 08 [HEVC]")
	if result := f.claim(t, 0, single, proof); !result.Skipped {
		t.Fatal("later ordinary release bypassed batch claim", result)
	}
	// A collected collection between organization turns still owns its episodes.
	if _, err := f.store.db.Exec(`UPDATE anime_download_jobs SET download_state='completed',file_state='verified' WHERE id=?`, out.Job.ID); err != nil {
		t.Fatal(err)
	}
	single, proof = f.selectTitle(t, 0, "rel_during_organization", fmt.Sprintf("%040x", 3010), "Anime - 08 [HEVC]")
	if result := f.claim(t, 0, single, proof); !result.Skipped {
		t.Fatal("organization turn released collection coverage", result)
	}
	// The same publisher on another RSS remains independent. Use a different
	// resource here; sharing one hash must never broaden an existing job's work.
	other, proof := f.selectTitle(t, 1, "rel_batch_other", fmt.Sprintf("%040x", 3003), "Anime 01-12 合集")
	if result := claimBatch(t, f, 1, other, proof, files); !result.Created || len(result.Job.Snapshot.BatchFiles) != 11 {
		t.Fatal("another RSS lost its independent coverage", result)
	}
	if _, err := f.store.db.Exec(`UPDATE anime_download_jobs SET download_state='completed',file_state='verified',workflow_state='organized' WHERE id=?`, out.Job.ID); err != nil {
		t.Fatal(err)
	}
	progress := refreshProgress(t, f)[f.sub.Sources[0].ID]
	if progress.OrganizedCount != 11 || progress.PublishedThrough != 12 || progress.MissingCount != 1 || !reflect.DeepEqual(progress.Missing, []model.AnimeEpisodeRange{{First: 10, Last: 10}}) {
		t.Fatal("batch receipts did not contribute per-episode progress", progress)
	}
}

func TestAnimeBatchNoMatchesOrAlreadyCompleteCreatesNoDownload(t *testing.T) {
	for _, mode := range []string{"no match", "complete", "below minimum"} {
		t.Run(mode, func(t *testing.T) {
			f := batchClaimFixture(t)
			if mode == "below minimum" {
				if _, err := f.store.db.Exec(`UPDATE anime_subscription_sources SET rules_json=json_set(rules_json,'$.minimumEpisode',10) WHERE id=?`, f.sub.Sources[0].ID); err != nil {
					t.Fatal(err)
				}
				f.sub.Sources[0].Rules.MinimumEpisode = animePtr(10)
			}
			baseline := 0
			if mode == "complete" {
				for n := 1; n <= 12; n++ {
					progressEpisode(t, f, 0, n, true)
				}
				baseline = 12
			}
			codec := "HEVC"
			if mode == "no match" {
				codec = "AVC"
			}
			r, v := f.selectTitle(t, 0, "rel_batch_no_work", fmt.Sprintf("%040x", 4000), "Anime 01-12 合集 [HEVC]")
			out := claimBatch(t, f, 0, r, v, []torrentmeta.File{{Path: "Anime - 08 [" + codec + "].mkv", Size: 1}})
			if !out.Skipped || out.Created || out.Job.ID != "" {
				t.Fatal(out)
			}
			assertDownloadCounts(t, f.store, baseline, baseline, baseline, baseline)
			want := "BATCH_NO_MISSING_FILES"
			if mode == "no match" {
				want = "BATCH_NO_MATCHING_FILES"
			} else if mode == "below minimum" {
				want = "BELOW_SOURCE_MINIMUM"
			}
			d, err := f.store.GetAnimeReleaseDecision(context.Background(), f.sub.Sources[0].ID, r.ID)
			if err != nil || d.Disposition != "filtered" || d.ReasonCode == nil || *d.ReasonCode != want || out.SkipReason != want || d.Snapshot.Selection != nil {
				t.Fatal("skipped collection remained pending", d, err)
			}
		})
	}
}
