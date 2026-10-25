package storage

import (
	"context"
	"fmt"

	"strm115/backend/internal/model"
)

// History remains available while its subscription exists, including archives.
// Listing never scans the drive or recreates a job.
func (s *Store) ListAnimeDownloadJobs(ctx context.Context, subscriptionID string, page, size int) (model.AnimeDownloadJobPage, error) {
	out := model.AnimeDownloadJobPage{Items: []model.AnimeDownloadJob{}, Page: page, PageSize: size}
	if !model.ValidAnimeID(subscriptionID) || page < 1 || page > 10000 || size < 1 || size > 100 {
		return out, ErrAnimeInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return out, fmt.Errorf("begin anime job listing: %w", err)
	}
	defer tx.Rollback()
	var id string
	if err := tx.QueryRowContext(ctx, `SELECT id FROM anime_subscriptions WHERE id=?`, subscriptionID).Scan(&id); err != nil {
		return out, err
	}
	const filter = ` NOT EXISTS(SELECT 1 FROM anime_download_jobs newer WHERE newer.claim_id=j.claim_id AND newer.attempt>j.attempt) AND EXISTS(SELECT 1 FROM anime_job_selections x JOIN anime_subscription_sources s ON s.id=x.source_id WHERE x.job_id=j.id AND s.subscription_id=?)`
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM anime_download_jobs j WHERE`+filter, subscriptionID).Scan(&out.Total); err != nil {
		return out, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+animeJobColumns+` FROM anime_download_jobs j WHERE`+filter+` ORDER BY j.created_at DESC,j.id LIMIT ? OFFSET ?`, subscriptionID, size, (page-1)*size)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		job, err := scanAnimeDownloadJob(rows)
		if err != nil {
			return out, err
		}
		out.Items = append(out.Items, job)
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	if err := rows.Close(); err != nil {
		return out, err
	}
	if err := tx.Commit(); err != nil {
		return out, err
	}
	return out, nil
}
