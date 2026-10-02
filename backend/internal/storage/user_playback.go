package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"strm115/backend/internal/model"
)

func (s *Store) UserPlayback(ctx context.Context) (model.UserPlaybackDocument, error) {
	var d model.UserPlaybackDocument
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT record FROM user_playback_leases WHERE id=1`).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return d, nil
	}
	if err != nil {
		return d, fmt.Errorf("read user playback: %w", err)
	}
	err = json.Unmarshal([]byte(raw), &d)
	return d, err
}
func (s *Store) SaveUserPlayback(ctx context.Context, d model.UserPlaybackDocument) error {
	raw, err := json.Marshal(d)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO user_playback_leases(id,record) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET record=excluded.record`, string(raw))
	if err != nil {
		return fmt.Errorf("save user playback: %w", err)
	}
	return nil
}
