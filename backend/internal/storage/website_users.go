package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"strm115/backend/internal/model"
)

var ErrWebsiteUserExists = errors.New("用户名已存在")
var ErrWebsiteUserNotFound = errors.New("用户不存在")

func scanWebsiteUser(row interface{ Scan(...any) error }) (model.WebsiteUser, error) {
	var u model.WebsiteUser
	err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.Disabled, &u.TokenVersion, &u.CreatedAt, &u.CleanupState)
	if errors.Is(err, sql.ErrNoRows) {
		return u, ErrWebsiteUserNotFound
	}
	if err != nil {
		return u, fmt.Errorf("读取网站用户: %w", err)
	}
	return u, nil
}

const websiteUserColumns = "id,username,password_hash,role,disabled,token_version,created_at,COALESCE((SELECT json_extract(record,'$.cleanupState') FROM member_playback WHERE user_id=website_users.id),'')"

func (s *Store) WebsiteUserByName(ctx context.Context, name string) (model.WebsiteUser, error) {
	return scanWebsiteUser(s.db.QueryRowContext(ctx, "SELECT "+websiteUserColumns+" FROM website_users WHERE username=?", name))
}
func (s *Store) WebsiteUser(ctx context.Context, id string) (model.WebsiteUser, error) {
	return scanWebsiteUser(s.db.QueryRowContext(ctx, "SELECT "+websiteUserColumns+" FROM website_users WHERE id=?", id))
}
func (s *Store) CreateWebsiteUser(ctx context.Context, u model.WebsiteUser) error {
	r, err := s.db.ExecContext(ctx, `INSERT INTO website_users(id,username,password_hash,created_at) VALUES(?,?,?,?) ON CONFLICT(username) DO NOTHING`, u.ID, u.Username, u.PasswordHash, u.CreatedAt)
	if err != nil {
		return fmt.Errorf("创建网站用户: %w", err)
	}
	n, err := r.RowsAffected()
	if err != nil {
		return fmt.Errorf("读取用户创建结果: %w", err)
	}
	if n == 0 {
		return ErrWebsiteUserExists
	}
	return nil
}
func (s *Store) WebsiteUsers(ctx context.Context, offset int) ([]model.WebsiteUser, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+websiteUserColumns+" FROM website_users ORDER BY created_at DESC,id LIMIT 51 OFFSET ?", offset)
	if err != nil {
		return nil, fmt.Errorf("查询网站用户列表: %w", err)
	}
	defer rows.Close()
	users := make([]model.WebsiteUser, 0, 51)
	for rows.Next() {
		u, err := scanWebsiteUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("读取用户列表: %w", err)
	}
	return users, nil
}

// 重置密码或停用、启用均递增版本，旧令牌不能重新生效。
func (s *Store) UpdateWebsiteUser(ctx context.Context, id, passwordHash string, disabled *bool) error {
	var value any
	if disabled != nil {
		value = *disabled
	}
	r, err := s.db.ExecContext(ctx, `UPDATE website_users SET password_hash=CASE WHEN ?='' THEN password_hash ELSE ? END, disabled=COALESCE(?,disabled),token_version=token_version+1 WHERE id=? AND NOT EXISTS(SELECT 1 FROM member_playback WHERE user_id=? AND COALESCE(json_extract(record,'$.cleanupState'),'')<>'')`, passwordHash, passwordHash, value, id, id)
	if err != nil {
		return fmt.Errorf("更新网站用户: %w", err)
	}
	n, err := r.RowsAffected()
	if err != nil {
		return fmt.Errorf("读取用户更新结果: %w", err)
	}
	if n == 0 {
		return ErrWebsiteUserNotFound
	}
	return nil
}
