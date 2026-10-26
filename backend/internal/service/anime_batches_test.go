package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/anacrolix/torrent/bencode"

	"strm115/backend/internal/model"
	"strm115/backend/internal/releaseparse"
)

func TestAnimeDiscoveryRunnerBatchFiltersBeforeDownloadAndDoesNotRepeat(t *testing.T) {
	for _, site := range []string{"mikan", "anibt", "garden"} {
		for _, matches := range []bool{true, false} {
			t.Run(site+"/"+map[bool]string{true: "matching files", false: "no matching files"}[matches], func(t *testing.T) {
				f, db, runner, source, now := discoveryServiceFixture(t)
				group := "45"
				if site == "garden" {
					group = "dmhy_f45"
				}
				if _, err := db.Exec(`UPDATE anime_subscription_sources SET provider=?,source_key=?,group_id=?,binding_json=json_set(binding_json,'$.review.groupId',?) WHERE id=?`, site, site+":2259:"+group, group, group, source.ID); err != nil {
					t.Fatal(err)
				}
				plan := model.AnimeNamingPlan{SchemaVersion: 1, Target: model.AnimeNamingTarget{TMDBID: "335790", Title: "作品", Year: "2026", Season: 1, Fingerprint: strings.Repeat("a", 64), Seasons: []model.AnimeNamingSeason{{Number: 1, Episodes: []model.AnimeNamingEpisode{{ID: "201", Number: 1}}}}}, Sources: []model.AnimeSourceMapping{{SourceRef: source.ID, Segments: []model.AnimeMappingSegment{{First: 1, SourceBase: 1, TargetSeason: 1, TargetBase: 1}}}}}
				raw, err := json.Marshal(plan)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := db.Exec(`UPDATE anime_subscriptions SET naming_json=? WHERE id=?`, string(raw), source.SubscriptionID); err != nil {
					t.Fatal(err)
				}
				if _, err := db.Exec(`UPDATE anime_subscription_sources SET rules_json=json_set(rules_json,'$.match',json('["(?i)HEVC","(?i)CHT"]')) WHERE id=?`, source.ID); err != nil {
					t.Fatal(err)
				}
				runner.Feeds = animeRSSFunc(func(_ context.Context, _ *AnimeSourceBudget, in AnimeSourceInput) (AnimeSourceResult, error) {
					page := discoveryServicePage(in, now, 1)
					page.Releases[0].Title = "[组] Anime - 08-10 合集 [1080p/720p]"
					if site != "mikan" {
						page.Releases[0].PostKey = "rel_batch_fixture"
						page.Releases[0].TorrentURL = "https://anibt.net/api/torrent/rel_batch_fixture.torrent"
					}
					if site == "garden" {
						page.Releases[0].TorrentURL = ""
						page.Releases[0].MagnetHint = "magnet:?xt=urn:btih:" + gardenTestHash
						page.Releases[0].Origin, page.Releases[0].OriginID = "dmhy", "688777"
					}
					return page, nil
				})
				codec := "HEVC"
				if !matches {
					codec = "AVC"
				}
				files := []any{
					map[string]any{"length": int64(1024), "path": []string{"Anime - 08 [" + codec + "][CHT].mkv"}},
					map[string]any{"length": int64(1024), "path": []string{"Anime - 09 [AVC][CHT].mkv"}},
					map[string]any{"length": int64(1024), "path": []string{"Anime - 10 [" + codec + "][CHT].mkv"}},
				}
				torrent, err := bencode.Marshal(map[string]any{"info": map[string]any{"name": "Batch", "files": files, "piece length": 16384, "pieces": strings.Repeat("p", 20)}})
				if err != nil {
					t.Fatal(err)
				}
				reads := 0
				f.service.Torrents = animeClaimTorrentFunc(func(context.Context, string, string, string) ([]byte, error) {
					reads++
					return torrent, nil
				})
				readCount := func() int { return reads }
				if site == "garden" {
					transport := &gardenDetailFake{names: []string{"Batch/Anime - 08 [" + codec + "][CHT].mkv", "Batch/Anime - 09 [AVC][CHT].mkv", "Batch/Anime - 10 [" + codec + "][CHT].mkv"}}
					f.service.Bindings = &AnimeBindingService{Garden: NewGardenService(transport)}
					readCount = func() int { return len(transport.paths) }
				}
				for i := 0; i < 2; i++ {
					if _, err := db.Exec(`UPDATE anime_subscription_sources SET next_check_at=? WHERE id=?`, now, source.ID); err != nil {
						t.Fatal(err)
					}
					if err := runner.PollOnce(context.Background()); err != nil {
						t.Fatal(err)
					}
					if err := runner.VerifyOnce(context.Background()); err != nil {
						t.Fatal(err)
					}
				}
				want := 0
				if matches {
					want = 1
				}
				for query, count := range map[string]int{`SELECT count(*) FROM anime_download_jobs WHERE download_state='queued'`: want, `SELECT count(*) FROM anime_drive_operations`: 0, `SELECT count(*) FROM anime_release_decisions WHERE next_verify_at IS NOT NULL OR lease_token IS NOT NULL`: 0} {
					var got int
					if err := db.QueryRow(query).Scan(&got); err != nil || got != count {
						t.Fatal(query, got, count, err)
					}
				}
				if readCount() != 1 || site == "garden" && reads != 0 {
					t.Fatal("unchanged batch repeatedly fetched its file list", readCount(), reads)
				}
				if matches {
					var jobID string
					if err := db.QueryRow(`SELECT id FROM anime_download_jobs`).Scan(&jobID); err != nil {
						t.Fatal(err)
					}
					job, err := f.service.Store.GetAnimeDownloadJob(context.Background(), jobID)
					if err != nil || len(job.Snapshot.BatchFiles) != 2 || *job.Snapshot.BatchFiles[0].Parse.Episode != 8 || *job.Snapshot.BatchFiles[1].Parse.Episode != 10 || len(job.Snapshot.Manifest) != 3 {
						t.Fatal("wrong frozen file selection", job.Snapshot.BatchFiles, err)
					}
				} else {
					var disposition, reason string
					if err := db.QueryRow(`SELECT disposition,reason_code FROM anime_release_decisions`).Scan(&disposition, &reason); err != nil || disposition != "filtered" || reason != "BATCH_NO_MATCHING_FILES" {
						t.Fatal("no-work decision remained pending", disposition, reason, err)
					}
				}
			})
		}
	}
}

