package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strm115/backend/internal/model"
)

type MemberPlaybackOwner struct {
	UserID   string
	Username string
	Disabled bool
}

func (s *Store) MemberPlaybackOwners(ctx context.Context) ([]MemberPlaybackOwner, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT u.id,u.username,u.disabled FROM website_users u JOIN member_playback m ON m.user_id=u.id ORDER BY u.id`)
	if err != nil {
		return nil, fmt.Errorf("读取播放用户归属: %w", err)
	}
	defer rows.Close()
	out := make([]MemberPlaybackOwner, 0)
	for rows.Next() {
		var owner MemberPlaybackOwner
		if err := rows.Scan(&owner.UserID, &owner.Username, &owner.Disabled); err != nil {
			return nil, err
		}
		out = append(out, owner)
	}
	return out, rows.Err()
}

func (s *Store) MemberPlaybackRecords(ctx context.Context) ([]model.MemberPlayback, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT record FROM member_playback`)
	if err != nil {
		return nil, fmt.Errorf("读取个人播放绑定: %w", err)
	}
	defer rows.Close()
	result := make([]model.MemberPlayback, 0)
	for rows.Next() {
		var raw string
		var record model.MemberPlayback
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(raw), &record); err != nil {
			return nil, err
		}
		result = append(result, record)
	}
	return result, rows.Err()
}
func (s *Store) SaveMemberPlayback(ctx context.Context, record model.MemberPlayback) error {
	raw, err := json.Marshal(record)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO member_playback(user_id,record) VALUES(?,?) ON CONFLICT(user_id) DO UPDATE SET record=excluded.record`, record.UserID, string(raw))
	if err != nil {
		return fmt.Errorf("保存个人播放绑定: %w", err)
	}
	return nil
}
func (s *Store) MemberPlaybackSettings(ctx context.Context) (model.MemberPlaybackSettings, error) {
	var settings model.MemberPlaybackSettings
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT record FROM member_playback_settings WHERE id=1`).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return settings, nil
	}
	if err != nil {
		return settings, err
	}
	err = json.Unmarshal([]byte(raw), &settings)
	return settings, err
}
func (s *Store) SaveMemberPlaybackSettings(ctx context.Context, settings model.MemberPlaybackSettings) error {
	raw, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO member_playback_settings(id,record) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET record=excluded.record`, string(raw))
	return err
}

// Conservative reference audit before removing an empty personal account.
func (s *Store) MemberAccountReferenced(ctx context.Context, userID, accountID string) (bool, error) {
	var used bool
	err := s.db.QueryRowContext(ctx, `SELECT
EXISTS(SELECT 1 FROM member_playback WHERE user_id<>? AND instr(record,?)>0)
OR EXISTS(SELECT 1 FROM playback_copies WHERE account_id=? OR instr(record,?)>0)
OR EXISTS(SELECT 1 FROM playback_settings WHERE instr(record,?)>0)
OR EXISTS(SELECT 1 FROM share_tasks WHERE instr(record,?)>0 OR instr(request,?)>0)
OR EXISTS(SELECT 1 FROM share_run_cache WHERE instr(record,?)>0)
OR EXISTS(SELECT 1 FROM p115_folder_monitors WHERE account_id=? OR instr(record,?)>0)
OR EXISTS(SELECT 1 FROM follow_settings WHERE account_id=?)
OR EXISTS(SELECT 1 FROM p115_feature_migration WHERE account_id=?)`, userID, accountID, accountID, accountID, accountID, accountID, accountID, accountID, accountID, accountID, accountID, accountID).Scan(&used)
	if err != nil {
		return false, fmt.Errorf("检查个人网盘引用: %w", err)
	}
	return used, nil
}
