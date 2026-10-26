package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/model"
)

func animeOrganizeCollection(t *testing.T, episodes int) (*animeDownloadFixture, *sql.DB, *AnimeDownloadSession, model.AnimeDownloadJob, *animeOrganizeDrive) {
	t.Helper()
	f, db, session, job, drive := animeOrganizeFixture(t)
	manifest := *job.Files
	manifest.Entries = append([]model.AnimeDriveEntry{}, manifest.Entries[:2]...)
	for episode := 1; episode <= episodes; episode++ {
		base := fmt.Sprintf("[MMSUB] Kono Oto Tomare - %02d [WebRip 1080p AVC-8bit AAC]", episode)
		for i, suffix := range []string{".mkv", ".CHS.ass", ".CHT.ass"} {
			manifest.Entries = append(manifest.Entries, model.AnimeDriveEntry{ID: fmt.Sprint(10000 + episode*3 + i), ParentCID: "300", Kind: "file", Name: base + suffix, Size: 100})
		}
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE anime_download_jobs SET files_json=? WHERE id=?`, string(raw), job.ID); err != nil {
		t.Fatal(err)
	}
	job.Files = &manifest
	drive.entries["300"] = append([]model.AnimeDriveEntry{}, manifest.Entries[2:]...)
	return f, db, session, job, drive
}

func animeExistingSeason(drive *animeOrganizeDrive) {
	drive.entries["900"] = []model.AnimeDriveEntry{{ID: "901", ParentCID: "900", Kind: "directory", Name: "黑暗召唤师在地下恋爱中 (2026) {tmdb-335790}"}}
	drive.entries["901"] = []model.AnimeDriveEntry{{ID: "902", ParentCID: "901", Kind: "directory", Name: "Season 01"}}
	drive.entries["902"] = []model.AnimeDriveEntry{}
}

func TestAnimeOrganizeCollectionKeepsExistingFilesAndCleansOnce(t *testing.T) {
	for _, allExisting := range []bool{false, true} {
		t.Run(fmt.Sprint(allExisting), func(t *testing.T) {
			f, _, session, job, drive := animeOrganizeCollection(t, 13)
			cleanup := animeEnableCleanup(f, drive)
			animeExistingSeason(drive)
			plan, err := f.service.JobOrganizationPreview(context.Background(), job.ID)
			if err != nil || plan.Ready != 39 {
				t.Fatal("collection not fully planned", plan, err)
			}
			for i, item := range plan.Items {
				if allExisting || strings.Contains(item.TargetPath, "S01E11.") || strings.Contains(item.TargetPath, "S01E12.") || strings.Contains(item.TargetPath, "S01E13.") {
					drive.entries["902"] = append(drive.entries["902"], model.AnimeDriveEntry{ID: fmt.Sprint(20000 + i), ParentCID: "902", Kind: "file", Name: strings.ToUpper(path.Base(item.TargetPath)), Size: 99})
				}
			}
			skipped := len(drive.entries["902"])
			op := organizeTestCall(t, f, session, job.ID)
			if op.Progress.State != "done" || op.Progress.Cleanup != "recycled" || cleanup.deletes != 1 || drive.reads != 3 || drive.creates != 0 || len(drive.entries["902"]) != 39 {
				t.Fatal("collection blocked, changed existing files, or added reads", op.Progress, drive)
			}
			for _, entry := range drive.entries["902"][:skipped] {
				if entry.Size != 99 {
					t.Fatal("existing media was modified", entry)
				}
			}
			if allExisting && drive.renames+drive.moves != 0 || !allExisting && (drive.renames != 1 || drive.moves != 1) {
				t.Fatal("skipped copies were sent to mutation batches", drive)
			}
			view, err := f.service.JobView(context.Background(), job.ID)
			if err != nil || view.Organization.Skipped != skipped || view.Organization.Moved != 39-skipped || view.WorkflowState != "organized" || view.PermittedActions.Retry {
				t.Fatal("skip completion was miscounted", view, err)
			}
			plan, err = f.service.JobOrganizationPreview(context.Background(), job.ID)
			if err != nil {
				t.Fatal(err)
			}
			counts := map[string]int{}
			for _, item := range plan.Items {
				counts[item.Reason]++
			}
			if counts["existing_target"] != skipped || counts["moved"] != 39-skipped || drive.reads != 3 {
				t.Fatal("local preview hid outcomes or queried the drive", counts, drive.reads)
			}
			runner := &AnimeStatusRunner{Admin: f.service, now: func() time.Time { return time.Now().Add(time.Hour) }}
			if err := runner.PollOnce(context.Background()); err != nil {
				t.Fatal(err)
			}
			organizeTestCall(t, f, session, job.ID)
			if drive.reads != 3 || cleanup.deletes != 1 {
				t.Fatal("completed collection was revisited", drive)
			}
		})
	}
}

func TestAnimeOrganizeUnresolvedVideoKeepsSubtitlesAndContinuesOtherEpisodes(t *testing.T) {
	f, _, session, job, drive := animeOrganizeCollection(t, 2)
	cleanup := animeEnableCleanup(f, drive)
	animeExistingSeason(drive)
	plan, err := f.service.JobOrganizationPreview(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range plan.Items {
		if item.Kind == "video" && strings.Contains(item.TargetPath, "S01E01.") {
			drive.entries["902"] = append(drive.entries["902"], model.AnimeDriveEntry{ID: "999", ParentCID: "902", Kind: "directory", Name: path.Base(item.TargetPath)})
		}
	}
	op := organizeTestCall(t, f, session, job.ID)
	if op.Progress.State != "needs_review" || op.Progress.Code != "ORGANIZE_TARGET_CONFLICT" || cleanup.deletes != 0 || len(drive.entries["300"]) != 3 || drive.moves != 1 {
		t.Fatal("one conflict blocked the collection or orphaned its subtitles", op.Progress, drive)
	}
	view, err := f.service.JobView(context.Background(), job.ID)
	if err != nil || view.Organization.Moved != 3 || !view.PermittedActions.Retry || view.PermittedActions.Reconcile {
		t.Fatal("original task cannot continue", view, err)
	}
	plan, err = f.service.JobOrganizationPreview(context.Background(), job.ID)
	if err != nil || plan.Items[0].Reason != "execution_conflict" {
		t.Fatal("conflicting file is not identified", plan, err)
	}
	// The user fixes only the blocking directory. Completed episode 2 remains
	// untouched; the original operation, manifest and download are reused.
	drive.entries["902"] = drive.entries["902"][1:]
	if _, err := f.service.JobAction(context.Background(), "admin", "continue_existing_collection", job.ID, "retry", AnimeControlInput{ExpectedRevision: view.Revision}); err != nil {
		t.Fatal(err)
	}
	op = organizeTestCall(t, f, session, job.ID)
	if op.Progress.State != "done" || cleanup.deletes != 1 || drive.renames != 2 || drive.moves != 2 || len(drive.entries["902"]) != 6 {
		t.Fatal("continuation repeated completed files or lost pending files", op.Progress, drive)
	}
}

func TestAnimeOrganizeSkippedFilesSurviveUnknownMoveAndRestart(t *testing.T) {
	f, db, session, job, drive := animeOrganizeCollection(t, 1)
	cleanup := animeEnableCleanup(f, drive)
	animeExistingSeason(drive)
	plan, err := f.service.JobOrganizationPreview(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	drive.entries["902"] = append(drive.entries["902"], model.AnimeDriveEntry{ID: "999", ParentCID: "902", Kind: "file", Name: path.Base(plan.Items[0].TargetPath)})
	drive.lost = "move"
	op := organizeTestCall(t, f, session, job.ID)
	if op.Progress.State != "unknown" || cleanup.deletes != 0 {
		t.Fatal(op.Progress, cleanup.deletes)
	}
	reopenAnimeRecoveryFixture(t, f, db)
	drive.db, err = sql.Open("sqlite", f.path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = drive.db.Close() })
	session, err = f.service.ResumeDownloadTarget(context.Background(), job.Snapshot.Target)
	if err != nil {
		t.Fatal(err)
	}
	drive.lost = ""
	op = organizeTestCall(t, f, session, job.ID)
	if op.Progress.Step != nil || op.Progress.Files[0].ExistingID != "999" || cleanup.deletes != 0 || drive.moves != 1 {
		t.Fatal("restart lost skip or replayed unknown move", op.Progress, drive)
	}
	op = organizeTestCall(t, f, session, job.ID)
	if op.Progress.State != "done" || cleanup.deletes != 1 || drive.moves != 1 || drive.renames != 1 || drive.reads != 4 {
		t.Fatal("restart repeated work or added scans", op.Progress, drive)
	}
}

func TestAnimeOrganizeDirectoryConflictDoesNotBlockAnotherSeason(t *testing.T) {
	f, db, session, job, drive := animeOrganizeCollection(t, 2)
	cleanup := animeEnableCleanup(f, drive)
	animeExistingSeason(drive)
	op, err := f.service.Store.PrepareAnimeOrganization(context.Background(), job, "")
	if err != nil {
		t.Fatal(err)
	}
	// Freeze a two-season plan using the existing request format.
	for i := range op.Request.Files {
		file := &op.Request.Files[i]
		if strings.Contains(file.TargetPath, "S01E02.") {
			file.TargetPath = strings.ReplaceAll(strings.ReplaceAll(file.TargetPath, "/Season 01/", "/Season 02/"), "S01E02.", "S02E02.")
		}
	}
	raw, err := json.Marshal(op.Request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE anime_drive_operations SET request_json=? WHERE id=?`, string(raw), op.ID); err != nil {
		t.Fatal(err)
	}
	drive.entries["901"] = []model.AnimeDriveEntry{
		{ID: "999", ParentCID: "901", Kind: "file", Name: "Season 01"},
		{ID: "902", ParentCID: "901", Kind: "directory", Name: "Season 02"},
	}
	op = organizeTestCall(t, f, session, job.ID)
	blocked, moved := 0, 0
	for _, file := range op.Progress.Files {
		if file.Conflict {
			blocked++
		}
		if file.Moved {
			moved++
		}
	}
	if op.Progress.State != "needs_review" || blocked != 3 || moved != 3 || cleanup.deletes != 0 || drive.renames != 1 || drive.moves != 1 || drive.reads != 3 {
		t.Fatal("blocked season stopped unrelated files or allowed cleanup", op.Progress, drive)
	}
}

