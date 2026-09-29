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

var ErrPortalInviteInvalid = errors.New("邀请码无效、已过期、已撤销或已使用；如果刚刚注册过，请尝试登录")
var ErrPortalInviteCollision = errors.New("邀请码冲突")

func (s *Store) CreatePortalInvites(ctx context.Context, invites []model.PortalInvite) error {
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
		r, err := tx.ExecContext(ctx, `INSERT INTO portal_invites(id,code_hash,created_by,note,created_at,expires_at,playback_source) VALUES(?,?,?,?,?,?,?) ON CONFLICT DO NOTHING`, invite.ID, invite.CodeHash, invite.CreatedBy, invite.Note, invite.CreatedAt, invite.ExpiresAt, source)
		if err != nil {
			return fmt.Errorf("保存邀请: %w", err)
		}
		n, err := r.RowsAffected()
		if err != nil {
			return fmt.Errorf("读取邀请写入结果: %w", err)
		}
		if n != 1 {
			return ErrPortalInviteCollision
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交创建邀请: %w", err)
	}
	return nil
}

func (s *Store) CheckPortalInvite(ctx context.Context, hash string) error {
	var valid bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM portal_invites WHERE code_hash=? AND used_at=0 AND revoked_at=0 AND expires_at>?)`, hash, time.Now().Unix()).Scan(&valid)
	if err != nil {
		return fmt.Errorf("检查邀请: %w", err)
	}
	if !valid {
		return ErrPortalInviteInvalid
	}
	return nil
}

func (s *Store) PortalInviteSource(ctx context.Context, hash string) (string, error) {
	var source string
	err := s.db.QueryRowContext(ctx, `SELECT playback_source FROM portal_invites WHERE code_hash=? AND used_at=0 AND revoked_at=0 AND expires_at>?`, hash, time.Now().Unix()).Scan(&source)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrPortalInviteInvalid
	}
	if err != nil {
		return "", fmt.Errorf("读取邀请待遇: %w", err)
	}
	return source, nil
}

func (s *Store) PortalInvites(ctx context.Context, offset int) ([]model.PortalInvite, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT i.id,i.created_by,i.note,i.created_at,i.expires_at,i.revoked_at,i.used_at,i.used_by,COALESCE(u.username,''),i.playback_source FROM portal_invites i LEFT JOIN emby_profiles u ON u.id=i.used_by ORDER BY i.created_at DESC,i.id LIMIT 51 OFFSET ?`, offset)
	if err != nil {
		return nil, fmt.Errorf("查询邀请列表: %w", err)
	}
	defer rows.Close()
	invites := make([]model.PortalInvite, 0, 51)
	for rows.Next() {
		var invite model.PortalInvite
		if err := rows.Scan(&invite.ID, &invite.CreatedBy, &invite.Note, &invite.CreatedAt, &invite.ExpiresAt, &invite.RevokedAt, &invite.UsedAt, &invite.UsedBy, &invite.Username, &invite.PlaybackSource); err != nil {
			return nil, fmt.Errorf("读取邀请: %w", err)
		}
		invites = append(invites, invite)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历邀请: %w", err)
	}
	return invites, nil
}

func (s *Store) RevokePortalInvite(ctx context.Context, id string) error {
	r, err := s.db.ExecContext(ctx, `UPDATE portal_invites SET revoked_at=? WHERE id=? AND used_at=0 AND revoked_at=0`, time.Now().Unix(), id)
	if err != nil {
		return fmt.Errorf("撤销邀请: %w", err)
	}
	n, err := r.RowsAffected()
	if err != nil {
		return fmt.Errorf("读取撤销结果: %w", err)
	}
	if n != 1 {
		return ErrPortalInviteInvalid
	}
	return nil
}

// Reservation is persisted before any upstream write. A retry can only resume
// the same random reservation on the same Emby service, never a same-name user.
type EmbyRegistration struct {
	ServerID     string `json:"serverId"`
	EmbyURL      string `json:"embyUrl"`
	Name         string `json:"name"`
	ReservedName string `json:"reservedName"`
	EmbyID       string `json:"embyId"`
	Started      bool   `json:"started"`
	Source       string `json:"source"`
}

func (s *Store) Registration(ctx context.Context, hash string) (EmbyRegistration, error) {
	var r EmbyRegistration
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT registration,playback_source FROM portal_invites WHERE code_hash=? AND used_at=0 AND revoked_at=0 AND expires_at>?`, hash, time.Now().Unix()).Scan(&raw, &r.Source)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrPortalInviteInvalid
	}
	if err != nil {
		return r, err
	}
	if raw != "" {
		err = json.Unmarshal([]byte(raw), &r)
	}
	return r, err
}
func (s *Store) SaveRegistration(ctx context.Context, hash string, r EmbyRegistration) error {
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE portal_invites SET registration=? WHERE code_hash=? AND used_at=0 AND revoked_at=0 AND expires_at>?`, string(raw), hash, time.Now().Unix())
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrPortalInviteInvalid
	}
	return nil
}
func (s *Store) CompleteRegistration(ctx context.Context, hash string, u model.PortalUser, m model.MemberPlayback) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().Unix()
	result, err := tx.ExecContext(ctx, `UPDATE portal_invites SET used_at=?,used_by=?,registration='' WHERE code_hash=? AND used_at=0 AND revoked_at=0 AND expires_at>? AND json_extract(registration,'$.embyId')=? AND json_extract(registration,'$.serverId')=?`, now, u.ID, hash, now, u.EmbyID, u.ServerID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrPortalInviteInvalid
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO emby_profiles(id,server_id,emby_id,username,created_at) VALUES(?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET username=excluded.username`, u.ID, u.ServerID, u.EmbyID, u.Username, u.CreatedAt)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO member_playback(user_id,record) VALUES(?,?) ON CONFLICT(user_id) DO UPDATE SET record=excluded.record`, u.ID, string(raw)); err != nil {
		return err
	}
	return tx.Commit()
}
