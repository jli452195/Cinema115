package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"strm115/backend/internal/model"
)

// Isolated fixture emulating a plan rejected by the old subtitle matcher,
// before it authorized any external organization write.
func animeUnissuedMappingFixture(t *testing.T) (*animeDownloadFixture, *sql.DB, model.AnimeDownloadJob, *animeOrganizeDrive) {
	t.Helper()
	f, db, _, job, drive := animeOrganizeFixture(t)
	job.Files.Entries[2].Name = "Kono Oto Tomare! - 05 [1080p].mkv"
	job.Files.Entries[3].Name = "[Kono Oto Tomare][05][CHT].ass"
	raw, err := json.Marshal(job.Files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE anime_download_jobs SET files_json=? WHERE id=?`, string(raw), job.ID); err != nil {
		t.Fatal(err)
	}
	drive.entries["300"] = append([]model.AnimeDriveEntry{}, job.Files.Entries[2:]...)
	op, err := f.service.Store.PrepareAnimeOrganization(context.Background(), job, "")
	if err != nil || len(op.Request.Files) != 2 {
		t.Fatal("current matcher did not produce video and subtitle plans", op, err)
	}
	op.Request.Files, op.Progress.Files = op.Request.Files[:1], op.Progress.Files[:1]
	op.Progress.State, op.Progress.Code = "needs_review", "ORGANIZE_MAPPING_REQUIRED"
	request, err := json.Marshal(op.Request)
	if err != nil {
		t.Fatal(err)
	}
	progress, err := json.Marshal(op.Progress)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE anime_drive_operations SET state='failed',request_json=?,result_json=?,error_code='ORGANIZE_MAPPING_REQUIRED' WHERE id=?`, string(request), string(progress), op.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE anime_download_jobs SET block_code='ORGANIZE_MAPPING_REQUIRED',next_run_at=NULL,raw_status_json=json_set(raw_status_json,'$.attemptedAt',0),revision=revision+1 WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	job, err = f.service.Store.GetAnimeDownloadJob(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	return f, db, job, drive
}

func TestAnimeRetryUnissuedOrganizationReplansAndCompletesOriginalJob(t *testing.T) {
	f, _, job, drive := animeUnissuedMappingFixture(t)
	cleanup := animeEnableCleanup(f, drive)
	ctx := context.Background()
	view, err := f.service.JobView(ctx, job.ID)
	if err != nil || !view.PermittedActions.Retry || view.PermittedActions.Reconcile {
		t.Fatal("unissued mapping plan cannot be retried", view, err)
	}
	in := AnimeControlInput{ExpectedRevision: job.Revision}
	receipt, err := f.service.JobAction(ctx, "admin", "retry_mapping_plan", job.ID, "retry", in)
	if err != nil {
		t.Fatal(err)
	}
	scheduled, err := f.service.Store.GetAnimeDownloadJob(ctx, job.ID)
	if err != nil || !reflect.DeepEqual(scheduled.Snapshot, job.Snapshot) || !reflect.DeepEqual(scheduled.Files, job.Files) || scheduled.Generation != job.Generation || scheduled.DirectoryCID == nil || *scheduled.DirectoryCID != *job.DirectoryCID || scheduled.BlockCode != nil || scheduled.NextRunAt == nil {
		t.Fatal("retry changed frozen download facts", scheduled, err)
	}
	if _, err := f.service.Store.GetAnimeOrganization(ctx, job.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("old unissued plan was not discarded", err)
	}
	runner := &AnimeStatusRunner{Admin: f.service, now: func() time.Time { return time.Now().Add(time.Minute) }}
	if err := runner.PollOnce(ctx); err != nil {
		t.Fatal(err)
	}
	view, err = f.service.JobView(ctx, job.ID)
	if err != nil || view.WorkflowState != "organized" || view.Organization.Moved != 2 || view.PermittedActions.Retry || view.PermittedActions.Reconcile {
		t.Fatal("original job did not complete with its subtitle", view, err)
	}
	if drive.reads != 1 || drive.creates != 2 || drive.renames != 1 || drive.moves != 1 || cleanup.deletes != 1 {
		t.Fatal("retry added cloud reads or repeated writes", drive, cleanup.deletes)
	}
	// A lost action response may be replayed even after organization finishes.
	again, err := f.service.JobAction(ctx, "admin", "retry_mapping_plan", job.ID, "retry", in)
	if err != nil || again != receipt {
		t.Fatal("retry receipt was not idempotent", again, err)
	}
	if err := runner.PollOnce(ctx); err != nil || drive.moves != 1 || cleanup.deletes != 1 {
		t.Fatal("receipt replay repeated organization", err)
	}
}

func TestAnimeOrganizationRetryKeepsWriteHistoryAndRollsBackFailedReceipt(t *testing.T) {
	for _, mode := range []string{"authorized write", "pending step", "moved file", "created directory", "stale generation", "paused", "source paused", "source disabled", "changed account", "active lease", "receipt failure"} {
		t.Run(mode, func(t *testing.T) {
			f, db, job, _ := animeUnissuedMappingFixture(t)
			statement := map[string]string{
				"authorized write":  `UPDATE anime_drive_operations SET authorized_at=1 WHERE kind='organize'`,
				"pending step":      `UPDATE anime_drive_operations SET result_json=json_set(result_json,'$.step',json('{"kind":"rename","cid":"300","ids":["400"]}')) WHERE kind='organize'`,
				"moved file":        `UPDATE anime_drive_operations SET result_json=json_set(result_json,'$.files[0].moved',json('true')) WHERE kind='organize'`,
				"created directory": `UPDATE anime_drive_operations SET result_json=json_set(result_json,'$.directories',json('[{"path":"series","cid":"901"}]')) WHERE kind='organize'`,
				"stale generation":  `UPDATE anime_download_jobs SET generation=generation+1`,
				"paused":            `UPDATE anime_subscriptions SET status='paused'`,
				"source paused":     `UPDATE anime_subscription_sources SET status='paused'`,
				"source disabled":   `UPDATE anime_subscription_sources SET run_when_enabled=0`,
				"changed account":   `UPDATE p115_accounts SET is_primary=0`,
				"active lease":      `UPDATE anime_download_jobs SET lease_token='busy',lease_until=9999999999999`,
				"receipt failure":   `CREATE TRIGGER retry_receipt_failure BEFORE INSERT ON anime_request_keys BEGIN SELECT RAISE(ABORT,'injected'); END`,
			}[mode]
			if _, err := db.Exec(statement); err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			before, err := f.service.Store.GetAnimeOrganization(ctx, job.ID)
			if err != nil {
				t.Fatal(err)
			}
			job, err = f.service.Store.GetAnimeDownloadJob(ctx, job.ID)
			if err != nil {
				t.Fatal(err)
			}
			actions, err := f.service.Store.AnimeDownloadJobActions(ctx, job)
			if err != nil || actions.Retry != (mode == "receipt failure") {
				t.Fatal("unexpected retry capability", actions, err)
			}
			if _, err := f.service.JobAction(ctx, "admin", "blocked_mapping_retry", job.ID, "retry", AnimeControlInput{ExpectedRevision: job.Revision}); err == nil {
				t.Fatal("invalid or uncommitted retry was accepted")
			}
			after, err := f.service.Store.GetAnimeOrganization(ctx, job.ID)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("failed retry erased organization history", after, err)
			}
			afterJob, err := f.service.Store.GetAnimeDownloadJob(ctx, job.ID)
			if err != nil || !reflect.DeepEqual(job, afterJob) {
				t.Fatal("failed retry changed the job", afterJob, err)
			}
		})
	}
}
