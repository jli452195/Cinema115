package storage

import (
	"context"
	"fmt"
	"time"

	"strm115/backend/internal/model"
)

// StartAnimeDirectory commits exactly one create permission. All subsequent
// calls return the saved intent for read-only reconciliation, never a replay.
func (s *Store) StartAnimeDirectory(ctx context.Context, jobID string, generation int64, target model.AnimeDownloadTarget, leaseToken ...string) (model.AnimeDownloadJob, bool, error) {
	var empty model.AnimeDownloadJob
	if !model.ValidAnimeID(jobID) || generation < 1 || target.Validate() != nil {
		return empty, false, ErrAnimeInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return empty, false, err
	}
	defer tx.Rollback()
	if err := checkAnimeLeaseTx(ctx, tx, jobID, leaseToken...); err != nil {
		return empty, false, err
	}
	job, err := scanAnimeDownloadJob(tx.QueryRowContext(ctx, `SELECT `+animeJobColumns+` FROM anime_download_jobs WHERE id=?`, jobID))
	if err != nil {
		return empty, false, err
	}
	if job.Generation != generation || job.Snapshot.Target != target || job.DirectoryName != "anime-"+job.ID || job.WorkflowState != "active" {
		return empty, false, ErrAnimeConflict
	}
	if err := checkAnimeDownloadTargetTx(ctx, tx, target, false); err != nil {
		return empty, false, err
	}
	if job.DirectoryCID != nil || job.DirectoryOperation != nil {
		return job, false, nil
	}
	if job.DownloadState != "queued" {
		return empty, false, ErrAnimeConflict
	}
	// Directory creation uses exactly the same resource/authorization boundary
	// as submit, except that no directory CID exists yet.
	if err := checkAnimeJobAuthorizationTx(ctx, tx, job); err != nil {
		return empty, false, err
	}
	var valid bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM anime_resource_claims c JOIN anime_resources r ON r.id=c.resource_id WHERE c.id=? AND c.uid=? AND c.account_id=? AND c.target_cid=? AND r.protocol='bt_v1' AND r.info_hash=?)`, job.ClaimID, job.UID, job.AccountID, job.ParentCID, job.Snapshot.InfoHash).Scan(&valid); err != nil {
		return empty, false, err
	}
	if !valid || job.AccountID != target.AccountID || job.AccountRevision != target.AccountRevision || job.UID != target.UID || job.ParentCID != target.ParentCID {
		return empty, false, ErrAnimeIdentity
	}
	operation := model.AnimeDirectoryOperation{SchemaVersion: 1, State: "dispatched", Generation: generation, AuthorizedAt: time.Now().UnixMilli()}
	raw, err := animeJSON(operation)
	if err != nil {
		return empty, false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE anime_download_jobs SET directory_operation_json=?,revision=revision+1,block_code=NULL,updated_at=? WHERE id=?`, raw, operation.AuthorizedAt, job.ID); err != nil {
		return empty, false, err
	}
	job.DirectoryOperation = &operation
	job.Revision++
	job.UpdatedAt = operation.AuthorizedAt
	if err := tx.Commit(); err != nil {
		return empty, false, err
	}
	return job, true, nil
}

