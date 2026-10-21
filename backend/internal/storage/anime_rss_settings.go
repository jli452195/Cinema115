package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"strm115/backend/internal/model"
)

func readAnimeRSSSettings(ctx context.Context, q animeReader) (model.AnimeRSSSettings, error) {
	var out model.AnimeRSSSettings
	err := q.QueryRowContext(ctx, `SELECT revision,poll_minutes,auto_archive,inactive_archive_days FROM anime_rss_settings WHERE id=1`).Scan(&out.Revision, &out.PollMinutes, &out.AutoArchive, &out.InactiveArchiveDays)
	return out, err
}

func (s *Store) GetAnimeRSSSettings(ctx context.Context) (model.AnimeRSSSettings, error) {
	out, err := readAnimeRSSSettings(ctx, s.db)
	if err != nil {
		return out, fmt.Errorf("read anime RSS settings: %w", err)
	}
	return out, nil
}

func (s *Store) SaveAnimeRSSSettings(ctx context.Context, in model.AnimeRSSSettings, key AnimeWriteKey) (model.AnimeRSSSettings, error) {
	var out model.AnimeRSSSettings
	if !in.Valid() {
		return out, ErrAnimeInvalid
	}
	if err := key.validate(); err != nil {
		return out, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return out, fmt.Errorf("begin anime RSS settings: %w", err)
	}
	defer tx.Rollback()
	var digest, raw string
	err = tx.QueryRowContext(ctx, `SELECT request_digest,receipt_json FROM anime_request_keys WHERE actor_key=? AND operation=? AND request_key=?`, key.ActorKey, key.Operation, key.Key).Scan(&digest, &raw)
	if err == nil {
		if digest != key.Digest {
			return out, ErrAnimeIdempotency
		}
		if err := decodeAnimeJSON(raw, &out); err != nil {
			return out, err
		}
		return out, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE anime_rss_settings SET revision=revision+1,poll_minutes=?,auto_archive=?,inactive_archive_days=? WHERE id=1 AND revision=?`, in.PollMinutes, in.AutoArchive, in.InactiveArchiveDays, in.Revision)
	if err != nil {
		return out, err
	}
	if err := animeChanged(result); err != nil {
		return out, err
	}
	out = in
	out.Revision++
	raw, err = animeJSON(out)
	if err != nil {
		return out, err
	}
	now := time.Now().UnixMilli()
	// Bring healthy sources onto a shorter interval without bypassing leases
	// or upstream cooldowns. Quiet feeds keep their once-a-day ceiling.
	if _, err := tx.ExecContext(ctx, `UPDATE anime_subscription_sources SET next_check_at=min(next_check_at,?+CASE WHEN EXISTS(SELECT 1 FROM anime_source_activity a WHERE a.source_id=anime_subscription_sources.id AND a.source_revision=anime_subscription_sources.revision AND a.quiet_since<=?) THEN max(?,?) ELSE ? END) WHERE health='ok' AND next_check_at IS NOT NULL AND subscription_id IN (SELECT id FROM anime_subscriptions WHERE status='enabled')`, now, now-30*animeDay, int64(in.PollMinutes)*60000, animeDay, int64(in.PollMinutes)*60000); err != nil {
		return out, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO anime_request_keys(actor_key,operation,request_key,request_digest,http_status,receipt_json,created_at) VALUES(?,?,?,?,200,?,?)`, key.ActorKey, key.Operation, key.Key, key.Digest, raw, now); err != nil {
		return out, err
	}
	if err := tx.Commit(); err != nil {
		return out, fmt.Errorf("commit anime RSS settings: %w", err)
	}
	return out, nil
}
