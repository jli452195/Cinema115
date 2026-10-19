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

const animeStatusEligible = `j.workflow_state='active' AND j.file_state<>'verified'
 AND j.download_state IN ('accepted','downloading','unknown','completed')
 AND (j.block_code IS NULL OR j.block_code IN ('TASK_READ_FAILED','TASK_STATUS_UNKNOWN','TASK_MISSING','TASK_RATE_LIMITED','TASK_POLL_CANCELLED','TASK_RESULT_PENDING'))
 AND EXISTS(SELECT 1 FROM anime_drive_operations o WHERE o.download_job_id=j.id AND o.kind='submit' AND o.state='confirmed' AND o.generation=j.generation AND o.authorized_at IS NOT NULL)`

// Status reads have their own short reservation. They cannot authorize a drive
// write or replay a submit. Full write-stage recovery remains a separate worker.
type AnimeStatusPoll struct {
	Job       model.AnimeDownloadJob
	Operation model.AnimeSubmitOperation
	Token     string
	Until     int64
}

// A pass excludes UIDs already read, sharing the same budget across that UID's
// jobs. NULL next_run_at means an initial confirmed task has not been polled.
func (s *Store) ReserveAnimeStatusPoll(ctx context.Context, now int64, excludeUIDs []string) (AnimeStatusPoll, bool, error) {
	var out AnimeStatusPoll
	if now <= 0 || len(excludeUIDs) > 4 {
		return out, false, ErrAnimeInvalid
	}
	args := []any{now, now, now, now, now - 5000}
	query := `SELECT ` + animeJobColumns + ` FROM anime_download_jobs j WHERE ` + animeStatusEligible + ` AND (j.next_run_at IS NULL OR j.next_run_at<=?) AND (j.lease_until IS NULL OR j.lease_until<=?)
 AND NOT EXISTS(SELECT 1 FROM anime_download_jobs busy WHERE busy.uid=j.uid AND busy.workflow_state='active' AND busy.file_state<>'verified' AND busy.lease_until>?)
 AND NOT EXISTS(SELECT 1 FROM anime_download_jobs limited WHERE limited.uid=j.uid AND limited.workflow_state='active' AND limited.file_state<>'verified' AND limited.block_code='TASK_RATE_LIMITED' AND limited.next_run_at>?)
 AND NOT EXISTS(SELECT 1 FROM anime_download_jobs recent WHERE recent.uid=j.uid AND recent.workflow_state='active' AND recent.file_state<>'verified' AND json_extract(recent.raw_status_json,'$.attemptedAt')>?)
 AND NOT EXISTS(SELECT 1 FROM anime_download_jobs denied WHERE denied.uid=j.uid AND denied.workflow_state='active' AND denied.file_state<>'verified' AND denied.account_id=j.account_id AND denied.account_revision=j.account_revision AND denied.block_code='TASK_ACCESS_DENIED')`
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
	out.Operation, err = scanAnimeSubmit(tx.QueryRowContext(ctx, `SELECT `+animeSubmitColumns+` FROM anime_drive_operations WHERE download_job_id=? AND kind='submit'`, out.Job.ID))
	if err != nil {
		return out, false, err
	}
	request, err := animeSubmitRequest(out.Job, out.Job.Snapshot.Target)
	if err != nil || request != out.Operation.Request || out.Operation.Result == nil || out.Operation.Result.Kind != "matched" || out.Operation.Result.TargetCID != request.DirectoryCID {
		return out, false, ErrAnimeIdentity
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
		return AnimeStatusPoll{}, false, err
	}
	return out, true, nil
}

func animeStatusCodeValid(code string) bool {
	switch code {
	case "", "TASK_READ_FAILED", "TASK_STATUS_UNKNOWN", "TASK_MISSING", "TASK_RATE_LIMITED", "TASK_POLL_CANCELLED", "TASK_RESULT_PENDING", "TASK_TARGET_CONFLICT", "TASK_RESULT_CHANGED", "TASK_STATUS_CONFLICT", "DOWNLOAD_ACCOUNT_CHANGED", "TASK_ACCESS_DENIED":
		return true
	}
	return false
}

