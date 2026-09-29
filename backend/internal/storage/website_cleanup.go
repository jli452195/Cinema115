package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"strm115/backend/internal/model"
)

// Freeze authentication and persist the immutable cleanup choice together.
func (s *Store) BeginWebsiteCleanup(ctx context.Context, r model.MemberPlayback) error {
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE website_users SET disabled=1,token_version=token_version+1 WHERE id=? AND role='user'`, r.UserID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrWebsiteUserNotFound
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO member_playback(user_id,record) VALUES(?,?) ON CONFLICT(user_id) DO UPDATE SET record=excluded.record`, r.UserID, string(raw)); err != nil {
		return err
	}
	if r.EmbyID != "" {
		if _, err := tx.ExecContext(ctx, `UPDATE website_invites SET revoked_at=strftime('%s','now') WHERE used_at=0 AND revoked_at=0 AND emby_target<>'' AND json_extract(emby_target,'$.serverId')=? AND json_extract(emby_target,'$.embyId')=?`, r.ServerID, r.EmbyID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Keep a credential-free identity guard after deleting the website login.
func (s *Store) FinishWebsiteCleanup(ctx context.Context, r model.MemberPlayback) error {
	if r.CleanupState != "deleted" {
		return fmt.Errorf("用户清理状态无效")
	}
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var pending bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM member_playback WHERE user_id=? AND json_extract(record,'$.cleanupState')='deleting' AND json_extract(record,'$.cleanupMode')=?)`, r.UserID, r.CleanupMode).Scan(&pending); err != nil {
		return err
	}
	if !pending {
		return fmt.Errorf("用户清理记录已变化")
	}
	for _, query := range []string{
		`DELETE FROM user_follows WHERE user_id=?`,
		`DELETE FROM website_notifications WHERE user_id=?`,
		`DELETE FROM member_playback WHERE user_id=?`,
		`DELETE FROM website_users WHERE id=? AND disabled=1`,
	} {
		if _, err := tx.ExecContext(ctx, query, r.UserID); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE follow_checks SET due=9223372036854775807 WHERE NOT EXISTS(SELECT 1 FROM user_follows f WHERE f.kind=follow_checks.kind AND f.tmdb_id=follow_checks.tmdb_id AND f.active=1)`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO website_user_cleanup(user_id,record) VALUES(?,?)`, r.UserID, string(raw)); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) WebsiteCleanupRecords(ctx context.Context) ([]model.MemberPlayback, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT record FROM website_user_cleanup`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]model.MemberPlayback, 0)
	for rows.Next() {
		var raw string
		var r model.MemberPlayback
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(raw), &r); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
