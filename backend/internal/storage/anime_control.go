package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"strm115/backend/internal/animeparse"
	"strm115/backend/internal/animepreview"
	"strm115/backend/internal/model"
)

var (
	ErrAnimeTransition = errors.New("anime subscription state does not allow this action")
	ErrAnimeReconfirm  = errors.New("anime activation or updated preview is required")
)

// Pause masks execution while preserving confirmed choices and their run
// generation. Retiring read/write lease tokens fences old workers without
// rewriting immutable snapshots or creating a new baseline on resume.
func (s *Store) ControlAnimeSubscription(ctx context.Context, id, action string, expectedRevision int64, key *AnimeWriteKey) (model.AnimeControlReceipt, error) {
	out := model.AnimeControlReceipt{ID: id, ResumedSourceIDs: []string{}, UnchangedSources: []model.AnimeUnchangedSource{}}
	if !model.ValidAnimeID(id) || expectedRevision < 1 || expectedRevision >= model.AnimeMaxRevision {
		return out, ErrAnimeInvalid
	}
	if key != nil {
		if err := key.validate(); err != nil {
			return out, err
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return out, fmt.Errorf("begin anime control: %w", err)
	}
	defer tx.Rollback()
	if key != nil {
		var digest, raw string
		err := tx.QueryRowContext(ctx, `SELECT request_digest,receipt_json FROM anime_request_keys WHERE actor_key=? AND operation=? AND request_key=?`, key.ActorKey, key.Operation, key.Key).Scan(&digest, &raw)
		if err == nil {
			if digest != key.Digest {
				return out, ErrAnimeIdempotency
			}
			if err := decodeAnimeJSON(raw, &out); err != nil {
				return out, fmt.Errorf("read anime control receipt: %w", err)
			}
			return out, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return out, err
		}
	}
	sub, err := scanAnimeSubscription(tx.QueryRowContext(ctx, `SELECT `+animeSubscriptionColumns+` FROM anime_subscriptions WHERE id=?`, id))
	if err != nil {
		return out, err
	}
	if sub.Revision != expectedRevision {
		return out, ErrAnimeConflict
	}
	next, err := animeControlState(sub, action)
	if err != nil {
		return out, err
	}
	sources, err := readAnimeSources(ctx, tx, id, true)
	if err != nil {
		return out, err
	}
	if action == "resume" && next != sub.Status {
		if err := checkAnimeResumeTx(ctx, tx, sub, sources); err != nil {
			return out, err
		}
	}
	now := time.Now().UnixMilli()
	out.Status, out.Revision = next, sub.Revision
	if next != sub.Status {
		out.Revision++
		if _, err := tx.ExecContext(ctx, `UPDATE anime_subscriptions SET status=?,revision=revision+1,updated_at=? WHERE id=?`, next, now, id); err != nil {
			return out, err
		}
		if action != "resume" {
			// Keep RSS deadlines, cooldowns, successful observations and T0.
			if _, err := tx.ExecContext(ctx, `UPDATE anime_subscription_sources SET check_token=NULL,check_until=NULL WHERE subscription_id=?`, id); err != nil {
				return out, err
			}
			var exhausted bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM anime_release_decisions WHERE disposition='selected' AND generation>=? AND source_id IN (SELECT id FROM anime_subscription_sources WHERE subscription_id=?))`, model.AnimeMaxRevision, id).Scan(&exhausted); err != nil {
				return out, err
			}
			if exhausted {
				return out, ErrAnimeConflict
			}
			if _, err := tx.ExecContext(ctx, `UPDATE anime_release_decisions SET lease_token=NULL,lease_until=NULL,generation=generation+1 WHERE disposition='selected' AND source_id IN (SELECT id FROM anime_subscription_sources WHERE subscription_id=?)`, id); err != nil {
				return out, err
			}
		}
		if action == "remove" {
			if _, err := tx.ExecContext(ctx, `UPDATE anime_job_selections SET status='withdrawn' WHERE source_id IN (SELECT id FROM anime_subscription_sources WHERE subscription_id=?)`, id); err != nil {
				return out, err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE anime_release_decisions SET disposition='dismissed',reason_code='SUBSCRIPTION_REMOVED',next_verify_at=NULL,updated_at=? WHERE disposition='selected' AND source_id IN (SELECT id FROM anime_subscription_sources WHERE subscription_id=?)`, now, id); err != nil {
				return out, err
			}
		}
		out.CancelledJobs, err = controlAnimeQueuedJobsTx(ctx, tx, id, action == "resume", now)
		if err != nil {
			return out, err
		}
	}
	for _, source := range sources {
		if action == "resume" && source.Status == "enabled" && source.RunWhenEnabled {
			out.ResumedSourceIDs = append(out.ResumedSourceIDs, source.ID)
		} else if action == "resume" {
			reason := "source_paused"
			if source.Status == "draft" || source.Status == "removed" {
				reason = source.Status
			}
			out.UnchangedSources = append(out.UnchangedSources, model.AnimeUnchangedSource{ID: source.ID, Reason: reason})
		}
	}
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM anime_download_jobs j WHERE j.workflow_state='active' AND j.download_state NOT IN ('failed','blocked','cancelled') AND EXISTS(SELECT 1 FROM anime_job_selections x JOIN anime_subscription_sources s ON s.id=x.source_id WHERE x.job_id=j.id AND s.subscription_id=?)`, id).Scan(&out.PendingJobs); err != nil {
		return out, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM anime_download_jobs j WHERE j.workflow_state='active' AND (json_extract(j.directory_operation_json,'$.state') IN ('dispatched','unknown') OR EXISTS(SELECT 1 FROM anime_drive_operations o WHERE o.download_job_id=j.id AND o.kind='submit' AND o.state IN ('dispatched','unknown'))) AND EXISTS(SELECT 1 FROM anime_job_selections x JOIN anime_subscription_sources s ON s.id=x.source_id WHERE x.job_id=j.id AND s.subscription_id=?)`, id).Scan(&out.InFlightOperationCount); err != nil {
		return out, err
	}
	if key != nil {
		raw, err := animeJSON(out)
		if err != nil {
			return out, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO anime_request_keys(actor_key,operation,request_key,request_digest,http_status,receipt_json,created_at) VALUES(?,?,?,?,200,?,?)`, key.ActorKey, key.Operation, key.Key, key.Digest, raw, now); err != nil {
			return out, err
		}
	}
	if err := tx.Commit(); err != nil {
		return out, fmt.Errorf("commit anime control: %w", err)
	}
	return out, nil
}

func animeControlState(sub model.AnimeSubscription, action string) (string, error) {
	switch action {
	case "pause":
		if sub.Status == "enabled" || sub.Status == "paused" {
			return "paused", nil
		}
	case "archive":
		if sub.Status != "removed" {
			return "archived", nil
		}
	case "remove":
		return "removed", nil
	case "unarchive":
		if sub.Status == "archived" || sub.Status == "paused" {
			return "paused", nil
		}
	case "resume":
		if sub.Status == "paused" || sub.Status == "enabled" {
			if sub.FirstEnabledAt == nil {
				return "", ErrAnimeReconfirm
			}
			return "enabled", nil
		}
	default:
		return "", ErrAnimeInvalid
	}
	return "", ErrAnimeTransition
}

func checkAnimeResumeTx(ctx context.Context, tx *sql.Tx, sub model.AnimeSubscription, sources []model.AnimeSource) error {
	active := 0
	for _, source := range sources {
		if source.Status != "enabled" || !source.RunWhenEnabled {
			continue
		}
		if source.ActiveBaselineID == nil || source.Binding.Validate(true) != nil || source.Rules.Validate(true) != nil {
			return ErrAnimeReconfirm
		}
		var raw string
		var revision, t0 int64
		if err := tx.QueryRowContext(ctx, `SELECT source_revision,t0,snapshot_json FROM anime_source_baselines WHERE id=? AND source_id=?`, *source.ActiveBaselineID, source.ID).Scan(&revision, &t0, &raw); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrAnimeReconfirm
			}
			return err
		}
		var baseline model.AnimeExecutionBaseline
		if decodeAnimeJSON(raw, &baseline) != nil || baseline.SchemaVersion != model.AnimeSchemaVersion || baseline.RuleVersion != animepreview.RuleVersion || baseline.ParserVersion != animeparse.ParserVersion || baseline.TimeVersion != animeparse.TimeVersion || revision != source.Revision || t0 <= 0 {
			return ErrAnimeReconfirm
		}
		if err := checkAnimeDownloadTargetTx(ctx, tx, baseline.Target, true); err != nil {
			return err
		}
		active++
	}
	if active == 0 || sub.FirstEnabledAt == nil {
		return ErrAnimeReconfirm
	}
	return nil
}

func controlAnimeQueuedJobsTx(ctx context.Context, tx *sql.Tx, id string, resuming bool, now int64) (int, error) {
	rows, err := tx.QueryContext(ctx, `SELECT `+animeJobColumns+` FROM anime_download_jobs j WHERE workflow_state='active' AND download_state='queued' AND EXISTS(SELECT 1 FROM anime_job_selections x JOIN anime_subscription_sources s ON s.id=x.source_id WHERE x.job_id=j.id AND s.subscription_id=? AND (?=0 OR (x.status='authorized' AND s.status='enabled' AND s.run_when_enabled=1)))`, id, resuming)
	if err != nil {
		return 0, err
	}
	jobs := make([]model.AnimeDownloadJob, 0, 8)
	for rows.Next() {
		job, err := scanAnimeDownloadJob(rows)
		if err != nil {
			rows.Close()
			return 0, err
		}
		jobs = append(jobs, job)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	cancelled := 0
	for _, job := range jobs {
		err := checkAnimeJobAuthorizationTx(ctx, tx, job)
		if err == nil {
			if resuming && job.BlockCode != nil && *job.BlockCode == "DOWNLOAD_PAUSED" {
				if _, err := tx.ExecContext(ctx, `UPDATE anime_download_jobs SET block_code=NULL,next_run_at=COALESCE(next_run_at,?),lease_token=NULL,lease_until=NULL,revision=revision+1,updated_at=? WHERE id=?`, now, now, job.ID); err != nil {
					return 0, err
				}
			}
			continue
		}
		if !errors.Is(err, ErrAnimeWriteUnauthorized) {
			return 0, err
		}
		if resuming {
			return 0, ErrAnimeReconfirm
		}
		changed, err := suspendAnimeQueuedJobTx(ctx, tx, job, now)
		if err != nil {
			return 0, err
		}
		if changed {
			cancelled++
		}
	}
	return cancelled, nil
}

// Only an unsubmitted, fully known intent may be cancelled. Uncertain creates
// still need one scoped reconciliation; dispatched submits are never replayed.
func suspendAnimeQueuedJobTx(ctx context.Context, tx *sql.Tx, job model.AnimeDownloadJob, now int64) (bool, error) {
	// Dispatch and download state normally advance atomically. Preserve any
	// durable external permission even if a damaged row still says queued.
	var submitted bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM anime_drive_operations WHERE download_job_id=? AND kind='submit' AND state IN ('dispatched','unknown','confirmed'))`, job.ID).Scan(&submitted); err != nil {
		return false, err
	}
	if submitted || job.WorkflowState != "active" || job.DownloadState != "queued" {
		return false, nil
	}
	if job.DirectoryOperation != nil && (job.DirectoryOperation.State == "dispatched" || job.DirectoryOperation.State == "unknown") {
		// An in-flight create may still persist its receipt. Keep its lease and
		// revision; authorization is checked again before the next write.
		return false, nil
	}
	var keep bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM anime_job_selections x JOIN anime_subscription_sources s ON s.id=x.source_id JOIN anime_subscriptions p ON p.id=s.subscription_id WHERE x.job_id=? AND x.status='authorized' AND s.status<>'removed' AND p.status<>'removed')`, job.ID).Scan(&keep); err != nil {
		return false, err
	}
	if keep {
		if job.BlockCode != nil && *job.BlockCode != "DOWNLOAD_PAUSED" {
			_, err := tx.ExecContext(ctx, `UPDATE anime_download_jobs SET lease_token=NULL,lease_until=NULL,revision=revision+1,updated_at=? WHERE id=?`, now, job.ID)
			return false, err
		}
		_, err := tx.ExecContext(ctx, `UPDATE anime_download_jobs SET block_code='DOWNLOAD_PAUSED',next_run_at=NULL,lease_token=NULL,lease_until=NULL,revision=revision+1,updated_at=? WHERE id=?`, now, job.ID)
		return false, err
	}
	if job.Generation >= model.AnimeMaxRevision {
		return false, ErrAnimeConflict
	}
	_, err := tx.ExecContext(ctx, `UPDATE anime_download_jobs SET workflow_state='cancelled',download_state='cancelled',block_code='SUBSCRIPTION_REMOVED',next_run_at=NULL,lease_token=NULL,lease_until=NULL,generation=generation+1,revision=revision+1,updated_at=? WHERE id=?`, now, job.ID)
	return err == nil, err
}