// CompleteAnimeStatusPoll only updates read observations/download state. It
// never verifies files, changes workflow, touches submit permission or creates
// a new job. CAS prevents late receipts reviving a cancelled/verified task.
func (s *Store) CompleteAnimeStatusPoll(ctx context.Context, poll AnimeStatusPoll, observation *model.AnimeOfflineStatus, code string, retryAt, now int64) error {
	if !model.ValidAnimeID(poll.Job.ID) || !model.ValidAnimeID(poll.Token) || now <= 0 || retryAt < 0 || !animeStatusCodeValid(code) || observation != nil && (!observation.Valid() || code != "") || observation == nil && code == "" {
		return ErrAnimeInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var current bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM anime_download_jobs j WHERE j.id=? AND j.generation=? AND j.revision=? AND j.lease_token=? AND j.lease_until=? AND j.lease_until>? AND `+animeStatusEligible+`)`, poll.Job.ID, poll.Job.Generation, poll.Job.Revision, poll.Token, poll.Until, now).Scan(&current); err != nil {
		return err
	}
	if !current {
		return ErrAnimeConflict
	}
	job, err := scanAnimeDownloadJob(tx.QueryRowContext(ctx, `SELECT `+animeJobColumns+` FROM anime_download_jobs WHERE id=?`, poll.Job.ID))
	if err != nil {
		return err
	}
	request, err := animeSubmitRequest(job, job.Snapshot.Target)
	if err != nil || request != poll.Operation.Request {
		return ErrAnimeIdentity
	}
	op, err := scanAnimeSubmit(tx.QueryRowContext(ctx, `SELECT `+animeSubmitColumns+` FROM anime_drive_operations WHERE download_job_id=? AND kind='submit'`, job.ID))
	if err != nil {
		return err
	}
	if op.ID != poll.Operation.ID || op.Request != request || op.Result == nil || op.Result.Kind != "matched" || op.Result.TargetCID != request.DirectoryCID {
		return ErrAnimeIdentity
	}
	if code != "DOWNLOAD_ACCOUNT_CHANGED" {
		if err := checkAnimeSubmitIdentityTx(ctx, tx, job, request); err != nil {
			return err
		}
	}
	tracking := model.AnimeDownloadTracking{SchemaVersion: 1}
	if job.Tracking != nil {
		tracking = *job.Tracking
	}
	tracking.AttemptedAt = now
	state := job.DownloadState
	resultID := job.ResultObjectID
	if resultID == nil && op.Result.ResultID != "" {
		id := op.Result.ResultID
		resultID = &id
	}
	if observation != nil {
		if observation.Present && (observation.InfoHash != request.InfoHash || observation.TargetCID != "" && observation.TargetCID != request.DirectoryCID) {
			code, observation = "TASK_TARGET_CONFLICT", nil
		} else if observation.Present && resultID != nil && observation.ResultID != "" && observation.ResultID != *resultID {
			code, observation = "TASK_RESULT_CHANGED", nil
		} else if observation.Present && observation.TargetCID == "" && (resultID == nil || observation.ResultID != *resultID) {
			code, observation = "TASK_TARGET_CONFLICT", nil
		} else if state == "completed" && observation.State != "completed" && observation.State != "unknown" {
			code, observation = "TASK_STATUS_CONFLICT", nil
		}
	}
	if observation != nil {
		tracking.Observation, tracking.ObservedAt = observation, now
		if !observation.Present {
			code = "TASK_MISSING"
		} else {
			if observation.ResultID != "" {
				id := observation.ResultID
				resultID = &id
			}
			code = ""
			if observation.State == "unknown" {
				code = "TASK_STATUS_UNKNOWN"
			}
			if observation.State == "completed" && resultID == nil {
				code = "TASK_RESULT_PENDING"
			}
		}
		// Preserve already observed remote completion while a subsequent read is
		// unknown/missing. This is never equivalent to file verification.
		if state != "completed" {
			state = observation.State
		}
	}
	var next *int64
	stop := code == "TASK_TARGET_CONFLICT" || code == "TASK_RESULT_CHANGED" || code == "TASK_STATUS_CONFLICT" || code == "DOWNLOAD_ACCOUNT_CHANGED" || code == "TASK_ACCESS_DENIED"
	if code == "" || code == "TASK_RESULT_PENDING" {
		tracking.Failures = 0
	} else {
		tracking.Failures = min(tracking.Failures+1, 1000)
	}
	if !stop && state != "failed" {
		delay := int64(time.Minute / time.Millisecond)
		if state == "completed" {
			delay = int64(5 * time.Minute / time.Millisecond)
		}
		if tracking.Failures > 0 {
			delay = max(delay, animeDiscoveryBackoff(tracking.Failures))
		}
		due := max(now+delay+animeDiscoveryJitter(job.ID)/6, retryAt)
		next = &due
	}
	fileState := job.FileState
	if stop {
		fileState = "needs_review"
	}
	raw, err := animeJSON(tracking)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE anime_download_jobs SET download_state=?,file_state=?,block_code=NULLIF(?,''),raw_status_json=?,result_object_id=?,result_object_kind=CASE WHEN ? IS NULL THEN result_object_kind ELSE COALESCE(result_object_kind,'unknown') END,next_run_at=?,lease_token=NULL,lease_until=NULL,revision=revision+1,updated_at=? WHERE id=?`, state, fileState, code, raw, resultID, resultID, next, now, job.ID); err != nil {
		return fmt.Errorf("record anime status: %w", err)
	}
	return tx.Commit()
}
