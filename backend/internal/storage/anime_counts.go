package storage

import (
	"context"
	"fmt"

	"strm115/backend/internal/model"
)

func (s *Store) AnimeDecisionCountsBySource(ctx context.Context, id string) (map[string]map[string]int, error) {
	if !model.ValidAnimeID(id) {
		return nil, ErrAnimeInvalid
	}
	rows, err := s.db.QueryContext(ctx, `SELECT d.source_id,d.disposition,count(*) FROM anime_release_decisions d JOIN anime_subscription_sources s ON s.id=d.source_id WHERE s.subscription_id=? GROUP BY d.source_id,d.disposition
 UNION ALL SELECT d.source_id,'pending_verification',count(*) FROM anime_release_decisions d JOIN anime_subscription_sources s ON s.id=d.source_id WHERE s.subscription_id=? AND d.disposition='selected' AND NOT EXISTS(SELECT 1 FROM anime_job_selections x WHERE x.source_id=d.source_id AND x.release_id=d.release_id AND x.status='authorized') GROUP BY d.source_id`, id, id)
	if err != nil {
		return nil, fmt.Errorf("count anime decisions: %w", err)
	}
	defer rows.Close()
	out := make(map[string]map[string]int, model.AnimeMaxSources)
	for rows.Next() {
		var sourceID, key string
		var n int
		if err := rows.Scan(&sourceID, &key, &n); err != nil {
			return nil, fmt.Errorf("read anime counts: %w", err)
		}
		if out[sourceID] == nil {
			out[sourceID] = make(map[string]int, 7)
		}
		out[sourceID][key] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read anime counts: %w", err)
	}
	return out, nil
}