func TestAnimeDiscoveryBatchPriorityAvoidsRepeatedTorrentReadsAndFallsBack(t *testing.T) {
	for _, mode := range []string{"whole collection", "partial collection", "read failed"} {
		t.Run(mode, func(t *testing.T) {
			_, db, runner, source, now := discoveryServiceFixture(t)
			naming := model.AnimeNamingPlan{SchemaVersion: 1, Target: model.AnimeNamingTarget{TMDBID: "335790", Title: "作品", Year: "2026", Season: 1, Fingerprint: strings.Repeat("a", 64), Seasons: []model.AnimeNamingSeason{{Number: 1, Episodes: []model.AnimeNamingEpisode{{ID: "201", Number: 1}}}}}, Sources: []model.AnimeSourceMapping{{SourceRef: source.ID, Segments: []model.AnimeMappingSegment{{First: 1, SourceBase: 1, TargetSeason: 1, TargetBase: 1}}}}}
			raw, err := json.Marshal(naming)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`UPDATE anime_subscriptions SET naming_json=? WHERE id=?`, string(raw), source.SubscriptionID); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`UPDATE anime_subscription_sources SET rules_json=json_set(rules_json,'$.match',json('["(?i)1080p","外挂"]')) WHERE id=?`, source.ID); err != nil {
				t.Fatal(err)
			}
			runner.Feeds = animeRSSFunc(func(_ context.Context, _ *AnimeSourceBudget, in AnimeSourceInput) (AnimeSourceResult, error) {
				page := discoveryServicePage(in, now, 4)
				for i := range page.Releases {
					page.Releases[i].Title = fmt.Sprintf("Anime - %02d [1080p][外挂]", i+1)
				}
				// Feed order must not let the earlier singles beat the collection.
				page.Releases[3].Title = "Anime 01-03 合集 [1080p][简繁外挂字幕]"
				return page, nil
			})
			reads := 0
			runner.Admin.Torrents = animeClaimTorrentFunc(func(_ context.Context, _, _, url string) ([]byte, error) {
				reads++
				batch := strings.HasSuffix(url, "00000004.torrent")
				if reads == 1 && !batch {
					t.Fatal("single torrent was fetched before collection")
				}
				if batch && mode == "read failed" {
					return nil, errors.New("temporary source read failure")
				}
				if !batch {
					return bencode.Marshal(map[string]any{"info": map[string]any{"name": url[len(url)-9:len(url)-8] + "-single.mkv", "length": 1024, "piece length": 16384, "pieces": strings.Repeat("p", 20)}})
				}
				files := make([]any, 0, 3)
				for n := 1; n <= 3; n++ {
					if mode == "partial collection" && n == 2 {
						continue
					}
					files = append(files, map[string]any{"length": 1024, "path": []string{fmt.Sprintf("Anime - %02d [1080p].mkv", n)}})
				}
				return bencode.Marshal(map[string]any{"info": map[string]any{"name": "Batch", "files": files, "piece length": 16384, "pieces": strings.Repeat("p", 20)}})
			})
			if err := runner.PollOnce(context.Background()); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 2; i++ {
				if err := runner.VerifyOnce(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			wantReads, wantJobs, wantCovered := 1, 1, 3
			if mode == "partial collection" {
				wantReads, wantJobs, wantCovered = 2, 2, 2
			} else if mode == "read failed" {
				wantReads, wantJobs, wantCovered = 4, 3, 0
			}
			if reads != wantReads {
				t.Fatal("unexpected torrent requests", reads, wantReads)
			}
			for query, want := range map[string]int{`SELECT count(*) FROM anime_download_jobs`: wantJobs, `SELECT count(*) FROM anime_release_decisions WHERE reason_code='EPISODE_ALREADY_COVERED'`: wantCovered, `SELECT count(*) FROM anime_drive_operations`: 0} {
				var count int
				if err := db.QueryRow(query).Scan(&count); err != nil || count != want {
					t.Fatal(query, count, want, err)
				}
			}
		})
	}
}

