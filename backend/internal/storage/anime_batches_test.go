package storage

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"strm115/backend/internal/btresource"
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
	progressEpisode(t, f, 0, 11, false) // selected ordinary post, not yet verified
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
	if !out.Created || out.Skipped || out.Job.Snapshot.Organization == nil || len(out.Job.Snapshot.BatchFiles) != 2 {
		t.Fatal(out)
	}
	for i, n := range []int{8, 12} {
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
	if progress.OrganizedCount != 10 || progress.PublishedThrough != 12 || progress.MissingCount != 2 || !reflect.DeepEqual(progress.Missing, []model.AnimeEpisodeRange{{First: 10, Last: 11}}) {
		t.Fatal("batch receipts did not contribute per-episode progress", progress)
	}
}

func TestAnimeBatchNoMatchesOrAlreadyCompleteCreatesNoDownload(t *testing.T) {
	for _, mode := range []string{"no match", "complete"} {
		t.Run(mode, func(t *testing.T) {
			f := batchClaimFixture(t)
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
			d, err := f.store.GetAnimeReleaseDecision(context.Background(), f.sub.Sources[0].ID, r.ID)
			if err != nil || d.Disposition != "filtered" || d.ReasonCode == nil || *d.ReasonCode != "BATCH_NO_MISSING_FILES" || d.Snapshot.Selection != nil {
				t.Fatal("skipped collection remained pending", d, err)
			}
		})
	}
}
