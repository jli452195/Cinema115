package storage

import (
	"context"
	"database/sql"
	"fmt"
)

// The anime module has no production data. Reset only these owned tables once
// when entering the current development schema; never drop the database or touch other business tables.
// initialize suspends FK enforcement on its dedicated connection because retired prototype tables have cyclic references, and restores it on every exit.
func resetDevelopmentAnime(ctx context.Context, tx *sql.Tx, preserveSettings bool) error {
	for _, table := range []string{
		"anime_source_progress",
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
