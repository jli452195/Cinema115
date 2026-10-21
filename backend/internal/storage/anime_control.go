package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"strm115/backend/internal/model"
)

var (
	ErrAnimeTransition            = errors.New("anime subscription state does not allow this action")
	ErrAnimeConfigurationRequired = errors.New("anime subscription configuration is required")
	ErrAnimeDeleteBusy            = errors.New("anime subscription has an active download worker")
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
		if found, err := readAnimeActionReceiptTx(ctx, tx, *key, &out); found || err != nil {
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
	if action == "remove" {
		if err := deleteAnimeSubscriptionTx(ctx, tx, sub); err != nil {
			return out, err
		}
		out.Status, out.Revision = "removed", sub.Revision+1
		if key != nil {
			// Keep only this minimal receipt so a lost deletion response is safe
			// to replay. No subscription definition or execution history remains.
			if err := writeAnimeActionReceiptTx(ctx, tx, *key, out, time.Now().UnixMilli()); err != nil {
				return out, err
			}
		}
		return out, tx.Commit()
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
		settings, err := readAnimeSettingsTx(ctx, tx)
		if err != nil {
			return out, err
		}
		target, err := animeTargetFromSettings(settings)
		if err != nil {
			return out, err
		}
		if err := checkAnimeDownloadTargetTx(ctx, tx, target, true); err != nil {
			return out, err
		}
		for _, source := range sources {
			if source.RunWhenEnabled && (source.Binding.Validate(true) != nil || source.Rules.Validate(true) != nil) {
				return out, ErrAnimeInvalid
			}
		}
	}
	now := time.Now().UnixMilli()
	out.Status, out.Revision = next, sub.Revision
	if next != sub.Status {
		out.Revision++
		if _, err := tx.ExecContext(ctx, `UPDATE anime_subscriptions SET status=?,revision=revision+1,updated_at=? WHERE id=?`, next, now, id); err != nil {
			return out, err
		}
		if action == "resume" || action == "unarchive" {
			if err := resetAnimeSubscriptionActivityTx(ctx, tx, id, now); err != nil {
				return out, err
			}
		}
		if action == "archive" {
			if _, err := tx.ExecContext(ctx, `INSERT INTO anime_subscription_archives(subscription_id,reason,archived_at) VALUES(?,'manual',?) ON CONFLICT(subscription_id) DO UPDATE SET reason='manual',archived_at=excluded.archived_at`, id, now); err != nil {
				return out, err
			}
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
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM anime_download_jobs j WHERE j.workflow_state='active' AND (json_extract(j.directory_operation_json,'$.state') IN ('dispatched','unknown') OR EXISTS(SELECT 1 FROM anime_drive_operations o WHERE o.download_job_id=j.id AND o.kind IN ('submit','organize') AND o.state IN ('dispatched','unknown'))) AND EXISTS(SELECT 1 FROM anime_job_selections x JOIN anime_subscription_sources s ON s.id=x.source_id WHERE x.job_id=j.id AND s.subscription_id=?)`, id).Scan(&out.InFlightOperationCount); err != nil {
		return out, err
	}
	if key != nil {
		if err := writeAnimeActionReceiptTx(ctx, tx, *key, out, now); err != nil {
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
	case "unarchive":
		if sub.Status == "archived" || sub.Status == "paused" {
			return "paused", nil
		}
	case "resume":
		if sub.Status == "paused" || sub.Status == "enabled" || sub.Status == "archived" {
			if sub.FirstEnabledAt == nil {
				return "", ErrAnimeConfigurationRequired
			}
			return "enabled", nil
		}
	default:
		return "", ErrAnimeInvalid
	}
	return "", ErrAnimeTransition
}

func resetAnimeSubscriptionActivityTx(ctx context.Context, tx *sql.Tx, id string, now int64) error {
	for _, query := range []string{
		`DELETE FROM anime_subscription_archives WHERE subscription_id=?`,
		`DELETE FROM anime_source_activity WHERE source_id IN (SELECT id FROM anime_subscription_sources WHERE subscription_id=?)`,
	} {
		if _, err := tx.ExecContext(ctx, query, id); err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx, `UPDATE anime_subscription_sources SET next_check_at=? WHERE subscription_id=? AND status='enabled' AND run_when_enabled=1 AND health<>'unavailable'`, now, id)
	return err
}

func controlAnimeQueuedJobsTx(ctx context.Context, tx *sql.Tx, id string, resuming bool, now int64) (int, error) {
	rows, err := tx.QueryContext(ctx, `SELECT `+animeJobColumns+` FROM anime_download_jobs j WHERE workflow_state='active' AND (download_state='queued' OR (file_state='verified' AND json_extract(snapshot_json,'$.organization') IS NOT NULL)) AND EXISTS(SELECT 1 FROM anime_job_selections x JOIN anime_subscription_sources s ON s.id=x.source_id WHERE x.job_id=j.id AND s.subscription_id=? AND (?=0 OR (x.status='authorized' AND s.status='enabled' AND s.run_when_enabled=1)))`, id, resuming)
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
			if job.BlockCode != nil && *job.BlockCode == "CONFIGURATION_CHANGED" {
				continue
			}
			return 0, ErrAnimeConfigurationRequired
		}
		if job.FileState == "verified" {
			// Keep in-flight receipts and leases intact. Pause suppresses new
			// writes, not the observation of an already-issued request.
			if _, err := tx.ExecContext(ctx, `UPDATE anime_download_jobs SET block_code='DOWNLOAD_PAUSED',next_run_at=NULL,revision=revision+1,updated_at=? WHERE id=? AND block_code IS NULL AND NOT EXISTS(SELECT 1 FROM anime_drive_operations o WHERE o.download_job_id=? AND o.kind='organize' AND json_extract(o.result_json,'$.step') IS NOT NULL)`, now, job.ID, job.ID); err != nil {
				return 0, err
			}
			continue
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
