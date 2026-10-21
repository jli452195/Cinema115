package storage

import (
	"context"
	"database/sql"
	"fmt"

	"strm115/backend/internal/model"
)

// v55 retires the old subscription trash state, using the same local cleanup
// as an explicit deletion. This runs before workers start on this connection.
func purgeRemovedAnimeSubscriptions(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `SELECT id,entry_id FROM anime_subscriptions WHERE status='removed'`)
	if err != nil {
		return err
	}
	subs := make([]model.AnimeSubscription, 0)
	for rows.Next() {
		var sub model.AnimeSubscription
		if err := rows.Scan(&sub.ID, &sub.EntryID); err != nil {
			rows.Close()
			return err
		}
		subs = append(subs, sub)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, sub := range subs {
		if _, err := tx.ExecContext(ctx, `UPDATE anime_download_jobs SET lease_token=NULL,lease_until=NULL WHERE EXISTS(SELECT 1 FROM anime_job_selections x JOIN anime_subscription_sources s ON s.id=x.source_id WHERE x.job_id=anime_download_jobs.id AND s.subscription_id=?)`, sub.ID); err != nil {
			return err
		}
		if err := deleteAnimeSubscriptionTx(ctx, tx, sub); err != nil {
			return err
		}
	}
	return nil
}

// The anime module has no production data. Reset only these owned tables once
// when entering v53; never drop the database or touch other business tables.
// initialize suspends FK enforcement on its dedicated connection because source
// and baseline tables reference one another, and restores it on every exit.
func resetDevelopmentAnime(ctx context.Context, tx *sql.Tx, preserveSettings bool) error {
	for _, table := range []string{
		"anime_source_activity", "anime_subscription_archives", "anime_rss_settings",
		"anime_drive_operations", "anime_job_selections", "anime_download_jobs",
		"anime_resource_claims", "anime_release_decisions", "anime_baseline_releases",
		"anime_preview_items", "anime_source_previews", "anime_preview_batches",
		"anime_source_baselines", "anime_subscription_sources", "anime_releases",
		"anime_resources", "anime_catalog_links", "anime_transfers", "anime_download_claims",
		"anime_request_keys", "anime_subscriptions", "anime_entries", "anime_settings",
		"legacy_anime_v38_releases", "legacy_anime_v38_transfers",
		"legacy_anime_v38_download_claims", "legacy_anime_v38_subscriptions",
	} {
		if preserveSettings && table == "anime_settings" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `DROP TABLE IF EXISTS `+table); err != nil {
			return fmt.Errorf("drop development table %s: %w", table, err)
		}
	}
	if _, err := tx.ExecContext(ctx, animeSchema); err != nil {
		return fmt.Errorf("create current anime schema: %w", err)
	}
	return nil
}
