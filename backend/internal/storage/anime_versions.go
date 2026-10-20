package storage

import (
	"context"
	"database/sql"
	"fmt"

	"strm115/backend/internal/animepreview"
	"strm115/backend/internal/model"
)

// Persisted authorization, including a failed/finished task's original choice,
// freezes this source's episode edition. No drive read or filename scan is used.
func animeAuthorizedEditionsTx(ctx context.Context, tx *sql.Tx, sourceID string) (map[string]bool, error) {
	rows, err := tx.QueryContext(ctx, `SELECT snapshot_json FROM anime_release_decisions WHERE source_id=? AND json_type(snapshot_json,'$.selection')='object'`, sourceID)
	if err != nil {
		return nil, fmt.Errorf("read anime edition authorizations: %w", err)
	}
	defer rows.Close()
	editions := make(map[string]bool)
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("scan anime edition authorization: %w", err)
		}
		var snapshot model.AnimeDecisionSnapshot
		if err := decodeAnimeJSON(raw, &snapshot); err != nil {
			return nil, fmt.Errorf("decode anime edition authorization: %w", err)
		}
		if key := animepreview.EpisodeEdition(snapshot.Parse); key != "" {
			editions[key] = true
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate anime edition authorizations: %w", err)
	}
	return editions, nil
}

// A direct claimant still cannot replace an established edition. Repeated posts
// for the exact verified torrent may link its existing job, without new writes.
func checkAnimeClaimedEditionTx(ctx context.Context, tx *sql.Tx, sourceID string, release model.AnimeRelease, hash string) error {
	key := animepreview.EpisodeEdition(release.Parse)
	if key == "" {
		return nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT d.snapshot_json,r.info_hash FROM anime_job_selections j JOIN anime_release_decisions d ON d.source_id=j.source_id AND d.release_id=j.release_id JOIN anime_download_jobs job ON job.id=j.job_id JOIN anime_resource_claims c ON c.id=job.claim_id JOIN anime_resources r ON r.id=c.resource_id WHERE j.source_id=? AND j.release_id<>?`, sourceID, release.ID)
	if err != nil {
		return fmt.Errorf("read anime claimed editions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var raw, existingHash string
		if err := rows.Scan(&raw, &existingHash); err != nil {
			return fmt.Errorf("scan anime claimed edition: %w", err)
		}
		var snapshot model.AnimeDecisionSnapshot
		if err := decodeAnimeJSON(raw, &snapshot); err != nil {
			return fmt.Errorf("decode anime claimed edition: %w", err)
		}
		if animepreview.EpisodeEdition(snapshot.Parse) == key && existingHash != hash {
			return ErrAnimeConflict
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate anime claimed editions: %w", err)
	}
	return nil
}
