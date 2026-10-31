package storage

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
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

// Remove the retired setting once so strict DTO decoding needs no legacy field.
// Job targets, names, claims and execution receipts remain unchanged.
func removeAnimeFirstEpisode(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `UPDATE anime_subscription_sources SET rules_json=json_remove(rules_json,'$.chapterFirstEpisode'),binding_json=json_set(binding_json,'$.scope',json('{"kind":"feed"}'),'$.chapterCorrespondence',NULL,'$.review',json_object('method','source_review','itemId',item_id,'groupId',group_id)),revision=revision+1,run_generation=run_generation+1 WHERE status<>'removed' AND json_extract(binding_json,'$.confirmed')=1 AND item_id IS NOT NULL AND group_id IS NOT NULL AND (json_type(rules_json,'$.chapterFirstEpisode') IS NOT NULL OR json_extract(binding_json,'$.scope.kind')<>'feed') RETURNING id`)
	if err != nil {
		return err
	}
	ids := make([]string, 0, 32)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	readErr, closeErr := rows.Err(), rows.Close()
	if readErr != nil {
		return readErr
	}
	if closeErr != nil {
		return closeErr
	}
	now := time.Now().UnixMilli()
	for _, id := range ids {
		if err := invalidateAnimeSourceConfigurationTx(ctx, tx, id, now); err != nil {
			return err
		}
	}
	for _, query := range []string{
		`DELETE FROM anime_preview_items`,
		`DELETE FROM anime_source_previews`,
		`DELETE FROM anime_preview_batches`,
		`DELETE FROM anime_request_keys WHERE operation LIKE '%preview%'`,
		`UPDATE anime_subscription_sources SET rules_json=json_remove(rules_json,'$.chapterFirstEpisode') WHERE json_type(rules_json,'$.chapterFirstEpisode') IS NOT NULL`,
		`UPDATE anime_release_decisions SET snapshot_json=json_remove(snapshot_json,'$.rules.chapterFirstEpisode') WHERE json_type(snapshot_json,'$.rules.chapterFirstEpisode') IS NOT NULL`,
		`UPDATE anime_download_jobs SET snapshot_json=json_remove(snapshot_json,'$.decision.rules.chapterFirstEpisode') WHERE json_type(snapshot_json,'$.decision.rules.chapterFirstEpisode') IS NOT NULL`,
		`UPDATE anime_job_selections SET snapshot_json=json_remove(snapshot_json,'$.decision.rules.chapterFirstEpisode') WHERE json_type(snapshot_json,'$.decision.rules.chapterFirstEpisode') IS NOT NULL`,
		`UPDATE anime_source_progress SET generation=generation+1 WHERE generation=computed_generation`,
	} {
		if _, err := tx.ExecContext(ctx, query); err != nil {
			return err
		}
	}
	return nil
}

// Remove credential revisions from durable account ownership. Keep existing
// subscriptions, task progress and directories; runtime DTOs need no legacy fields.
func simplifyAnimeAccountBindings(ctx context.Context, tx *sql.Tx) error {
	var settingsColumn, jobColumn int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM pragma_table_info('anime_settings') WHERE name='account_revision'`).Scan(&settingsColumn); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM pragma_table_info('anime_download_jobs') WHERE name='account_revision'`).Scan(&jobColumn); err != nil {
		return err
	}
	if settingsColumn == 0 && jobColumn == 0 {
		return nil
	}
	if settingsColumn != 0 {
		const columns = "id,revision,account_id,download_cid,download_path,organize_cid,organize_path"
		for _, query := range []string{
			strings.Replace(animeSettingsSchema, "anime_settings (", "anime_settings_current (", 1),
			`INSERT INTO anime_settings_current(` + columns + `) SELECT ` + columns + ` FROM anime_settings`,
			`DROP TABLE anime_settings`,
			`ALTER TABLE anime_settings_current RENAME TO anime_settings`,
		} {
			if _, err := tx.ExecContext(ctx, query); err != nil {
				return fmt.Errorf("simplify anime account settings: %w", err)
			}
		}
	}
	if jobColumn != 0 {
		if _, err := tx.ExecContext(ctx, `ALTER TABLE anime_download_jobs DROP COLUMN account_revision`); err != nil {
			return fmt.Errorf("remove anime task account revision: %w", err)
		}
	}
	for _, query := range []string{
		`UPDATE anime_preview_batches SET snapshot_json=json_remove(snapshot_json,'$.settings.accountRevision','$.settings.uid')`,
		`UPDATE anime_release_decisions SET snapshot_json=json_remove(snapshot_json,'$.selection.target.accountRevision','$.selection.target.uid')`,
		`UPDATE anime_download_jobs SET snapshot_json=json_remove(snapshot_json,'$.target.accountRevision','$.target.uid','$.decision.selection.target.accountRevision','$.decision.selection.target.uid','$.organization.target.accountRevision','$.organization.target.uid')`,
		`UPDATE anime_job_selections SET snapshot_json=json_remove(snapshot_json,'$.target.accountRevision','$.target.uid','$.decision.selection.target.accountRevision','$.decision.selection.target.uid','$.organization.target.accountRevision','$.organization.target.uid')`,
		`UPDATE anime_drive_operations SET request_json=json_remove(request_json,'$.target.accountRevision','$.target.uid')`,
		`UPDATE anime_request_keys SET receipt_json=json_remove(receipt_json,'$.accountRevision','$.uid','$.settings.accountRevision','$.settings.uid')`,
	} {
		if _, err := tx.ExecContext(ctx, query); err != nil {
			return fmt.Errorf("remove anime account snapshot fields: %w", err)
		}
	}
	return nil
}
