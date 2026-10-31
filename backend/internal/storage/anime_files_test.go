package storage

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"strm115/backend/internal/model"
)

func animeDirectoryStoreFixture(t *testing.T) (*downloadClaimFixture, model.AnimeDownloadJob) {
	t.Helper()
	f, job := animeSubmitStoreFixture(t)
	if _, err := f.store.db.Exec(`UPDATE anime_download_jobs SET directory_cid=NULL WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	job.DirectoryCID = nil
	return f, job
}

func TestAnimeFilesDirectoryPermissionIsSingleAndDurable(t *testing.T) {
	f, job := animeDirectoryStoreFixture(t)
	var count atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, create, err := f.store.StartAnimeDirectory(context.Background(), job.ID, job.Generation, f.target)
			if err != nil {
				t.Error(err)
			}
			if create {
				count.Add(1)
			}
		}()
	}
	wg.Wait()
	if count.Load() != 1 {
		t.Fatal("duplicate create permissions", count.Load())
	}
	out, create, err := f.store.StartAnimeDirectory(context.Background(), job.ID, job.Generation, f.target)
	if err != nil || create || out.DirectoryOperation.State != "dispatched" || out.DirectoryCID != nil {
		t.Fatal(out, create, err)
	}
	root := model.AnimeDriveEntry{ID: "200", ParentCID: job.ParentCID, Kind: "directory", Name: job.DirectoryName}
	if err := f.store.FinishAnimeDirectory(context.Background(), out, &root, ""); err != nil {
		t.Fatal(err)
	}
	got, _ := f.store.GetAnimeDownloadJob(context.Background(), job.ID)
	if got.DirectoryCID == nil || *got.DirectoryCID != "200" || got.DirectoryOperation.State != "confirmed" || got.DownloadState != "queued" {
		t.Fatal(got)
	}
	var path string
	if err := f.store.db.QueryRow(`SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&path); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	read, create, err := reopened.StartAnimeDirectory(context.Background(), job.ID, job.Generation, f.target)
	if err != nil || create || !reflect.DeepEqual(got, read) {
		t.Fatal("reopen lost bound directory", read, create, err)
	}
}

func TestAnimeFilesDirectoryAuthorizationAndStaleReceipt(t *testing.T) {
	for _, mode := range []string{"paused", "withdrawn", "generation", "target", "stale receipt"} {
		t.Run(mode, func(t *testing.T) {
			f, job := animeDirectoryStoreFixture(t)
			target := f.target
			switch mode {
			case "paused":
				_, _ = f.store.db.Exec(`UPDATE anime_subscriptions SET status='paused'`)
			case "withdrawn":
				_, _ = f.store.db.Exec(`UPDATE anime_job_selections SET status='withdrawn'`)
			case "generation":
				job.Generation++
			case "target":
				target.ParentCID = "999"
			}
			out, create, err := f.store.StartAnimeDirectory(context.Background(), job.ID, job.Generation, target)
			if mode != "stale receipt" {
				if err == nil || create {
					t.Fatal("unauthorized create permitted", out, create, err)
				}
				return
			}
			if err != nil || !create {
				t.Fatal(err)
			}
			if _, err := f.store.db.Exec(`UPDATE anime_download_jobs SET generation=generation+1 WHERE id=?`, job.ID); err != nil {
				t.Fatal(err)
			}
			before := animeMigrationRows(t, f.store.db, "anime_download_jobs")
			root := model.AnimeDriveEntry{ID: "200", ParentCID: job.ParentCID, Name: job.DirectoryName, Kind: "directory"}
			if err := f.store.FinishAnimeDirectory(context.Background(), out, &root, ""); !errors.Is(err, ErrAnimeConflict) {
				t.Fatal("stale create receipt bound directory", err)
			}
			if after := animeMigrationRows(t, f.store.db, "anime_download_jobs"); !reflect.DeepEqual(before, after) {
				t.Fatal("stale create mutated task")
			}
		})
	}
}

func animeFilesStoreManifest(job model.AnimeDownloadJob) model.AnimeFileManifest {
	return model.AnimeFileManifest{SchemaVersion: 1, UID: job.UID, ResultID: "300", CollectedAt: 123456, Entries: []model.AnimeDriveEntry{
		{ID: "200", ParentCID: job.ParentCID, Kind: "directory", Name: job.DirectoryName},
		{ID: "300", ParentCID: "200", Kind: "directory", Name: "release"},
		{ID: "400", ParentCID: "300", Kind: "file", Name: "01.mkv", Size: 1234},
	}}
}

