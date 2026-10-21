package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"time"

	"strm115/backend/internal/animepreview"
	"strm115/backend/internal/model"
)

var (
	ErrAnimeWriteUnauthorized = errors.New("anime download write is not authorized")
	ErrAnimeDirectoryRequired = errors.New("anime resource directory is not ready")
)

const animeSubmitColumns = `id,download_job_id,operation_key,state,generation,request_json,result_json,error_code,authorized_at,updated_at`

func scanAnimeSubmit(row animeScanner) (model.AnimeSubmitOperation, error) {
	var op model.AnimeSubmitOperation
	var request string
	var result *string
	if err := row.Scan(&op.ID, &op.JobID, &op.OperationKey, &op.State, &op.Generation, &request, &result, &op.ErrorCode, &op.AuthorizedAt, &op.UpdatedAt); err != nil {
		return op, err
	}
	if err := decodeAnimeJSON(request, &op.Request); err != nil {
		return op, fmt.Errorf("decode anime submit request: %w", err)
	}
	if result != nil {
		op.Result = &model.AnimeSubmitObservation{}
		if err := decodeAnimeJSON(*result, op.Result); err != nil {
			return op, fmt.Errorf("decode anime submit observation: %w", err)
		}
	}
	return op, nil
}

func (s *Store) GetAnimeSubmitOperation(ctx context.Context, jobID string) (model.AnimeSubmitOperation, error) {
	if !model.ValidAnimeID(jobID) {
		return model.AnimeSubmitOperation{}, ErrAnimeInvalid
	}
	return scanAnimeSubmit(s.db.QueryRowContext(ctx, `SELECT `+animeSubmitColumns+` FROM anime_drive_operations WHERE download_job_id=? AND kind='submit'`, jobID))
}

func animeSubmitRequest(job model.AnimeDownloadJob, target model.AnimeDownloadTarget) (model.AnimeSubmitRequest, error) {
	var empty model.AnimeSubmitRequest
	if target.Validate() != nil || job.Snapshot.Target != target || job.AccountID != target.AccountID || job.UID != target.UID || job.AccountRevision != target.AccountRevision || job.SettingsRevision != target.SettingsRevision || job.ParentCID != target.ParentCID || job.Snapshot.Protocol != "bt_v1" {
		return empty, ErrAnimeResourceTarget
	}
	if job.DirectoryCID == nil || !model.ValidAnimeExternalID(*job.DirectoryCID) || *job.DirectoryCID == job.ParentCID || job.DirectoryName != "anime-"+job.ID {
		return empty, ErrAnimeDirectoryRequired
	}
	// Validate the hash against its durable resource/claim separately in the
	// transaction; request JSON never accepts a hash or directory from HTTP.
	return model.AnimeSubmitRequest{SchemaVersion: 1, Target: target, InfoHash: job.Snapshot.InfoHash, DirectoryCID: *job.DirectoryCID}, nil
}

