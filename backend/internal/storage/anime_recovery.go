package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"strm115/backend/internal/model"
)

// Reuse the download row's lease. No second queue or heartbeat is needed:
// each execution is bounded to 90 seconds, below this two-minute lease.
type AnimeDownloadWork struct {
	Job   model.AnimeDownloadJob
	Token string
	Until int64
}

func (s *Store) ReserveAnimeDownloadWork(ctx context.Context, now int64, excludeUIDs []string) (AnimeDownloadWork, bool, error) {
	var out AnimeDownloadWork
	if now <= 0 || len(excludeUIDs) > 4 {
		return out, false, ErrAnimeInvalid
	}
	query := `SELECT ` + animeJobColumns + ` FROM anime_download_jobs j WHERE j.workflow_state='active' AND j.file_state IN ('pending','checking')
 AND ((j.download_state='queued' AND NOT EXISTS(SELECT 1 FROM anime_drive_operations done WHERE done.download_job_id=j.id AND done.kind='submit' AND done.state IN ('confirmed','failed'))) OR (j.download_state IN ('submitting','unknown') AND EXISTS(SELECT 1 FROM anime_drive_operations o WHERE o.download_job_id=j.id AND o.kind='submit' AND o.state IN ('dispatched','unknown') AND o.generation=j.generation)) OR (j.download_state='completed' AND j.result_object_id IS NOT NULL))
 AND (j.download_state<>'queued' OR json_extract(j.directory_operation_json,'$.state') IN ('dispatched','unknown') OR EXISTS(SELECT 1 FROM anime_job_selections x JOIN anime_subscription_sources s ON s.id=x.source_id JOIN anime_subscriptions p ON p.id=s.subscription_id WHERE x.job_id=j.id AND x.status='authorized' AND s.status='enabled' AND s.run_when_enabled=1 AND p.status='enabled' AND p.first_enabled_at IS NOT NULL))
 AND (j.block_code IS NULL OR j.block_code IN ('SUBMIT_UNCERTAIN','TASK_READ_FAILED','TASK_MISSING','TASK_POLL_CANCELLED','DIRECTORY_CREATE_UNCERTAIN','DIRECTORY_READ_FAILED'))
 AND (j.next_run_at IS NULL OR j.next_run_at<=?) AND (j.lease_until IS NULL OR j.lease_until<=?)
 AND NOT EXISTS(SELECT 1 FROM anime_download_jobs busy WHERE busy.uid=j.uid AND busy.lease_until>?)
 AND NOT EXISTS(SELECT 1 FROM anime_download_jobs limited WHERE limited.uid=j.uid AND limited.workflow_state='active' AND limited.block_code='TASK_RATE_LIMITED' AND limited.next_run_at>?)
 AND NOT EXISTS(SELECT 1 FROM anime_download_jobs recent WHERE recent.uid=j.uid AND recent.workflow_state='active' AND json_extract(recent.raw_status_json,'$.attemptedAt')>?)
 AND NOT EXISTS(SELECT 1 FROM anime_download_jobs denied WHERE denied.account_id=j.account_id AND denied.account_revision=j.account_revision AND denied.block_code='TASK_ACCESS_DENIED' AND denied.workflow_state='active')`
	args := []any{now, now, now, now, now - 5000}
	if len(excludeUIDs) > 0 {
		query += ` AND j.uid NOT IN (` + strings.TrimSuffix(strings.Repeat("?,", len(excludeUIDs)), ",") + `)`
		for _, uid := range excludeUIDs {
			if !model.ValidAnimeExternalID(uid) {
				return out, false, ErrAnimeInvalid
			}
			args = append(args, uid)
		}
	}
	query += ` ORDER BY COALESCE(j.next_run_at,0),j.id LIMIT 1`
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return out, false, err
	}
	defer tx.Rollback()
	out.Job, err = scanAnimeDownloadJob(tx.QueryRowContext(ctx, query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return out, false, tx.Commit()
	}
	if err != nil {
		return out, false, err
	}
	out.Token, err = newAnimeID()
	if err != nil {
		return out, false, err
	}
	out.Until = now + int64(2*time.Minute/time.Millisecond)
	if _, err := tx.ExecContext(ctx, `UPDATE anime_download_jobs SET lease_token=?,lease_until=? WHERE id=?`, out.Token, out.Until, out.Job.ID); err != nil {
		return out, false, err
	}
	if err := tx.Commit(); err != nil {
		return AnimeDownloadWork{}, false, err
	}
	return out, true, nil
}