// A successful create receipt or a unique scoped recovery listing binds the
// directory. Successful writes do not require a second metadata request.
func (s *Store) FinishAnimeDirectory(ctx context.Context, expected model.AnimeDownloadJob, directory *model.AnimeDriveEntry, code string, leaseToken ...string) error {
	if !model.ValidAnimeID(expected.ID) || expected.DirectoryOperation == nil {
		return ErrAnimeInvalid
	}
	if directory != nil && (!directory.Valid() || directory.Kind != "directory" || directory.ParentCID != expected.ParentCID || directory.Name != expected.DirectoryName || code != "") {
		return ErrAnimeInvalid
	}
	switch code {
	case "", "DIRECTORY_CREATE_UNCERTAIN", "DIRECTORY_READ_FAILED", "DIRECTORY_NOT_FOUND", "DIRECTORY_CONFLICT", "DOWNLOAD_ACCOUNT_CHANGED":
	default:
		return ErrAnimeInvalid
	}
	if directory == nil && code == "" {
		return ErrAnimeInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := checkAnimeLeaseTx(ctx, tx, expected.ID, leaseToken...); err != nil {
		return err
	}
	job, err := scanAnimeDownloadJob(tx.QueryRowContext(ctx, `SELECT `+animeJobColumns+` FROM anime_download_jobs WHERE id=?`, expected.ID))
	if err != nil {
		return err
	}
	if job.Generation != expected.Generation || job.Revision != expected.Revision || job.WorkflowState != "active" || job.DownloadState != "queued" || job.DirectoryCID != nil || job.DirectoryOperation == nil || job.DirectoryOperation.SchemaVersion != 1 || job.DirectoryOperation.State != "dispatched" && job.DirectoryOperation.State != "unknown" || job.DirectoryOperation.Generation != expected.Generation || job.DirectoryOperation.AuthorizedAt != expected.DirectoryOperation.AuthorizedAt {
		return ErrAnimeConflict
	}
	if code != "DOWNLOAD_ACCOUNT_CHANGED" {
		if err := checkAnimeDownloadTargetTx(ctx, tx, job.Snapshot.Target, false); err != nil {
			return err
		}
	}
	op := *job.DirectoryOperation
	op.State, op.ErrorCode = "unknown", code
	var cid *string
	if directory != nil {
		op.State, op.CID = "confirmed", directory.ID
		cid = &op.CID
	}
	if code == "DIRECTORY_CONFLICT" || code == "DOWNLOAD_ACCOUNT_CHANGED" {
		op.State = "failed"
	}
	raw, err := animeJSON(op)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE anime_download_jobs SET directory_operation_json=?,directory_cid=?,block_code=NULLIF(?,''),revision=revision+1,updated_at=? WHERE id=?`, raw, cid, code, time.Now().UnixMilli(), job.ID); err != nil {
		return err
	}
	return tx.Commit()
}

// SaveAnimeFiles atomically saves one bounded result tree and ends offline
// polling. Organizing is a later action over this same manifest, not a copy.
func (s *Store) SaveAnimeFiles(ctx context.Context, expected model.AnimeDownloadJob, manifest model.AnimeFileManifest, leaseToken ...string) error {
	if !model.ValidAnimeID(expected.ID) || manifest.Validate(expected) != nil {
		return ErrAnimeInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := checkAnimeLeaseTx(ctx, tx, expected.ID, leaseToken...); err != nil {
		return err
	}
	job, err := scanAnimeDownloadJob(tx.QueryRowContext(ctx, `SELECT `+animeJobColumns+` FROM anime_download_jobs WHERE id=?`, expected.ID))
	if err != nil {
		return err
	}
	if job.Generation != expected.Generation || job.Revision != expected.Revision || !job.CanCollectFiles() {
		return ErrAnimeConflict
	}
	if err := manifest.Validate(job); err != nil {
		return ErrAnimeIdentity
	}
	request, err := animeSubmitRequest(job, job.Snapshot.Target)
	if err != nil {
		return err
	}
	if err := checkAnimeSubmitIdentityTx(ctx, tx, job, request); err != nil {
		return err
	}
	op, err := scanAnimeSubmit(tx.QueryRowContext(ctx, `SELECT `+animeSubmitColumns+` FROM anime_drive_operations WHERE download_job_id=? AND kind='submit'`, job.ID))
	if err != nil {
		return err
	}
	if op.State != "confirmed" || op.Generation != job.Generation || op.Request != request {
		return ErrAnimeIdentity
	}
	raw, err := animeJSON(manifest)
	if err != nil {
		return err
	}
	resultKind := ""
	for _, entry := range manifest.Entries {
		if entry.ID == manifest.ResultID {
			resultKind = entry.Kind
		}
	}
	// Metadata alone does not authorize organization. Only an explicit frozen
	// organization plan leaves work for the later organizer.
	workflow := "active"
	if job.Snapshot.Organization == nil {
		workflow = "original_saved"
	}
	if _, err := tx.ExecContext(ctx, `UPDATE anime_download_jobs SET files_json=?,file_state='verified',result_object_kind=?,workflow_state=?,block_code=NULL,next_run_at=NULL,lease_token=NULL,lease_until=NULL,revision=revision+1,updated_at=? WHERE id=?`, raw, resultKind, workflow, manifest.CollectedAt, job.ID); err != nil {
		return fmt.Errorf("save anime download files: %w", err)
	}
	return tx.Commit()
}

// Only explicitly transient failures receive two scheduled retries. The
// counter survives restarts; an old receipt cannot clear a newer worker lease.
func (s *Store) RecordAnimeFilesFailure(ctx context.Context, expected model.AnimeDownloadJob, code string, retryAt int64, leaseToken ...string) error {
	if !model.ValidAnimeID(expected.ID) || !expected.CanCollectFiles() || retryAt < 0 {
		return ErrAnimeInvalid
	}
	switch code {
	case "FILES_READ_FAILED", "FILES_INCOMPLETE", "TASK_RATE_LIMITED", "TASK_POLL_CANCELLED":
	case "FILES_OUTSIDE_TASK", "DOWNLOAD_ACCOUNT_CHANGED", "TASK_ACCESS_DENIED":
		if retryAt != 0 {
			return ErrAnimeInvalid
		}
	default:
		return ErrAnimeInvalid
	}
	token := ""
	if len(leaseToken) > 1 {
		return ErrAnimeInvalid
	}
	if len(leaseToken) == 1 {
		token = leaseToken[0]
	}
	now := time.Now().UnixMilli()
	tracking := model.AnimeDownloadTracking{SchemaVersion: 1}
	if expected.Tracking != nil {
		tracking = *expected.Tracking
	}
	tracking.AttemptedAt = now
	tracking.FileReadFailures = min(tracking.FileReadFailures+1, 3)
	state := "needs_review"
	var next *int64
	if retryAt > 0 && tracking.FileReadFailures <= 2 {
		delay := [...]time.Duration{5 * time.Second, 30 * time.Second}[tracking.FileReadFailures-1]
		due := max(now+delay.Milliseconds(), retryAt)
		next, state = &due, "checking"
	} else if code == "TASK_RATE_LIMITED" && retryAt > now {
		// Retain the UID-wide cooldown even when this job exhausts its budget.
		next = &retryAt
	}
	raw, err := animeJSON(tracking)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE anime_download_jobs SET file_state=?,block_code=?,raw_status_json=?,revision=revision+1,next_run_at=?,lease_token=NULL,lease_until=NULL,updated_at=? WHERE id=? AND revision=? AND generation=? AND workflow_state='active' AND download_state='completed' AND file_state<>'verified' AND ((?='' AND lease_token IS NULL) OR (lease_token=? AND lease_until>?))`, state, code, raw, next, now, expected.ID, expected.Revision, expected.Generation, token, token, now)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrAnimeConflict
	}
	return nil
}