func checkAnimeSubmitIdentityTx(ctx context.Context, tx *sql.Tx, job model.AnimeDownloadJob, request model.AnimeSubmitRequest) error {
	if err := checkAnimeDownloadTargetTx(ctx, tx, request.Target, false); err != nil {
		return err
	}
	var valid bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM anime_resource_claims c JOIN anime_resources r ON r.id=c.resource_id WHERE c.id=? AND c.uid=? AND c.account_id=? AND c.target_cid=? AND r.protocol='bt_v1' AND r.info_hash=?)`, job.ClaimID, job.UID, job.AccountID, job.ParentCID, request.InfoHash).Scan(&valid); err != nil {
		return err
	}
	if !valid {
		return ErrAnimeIdentity
	}
	return nil
}

// At least one independently authorized source must remain current. The job's
// original publisher/naming snapshot is never replaced by a backup source.
func checkAnimeJobAuthorizationTx(ctx context.Context, tx *sql.Tx, job model.AnimeDownloadJob) error {
	if job.WorkflowState != "active" {
		return ErrAnimeWriteUnauthorized
	}
	rows, err := tx.QueryContext(ctx, `SELECT source_id,release_id,snapshot_json FROM anime_job_selections WHERE job_id=? AND status='authorized'`, job.ID)
	if err != nil {
		return err
	}
	type selection struct{ source, release, raw string }
	selections := make([]selection, 0, 5)
	for rows.Next() {
		var v selection
		if err := rows.Scan(&v.source, &v.release, &v.raw); err != nil {
			rows.Close()
			return err
		}
		selections = append(selections, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, selection := range selections {
		var snap model.AnimeDownloadSnapshot
		if err := decodeAnimeJSON(selection.raw, &snap); err != nil {
			return err
		}
		if snap.SchemaVersion != 1 || !sameAnimeDownloadBinding(snap.Target, job.Snapshot.Target) || snap.Protocol != "bt_v1" || snap.InfoHash != job.Snapshot.InfoHash || snap.Source.SourceID != selection.source || snap.Release.ID != selection.release || snap.Source.GroupName != job.Snapshot.Source.GroupName {
			continue
		}
		source, err := scanAnimeSource(tx.QueryRowContext(ctx, `SELECT `+animeSourceColumns+` FROM anime_subscription_sources WHERE id=?`, selection.source))
		if err != nil {
			return err
		}
		sub, err := scanAnimeSubscription(tx.QueryRowContext(ctx, `SELECT `+animeSubscriptionColumns+` FROM anime_subscriptions WHERE id=?`, source.SubscriptionID))
		if err != nil {
			return err
		}
		if job.DownloadState != "queued" && sub.Status == "enabled" {
			submitted, err := animeJobSubmittedTx(ctx, tx, job.ID)
			if err != nil {
				return err
			}
			if submitted {
				return nil
			}
		}
		if source.Status != "enabled" || !source.RunWhenEnabled || sub.Status != "enabled" || sub.FirstEnabledAt == nil || sub.RunGeneration != snap.Source.SubscriptionGeneration || source.RunGeneration != snap.Source.SourceGeneration || source.Revision != snap.Source.SourceRevision || source.SubscriptionID != snap.Source.SubscriptionID {
			continue
		}
		if source.Provider != snap.Source.Provider || source.NamespaceID != snap.Source.NamespaceID || source.ItemID == nil || *source.ItemID != snap.Source.ItemID || source.GroupID == nil || *source.GroupID != snap.Source.GroupID || source.GroupName == nil || *source.GroupName != snap.Source.GroupName {
			continue
		}
		d, err := readAnimeReleaseDecision(ctx, tx, selection.source, selection.release)
		if err != nil {
			return err
		}
		if d.Disposition != "selected" && d.Disposition != "linked" || d.SourceRevision != source.Revision || !reflect.DeepEqual(d.Snapshot, snap.Decision) {
			continue
		}
		basis := d.Snapshot.Selection
		if basis == nil || !sameAnimeDownloadBinding(basis.Target, job.Snapshot.Target) || basis.SourceGeneration != source.RunGeneration || basis.SubscriptionGeneration != sub.RunGeneration || basis.SelectedAt <= 0 {
			continue
		}
		release, err := scanAnimeRelease(tx.QueryRowContext(ctx, `SELECT `+animeReleaseColumns+` FROM anime_releases WHERE id=?`, selection.release))
		if err != nil {
			return err
		}
		if release.Provider != source.Provider || release.ContentFingerprint != snap.Release.ContentFingerprint || release.ContentFingerprint != d.ReleaseFingerprint || release.Title != d.Snapshot.Title || !reflect.DeepEqual(source.Binding, d.Snapshot.Binding) || !reflect.DeepEqual(source.Rules, d.Snapshot.Rules) {
			continue
		}
		release.Parse = snap.Release.Parse
		match, err := animepreview.MatchesRelease(source.Binding, source.Rules, release)
		if err != nil {
			return err
		}
		if match {
			return nil
		}
	}
	return ErrAnimeWriteUnauthorized
}

// PrepareAnimeSubmit is idempotent. A dispatched/unknown operation is returned
// for reconciliation even after pause/withdrawal; it never authorizes a replay.
func (s *Store) PrepareAnimeSubmit(ctx context.Context, jobID string, generation int64, target model.AnimeDownloadTarget, leaseToken ...string) (model.AnimeSubmitOperation, error) {
	var empty model.AnimeSubmitOperation
	if !model.ValidAnimeID(jobID) || generation < 1 || target.Validate() != nil {
		return empty, ErrAnimeInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	if err := checkAnimeLeaseTx(ctx, tx, jobID, leaseToken...); err != nil {
		return empty, err
	}
	job, err := scanAnimeDownloadJob(tx.QueryRowContext(ctx, `SELECT `+animeJobColumns+` FROM anime_download_jobs WHERE id=?`, jobID))
	if err != nil {
		return empty, err
	}
	if job.Generation != generation {
		return empty, ErrAnimeConflict
	}
	request, err := animeSubmitRequest(job, target)
	if err != nil {
		return empty, err
	}
	if err := checkAnimeSubmitIdentityTx(ctx, tx, job, request); err != nil {
		return empty, err
	}
	op, err := scanAnimeSubmit(tx.QueryRowContext(ctx, `SELECT `+animeSubmitColumns+` FROM anime_drive_operations WHERE download_job_id=? AND kind='submit'`, jobID))
	if err == nil {
		if op.Request != request {
			return empty, ErrAnimeResourceTarget
		}
		if op.State == "prepared" {
			if err := checkAnimeJobAuthorizationTx(ctx, tx, job); err != nil {
				return empty, err
			}
		}
		return op, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return empty, err
	}
	if job.DownloadState != "queued" || job.WorkflowState != "active" {
		return empty, ErrAnimeConflict
	}
	if err := checkAnimeJobAuthorizationTx(ctx, tx, job); err != nil {
		return empty, err
	}
	id, err := newAnimeID()
	if err != nil {
		return empty, err
	}
	raw, err := animeJSON(request)
	if err != nil {
		return empty, err
	}
	now := time.Now().UnixMilli()
	if _, err := tx.ExecContext(ctx, `INSERT INTO anime_drive_operations(id,download_job_id,operation_key,kind,state,generation,request_json,updated_at) VALUES(?,?,?,'submit','prepared',?,?,?)`, id, jobID, "submit:"+jobID, generation, raw, now); err != nil {
		return empty, animeDBError("prepare anime submission", err)
	}
	if err := tx.Commit(); err != nil {
		return empty, err
	}
	return model.AnimeSubmitOperation{ID: id, JobID: jobID, OperationKey: "submit:" + jobID, State: "prepared", Generation: generation, Request: request, UpdatedAt: now}, nil
}

// DispatchAnimeSubmit commits permission for exactly one external request.
// A caller may send only after this transaction returns successfully.
func (s *Store) DispatchAnimeSubmit(ctx context.Context, operationID string, leaseToken ...string) error {
	if !model.ValidAnimeID(operationID) {
		return ErrAnimeInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	op, err := scanAnimeSubmit(tx.QueryRowContext(ctx, `SELECT `+animeSubmitColumns+` FROM anime_drive_operations WHERE id=?`, operationID))
	if err != nil {
		return err
	}
	if err := checkAnimeLeaseTx(ctx, tx, op.JobID, leaseToken...); err != nil {
		return err
	}
	if op.State != "prepared" {
		return ErrAnimeConflict
	}
	job, err := scanAnimeDownloadJob(tx.QueryRowContext(ctx, `SELECT `+animeJobColumns+` FROM anime_download_jobs WHERE id=?`, op.JobID))
	if err != nil {
		return err
	}
	request, err := animeSubmitRequest(job, op.Request.Target)
	if err != nil {
		return err
	}
	if request != op.Request || job.Generation != op.Generation || job.DownloadState != "queued" {
		return ErrAnimeConflict
	}
	if err := checkAnimeSubmitIdentityTx(ctx, tx, job, request); err != nil {
		return err
	}
	if err := checkAnimeJobAuthorizationTx(ctx, tx, job); err != nil {
		return err
	}
	now := time.Now().UnixMilli()
	if _, err := tx.ExecContext(ctx, `UPDATE anime_drive_operations SET state='dispatched',authorized_at=?,updated_at=? WHERE id=? AND state='prepared'`, now, now, operationID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE anime_download_jobs SET download_state='submitting',block_code=NULL,revision=revision+1,next_run_at=NULL,updated_at=? WHERE id=?`, now, job.ID); err != nil {
		return err
	}
	return tx.Commit()
}

