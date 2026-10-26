package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"strm115/backend/internal/model"
	"strm115/backend/internal/naming"
)

// Retire stale metadata permissions without touching accepted drive operations.
// Source revision/generation fences a worker that was already reading metadata.
func invalidateAnimeSourceConfigurationTx(ctx context.Context, tx *sql.Tx, sourceID string, now int64) error {
	if _, err := tx.ExecContext(ctx, `UPDATE anime_subscription_sources SET checked_at=NULL,check_token=NULL,check_until=NULL,next_check_at=?,poll_failures=0,health=CASE WHEN health='unavailable' THEN 'unknown' ELSE health END WHERE id=? AND status<>'removed'`, now, sourceID); err != nil {
		return fmt.Errorf("schedule anime definition: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE anime_release_decisions SET disposition='seen',snapshot_json=json_remove(snapshot_json,'$.selection'),generation=generation+1,lease_token=NULL,lease_until=NULL,next_verify_at=NULL WHERE source_id=? AND NOT EXISTS(SELECT 1 FROM anime_job_selections j WHERE j.source_id=anime_release_decisions.source_id AND j.release_id=anime_release_decisions.release_id)`, sourceID); err != nil {
		return fmt.Errorf("retire anime metadata decisions: %w", err)
	}
	// An external create or submit already in flight must be allowed to record
	// its result. The next write still rechecks the current source generation.
	_, err := tx.ExecContext(ctx, `UPDATE anime_download_jobs SET block_code='CONFIGURATION_CHANGED',next_run_at=NULL,lease_token=NULL,lease_until=NULL,revision=revision+1,updated_at=? WHERE download_state='queued' AND workflow_state='active' AND EXISTS(SELECT 1 FROM anime_job_selections x WHERE x.job_id=anime_download_jobs.id AND x.source_id=?) AND COALESCE(json_extract(directory_operation_json,'$.state'),'') NOT IN ('dispatched','unknown') AND NOT EXISTS(SELECT 1 FROM anime_drive_operations o WHERE o.download_job_id=anime_download_jobs.id AND o.kind='submit' AND o.state IN ('dispatched','unknown','confirmed'))`, now, sourceID)
	return err
}

func animeJobSubmittedTx(ctx context.Context, tx *sql.Tx, id string) (bool, error) {
	var submitted bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM anime_drive_operations WHERE download_job_id=? AND kind='submit' AND state IN ('dispatched','unknown','confirmed'))`, id).Scan(&submitted)
	return submitted, err
}

func animeReleaseCommittedTx(ctx context.Context, tx *sql.Tx, sourceID, releaseID string) (bool, error) {
	var committed bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM anime_job_selections x JOIN anime_download_jobs j ON j.id=x.job_id WHERE x.source_id=? AND x.release_id=? AND (j.download_state<>'queued' OR j.workflow_state<>'active' OR EXISTS(SELECT 1 FROM anime_drive_operations o WHERE o.download_job_id=j.id AND o.kind='submit' AND o.state IN ('dispatched','unknown','confirmed'))))`, sourceID, releaseID).Scan(&committed)
	return committed, err
}

func animeReleasePendingJobTx(ctx context.Context, tx *sql.Tx, sourceID, releaseID string) (bool, error) {
	var pending bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM anime_job_selections x JOIN anime_download_jobs j ON j.id=x.job_id WHERE x.source_id=? AND x.release_id=? AND x.status='authorized' AND j.download_state='queued' AND j.workflow_state='active' AND NOT EXISTS(SELECT 1 FROM anime_drive_operations o WHERE o.download_job_id=j.id AND o.kind='submit' AND o.state IN ('dispatched','unknown','confirmed')))`, sourceID, releaseID).Scan(&pending)
	return pending, err
}

// Reauthorize only the same chosen torrent before its first submit. The claim,
// directory and credential snapshot stay fixed; submitted/unknown jobs never
// enter here. A naming edit may update a not-yet-submitted organization plan.
func refreshAnimePendingJobTx(ctx context.Context, tx *sql.Tx, p AnimeSourcePoll, release model.AnimeRelease, decision model.AnimeDecisionSnapshot, now int64) error {
	pending, err := animeReleasePendingJobTx(ctx, tx, p.Source.ID, release.ID)
	if err != nil || !pending {
		return err
	}
	job, err := scanAnimeDownloadJob(tx.QueryRowContext(ctx, `SELECT `+animeJobColumns+` FROM anime_download_jobs WHERE id=(SELECT job_id FROM anime_job_selections WHERE source_id=? AND release_id=? AND status='authorized' ORDER BY created_at DESC LIMIT 1)`, p.Source.ID, release.ID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if job.Snapshot.Source.SourceID != p.Source.ID || !sameAnimeDownloadBinding(job.Snapshot.Target, p.Target) {
		return nil
	}
	snapshot := job.Snapshot
	if release.Parse.Kind == "batch" {
		if p.Subscription.Naming == nil {
			return nil
		}
		all, selected, err := selectAnimeBatchFilesTx(ctx, tx, p.Source, release, snapshot.Manifest)
		if errors.Is(err, ErrAnimeConflict) {
			// Like a now-excluded ordinary post, a queued collection remains
			// blocked when its frozen files no longer satisfy the edited rules.
			return nil
		}
		if err != nil {
			return err
		}
		decision.BatchFiles, snapshot.BatchFiles = all, selected
	}
	snapshot.Release, snapshot.Decision, snapshot.Naming = release, decision, p.Subscription.Naming
	snapshot.Source.SourceRevision, snapshot.Source.SourceGeneration = p.Source.Revision, p.Source.RunGeneration
	snapshot.Source.SubscriptionRevision, snapshot.Source.SubscriptionGeneration = p.Subscription.Revision, p.Subscription.RunGeneration
	settings, err := readAnimeSettingsTx(ctx, tx)
	if err != nil {
		return err
	}
	// A queued task keeps its template even when the source is reauthorized.
	template := naming.Snapshot{}
	if snapshot.Organization != nil {
		template = snapshot.Organization.Naming
	} else if p.Subscription.Naming != nil {
		template, err = readNamingTemplate(ctx, tx, naming.TV)
		if err != nil {
			return err
		}
	}
	snapshot.Organization, err = model.FreezeAnimeOrganization(p.Subscription.Naming, settings, p.Subscription.Revision, snapshot.Target, template)
	if err != nil {
		return err
	}
	raw, err := animeJSON(snapshot)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE anime_job_selections SET source_revision=?,snapshot_json=? WHERE job_id=? AND source_id=? AND release_id=?`, p.Source.Revision, raw, job.ID, p.Source.ID, release.ID); err != nil {
		return err
	}
	// Do not revoke an in-flight directory create's receipt permission. It has
	// exactly the same target and name, independent of RSS/naming changes.
	_, err = tx.ExecContext(ctx, `UPDATE anime_download_jobs SET snapshot_json=?,block_code=CASE WHEN block_code IN ('CONFIGURATION_CHANGED','DOWNLOAD_PAUSED') THEN NULL ELSE block_code END,next_run_at=COALESCE(next_run_at,?),updated_at=? WHERE id=?`, raw, now, now, job.ID)
	return err
}
