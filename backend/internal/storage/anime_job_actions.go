package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"strm115/backend/internal/model"
)

type AnimeJobActions struct {
	Reconcile bool `json:"reconcile"`
	Retry     bool `json:"retry"`
}

type AnimeJobActionReceipt struct {
	ID          string `json:"id"`
	Revision    int64  `json:"revision"`
	ScheduledAt int64  `json:"scheduledAt"`
}

// Both actions schedule existing work. Retry means retry a failed READ or an
// unissued preparation; neither can create a job or replay a dispatched write.
func animeJobActionsTx(ctx context.Context, tx *sql.Tx, job model.AnimeDownloadJob, now int64) (AnimeJobActions, error) {
	var out AnimeJobActions
	if job.WorkflowState != "active" || job.FileState == "verified" && job.Snapshot.Organization == nil || job.DownloadState == "cancelled" || job.DownloadState == "failed" || job.DownloadState == "blocked" {
		return out, nil
	}
	var busy bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM anime_download_jobs WHERE id=? AND lease_until>?)`, job.ID, now).Scan(&busy); err != nil {
		return out, err
	}
	if busy {
		return out, nil
	}
	if job.Tracking != nil && job.Tracking.AttemptedAt > now-int64(30*time.Second/time.Millisecond) {
		return out, nil
	}
	if err := checkAnimeDownloadTargetTx(ctx, tx, job.Snapshot.Target, false); err != nil {
		if errors.Is(err, ErrAnimeAccountChanged) || errors.Is(err, ErrAnimeDirectoryRequired) {
			return out, nil
		}
		return out, err
	}
	code := ""
	if job.BlockCode != nil {
		code = *job.BlockCode
	}
	// A recorded Retry-After must survive every manual click.
	if code == "TASK_RATE_LIMITED" && job.NextRunAt != nil && *job.NextRunAt > now {
		return out, nil
	}
	if job.FileState == "verified" {
		op, err := scanAnimeOrganize(tx.QueryRowContext(ctx, `SELECT `+animeOrganizeColumns+` FROM anime_drive_operations WHERE download_job_id=? AND kind='organize'`, job.ID))
		if errors.Is(err, sql.ErrNoRows) {
			return out, nil
		}
		if err != nil {
			return out, err
		}
		// Reconciliation uses the recorded step. Replanning is limited to a
		// mapping failure before any external write was authorized.
		out.Reconcile = op.Progress.Step != nil && op.Progress.State != "done" && code != "DOWNLOAD_ACCOUNT_CHANGED" && code != "TASK_ACCESS_DENIED"
		if code == "ORGANIZE_TARGET_CONFLICT" && job.DownloadState == "completed" &&
			op.Generation == job.Generation && op.Request.Target == job.Snapshot.Organization.Target &&
			op.Progress.State == "needs_review" && op.Progress.Step == nil && op.Progress.Cleanup == "" {
			if err := checkAnimeJobAuthorizationTx(ctx, tx, job); err == nil {
				out.Retry = true
			} else if !errors.Is(err, ErrAnimeWriteUnauthorized) {
				return out, err
			}
		}
		if code == "ORGANIZE_MAPPING_REQUIRED" && job.DownloadState == "completed" &&
			op.Generation == job.Generation && op.Request.Target == job.Snapshot.Organization.Target &&
			op.Progress.State == "needs_review" && op.Progress.Code == "ORGANIZE_MAPPING_REQUIRED" &&
			op.Progress.Step == nil && len(op.Progress.Directories) == 0 && op.Progress.Cleanup == "" {
			var unissued bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM anime_drive_operations WHERE id=? AND state='failed' AND authorized_at IS NULL)
 AND (? OR EXISTS(SELECT 1 FROM anime_job_selections x JOIN anime_subscription_sources s ON s.id=x.source_id JOIN anime_subscriptions p ON p.id=s.subscription_id WHERE x.job_id=? AND x.status='authorized' AND s.status='enabled' AND s.run_when_enabled=1 AND p.status='enabled'))`, op.ID, job.Snapshot.Redownload, job.ID).Scan(&unissued); err != nil {
				return out, err
			}
			if unissued {
				for _, file := range op.Progress.Files {
					unissued = unissued && !file.Moved
				}
			}
			if unissued {
				if err := checkAnimeJobAuthorizationTx(ctx, tx, job); err == nil {
					out.Retry = true
				} else if !errors.Is(err, ErrAnimeWriteUnauthorized) {
					return out, err
				}
			}
		}
		return out, nil
	}
	switch code {
	case "", "TASK_RATE_LIMITED", "SUBMIT_UNCERTAIN", "TASK_READ_FAILED", "TASK_STATUS_UNKNOWN", "TASK_MISSING", "TASK_POLL_CANCELLED", "TASK_RESULT_PENDING", "DIRECTORY_CREATE_UNCERTAIN", "DIRECTORY_READ_FAILED", "FILES_READ_FAILED", "FILES_INCOMPLETE":
	default:
		return out, nil
	}
	if job.DownloadState == "completed" && job.ResultObjectID != nil {
		out.Reconcile = true
		out.Retry = code == "FILES_READ_FAILED" || code == "FILES_INCOMPLETE"
		return out, nil
	}
	op, err := scanAnimeSubmit(tx.QueryRowContext(ctx, `SELECT `+animeSubmitColumns+` FROM anime_drive_operations WHERE download_job_id=? AND kind='submit'`, job.ID))
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	if err == nil && (op.State == "confirmed" || op.State == "dispatched" || op.State == "unknown") {
		out.Reconcile = true
		out.Retry = op.State == "confirmed" && code == "TASK_READ_FAILED"
		return out, nil
	}
	// Directory reconciliation may lead into the first submit in the normal
	// worker. Leave it on that schedule rather than labelling it a read-only action.
	if job.DownloadState == "queued" && (code == "TASK_READ_FAILED" || code == "DIRECTORY_READ_FAILED") {
		if err := checkAnimeJobAuthorizationTx(ctx, tx, job); errors.Is(err, ErrAnimeWriteUnauthorized) {
			return out, nil
		} else if err != nil {
			return out, err
		}
		out.Retry = true
	}
	return out, nil
}