// An absent token is valid only for an unleased, explicitly invoked step.
func checkAnimeLeaseTx(ctx context.Context, tx *sql.Tx, jobID string, tokens ...string) error {
	if len(tokens) > 1 {
		return ErrAnimeInvalid
	}
	token := ""
	if len(tokens) == 1 {
		token = tokens[0]
	}
	var current bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM anime_download_jobs WHERE id=? AND ((?='' AND lease_token IS NULL) OR (lease_token=? AND lease_until>?)))`, jobID, token, token, time.Now().UnixMilli()).Scan(&current); err != nil {
		return err
	}
	if !current {
		return ErrAnimeConflict
	}
	return nil
}

func (s *Store) CheckAnimeDownloadWork(ctx context.Context, work AnimeDownloadWork) error {
	var current bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM anime_download_jobs WHERE id=? AND generation=? AND workflow_state='active' AND lease_token=? AND lease_until=? AND lease_until>?)`, work.Job.ID, work.Job.Generation, work.Token, work.Until, time.Now().UnixMilli()).Scan(&current); err != nil {
		return err
	}
	if !current {
		return ErrAnimeConflict
	}
	return nil
}

// Clear only our own lease, using current progress rather than an old revision.
// Confirmed receipts may have advanced several local stages during this lease.
func (s *Store) CompleteAnimeDownloadWork(ctx context.Context, work AnimeDownloadWork, code string, now int64) error {
	if !model.ValidAnimeID(work.Job.ID) || !model.ValidAnimeID(work.Token) || now <= 0 {
		return ErrAnimeInvalid
	}
	switch code {
	case "", "TASK_READ_FAILED", "TASK_POLL_CANCELLED", "DOWNLOAD_ACCOUNT_CHANGED", "DOWNLOAD_DIRECTORY_REQUIRED", "DOWNLOAD_PAUSED":
	default:
		return ErrAnimeInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var current bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM anime_download_jobs WHERE id=? AND generation=? AND lease_token=? AND lease_until=? AND lease_until>?)`, work.Job.ID, work.Job.Generation, work.Token, work.Until, now).Scan(&current); err != nil {
		return err
	}
	if !current {
		return ErrAnimeConflict
	}
	job, err := scanAnimeDownloadJob(tx.QueryRowContext(ctx, `SELECT `+animeJobColumns+` FROM anime_download_jobs WHERE id=?`, work.Job.ID))
	if err != nil {
		return err
	}
	if code == "DOWNLOAD_PAUSED" && job.DownloadState == "queued" && job.WorkflowState == "active" {
		// A create/reconciliation may finish after pause/remove. Save that
		// result, then suspend the next write without turning pause into a
		// file error. A quick resume or another source may already allow it.
		err := checkAnimeJobAuthorizationTx(ctx, tx, job)
		if errors.Is(err, ErrAnimeWriteUnauthorized) {
			if _, err := suspendAnimeQueuedJobTx(ctx, tx, job, now); err != nil {
				return err
			}
			return tx.Commit()
		}
		if err != nil {
			return err
		}
		code = ""
	}
	if code == "" && job.BlockCode != nil {
		code = *job.BlockCode
	}
	tracking := model.AnimeDownloadTracking{SchemaVersion: 1}
	if job.Tracking != nil {
		tracking = *job.Tracking
	}
	tracking.AttemptedAt = now
	if code == "" {
		tracking.Failures = 0
	} else {
		tracking.Failures = min(tracking.Failures+1, 1000)
	}
	next := job.NextRunAt
	fileState := job.FileState
	if job.WorkflowState != "active" || job.DownloadState == "failed" || job.DownloadState == "blocked" || job.FileState == "verified" || job.FileState == "needs_review" {
		next = nil
	} else {
		switch code {
		case "", "SUBMIT_UNCERTAIN", "TASK_READ_FAILED", "TASK_POLL_CANCELLED", "DIRECTORY_CREATE_UNCERTAIN", "DIRECTORY_READ_FAILED":
			delay := int64(5 * time.Minute / time.Millisecond)
			if code == "" && job.DownloadState == "accepted" {
				delay = int64(2 * time.Minute / time.Millisecond)
			}
			if tracking.Failures > 0 {
				delay = max(delay, animeDiscoveryBackoff(tracking.Failures))
			}
			due := now + delay
			if next != nil {
				due = max(due, *next)
			}
			next = &due
		default:
			// A completed reconciliation with no provable result needs a human,
			// not repeated directory scans or another write attempt.
			next, fileState = nil, "needs_review"
		}
	}
	raw, err := animeJSON(tracking)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE anime_download_jobs SET lease_token=NULL,lease_until=NULL,next_run_at=?,file_state=?,block_code=NULLIF(?,''),raw_status_json=?,revision=revision+1,updated_at=? WHERE id=?`, next, fileState, code, raw, now, job.ID); err != nil {
		return fmt.Errorf("complete anime download step: %w", err)
	}
	return tx.Commit()
}
