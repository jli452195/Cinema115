package storage

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"time"

	"strm115/backend/internal/anime/preview"
	"strm115/backend/internal/model"
	"strm115/backend/internal/utils/torrentmeta"
)

// Called inside the claim transaction. Existing choices, including failures
// awaiting an explicit retry, reserve episodes without any drive requests.
func animeReservedEpisodesTx(ctx context.Context, tx *sql.Tx, sourceID, exceptRelease string, batchesOnly bool) (map[int]bool, error) {
	out := make(map[int]bool)
	jobs, err := animeProgressRows(ctx, tx, `SELECT x.snapshot_json FROM anime_job_selections x JOIN anime_download_jobs j ON j.id=x.job_id WHERE x.source_id=? AND x.status='authorized' AND x.release_id<>? AND json_array_length(x.snapshot_json,'$.batchFiles')>0 AND NOT EXISTS(SELECT 1 FROM anime_download_jobs n WHERE n.claim_id=j.claim_id AND n.attempt>j.attempt)`, sourceID, exceptRelease)
	if err != nil {
		return nil, err
	}
	for _, raw := range jobs {
		var snapshot model.AnimeDownloadSnapshot
		if err := decodeAnimeJSON(raw, &snapshot); err != nil {
			return nil, err
		}
		for _, file := range snapshot.BatchFiles {
			if file.Parse.Kind != "episode" || file.Parse.Episode == nil {
				return nil, ErrAnimeIdentity
			}
			out[*file.Parse.Episode] = true
		}
	}
	if batchesOnly {
		return out, nil
	}
	decisions, err := animeProgressRows(ctx, tx, `SELECT snapshot_json FROM anime_release_decisions WHERE source_id=? AND release_id<>? AND json_type(snapshot_json,'$.selection')='object'`, sourceID, exceptRelease)
	if err != nil {
		return nil, err
	}
	for _, raw := range decisions {
		var d model.AnimeDecisionSnapshot
		if err := decodeAnimeJSON(raw, &d); err != nil {
			return nil, err
		}
		if d.Parse.Kind == "episode" && d.Parse.Episode != nil {
			out[*d.Parse.Episode] = true
		}
	}
	return out, nil
}

func selectAnimeBatchFilesTx(ctx context.Context, tx *sql.Tx, source model.AnimeSource, release model.AnimeRelease, manifest []torrentmeta.File) ([]model.AnimeBatchFile, []model.AnimeBatchFile, error) {
	rules := source.Rules
	zero := 0
	rules.MinimumEpisode = &zero
	all, err := preview.MatchBatchFiles(source.Binding, rules, manifest)
	if err != nil {
		return nil, nil, ErrAnimeInvalid
	}
	// Idempotent re-linking preserves the original selection, even after it has
	// completed. It never turns the rest of this torrent into another download.
	var raw string
	err = tx.QueryRowContext(ctx, `SELECT x.snapshot_json FROM anime_job_selections x JOIN anime_download_jobs j ON j.id=x.job_id WHERE x.source_id=? AND x.release_id=? AND x.status='authorized' ORDER BY j.attempt DESC LIMIT 1`, source.ID, release.ID).Scan(&raw)
	if err == nil {
		var snapshot model.AnimeDownloadSnapshot
		if err := decodeAnimeJSON(raw, &snapshot); err != nil {
			return nil, nil, err
		}
		for _, selected := range snapshot.BatchFiles {
			valid := false
			for _, file := range all {
				if reflect.DeepEqual(selected, file) && *file.Parse.Episode >= *source.Rules.MinimumEpisode {
					valid = true
					break
				}
			}
			if !valid {
				return nil, nil, ErrAnimeConflict
			}
		}
		return all, snapshot.BatchFiles, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, nil, err
	}
	occupied, err := animeReservedEpisodesTx(ctx, tx, source.ID, release.ID, false)
	if err != nil {
		return nil, nil, err
	}
	selected := make([]model.AnimeBatchFile, 0, len(all))
	for _, file := range all {
		if *file.Parse.Episode >= *source.Rules.MinimumEpisode && !occupied[*file.Parse.Episode] {
			selected = append(selected, file)
		}
	}
	return all, selected, nil
}

func saveAnimeBatchDecisionTx(ctx context.Context, tx *sql.Tx, d model.AnimeReleaseDecision) error {
	raw, err := animeJSON(d.Snapshot)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE anime_release_decisions SET snapshot_json=?,updated_at=? WHERE source_id=? AND release_id=? AND generation=?`, raw, time.Now().UnixMilli(), d.SourceID, d.ReleaseID, d.Generation)
	return err
}

func skipAnimeBatchTx(ctx context.Context, tx *sql.Tx, d model.AnimeReleaseDecision, reason string, out *model.AnimeDownloadClaimResult) error {
	d.Snapshot.Selection = nil
	if err := saveAnimeBatchDecisionTx(ctx, tx, d); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE anime_release_decisions SET disposition='filtered',reason_code=? WHERE source_id=? AND release_id=? AND generation=?`, reason, d.SourceID, d.ReleaseID, d.Generation)
	out.Skipped = err == nil
	return err
}
