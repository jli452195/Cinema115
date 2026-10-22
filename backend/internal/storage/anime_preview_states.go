package storage

import (
	"context"
	"encoding/json"
	"fmt"
)

// Preview status is local execution history, never a query for current drive
// contents. A completed file may subsequently have been moved by its owner.
func (s *Store) AnimePreviewStates(ctx context.Context, sourceID string, releaseIDs []string) (map[string]string, error) {
	out := make(map[string]string, len(releaseIDs))
	if sourceID == "" || len(releaseIDs) == 0 {
		return out, nil
	}
	if len(releaseIDs) > 2000 {
		return nil, ErrAnimeInvalid
	}
	raw, err := json.Marshal(releaseIDs)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT d.release_id,d.disposition,COALESCE(j.workflow_state,''),COALESCE(j.download_state,'') FROM anime_release_decisions d LEFT JOIN anime_job_selections x ON x.source_id=d.source_id AND x.release_id=d.release_id LEFT JOIN anime_download_jobs j ON j.id=x.job_id WHERE d.source_id=? AND d.release_id IN (SELECT value FROM json_each(?)) ORDER BY j.updated_at DESC`, sourceID, string(raw))
	if err != nil {
		return nil, fmt.Errorf("read anime preview execution states: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, decision, workflow, download string
		if err := rows.Scan(&id, &decision, &workflow, &download); err != nil {
			return nil, err
		}
		if _, ok := out[id]; ok {
			continue
		}
		state := decision
		if download != "" {
			state = download
		}
		if workflow == "organized" || workflow == "original_saved" {
			state = workflow
		}
		out[id] = state
	}
	return out, rows.Err()
}
