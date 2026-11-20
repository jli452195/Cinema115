package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"strm115/backend/internal/model"
)

func (s *Store) EmbyProxyConfig(ctx context.Context) (string, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT record FROM emby_proxy_config WHERE id=1`).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("读取 Emby 反代配置: %w", err)
	}
	return raw, nil
}

func (s *Store) SaveEmbyProxyConfig(ctx context.Context, raw string, settings *model.MemberPlaybackSettings) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开始保存 Emby 反代配置: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO emby_proxy_config(id,record) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET record=excluded.record`, raw); err != nil {
		return fmt.Errorf("保存 Emby 反代配置: %w", err)
	}
	if settings != nil {
		if err := saveMemberPlaybackSettings(ctx, tx, *settings); err != nil {
			return fmt.Errorf("保存成员播放设置: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交 Emby 反代配置: %w", err)
	}
	return nil
}
