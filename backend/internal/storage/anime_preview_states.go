package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"strm115/backend/internal/anime/preview"
)

// Preview status is local execution history, never a query for current drive
// contents. A completed file may subsequently have been moved by its owner.
func (s *Store) AnimePreviewStates(ctx context.Context, sourceID string, releaseIDs []string) (map[string]string, map[string]preview.Execution, error) {
	out := make(map[string]string, len(releaseIDs))
	jobs := make(map[string]preview.Execution, len(releaseIDs))
	if sourceID == "" || len(releaseIDs) == 0 {
		return out, jobs, nil
	}
	if len(releaseIDs) > 2000 {
		return nil, nil, ErrAnimeInvalid
	}
	raw, err := json.Marshal(releaseIDs)
	if err != nil {
		return nil, nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT d.release_id,d.disposition,COALESCE(d.reason_code,''),COALESCE(j.workflow_state,''),COALESCE(j.download_state,''),COALESCE(j.id,''),COALESCE(j.revision,0),CASE WHEN `+animeRedownloadEligible+` THEN 1 ELSE 0 END FROM anime_release_decisions d LEFT JOIN anime_job_selections x ON x.source_id=d.source_id AND x.release_id=d.release_id LEFT JOIN anime_download_jobs j ON j.id=x.job_id AND NOT EXISTS(SELECT 1 FROM anime_download_jobs newer WHERE newer.claim_id=j.claim_id AND newer.attempt>j.attempt) WHERE d.source_id=? AND d.release_id IN (SELECT value FROM json_each(?)) ORDER BY j.attempt DESC,j.updated_at DESC`, time.Now().UnixMilli(), sourceID, string(raw))
	if err != nil {
		return nil, nil, fmt.Errorf("read anime preview execution states: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, decision, reason, workflow, download string
		var execution preview.Execution
		if err := rows.Scan(&id, &decision, &reason, &workflow, &download, &execution.ID, &execution.Revision, &execution.CanRedownload); err != nil {
			return nil, nil, err
		}
		if _, ok := out[id]; ok {
			continue
		}
		state := decision
		if decision == "filtered" {
			switch reason {
			case "BATCH_NO_MATCHING_FILES", "BATCH_NO_MISSING_FILES", "EPISODE_ALREADY_COVERED", "BELOW_SOURCE_MINIMUM":
				state = reason
			}
		}
		if download != "" {
			state = download
		}
		if workflow == "organized" || workflow == "original_saved" {
			state = workflow
		}
		out[id] = state
		if execution.ID != "" {
			jobs[id] = execution
		}
	}
	return out, jobs, rows.Err()
}
