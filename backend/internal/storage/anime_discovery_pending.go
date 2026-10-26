package storage

import (
	"context"
	"database/sql"
	"errors"

	"strm115/backend/internal/client/animefeed"
	"strm115/backend/internal/model"
)

type AnimeDiscoveryWork struct {
	Decision    model.AnimeReleaseDecision
	Observation animefeed.SourceRelease
	Token       string
}

// Pending choices survive RSS window eviction and process restart. A random
// token plus a new decision generation fences a timed-out metadata verifier;
// this reservation never permits an external drive write.
func (s *Store) ReserveAnimeDiscovery(ctx context.Context, now int64, providers ...string) (AnimeDiscoveryWork, bool, error) {
	var out AnimeDiscoveryWork
	if now <= 0 || len(providers) > 1 {
		return out, false, ErrAnimeInvalid
	}
	provider := ""
	if len(providers) == 1 {
		provider = providers[0]
		if provider != "" && !animefeed.ValidProvider(provider) {
			return out, false, ErrAnimeInvalid
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return out, false, err
	}
	defer tx.Rollback()
	var sourceID, releaseID, raw string
	// Serialize collection verification within its source. A failed collection's
	// backoff releases ordinary singles; other sources are never held behind it.
	err = tx.QueryRowContext(ctx, `SELECT d.source_id,d.release_id,d.observation_json FROM anime_release_decisions d JOIN anime_subscription_sources s ON s.id=d.source_id JOIN anime_subscriptions p ON p.id=s.subscription_id WHERE d.disposition='selected' AND d.observation_json IS NOT NULL AND (?='' OR s.provider=?) AND (d.next_verify_at IS NULL OR d.next_verify_at<=?) AND (d.lease_until IS NULL OR d.lease_until<=?) AND d.generation<? AND `+animePollEligible+` AND NOT EXISTS(SELECT 1 FROM anime_job_selections j WHERE j.source_id=d.source_id AND j.release_id=d.release_id) AND NOT EXISTS(SELECT 1 FROM anime_release_decisions b WHERE b.source_id=d.source_id AND b.disposition='selected' AND b.lease_until>? AND json_extract(b.snapshot_json,'$.parse.kind')='batch') ORDER BY CASE WHEN json_extract(d.snapshot_json,'$.parse.kind')='batch' THEN 0 ELSE 1 END,COALESCE(d.next_verify_at,d.updated_at),d.source_id,d.release_id LIMIT 1`, provider, provider, now, now, model.AnimeMaxRevision-1, now).Scan(&sourceID, &releaseID, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return out, false, nil
	}
	if err != nil {
		return out, false, err
	}
	if err := decodeAnimeJSON(raw, &out.Observation); err != nil {
		return out, false, err
	}
	out.Token, err = newAnimeID()
	if err != nil {
		return out, false, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE anime_release_decisions SET lease_token=?,lease_until=?,generation=generation+1 WHERE source_id=? AND release_id=?`, out.Token, now+animeDiscoveryLease, sourceID, releaseID)
	if err != nil {
		return out, false, err
	}
	out.Decision, err = readAnimeReleaseDecision(ctx, tx, sourceID, releaseID)
	if err != nil {
		return out, false, err
	}
	if err := tx.Commit(); err != nil {
		return AnimeDiscoveryWork{}, false, err
	}
	return out, true, nil
}

func (s *Store) CompleteAnimeDiscovery(ctx context.Context, work AnimeDiscoveryWork, code string, retryable bool, now int64) error {
	if !model.ValidAnimeID(work.Token) || !model.ValidAnimeID(work.Decision.SourceID) || !model.ValidAnimeID(work.Decision.ReleaseID) || now <= 0 {
		return ErrAnimeInvalid
	}
	switch code {
	case "", "RESOURCE_READ_FAILED", "RESOURCE_REVIEW_REQUIRED", "DOWNLOAD_TARGET_CHANGED", "DISCOVERY_CANCELLED":
	default:
		return ErrAnimeInvalid
	}
	if retryable && code != "RESOURCE_READ_FAILED" {
		return ErrAnimeInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	d, err := readAnimeReleaseDecision(ctx, tx, work.Decision.SourceID, work.Decision.ReleaseID)
	if err != nil {
		return err
	}
	var token *string
	var failures int
	var linked bool
	err = tx.QueryRowContext(ctx, `SELECT lease_token,verify_failures,EXISTS(SELECT 1 FROM anime_job_selections j WHERE j.source_id=d.source_id AND j.release_id=d.release_id) FROM anime_release_decisions d WHERE source_id=? AND release_id=?`, d.SourceID, d.ReleaseID).Scan(&token, &failures, &linked)
	if err != nil {
		return err
	}
	if token == nil || *token != work.Token {
		return ErrAnimeConflict
	}
	var next, reason any
	disposition := d.Disposition
	if linked || d.Disposition == "filtered" {
		// Claim committed before its local receipt was lost: release the read
		// reservation but never downgrade or retry the existing job.
		reason = d.ReasonCode
	} else {
		if d.Generation != work.Decision.Generation || disposition != "selected" || code == "" {
			return ErrAnimeConflict
		}
		reason = code
		switch {
		case code == "DISCOVERY_CANCELLED":
			next = now + 5000
		case retryable:
			failures = min(failures+1, 4)
			next = now + animeDiscoveryBackoff(failures)
		default:
			disposition = "needs_confirmation"
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE anime_release_decisions SET disposition=?,reason_code=?,next_verify_at=?,verify_failures=?,lease_token=NULL,lease_until=NULL,updated_at=? WHERE source_id=? AND release_id=?`, disposition, reason, next, failures, now, d.SourceID, d.ReleaseID)
	if err != nil {
		return err
	}
	return tx.Commit()
}
