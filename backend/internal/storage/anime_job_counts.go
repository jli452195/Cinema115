package storage

import "context"

// Counts use distinct latest resource attempts. One resource selected through
// two RSS sources contributes once to its subscription, once to each source.
type AnimeJobCounts struct{ Total, Queued, Downloading, RemoteCompleted, Failed, NeedsReview, Verified, FileReview, OriginalSaved, Organized int }

func (s *Store) AnimeJobCountsBySource(ctx context.Context, id string) (map[string]AnimeJobCounts, error) {
	rows, err := s.db.QueryContext(ctx, `WITH linked AS (
 SELECT DISTINCT j.id,j.download_state,j.file_state,j.workflow_state,j.block_code,(SELECT count(*) FROM json_each(j.files_json,'$.entries') f WHERE json_extract(f.value,'$.kind')='file') AS file_count,x.source_id
 FROM anime_download_jobs j JOIN anime_job_selections x ON x.job_id=j.id JOIN anime_subscription_sources s ON s.id=x.source_id
 WHERE s.subscription_id=? AND NOT EXISTS(SELECT 1 FROM anime_download_jobs newer WHERE newer.claim_id=j.claim_id AND newer.attempt>j.attempt)
), dimensions AS (SELECT * FROM linked UNION SELECT id,download_state,file_state,workflow_state,block_code,file_count,'' FROM linked)
SELECT source_id,count(*),sum(download_state IN ('queued','submitting')),sum(download_state IN ('accepted','downloading')),sum(download_state='completed'),sum(download_state='failed'),sum(block_code IS NOT NULL OR file_state='needs_review' OR download_state IN ('blocked','unknown')),sum(CASE WHEN file_state='verified' THEN file_count ELSE 0 END),0,sum(file_state='verified'),sum(workflow_state='organized') FROM dimensions GROUP BY source_id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]AnimeJobCounts, 6)
	for rows.Next() {
		var key string
		var v AnimeJobCounts
		if err := rows.Scan(&key, &v.Total, &v.Queued, &v.Downloading, &v.RemoteCompleted, &v.Failed, &v.NeedsReview, &v.Verified, &v.FileReview, &v.OriginalSaved, &v.Organized); err != nil {
			return nil, err
		}
		out[key] = v
	}
	return out, rows.Err()
}