func TestAnimeJobPageIncludesFreshCachedProgressWithoutDriveReads(t *testing.T) {
	f, db, _, job, drive := animeOrganizeCollection(t, 1)
	read := func(state string) AnimeJobPageView {
		t.Helper()
		raw := fmt.Sprintf(`{"state":%q,"firstEpisode":1,"publishedThrough":1,"organizedCount":0,"missingCount":0}`, state)
		result, err := db.Exec(`UPDATE anime_source_progress SET summary_json=?,computed_generation=generation WHERE source_id=?`, raw, job.Snapshot.Source.SourceID)
		if err != nil {
			t.Fatal(err)
		}
		if count, err := result.RowsAffected(); err != nil || count != 1 {
			t.Fatal("progress fixture missing", count, err)
		}
		view, err := f.service.JobPageView(context.Background(), job.Snapshot.Source.SubscriptionID, 1, 20)
		if err != nil || view.SourceProgress[job.Snapshot.Source.SourceID].State != state {
			t.Fatal("job response retained old source progress", view, err)
		}
		return view
	}
	before, after := read("missing"), read("complete")
	if before.DataVersion == after.DataVersion || drive.reads+drive.creates+drive.renames+drive.moves != 0 {
		t.Fatal("progress changes require another request or drive scan", before.DataVersion, after.DataVersion, drive)
	}
}
