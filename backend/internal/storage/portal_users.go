package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strm115/backend/internal/model"
)

var ErrEmbyUserExists = errors.New("Emby 用户名已存在")
var ErrEmbyUserNotFound = errors.New("Emby 用户不存在")

func (s *Store) PortalUser(ctx context.Context, id string) (model.PortalUser, error) {
	var u model.PortalUser
	err := s.db.QueryRowContext(ctx, `SELECT id,server_id,emby_id,username,token_version,created_at FROM emby_profiles WHERE id=?`, id).Scan(&u.ID, &u.ServerID, &u.EmbyID, &u.Username, &u.TokenVersion, &u.CreatedAt)
	u.Role = "user"
	if errors.Is(err, sql.ErrNoRows) {
		return u, ErrEmbyUserNotFound
	}
	return u, err
}
func (s *Store) SaveEmbyProfile(ctx context.Context, u model.PortalUser) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO emby_profiles(id,server_id,emby_id,username,created_at) VALUES(?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET username=excluded.username WHERE server_id=excluded.server_id AND emby_id=excluded.emby_id`, u.ID, u.ServerID, u.EmbyID, u.Username, u.CreatedAt)
	if err != nil {
		return fmt.Errorf("保存 Emby 用户资料: %w", err)
	}
	return nil
}
func (s *Store) RevokePortalSessions(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE emby_profiles SET token_version=token_version+1 WHERE id=?`, id)
	return err
}
