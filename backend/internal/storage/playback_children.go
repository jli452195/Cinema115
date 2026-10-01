package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strm115/backend/internal/model"
)

var ErrChildQuota = errors.New("播放子账号名额不足")

func (s *Store) ChildUsage(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT owner_id,COUNT(*) FROM playback_child_creations c WHERE NOT EXISTS(SELECT 1 FROM member_playback m WHERE json_extract(m.record,'$.serverId')=json_extract(c.record,'$.serverId') AND json_extract(m.record,'$.embyId')=json_extract(c.record,'$.embyId') AND json_extract(m.record,'$.playbackOnly')=1 AND json_extract(m.record,'$.childRevoked')=1) GROUP BY owner_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]int)
	for rows.Next() {
		var id string
		var count int
		if err := rows.Scan(&id, &count); err != nil {
			return nil, err
		}
		out[id] = count
	}
	return out, rows.Err()
}

func (s *Store) ChildCreations(ctx context.Context, owner string) ([]model.ChildCreation, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT record FROM playback_child_creations WHERE owner_id=? ORDER BY id`, owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]model.ChildCreation, 0)
	for rows.Next() {
		var raw string
		var v model.ChildCreation
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(raw), &v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ReserveChild atomically checks the stored quota and reserves the request once.
func (s *Store) ReserveChild(ctx context.Context, v model.ChildCreation) (model.ChildCreation, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return v, err
	}
	defer tx.Rollback()
	var raw string
	err = tx.QueryRowContext(ctx, `SELECT record FROM playback_child_creations WHERE id=?`, v.ID).Scan(&raw)
	if err == nil {
		var old model.ChildCreation
		if err = json.Unmarshal([]byte(raw), &old); err != nil {
			return v, err
		}
		if old.OwnerID != v.OwnerID || old.Name != v.Name || old.ServerID != v.ServerID || old.EmbyURL != v.EmbyURL {
			return v, fmt.Errorf("创建请求与原任务不一致")
		}
		return old, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return v, err
	}
	var limit, used int
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(json_extract(record,'$.childLimit'),0) FROM member_playback WHERE user_id=?`, v.OwnerID).Scan(&limit); err != nil {
		return v, err
	}
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM playback_child_creations c WHERE owner_id=? AND NOT EXISTS(SELECT 1 FROM member_playback m WHERE json_extract(m.record,'$.serverId')=json_extract(c.record,'$.serverId') AND json_extract(m.record,'$.embyId')=json_extract(c.record,'$.embyId') AND json_extract(m.record,'$.playbackOnly')=1 AND json_extract(m.record,'$.childRevoked')=1)`, v.OwnerID).Scan(&used); err != nil {
		return v, err
	}
	if used >= limit {
		return v, ErrChildQuota
	}
	data, err := json.Marshal(v)
	if err != nil {
		return v, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO playback_child_creations(id,owner_id,record) VALUES(?,?,?)`, v.ID, v.OwnerID, string(data)); err != nil {
		return v, err
	}
	return v, tx.Commit()
}

func (s *Store) SaveChildCreation(ctx context.Context, v model.ChildCreation) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE playback_child_creations SET record=? WHERE id=? AND owner_id=?`, string(raw), v.ID, v.OwnerID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("子账号创建任务不存在")
	}
	return nil
}

// CommitChild installs identity, deny-first binding, and creation progress together.
func (s *Store) CommitChild(ctx context.Context, v model.ChildCreation, u model.PortalUser, r model.MemberPlayback) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO emby_profiles(id,server_id,emby_id,username,created_at) VALUES(?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET username=excluded.username`, u.ID, u.ServerID, u.EmbyID, u.Username, u.CreatedAt); err != nil {
		return err
	}
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO member_playback(user_id,record) VALUES(?,?) ON CONFLICT(user_id) DO UPDATE SET record=excluded.record`, r.UserID, string(raw)); err != nil {
		return err
	}
	raw, err = json.Marshal(v)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE playback_child_creations SET record=? WHERE id=? AND owner_id=?`, string(raw), v.ID, v.OwnerID); err != nil {
		return err
	}
	return tx.Commit()
}
