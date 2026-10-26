package service

import (
	"context"
	"encoding/json"
	"path"
	"strings"
	"testing"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/utils/torrentmeta"
)

func TestManualOrganizeBracketSubtitlesFollowVideo(t *testing.T) {
	for _, single := range []bool{false, true} {
		t.Run(map[bool]string{false: "directory", true: "single video"}[single], func(t *testing.T) {
			s, drive, account, _ := newOrganizeTest(t)
			drive.dirs["30"] = []p115.ShareFile{
				{ID: "101", Name: "[Kono Oto Tomare][02][1080P].mkv", Size: 100},
				{ID: "102", Name: "[Kono Oto Tomare][02][CHT].ass", Size: 10},
				{ID: "103", Name: "[Kono Oto Tomare][02][CHS].ass", Size: 10},
				{ID: "104", Name: "readme.txt", Size: 1},
			}
			input := OrganizeScanInput{AccountID: account, ParentID: "10", ParentPath: "/来源", EntryID: "30"}
			if single {
				input.ParentID, input.ParentPath, input.EntryID = "30", "/来源/待整理", "101"
			}
			scan, err := s.Scan(context.Background(), input)
			if err != nil || len(scan.Files) != 3 {
				t.Fatal("scan lost video or companion subtitles", scan, err)
			}
			plan, err := s.Preview(context.Background(), account, OrganizePreviewInput{ScanID: scan.ID, Kind: "tv", TMDBID: 42, Subtitles: true, Files: []OrganizeSelection{{ID: "101", Season: 1, Episode: 2}}})
			if err != nil || len(plan.Items) != 3 || drive.renames+drive.moves+drive.creates != 0 {
				t.Fatal("preview lost subtitles or wrote to drive", plan, err)
			}
			stem := strings.TrimSuffix(plan.Items[0].Name, ".mkv")
			if plan.Items[1].Name != stem+".zh-Hant.ass" || plan.Items[2].Name != stem+".zh-Hans.ass" {
				t.Fatal("subtitle language or shared video stem lost", plan.Items)
			}
			job := organizeTestRun(t, s, account, plan)
			if job.Status != "completed" || drive.moves != 1 || drive.renames != 1 || len(drive.dirs[job.Items[0].TargetCID]) != 3 || len(drive.dirs["30"]) != 1 || drive.dirs["30"][0].ID != "104" {
				t.Fatal("video and subtitles did not move together", job, drive.dirs)
			}
		})
	}
}

func TestManualOrganizeSingleVideoKeepsFallbackAmbiguity(t *testing.T) {
	s, drive, account, _ := newOrganizeTest(t)
	drive.dirs["30"] = []p115.ShareFile{
		{ID: "101", Name: "[Show][02][1080P].mkv", Size: 100},
		{ID: "102", Name: "[Show][02][CHT].ass", Size: 10},
		{ID: "103", Name: "[Show][02][720P].mkv", Size: 100},
	}
	scan, err := s.Scan(context.Background(), OrganizeScanInput{AccountID: account, ParentID: "30", ParentPath: "/来源/待整理", EntryID: "101"})
	if err != nil || len(scan.Files) != 1 || scan.Files[0].ID != "101" {
		t.Fatal("single-video selection hid an ambiguous sibling", scan, err)
	}
}

func TestTorrentOrganizeBracketSubtitleMapping(t *testing.T) {
	files, err := organizeResourceFiles(strings.Repeat("a", 40), []torrentmeta.File{
		{Path: "release/[Kono Oto Tomare][02][1080P].mkv", Size: 100},
		{Path: "release/[Kono Oto Tomare][02][CHT].ass", Size: 10},
		{Path: "other/[Kono Oto Tomare][02][CHS].ass", Size: 10},
	})
	if err != nil || len(files.Files) != 1 || len(files.Subtitles) != 2 {
		t.Fatal(files, err)
	}
	for _, sub := range files.Subtitles {
		if sub.File.ParentID == "release" {
			if sub.VideoID != files.Files[0].ID || sub.Tail != ".zh-Hant" {
				t.Fatal("torrent manifest did not reuse subtitle matching", sub)
			}
		} else if sub.VideoID != "" {
			t.Fatal("subtitle matched across directories", sub)
		}
	}
}

func TestAnimeOrganizeBracketSubtitlesAndCleanup(t *testing.T) {
	for _, matched := range []bool{true, false} {
		t.Run(map[bool]string{true: "matched", false: "unrelated subtitle"}[matched], func(t *testing.T) {
			f, db, session, job, drive := animeOrganizeFixture(t)
			for i := range job.Files.Entries {
				switch job.Files.Entries[i].ID {
				case "400":
					job.Files.Entries[i].Name = "Kono Oto Tomare! - 02 [1080p].mkv"
				case "401":
					job.Files.Entries[i].Name = "[Kono Oto Tomare][02][CHT].ass"
					if !matched {
						job.Files.Entries[i].Name = "[Other Show][02][CHT].ass"
					}
				}
			}
			raw, err := json.Marshal(job.Files)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`UPDATE anime_download_jobs SET files_json=? WHERE id=?`, string(raw), job.ID); err != nil {
				t.Fatal(err)
			}
			drive.entries["300"] = job.Files.Entries[2:]
			cleanup := animeEnableCleanup(f, drive)
			cleanup.beforeDelete = func() {
				if len(drive.entries["902"]) != 2 {
					t.Fatal("source cleanup preceded confirmed video and subtitle moves")
				}
			}
			op := organizeTestCall(t, f, session, job.ID)
			if !matched {
				if op.Progress.State != "needs_review" || cleanup.deletes != 0 || drive.creates+drive.moves+drive.renames+drive.reads != 0 {
					t.Fatal("unmatched subtitle allowed writes or source cleanup", op.Progress, drive)
				}
				return
			}
			if op.Progress.State != "done" || op.Progress.Cleanup != "recycled" || cleanup.deletes != 1 || drive.reads != 1 || drive.creates != 2 || drive.renames != 1 || drive.moves != 1 {
				t.Fatal("subtitle fallback changed request count or cleanup", op.Progress, drive)
			}
			stem := strings.TrimSuffix(drive.entries["902"][0].Name, ".mkv")
			if !strings.Contains(stem, "S01E02") || path.Ext(drive.entries["902"][0].Name) != ".mkv" || drive.entries["902"][1].Name != stem+".zh-Hant.ass" {
				t.Fatal("subtitle did not follow the final organized video name", drive.entries["902"])
			}
		})
	}
}