func animeSubmitOutcomeCode(code string) bool {
	switch code {
	case "", "SUBMIT_UNCERTAIN", "TASK_MISSING", "TASK_TARGET_CONFLICT", "TASK_READ_FAILED", "SUBMIT_REJECTED", "STALE_GENERATION":
		return true
	}
	return false
}

// RecordAnimeSubmitOutcome records facts even if authorization was withdrawn
// after dispatch. A changed job generation keeps the observation but does not
// advance the current job. It cannot turn an unknown request back to prepared.
func (s *Store) RecordAnimeSubmitOutcome(ctx context.Context, operationID, state, code string, observation model.AnimeSubmitObservation, leaseToken ...string) error {
	if !model.ValidAnimeID(operationID) || !animeSubmitOutcomeCode(code) || observation.SchemaVersion != 1 {
		return ErrAnimeInvalid
	}
	if state != "confirmed" && state != "unknown" && state != "failed" {
		return ErrAnimeInvalid
	}
	switch observation.Kind {
	case "accepted", "matched", "missing", "conflict", "unreadable", "rejected":
	default:
		return ErrAnimeInvalid
	}
	if observation.TargetCID != "" && !model.ValidAnimeExternalID(observation.TargetCID) || observation.ResultID != "" && !model.ValidAnimeExternalID(observation.ResultID) {
		return ErrAnimeInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	op, err := scanAnimeSubmit(tx.QueryRowContext(ctx, `SELECT `+animeSubmitColumns+` FROM anime_drive_operations WHERE id=?`, operationID))
	if err != nil {
		return err
	}
	if op.State == "confirmed" || op.State == "failed" {
		return nil
	}
	if err := checkAnimeLeaseTx(ctx, tx, op.JobID, leaseToken...); err != nil {
		return err
	}
	if op.State == "prepared" && (state != "failed" || code != "TASK_TARGET_CONFLICT" || observation.Kind != "conflict") {
		return ErrAnimeConflict
	}
	if state == "confirmed" && ((observation.Kind != "matched" && observation.Kind != "accepted") || observation.TargetCID != op.Request.DirectoryCID || code != "") {
		return ErrAnimeInvalid
	}
	if state == "failed" && op.State != "prepared" && (observation.Kind != "rejected" || code != "SUBMIT_REJECTED") {
		return ErrAnimeInvalid
	}
	if state == "unknown" && (code == "" || code == "SUBMIT_REJECTED") {
		return ErrAnimeInvalid
	}
	job, err := scanAnimeDownloadJob(tx.QueryRowContext(ctx, `SELECT `+animeJobColumns+` FROM anime_download_jobs WHERE id=?`, op.JobID))
	if err != nil {
		return err
	}
	currentRequest, identityErr := animeSubmitRequest(job, op.Request.Target)
	currentPhase := job.DownloadState == "submitting" || job.DownloadState == "unknown"
	if op.State == "prepared" {
		currentPhase = job.DownloadState == "queued"
	}
	stale := job.Generation != op.Generation || identityErr != nil || currentRequest != op.Request || job.WorkflowState != "active" || !currentPhase
	if stale && op.AuthorizedAt != nil {
		state, code = "unknown", "STALE_GENERATION"
	}
	raw, err := animeJSON(observation)
	if err != nil {
		return err
	}
	now := time.Now().UnixMilli()
	if _, err := tx.ExecContext(ctx, `UPDATE anime_drive_operations SET state=?,result_json=?,error_code=NULLIF(?,''),updated_at=? WHERE id=?`, state, raw, code, now, op.ID); err != nil {
		return err
	}
	if !stale {
		downloadState := "unknown"
		if state == "confirmed" {
			downloadState = "accepted"
		}
		if state == "failed" {
			downloadState = "failed"
			if code == "TASK_TARGET_CONFLICT" {
				downloadState = "blocked"
			}
		}
		var next *int64
		if state != "failed" {
			due := now + int64(5*time.Minute/time.Millisecond)
			if state == "confirmed" {
				due = now // First completion check is ready as soon as acceptance is durable.
				if observation.Kind == "matched" {
					// Recovery just read this exact task; avoid another immediate GET.
					due += 5000
				}
			}
			next = &due
		}
		if _, err := tx.ExecContext(ctx, `UPDATE anime_download_jobs SET download_state=?,block_code=NULLIF(?,''),revision=revision+1,next_run_at=?,updated_at=? WHERE id=?`, downloadState, code, next, now, job.ID); err != nil {
			return err
		}
	}
	return tx.Commit()
}
