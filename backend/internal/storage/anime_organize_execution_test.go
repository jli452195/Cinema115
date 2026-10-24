package storage

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/model"
	"strm115/backend/internal/naming"
)

func animeOrganizeStoreFixture(t *testing.T) (*downloadClaimFixture, model.AnimeDownloadJob) {
	t.Helper()
	f, job := animeFilesStoreFixture(t)
	job.Snapshot.Naming = organizationNaming(job.Snapshot.Source.SourceID)
	job.Snapshot.Naming.Sources[0].Segments[0] = model.AnimeMappingSegment{First: 1, SourceBase: 1, TargetSeason: 1, TargetBase: 1}
	target := job.Snapshot.Target
	target.ParentCID, target.ParentPath = "900", "/整理"
	template, _ := naming.Default(naming.TV)
	job.Snapshot.Organization = &model.AnimeOrganizationSnapshot{Naming: naming.Snapshot{Template: template}, SchemaVersion: 1, PolicyVersion: 1, PlanRevision: 1, Target: target}
	raw, _ := animeJSON(job.Snapshot)
	if _, err := f.store.db.Exec(`UPDATE anime_download_jobs SET snapshot_json=? WHERE id=?`, raw, job.ID); err != nil {
		t.Fatal(err)
	}
	manifest := animeFilesStoreManifest(job)
	manifest.Entries[2].Name = "[字幕组] 作品 - 01.mkv"
	if err := f.store.SaveAnimeFiles(context.Background(), job, manifest); err != nil {
		t.Fatal(err)
	}
	job, err := f.store.GetAnimeDownloadJob(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	return f, job
}

func TestAnimeCleanupStepRequiresMovedFilesAndExactIsolatedRoot(t *testing.T) {
	for _, mode := range []string{"valid", "unmoved", "shared download parent", "organize parent", "unknown root", "not our name", "wrong manifest root", "target inside root", "file IDs supplied"} {
		t.Run(mode, func(t *testing.T) {
			f, job := animeOrganizeStoreFixture(t)
			op, err := f.store.PrepareAnimeOrganization(context.Background(), job, "")
			if err != nil {
				t.Fatal(err)
			}
			for i := range op.Progress.Files {
				op.Progress.Files[i].Renamed, op.Progress.Files[i].Moved = true, true
			}
			step := model.AnimeOrganizeStep{Kind: "cleanup", CID: *job.DirectoryCID}
			switch mode {
			case "unmoved":
				op.Progress.Files[0].Moved = false
			case "shared download parent":
				step.CID = job.ParentCID
				job.DirectoryCID = &step.CID
			case "organize parent":
				step.CID = op.Request.Target.ParentCID
				job.DirectoryCID = &step.CID
			case "unknown root":
				step.CID = "999"
			case "not our name":
				job.DirectoryName = "anime-another-job"
			case "wrong manifest root":
				job.Files.Entries[0].ParentCID = "999"
			case "target inside root":
				op.Request.Target.ParentPath = strings.TrimRight(job.Snapshot.Target.ParentPath, "/") + "/" + job.DirectoryName + "/destination"
			case "file IDs supplied":
				step.IDs = []string{"400"}
			}
			if validAnimeCleanupStep(job, op, step) != (mode == "valid") {
				t.Fatal("cleanup boundary not enforced", mode)
			}
		})
	}
}

func TestAnimeOrganizeReceiptIssueOnlyAllowsBoundedClassification(t *testing.T) {
	f, job := animeOrganizeStoreFixture(t)
	ctx := context.Background()
	op, err := f.store.PrepareAnimeOrganization(ctx, job, "")
	if err != nil {
		t.Fatal(err)
	}
	p := op.Progress
	p.Step = &model.AnimeOrganizeStep{Kind: "rename", CID: "300", IDs: []string{"400"}}
	op, err = f.store.CheckpointAnimeOrganization(ctx, op, p, true, "")
	if err != nil {
		t.Fatal(err)
	}
	p.State, p.Code, p.ReceiptIssue = "unknown", "ORGANIZE_UNCERTAIN", "arbitrary upstream body"
	if _, err := f.store.CheckpointAnimeOrganization(ctx, op, p, false, ""); !errors.Is(err, ErrAnimeInvalid) {
		t.Fatal("unbounded receipt content accepted", err)
	}
	p.ReceiptIssue = "data_object"
	if _, err := f.store.CheckpointAnimeOrganization(ctx, op, p, false, ""); err != nil {
		t.Fatal("safe diagnostic rejected", err)
	}
}

func TestAnimeOrganizeJournalReopenCASAndNoSecondDispatch(t *testing.T) {
	f, job := animeOrganizeStoreFixture(t)
	ctx := context.Background()
	op, err := f.store.PrepareAnimeOrganization(ctx, job, "")
	if err != nil {
		t.Fatal(err)
	}
	p := op.Progress
	p.Step = &model.AnimeOrganizeStep{Kind: "rename", CID: "300", IDs: []string{"400"}}
	dispatched, err := f.store.CheckpointAnimeOrganization(ctx, op, p, true, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.CheckpointAnimeOrganization(ctx, op, p, true, ""); !errors.Is(err, ErrAnimeConflict) {
		t.Fatal("old checkpoint replaced current intent", err)
	}
	if _, err := f.store.CheckpointAnimeOrganization(ctx, dispatched, p, true, ""); !errors.Is(err, ErrAnimeConflict) {
		t.Fatal("in-flight intent replayed", err)
	}
	var databasePath string
	if err := f.store.db.QueryRow(`SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&databasePath); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got, err := reopened.GetAnimeOrganization(ctx, job.ID)
	if err != nil || !reflect.DeepEqual(got, dispatched) {
		t.Fatal("journal lost at restart", got, err)
	}
	if _, err := reopened.CheckpointAnimeOrganization(ctx, got, p, true, ""); !errors.Is(err, ErrAnimeConflict) {
		t.Fatal("restart granted second write", err)
	}
}

func TestAnimeOrganizeRejectsUnauthorizedAndUnplannedWrites(t *testing.T) {
	for _, mode := range []string{"paused", "withdrawn", "account", "generation", "unknown id", "wrong parent", "fake completion", "lease"} {
		t.Run(mode, func(t *testing.T) {
			f, job := animeOrganizeStoreFixture(t)
			ctx := context.Background()
			op, err := f.store.PrepareAnimeOrganization(ctx, job, "")
			if err != nil {
				t.Fatal(err)
			}
			p := op.Progress
			p.Step = &model.AnimeOrganizeStep{Kind: "rename", CID: "300", IDs: []string{"400"}}
			dispatch := true
			switch mode {
			case "paused":
				_, err = f.store.db.Exec(`UPDATE anime_subscriptions SET status='paused'`)
			case "withdrawn":
				_, err = f.store.db.Exec(`UPDATE anime_job_selections SET status='withdrawn'`)
			case "account":
				_, err = f.store.db.Exec(`UPDATE p115_accounts SET revision=revision+1 WHERE id=?`, job.AccountID)
			case "generation":
				_, err = f.store.db.Exec(`UPDATE anime_download_jobs SET generation=generation+1 WHERE id=?`, job.ID)
			case "unknown id":
				p.Step.IDs = []string{"999"}
			case "wrong parent":
				p.Step.CID = "999"
			case "fake completion":
				dispatch = false
				p.Files = append([]model.AnimeOrganizeFileProgress{}, p.Files...)
				p.Files[0].Renamed = true
				p.Files[0].Moved = true
				p.State = "done"
				p.Step = nil
			case "lease":
				_, found, e := f.store.ReserveAnimeDownloadWork(ctx, time.Now().UnixMilli()+6000, nil)
				if e != nil || !found {
					t.Fatal(e, found)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			before := animeMigrationRows(t, f.store.db, "anime_drive_operations")
			if _, err := f.store.CheckpointAnimeOrganization(ctx, op, p, dispatch, ""); err == nil {
				t.Fatal("invalid transition accepted", mode)
			}
			if after := animeMigrationRows(t, f.store.db, "anime_drive_operations"); !reflect.DeepEqual(before, after) {
				t.Fatal("rejected checkpoint mutated journal")
			}
		})
	}
}

func TestAnimeOrganizeCheckpointFailureRollsBackIntent(t *testing.T) {
	f, job := animeOrganizeStoreFixture(t)
	ctx := context.Background()
	op, err := f.store.PrepareAnimeOrganization(ctx, job, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.db.Exec(`CREATE TRIGGER anime_organize_fail BEFORE UPDATE ON anime_download_jobs BEGIN SELECT RAISE(ABORT,'injected'); END`); err != nil {
		t.Fatal(err)
	}
	p := op.Progress
	p.Step = &model.AnimeOrganizeStep{Kind: "rename", CID: "300", IDs: []string{"400"}}
	if _, err := f.store.CheckpointAnimeOrganization(ctx, op, p, true, ""); err == nil {
		t.Fatal("injected write succeeded")
	}
	got, err := f.store.GetAnimeOrganization(ctx, job.ID)
	if err != nil || !reflect.DeepEqual(got, op) {
		t.Fatal("failed commit dispatched external permission", got, err)
	}
}