func (s *Store) AnimeDownloadJobActions(ctx context.Context, job model.AnimeDownloadJob) (AnimeJobActions, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return AnimeJobActions{}, err
	}
	defer tx.Rollback()
	return animeJobActionsTx(ctx, tx, job, time.Now().UnixMilli())
}

func (s *Store) ScheduleAnimeJobAction(ctx context.Context, id, action string, revision int64, key AnimeWriteKey) (AnimeJobActionReceipt, error) {
	out := AnimeJobActionReceipt{ID: id}
	if !model.ValidAnimeID(id) || revision < 1 || revision >= model.AnimeMaxRevision || action != "reconcile" && action != "retry" {
		return out, ErrAnimeInvalid
	}
	if err := key.validate(); err != nil {
		return out, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if found, err := readAnimeActionReceiptTx(ctx, tx, key, &out); found || err != nil {
		return out, err
	}
	job, err := scanAnimeDownloadJob(tx.QueryRowContext(ctx, `SELECT `+animeJobColumns+` FROM anime_download_jobs WHERE id=?`, id))
	if err != nil {
		return out, err
	}
	if job.Revision != revision {
		return out, ErrAnimeConflict
	}
	now := time.Now().UnixMilli()
	actions, err := animeJobActionsTx(ctx, tx, job, now)
	if err != nil {
		return out, err
	}
	if action == "reconcile" && !actions.Reconcile || action == "retry" && !actions.Retry {
		return out, ErrAnimeTransition
	}
	// Do not erase an unknown directory/submit marker. The runner must still
	// choose reconciliation, even if a read is being retried.
	code := job.BlockCode
	if job.DownloadState == "completed" || job.DownloadState == "accepted" || job.DownloadState == "downloading" || job.DownloadState == "queued" && actions.Retry {
		code = nil
	}
	if job.FileState == "verified" && action == "retry" && job.BlockCode != nil && *job.BlockCode == "ORGANIZE_TARGET_CONFLICT" {
		// Resume the original journal. Successful moves and keep-existing
		// decisions survive; an in-flight write is never replayed here.
		if _, err := tx.ExecContext(ctx, `UPDATE anime_drive_operations SET state='prepared',result_json=json_set(result_json,'$.state','working','$.code',''),error_code=NULL,updated_at=? WHERE download_job_id=? AND kind='organize'`, now, job.ID); err != nil {
			return out, err
		}
	} else if job.FileState == "verified" && action == "retry" {
		// Discard only an unissued mapping plan. The worker rebuilds it from
		// the same manifest and frozen settings; download/submit facts stay intact.
		result, err := tx.ExecContext(ctx, `DELETE FROM anime_drive_operations WHERE download_job_id=? AND kind='organize' AND generation=? AND state='failed' AND authorized_at IS NULL`, job.ID, job.Generation)
		if err != nil {
			return out, err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return out, err
		}
		if count != 1 {
			return out, ErrAnimeConflict
		}
	} else if job.FileState == "verified" {
		organizationCode := "ORGANIZE_UNCERTAIN"
		code = &organizationCode
		if _, err := tx.ExecContext(ctx, `UPDATE anime_drive_operations SET state='unknown',result_json=json_set(result_json,'$.state','unknown','$.code','ORGANIZE_UNCERTAIN'),error_code='ORGANIZE_UNCERTAIN',updated_at=? WHERE download_job_id=? AND kind='organize' AND json_extract(result_json,'$.step') IS NOT NULL`, now, id); err != nil {
			return out, err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE anime_download_jobs SET file_state=CASE WHEN file_state='verified' THEN file_state ELSE 'pending' END,block_code=?,next_run_at=?,revision=revision+1,updated_at=? WHERE id=?`, code, now, now, id); err != nil {
		return out, err
	}
	out.Revision, out.ScheduledAt = revision+1, now
	if err := writeAnimeActionReceiptTx(ctx, tx, key, out, now); err != nil {
		return out, err
	}
	return out, tx.Commit()
}