func TestAnimeBatchOrganizationUsesExistingMoveAndCleanupWithoutExtraReads(t *testing.T) {
	f, db, session, job, drive := animeOrganizeFixture(t)
	episode := 13 // Frozen custom numbering overrides the actual filename "01".
	job.Snapshot.Release.Parse = releaseparse.Result{Kind: "batch"}
	job.Snapshot.BatchFiles = []model.AnimeBatchFile{{Path: "release/01.mkv", Size: 1000, Parse: releaseparse.Result{Kind: "episode", Episode: &episode}}}
	extra := []model.AnimeDriveEntry{{ID: "403", ParentCID: "300", Name: "02.mkv", Kind: "file", Size: 1000}, {ID: "404", ParentCID: "300", Name: "02.chs.ass", Kind: "file", Size: 10}}
	job.Files.Entries = append(job.Files.Entries, extra...)
	drive.entries["300"] = append(drive.entries["300"], extra...)
	snapshot, err := json.Marshal(job.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := json.Marshal(job.Files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE anime_download_jobs SET snapshot_json=?,files_json=? WHERE id=?`, string(snapshot), string(manifest), job.ID); err != nil {
		t.Fatal(err)
	}
	cleanup := animeEnableCleanup(f, drive)
	cleanup.beforeDelete = func() {
		if len(drive.entries["300"]) != 3 || len(drive.entries["902"]) != 2 {
			t.Fatal("unselected files moved or selected subtitle left behind", drive.entries)
		}
		for _, entry := range drive.entries["902"] {
			if entry.ID != "400" && entry.ID != "401" || !strings.Contains(entry.Name, "S01E13") {
				t.Fatal("wrong batch file or frozen numbering", entry)
			}
		}
	}
	op := organizeTestCall(t, f, session, job.ID)
	if op.Progress.State != "done" || op.Progress.Cleanup != "recycled" || cleanup.deletes != 1 || drive.reads != 1 || drive.moves != 1 || drive.renames != 1 {
		t.Fatal("batch added scans or did not complete existing workflow", op.Progress, drive)
	}
	organizeTestCall(t, f, session, job.ID)
	if cleanup.deletes != 1 || drive.reads != 1 || drive.moves != 1 {
		t.Fatal("finished collection revisited the drive")
	}
}
