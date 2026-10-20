package service

import (
	"context"
	"errors"
	"testing"

	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
)

type animeCleanupDrive struct {
	*animeOrganizeDrive
	deletes      int
	lostDelete   bool
	beforeDelete func()
}

func (d *animeCleanupDrive) DeleteDirectory(_ context.Context, cid, cookie string) error {
	d.intent("cleanup", cookie)
	if cid != "200" {
		d.t.Fatal("cleanup escaped saved resource root", cid)
	}
	d.deletes++
	if d.beforeDelete != nil {
		d.beforeDelete()
	}
	var recycle func(string)
	recycle = func(parent string) {
		for _, entry := range d.entries[parent] {
			if entry.Kind == "directory" {
				recycle(entry.ID)
			}
		}
		delete(d.entries, parent)
	}
	recycle(cid)
	if d.lostDelete {
		return context.DeadlineExceeded
	}
	return nil
}

func animeEnableCleanup(f *animeDownloadFixture, d *animeOrganizeDrive) *animeCleanupDrive {
	d.entries["200"] = []model.AnimeDriveEntry{{ID: "300", ParentCID: "200", Name: "release", Kind: "directory"}}
	cleanup := &animeCleanupDrive{animeOrganizeDrive: d}
	f.service.OrganizationGateway = cleanup
	return cleanup
}

func TestAnimeCleanupRecyclesOnlyOwnRootWithOneExtraWrite(t *testing.T) {
	f, _, session, job, d := animeOrganizeFixture(t)
	c := animeEnableCleanup(f, d)
	op := organizeTestCall(t, f, session, job.ID)
	if op.Progress.State != "done" || op.Progress.Cleanup != "recycled" || c.deletes != 1 || d.reads != 1 || d.moves != 1 || d.renames != 1 {
		t.Fatal("cleanup added scans or repeated mutations", op.Progress, c)
	}
	if _, exists := d.entries["200"]; exists {
		t.Fatal("resource root retained")
	}
	if _, exists := d.entries["300"]; exists {
		t.Fatal("nested attachments retained outside recycle bin")
	}
	if len(d.entries["902"]) != 2 || len(d.entries["900"]) != 1 {
		t.Fatal("organized media or shared destination affected", d.entries)
	}
	for i := 0; i < 2; i++ {
		organizeTestCall(t, f, session, job.ID)
	}
	if c.deletes != 1 || d.reads != 1 {
		t.Fatal("completed cleanup tracked or repeated", c)
	}
}

func TestAnimeCleanupNeverRunsBeforeAllMovesAreConfirmed(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(map[bool]string{false: "lost move receipt", true: "partial move"}[partial], func(t *testing.T) {
			f, _, session, job, d := animeOrganizeFixture(t)
			c := animeEnableCleanup(f, d)
			d.lost, d.partial = "move", partial
			op := organizeTestCall(t, f, session, job.ID)
			if op.Progress.State != "unknown" || c.deletes != 0 {
				t.Fatal("unconfirmed move deleted its source", op.Progress, c.deletes)
			}
			op = organizeTestCall(t, f, session, job.ID)
			if c.deletes != 0 {
				t.Fatal("read-only recovery mixed in a delete")
			}
			if partial {
				if op.Progress.State != "needs_review" {
					t.Fatal(op.Progress)
				}
				return
			}
			d.lost = ""
			op = organizeTestCall(t, f, session, job.ID)
			if op.Progress.Cleanup != "recycled" || c.deletes != 1 || d.moves != 1 {
				t.Fatal("confirmed move was repeated instead of cleanup", op.Progress, c)
			}
		})
	}
}

func TestAnimeCleanupLostDeleteReceiptDoesNotRepeatOrUndoOrganization(t *testing.T) {
	f, _, session, job, d := animeOrganizeFixture(t)
	c := animeEnableCleanup(f, d)
	c.lostDelete = true
	op := organizeTestCall(t, f, session, job.ID)
	if op.Progress.State != "done" || op.Progress.Cleanup != "uncertain" || c.deletes != 1 {
		t.Fatal(op.Progress, c.deletes)
	}
	v, err := f.service.JobView(context.Background(), job.ID)
	if err != nil || v.WorkflowState != "organized" || v.PermittedActions.Reconcile || v.PermittedActions.Retry {
		t.Fatal("cleanup uncertainty reactivated the media job", v, err)
	}
	organizeTestCall(t, f, session, job.ID)
	if c.deletes != 1 {
		t.Fatal("unknown delete replayed")
	}
}

func TestAnimeCleanupLostCheckpointReopensWithoutRepeatingDelete(t *testing.T) {
	f, db, session, job, d := animeOrganizeFixture(t)
	c := animeEnableCleanup(f, d)
	c.beforeDelete = func() {
		_, err := db.Exec(`CREATE TRIGGER anime_cleanup_fail BEFORE UPDATE ON anime_drive_operations WHEN json_extract(NEW.result_json,'$.cleanup')='recycled' BEGIN SELECT RAISE(ABORT,'injected'); END`)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := f.service.OrganizeDownloadedFiles(context.Background(), session, job.ID); err == nil || c.deletes != 1 {
		t.Fatal("receipt checkpoint failure not injected", err, c.deletes)
	}
	if _, err := db.Exec(`DROP TRIGGER anime_cleanup_fail`); err != nil {
		t.Fatal(err)
	}
	reopenAnimeRecoveryFixture(t, f, db)
	session, err := f.service.ResumeDownloadTarget(context.Background(), job.Snapshot.Target)
	if err != nil {
		t.Fatal(err)
	}
	reads := d.reads
	op := organizeTestCall(t, f, session, job.ID)
	if op.Progress.Cleanup != "uncertain" || op.Progress.State != "done" || c.deletes != 1 || d.reads != reads {
		t.Fatal("restart replayed or queried an unknown delete", op.Progress, c)
	}
}

func TestAnimeCleanupPauseAndAccountChangePreventNewDelete(t *testing.T) {
	for _, accountChange := range []bool{false, true} {
		t.Run(map[bool]string{false: "paused", true: "account changed"}[accountChange], func(t *testing.T) {
			f, db, session, job, d := animeOrganizeFixture(t)
			c := animeEnableCleanup(f, d)
			d.hook = func(kind string) {
				if kind != "move" {
					return
				}
				statement := `UPDATE anime_subscriptions SET status='paused'`
				if accountChange {
					statement = `UPDATE p115_accounts SET revision=revision+1`
				}
				if _, err := db.Exec(statement); err != nil {
					t.Fatal(err)
				}
			}
			err := f.service.OrganizeDownloadedFiles(context.Background(), session, job.ID)
			if accountChange && !errors.Is(err, storage.ErrAnimeAccountChanged) || !accountChange && err != nil || c.deletes != 0 {
				t.Fatal("new delete survived authorization change", err, c.deletes)
			}
			if !accountChange {
				op, err := f.service.Store.GetAnimeOrganization(context.Background(), job.ID)
				if err != nil || op.Progress.State != "done" || op.Progress.Cleanup != "skipped" {
					t.Fatal("paused cleanup invalidated confirmed moves", op.Progress, err)
				}
			}
		})
	}
}