func animeFilesStoreFixture(t *testing.T) (*downloadClaimFixture, model.AnimeDownloadJob) {
	t.Helper()
	f, job, _ := animeStatusStoreFixture(t)
	if _, err := f.store.db.Exec(`UPDATE anime_download_jobs SET download_state='completed',result_object_id='300',result_object_kind='unknown',next_run_at=1 WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	job, err := f.store.GetAnimeDownloadJob(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	return f, job
}

func TestAnimeFilesAtomicManifestAndStop(t *testing.T) {
	f, job := animeFilesStoreFixture(t)
	manifest := animeFilesStoreManifest(job)
	if err := f.store.SaveAnimeFiles(context.Background(), job, manifest); err != nil {
		t.Fatal(err)
	}
	got, err := f.store.GetAnimeDownloadJob(context.Background(), job.ID)
	if err != nil || got.FileState != "verified" || got.WorkflowState != "original_saved" || got.NextRunAt != nil || !reflect.DeepEqual(got.Files, &manifest) || *got.ResultObjectKind != "directory" {
		t.Fatal(got, err)
	}
	if _, found, err := f.store.ReserveAnimeStatusPoll(context.Background(), 999999999, nil); err != nil || found {
		t.Fatal("verified task still polled", found, err)
	}
	if err := f.store.SaveAnimeFiles(context.Background(), job, manifest); !errors.Is(err, ErrAnimeConflict) {
		t.Fatal("old receipt saved twice", err)
	}
}

func TestAnimeFilesInvalidFactsAndLostGenerationNeverCommit(t *testing.T) {
	for _, mode := range []string{"UID", "result", "outside", "duplicate", "empty", "cycle", "kind", "negative size", "status conflict", "generation", "revision", "submit unconfirmed", "account"} {
		t.Run(mode, func(t *testing.T) {
			f, job := animeFilesStoreFixture(t)
			manifest := animeFilesStoreManifest(job)
			switch mode {
			case "UID":
				manifest.UID = "999"
			case "result":
				manifest.ResultID = "999"
			case "outside":
				manifest.Entries[2].ParentCID = "999"
			case "duplicate":
				manifest.Entries = append(manifest.Entries, manifest.Entries[2])
			case "empty":
				manifest.Entries = manifest.Entries[:2]
			case "cycle":
				manifest.Entries[2].ParentCID = "400"
			case "kind":
				manifest.Entries[0].Kind = "file"
			case "negative size":
				manifest.Entries[2].Size = -1
			case "status conflict":
				_, _ = f.store.db.Exec(`UPDATE anime_download_jobs SET block_code='TASK_STATUS_CONFLICT' WHERE id=?`, job.ID)
				job, _ = f.store.GetAnimeDownloadJob(context.Background(), job.ID)
			case "generation":
				_, _ = f.store.db.Exec(`UPDATE anime_download_jobs SET generation=generation+1 WHERE id=?`, job.ID)
			case "revision":
				_, _ = f.store.db.Exec(`UPDATE anime_download_jobs SET revision=revision+1 WHERE id=?`, job.ID)
			case "submit unconfirmed":
				_, _ = f.store.db.Exec(`UPDATE anime_drive_operations SET state='unknown'`)
			case "account":
				_, _ = f.store.db.Exec(`UPDATE p115_accounts SET is_primary=0 WHERE id=?`, job.AccountID)
			}
			before := animeMigrationRows(t, f.store.db, "anime_download_jobs")
			if err := f.store.SaveAnimeFiles(context.Background(), job, manifest); err == nil {
				t.Fatal("invalid/stale facts accepted")
			}
			if after := animeMigrationRows(t, f.store.db, "anime_download_jobs"); !reflect.DeepEqual(before, after) {
				t.Fatal("failed manifest left partial writes")
			}
		})
	}
}

func TestAnimeFilesReceiptFailureRollsBack(t *testing.T) {
	for _, mode := range []string{"directory", "files"} {
		t.Run(mode, func(t *testing.T) {
			f, job := animeDirectoryStoreFixture(t)
			if mode == "files" {
				f, job = animeFilesStoreFixture(t)
			}
			if _, err := f.store.db.Exec(`CREATE TRIGGER reject_anime_file_write BEFORE UPDATE ON anime_download_jobs BEGIN SELECT RAISE(ABORT,'write failed'); END`); err != nil {
				t.Fatal(err)
			}
			before := animeMigrationRows(t, f.store.db, "anime_download_jobs")
			var err error
			if mode == "files" {
				err = f.store.SaveAnimeFiles(context.Background(), job, animeFilesStoreManifest(job))
			} else {
				_, _, err = f.store.StartAnimeDirectory(context.Background(), job.ID, job.Generation, f.target)
			}
			if err == nil {
				t.Fatal("failure swallowed")
			}
			if after := animeMigrationRows(t, f.store.db, "anime_download_jobs"); !reflect.DeepEqual(before, after) {
				t.Fatal("partial stage write")
			}
		})
	}
}
