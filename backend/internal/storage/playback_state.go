package storage

import (
	"context"
	"encoding/json"
	"fmt"

	"strm115/backend/internal/model"
)

// SavePlaybackState atomically hands over a user's playback and its pool slot.
func (s *Store) SavePlaybackState(ctx context.Context, user model.UserPlaybackDocument, pool model.PlaybackPoolDocument) error {
	u, err := json.Marshal(user)
	if err != nil {
		return fmt.Errorf("encode playback sessions: %w", err)
	}
	p, err := json.Marshal(pool)
	if err != nil {
		return fmt.Errorf("encode playback pool: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin playback handoff: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO user_playback_leases(id,record) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET record=excluded.record`, string(u)); err != nil {
		return fmt.Errorf("save playback sessions: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO playback_pool(id,record) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET record=excluded.record`, string(p)); err != nil {
		return fmt.Errorf("save playback pool: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit playback handoff: %w", err)
	}
	return nil
}
