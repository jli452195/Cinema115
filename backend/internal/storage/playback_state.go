package storage

import (
	"context"
	"encoding/json"
	"fmt"

	"strm115/backend/internal/model"
)

// SavePlaybackState 在同一事务中保存用户播放与池名额，保证容量交接的原子性。
func (s *Store) SavePlaybackState(ctx context.Context, user model.UserPlaybackDocument, pool model.PlaybackPoolDocument) error {
	return s.SavePlaybackStateChanges(ctx, &user, &pool)
}

// SavePlaybackStateChanges 仅保存非 nil 的变更文档；双文档变更共用事务，均为 nil 时不访问数据库。
func (s *Store) SavePlaybackStateChanges(ctx context.Context, user *model.UserPlaybackDocument, pool *model.PlaybackPoolDocument) error {
	if user == nil && pool == nil {
		return nil
	}
	var u, p []byte
	var err error
	if user != nil {
		u, err = json.Marshal(user)
		if err != nil {
			return fmt.Errorf("encode playback sessions: %w", err)
		}
	}
	if pool != nil {
		p, err = json.Marshal(pool)
		if err != nil {
			return fmt.Errorf("encode playback pool: %w", err)
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin playback handoff: %w", err)
	}
	defer tx.Rollback()
	if user != nil {
		if _, err := tx.ExecContext(ctx, `INSERT INTO user_playback_leases(id,record) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET record=excluded.record`, string(u)); err != nil {
			return fmt.Errorf("save playback sessions: %w", err)
		}
	}
	if pool != nil {
		if _, err := tx.ExecContext(ctx, `INSERT INTO playback_pool(id,record) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET record=excluded.record`, string(p)); err != nil {
			return fmt.Errorf("save playback pool: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit playback handoff: %w", err)
	}
	return nil
}
