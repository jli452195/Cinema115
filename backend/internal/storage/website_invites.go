package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strm115/backend/internal/model"
	"time"
)

var ErrWebsiteInviteInvalid = errors.New("邀请码无效、已过期、已撤销或已使用；如果刚刚注册过，请尝试登录")
var ErrWebsiteInviteCollision = errors.New("邀请码冲突")

func (s *Store) WebsiteInviteTarget(ctx context.Context, hash string) (string, error) {
	var target string
	err := s.db.QueryRowContext(ctx, `SELECT emby_target FROM website_invites WHERE code_hash=? AND used_at=0 AND revoked_at=0 AND expires_at>?`, hash, time.Now().Unix()).Scan(&target)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrWebsiteInviteInvalid
	}
	return target, err
}

func (s *Store) LinkExistingMember(ctx context.Context, r model.MemberPlayback) error {
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO member_playback(user_id,record) SELECT id,? FROM website_users WHERE id=? AND role='user' AND disabled=0 ON CONFLICT(user_id) DO UPDATE SET record=excluded.record WHERE COALESCE(json_extract(member_playback.record,'$.embyId'),'')='' AND COALESCE(json_extract(member_playback.record,'$.accountId'),'')='' AND json_extract(member_playback.record,'$.stage')='unbound'`, string(raw), r.UserID)
	if err != nil {
		return fmt.Errorf("关联网站用户: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrWebsiteInviteInvalid
	}
	return nil
}

func (s *Store) CreateWebsiteInvites(ctx context.Context, invites []model.WebsiteInvite) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开始创建邀请: %w", err)
	}
	defer tx.Rollback()
	for _, invite := range invites {
		source := invite.PlaybackSource
		if source == "" {
			source = "personal"
		}
		r, err := tx.ExecContext(ctx, `INSERT INTO website_invites(id,code_hash,created_by,note,created_at,expires_at,playback_source,emby_target,emby_name) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT DO NOTHING`, invite.ID, invite.CodeHash, invite.CreatedBy, invite.Note, invite.CreatedAt, invite.ExpiresAt, source, invite.EmbyTarget, invite.EmbyName)
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

func (s *Store) WebsiteInviteSource(ctx context.Context, hash string) (string, error) {
	var source string
	err := s.db.QueryRowContext(ctx, `SELECT CASE WHEN emby_target<>'' THEN 'existing' ELSE playback_source END FROM website_invites WHERE code_hash=? AND used_at=0 AND revoked_at=0 AND expires_at>?`, hash, time.Now().Unix()).Scan(&source)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrWebsiteInviteInvalid
	}
	if err != nil {
		return "", fmt.Errorf("读取邀请待遇: %w", err)
	}
	return source, nil
}

// The conditional update is the first transaction statement. Concurrent claims
// serialize at the write; any user creation failure rolls the claim back.
func (s *Store) RegisterWebsiteUser(ctx context.Context, hash string, user model.WebsiteUser, member ...model.MemberPlayback) error {
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
	var target string
	if err := tx.QueryRowContext(ctx, `SELECT emby_target FROM website_invites WHERE code_hash=?`, hash).Scan(&target); err != nil {
		return err
	}
	if (target != "") != (len(member) == 1) {
		return ErrWebsiteInviteInvalid
	}
	if len(member) == 1 {
		if member[0].UserID != user.ID {
			return ErrWebsiteInviteInvalid
		}
		raw, err := json.Marshal(member[0])
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO member_playback(user_id,record) VALUES(?,?)`, user.ID, string(raw)); err != nil {
			return fmt.Errorf("保存 Emby 关联: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交注册: %w", err)
	}
	return nil
}

func (s *Store) WebsiteInvites(ctx context.Context, offset int) ([]model.WebsiteInvite, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT i.id,i.created_by,i.note,i.created_at,i.expires_at,i.revoked_at,i.used_at,i.used_by,COALESCE(u.username,''),i.playback_source,i.emby_name FROM website_invites i LEFT JOIN website_users u ON u.id=i.used_by ORDER BY i.created_at DESC,i.id LIMIT 51 OFFSET ?`, offset)
	if err != nil {
		return nil, fmt.Errorf("查询邀请列表: %w", err)
	}
	defer rows.Close()
	invites := make([]model.WebsiteInvite, 0, 51)
	for rows.Next() {
		var invite model.WebsiteInvite
		if err := rows.Scan(&invite.ID, &invite.CreatedBy, &invite.Note, &invite.CreatedAt, &invite.ExpiresAt, &invite.RevokedAt, &invite.UsedAt, &invite.UsedBy, &invite.Username, &invite.PlaybackSource, &invite.EmbyName); err != nil {
			return nil, fmt.Errorf("读取邀请: %w", err)
		}
		invites = append(invites, invite)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历邀请: %w", err)
	}
	return invites, nil
}

func (s *Store) WebsitePlaybackSource(ctx context.Context, userID string) (string, error) {
	var source string
	err := s.db.QueryRowContext(ctx, `SELECT playback_source FROM website_invites WHERE used_by=? AND used_at>0 LIMIT 1`, userID).Scan(&source)
	if errors.Is(err, sql.ErrNoRows) {
		return "personal", nil
	}
	if err != nil {
		return "", fmt.Errorf("读取邀请播放待遇: %w", err)
	}
	if source != "primary" {
		return "personal", nil
	}
	return source, nil
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
