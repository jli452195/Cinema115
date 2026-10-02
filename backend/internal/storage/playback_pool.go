package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strm115/backend/internal/model"
)

func (s *Store) PlaybackPool(ctx context.Context) (model.PlaybackPoolDocument, error) {
	var d model.PlaybackPoolDocument
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT record FROM playback_pool WHERE id=1`).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return d, nil
	}
	if err != nil {
		return d, fmt.Errorf("read playback pool: %w", err)
	}
	err = json.Unmarshal([]byte(raw), &d)
	return d, err
}

func (s *Store) SavePlaybackPool(ctx context.Context, d model.PlaybackPoolDocument) error {
	raw, err := json.Marshal(d)
	if err != nil {
		return fmt.Errorf("encode playback pool: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO playback_pool(id,record) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET record=excluded.record`, string(raw))
	if err != nil {
		return fmt.Errorf("save playback pool: %w", err)
	}
	return nil
}
