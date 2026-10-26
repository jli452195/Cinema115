package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"strm115/backend/internal/model"
)

// Delete only local ownership. Jobs still selected by another source and shared
// releases/resources stay intact; no cloud I/O belongs in this operation.
func deleteAnimeSourcesTx(ctx context.Context, tx *sql.Tx, subscriptionID string, sourceIDs []string) error {
	sources, err := animeJSON(sourceIDs)
	if err != nil {
		return err
	}
	const sourceSet = `SELECT value FROM json_each(?)`
	// Verification can create a job after reading torrent metadata. Keep its
	// source and selection intact until that bounded worker releases its lease.
	now := time.Now().UnixMilli()
	var verifying bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM anime_release_decisions WHERE source_id IN (`+sourceSet+`) AND lease_until>?)`, sources, now).Scan(&verifying); err != nil {
		return err
	}
	if verifying {
		return ErrAnimeDeleteBusy
	}
	var releases, resources string
	if err := tx.QueryRowContext(ctx, `SELECT json_group_array(id) FROM anime_releases WHERE id IN (SELECT release_id FROM anime_release_decisions WHERE source_id IN (`+sourceSet+`) UNION SELECT release_id FROM anime_job_selections WHERE source_id IN (`+sourceSet+`) UNION SELECT release_id FROM anime_preview_items WHERE preview_id IN (SELECT id FROM anime_source_previews WHERE batch_id IN (SELECT id FROM anime_preview_batches WHERE subscription_id=?)))`, sources, sources, subscriptionID).Scan(&releases); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, `SELECT json_group_array(resource_id) FROM anime_releases WHERE id IN (SELECT value FROM json_each(?))`, releases).Scan(&resources); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,claim_id,COALESCE(lease_until,0) FROM anime_download_jobs j WHERE EXISTS(SELECT 1 FROM anime_job_selections x WHERE x.job_id=j.id AND x.source_id IN (`+sourceSet+`)) OR json_extract(j.snapshot_json,'$.source.sourceId') IN (`+sourceSet+`)`, sources, sources)
	if err != nil {
		return err
	}
	type ownedJob struct{ id, claim string }
	jobs := make([]ownedJob, 0, 8)
	for rows.Next() {
		var job ownedJob
		var until int64
		if err := rows.Scan(&job.id, &job.claim, &until); err != nil {
			rows.Close()
			return err
		}
		if until > now {
			rows.Close()
			return ErrAnimeDeleteBusy
		}
		jobs = append(jobs, job)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	// Every preview for this subscription is tied to the old configuration
	// revision. Invalidate the whole batch, including unsaved source references.
	for _, query := range []string{
		`DELETE FROM anime_preview_items WHERE preview_id IN (SELECT id FROM anime_source_previews WHERE batch_id IN (SELECT id FROM anime_preview_batches WHERE subscription_id=?))`,
		`DELETE FROM anime_source_previews WHERE batch_id IN (SELECT id FROM anime_preview_batches WHERE subscription_id=?)`,
		`UPDATE anime_request_keys SET receipt_json='{"deleted":true}' WHERE json_extract(receipt_json,'$.batchId') IN (SELECT id FROM anime_preview_batches WHERE subscription_id=?)`,
		`DELETE FROM anime_preview_batches WHERE subscription_id=?`,
	} {
		if _, err := tx.ExecContext(ctx, query, subscriptionID); err != nil {
			return fmt.Errorf("delete anime previews: %w", err)
		}
	}
	// Keep the dependency order explicit, rather than cascading into shared data.
	for _, query := range []string{
		`DELETE FROM anime_job_selections WHERE source_id IN (` + sourceSet + `)`,
		`DELETE FROM anime_release_decisions WHERE source_id IN (` + sourceSet + `)`,
		`DELETE FROM anime_source_activity WHERE source_id IN (` + sourceSet + `)`,
		`DELETE FROM anime_subscription_sources WHERE id IN (` + sourceSet + `)`,
	} {
		if _, err := tx.ExecContext(ctx, query, sources); err != nil {
			return fmt.Errorf("delete anime source records: %w", err)
		}
	}
	for _, job := range jobs {
		var shared bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM anime_job_selections WHERE job_id=?)`, job.id).Scan(&shared); err != nil {
			return err
		}
		if shared {
			continue
		}
		for _, query := range []string{
			`UPDATE anime_request_keys SET receipt_json='{"deleted":true}' WHERE json_extract(receipt_json,'$.id')=?`,
			`DELETE FROM anime_drive_operations WHERE download_job_id=?`,
			`DELETE FROM anime_download_jobs WHERE id=?`,
		} {
			if _, err := tx.ExecContext(ctx, query, job.id); err != nil {
				return fmt.Errorf("delete anime job records: %w", err)
			}
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM anime_resource_claims WHERE id=? AND NOT EXISTS(SELECT 1 FROM anime_download_jobs WHERE claim_id=?)`, job.claim, job.claim); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM anime_releases WHERE id IN (SELECT value FROM json_each(?)) AND NOT EXISTS(SELECT 1 FROM anime_release_decisions WHERE release_id=anime_releases.id) AND NOT EXISTS(SELECT 1 FROM anime_job_selections WHERE release_id=anime_releases.id) AND NOT EXISTS(SELECT 1 FROM anime_preview_items WHERE release_id=anime_releases.id) AND NOT EXISTS(SELECT 1 FROM anime_download_jobs WHERE json_extract(snapshot_json,'$.release.id')=anime_releases.id)`, releases); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM anime_resources WHERE id IN (SELECT value FROM json_each(?)) AND NOT EXISTS(SELECT 1 FROM anime_resource_claims WHERE resource_id=anime_resources.id) AND NOT EXISTS(SELECT 1 FROM anime_releases WHERE resource_id=anime_resources.id)`, resources); err != nil {
		return err
	}
	return nil
}

func deleteAnimeSubscriptionTx(ctx context.Context, tx *sql.Tx, sub model.AnimeSubscription) error {
	sources, err := readAnimeSources(ctx, tx, sub.ID, true)
	if err != nil {
		return err
	}
	ids := make([]string, 0, len(sources))
	for _, source := range sources {
		ids = append(ids, source.ID)
	}
	if err := deleteAnimeSourcesTx(ctx, tx, sub.ID, ids); err != nil {
		return err
	}
	for _, query := range []string{
		`UPDATE anime_request_keys SET receipt_json='{"deleted":true}' WHERE json_extract(receipt_json,'$.id')=?`,
		`DELETE FROM anime_subscription_archives WHERE subscription_id=?`,
		`DELETE FROM anime_subscriptions WHERE id=?`,
	} {
		if _, err := tx.ExecContext(ctx, query, sub.ID); err != nil {
			return fmt.Errorf("delete anime subscription records: %w", err)
		}
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM anime_entries WHERE id=? AND NOT EXISTS(SELECT 1 FROM anime_subscriptions WHERE entry_id=?)`, sub.EntryID, sub.EntryID)
	return err
}
