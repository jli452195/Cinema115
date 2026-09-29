package storage

import (
	"context"
	"errors"
	"fmt"
	"strm115/backend/internal/model"
	"time"
)

var ErrWebsiteInviteInvalid = errors.New("邀请码无效、已过期、已撤销或已使用；如果刚刚注册过，请尝试登录")
var ErrWebsiteInviteCollision = errors.New("邀请码冲突")

func (s *Store) CreateWebsiteInvites(ctx context.Context, invites []model.WebsiteInvite) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开始创建邀请: %w", err)
	}
	defer tx.Rollback()
	for _, invite := range invites {
		r, err := tx.ExecContext(ctx, `INSERT INTO website_invites(id,code_hash,created_by,note,created_at,expires_at) VALUES(?,?,?,?,?,?) ON CONFLICT DO NOTHING`, invite.ID, invite.CodeHash, invite.CreatedBy, invite.Note, invite.CreatedAt, invite.ExpiresAt)
		if err != nil {
			return fmt.Errorf("保存邀请: %w", err)
		}
		n, err := r.RowsAffected()
		if err != nil {
			return fmt.Errorf("读取邀请写入结果: %w", err)
		}
		if n != 1 {
			return ErrWebsiteInviteCollision
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交创建邀请: %w", err)
	}
	return nil
}

func (s *Store) CheckWebsiteInvite(ctx context.Context, hash string) error {
	var valid bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM website_invites WHERE code_hash=? AND used_at=0 AND revoked_at=0 AND expires_at>?)`, hash, time.Now().Unix()).Scan(&valid)
	if err != nil {
		return fmt.Errorf("检查邀请: %w", err)
	}
	if !valid {
		return ErrWebsiteInviteInvalid
	}
	return nil
}

// The conditional update is the first transaction statement. Concurrent claims
// serialize at the write; any user creation failure rolls the claim back.
func (s *Store) RegisterWebsiteUser(ctx context.Context, hash string, user model.WebsiteUser) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开始注册: %w", err)
	}
	defer tx.Rollback()
	now := time.Now().Unix()
	r, err := tx.ExecContext(ctx, `UPDATE website_invites SET used_at=?,used_by=? WHERE code_hash=? AND used_at=0 AND revoked_at=0 AND expires_at>?`, now, user.ID, hash, now)
	if err != nil {
		return fmt.Errorf("核销邀请: %w", err)
	}
	n, err := r.RowsAffected()
	if err != nil {
		return fmt.Errorf("读取邀请核销结果: %w", err)
	}
	if n != 1 {
		return ErrWebsiteInviteInvalid
	}
	r, err = tx.ExecContext(ctx, `INSERT INTO website_users(id,username,password_hash,created_at) VALUES(?,?,?,?) ON CONFLICT(username) DO NOTHING`, user.ID, user.Username, user.PasswordHash, user.CreatedAt)
	if err != nil {
		return fmt.Errorf("注册网站用户: %w", err)
	}
	n, err = r.RowsAffected()
	if err != nil {
		return fmt.Errorf("读取注册结果: %w", err)
	}
	if n != 1 {
		return ErrWebsiteUserExists
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交注册: %w", err)
	}
	return nil
}

func (s *Store) WebsiteInvites(ctx context.Context, offset int) ([]model.WebsiteInvite, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT i.id,i.created_by,i.note,i.created_at,i.expires_at,i.revoked_at,i.used_at,i.used_by,COALESCE(u.username,'') FROM website_invites i LEFT JOIN website_users u ON u.id=i.used_by ORDER BY i.created_at DESC,i.id LIMIT 51 OFFSET ?`, offset)
	if err != nil {
		return nil, fmt.Errorf("查询邀请列表: %w", err)
	}
	defer rows.Close()
	invites := make([]model.WebsiteInvite, 0, 51)
	for rows.Next() {
		var invite model.WebsiteInvite
		if err := rows.Scan(&invite.ID, &invite.CreatedBy, &invite.Note, &invite.CreatedAt, &invite.ExpiresAt, &invite.RevokedAt, &invite.UsedAt, &invite.UsedBy, &invite.Username); err != nil {
			return nil, fmt.Errorf("读取邀请: %w", err)
		}
		invites = append(invites, invite)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历邀请: %w", err)
	}
	return invites, nil
}

func (s *Store) RevokeWebsiteInvite(ctx context.Context, id string) error {
	r, err := s.db.ExecContext(ctx, `UPDATE website_invites SET revoked_at=? WHERE id=? AND used_at=0 AND revoked_at=0`, time.Now().Unix(), id)
	if err != nil {
		return fmt.Errorf("撤销邀请: %w", err)
	}
	n, err := r.RowsAffected()
	if err != nil {
		return fmt.Errorf("读取撤销结果: %w", err)
	}
	if n != 1 {
		return ErrWebsiteInviteInvalid
	}
	return nil
}
