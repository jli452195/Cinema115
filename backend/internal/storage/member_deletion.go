package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"strm115/backend/internal/model"
)

func (s *Store) MemberDeletions(ctx context.Context) ([]model.MemberDeletion, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT record FROM member_deletions ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]model.MemberDeletion, 0)
	for rows.Next() {
		var raw string
		var v model.MemberDeletion
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

func (s *Store) SaveMemberDeletion(ctx context.Context, v model.MemberDeletion) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO member_deletions(id,record) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET record=excluded.record`, v.ID, string(raw))
	return err
}

// Pending creations are listed even if creation never returned an Emby ID.
func (s *Store) IncompleteMembers(ctx context.Context, includeCompletedChildren ...bool) ([]model.MemberDeletionTarget, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,registration FROM portal_invites WHERE registration<>'' AND used_at=0`)
	if err != nil {
		return nil, err
	}
	out := make([]model.MemberDeletionTarget, 0)
	for rows.Next() {
		var id, raw string
		var r EmbyRegistration
		if err := rows.Scan(&id, &raw); err != nil {
			rows.Close()
			return nil, err
		}
		if err := json.Unmarshal([]byte(raw), &r); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, model.MemberDeletionTarget{Key: "invite:" + id, InviteID: id, ServerID: r.ServerID, EmbyURL: r.EmbyURL, EmbyID: r.EmbyID, Name: r.Name, ReservedName: r.ReservedName, Started: r.Started})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	query := `SELECT record FROM playback_child_creations WHERE COALESCE(json_extract(record,'$.completed'),0)=0`
	if len(includeCompletedChildren) > 0 && includeCompletedChildren[0] {
		query = `SELECT record FROM playback_child_creations`
	}
	rows, err = s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		var r model.ChildCreation
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(raw), &r); err != nil {
			return nil, err
		}
		out = append(out, model.MemberDeletionTarget{Key: "child:" + r.ID, ChildID: r.ID, OwnerID: r.OwnerID, ServerID: r.ServerID, EmbyURL: r.EmbyURL, EmbyID: r.EmbyID, Name: r.Name, ReservedName: r.ReservedName, Started: r.Started})
	}
	return out, rows.Err()
}

// Identity, creation progress and both capacity documents commit together.
// Personal credentials are removed in the preceding durable cleanup step.
func (s *Store) FinishMemberDeletion(ctx context.Context, job model.MemberDeletion, target model.MemberDeletionTarget, users model.UserPlaybackDocument, pool model.PlaybackPoolDocument) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, v := range []struct {
		query string
		args  []any
	}{
		{`UPDATE portal_invites SET revoked_at=CASE WHEN revoked_at=0 THEN unixepoch() ELSE revoked_at END, registration='' WHERE id=? OR (registration<>'' AND json_extract(registration,'$.serverId')=? AND json_extract(registration,'$.embyId')=? AND ?<>'')`, []any{target.InviteID, target.ServerID, target.EmbyID, target.EmbyID}},
		{`DELETE FROM playback_child_creations WHERE id=? OR (json_extract(record,'$.serverId')=? AND json_extract(record,'$.embyId')=? AND ?<>'')`, []any{target.ChildID, target.ServerID, target.EmbyID, target.EmbyID}},
		{`DELETE FROM member_playback WHERE user_id=?`, []any{target.UserID}},
		{`DELETE FROM user_follows WHERE user_id=?`, []any{target.UserID}},
		{`DELETE FROM website_notifications WHERE user_id=?`, []any{target.UserID}},
		{`DELETE FROM emby_profiles WHERE id=?`, []any{target.UserID}},
	} {
		if _, err := tx.ExecContext(ctx, v.query, v.args...); err != nil {
			return fmt.Errorf("清理用户关联: %w", err)
		}
	}
	for _, v := range []struct {
		query string
		value any
	}{
		{`INSERT INTO user_playback_leases(id,record) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET record=excluded.record`, users},
		{`INSERT INTO playback_pool(id,record) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET record=excluded.record`, pool},
	} {
		raw, err := json.Marshal(v.value)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, v.query, string(raw)); err != nil {
			return err
		}
	}
	raw, err := json.Marshal(job)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE member_deletions SET record=? WHERE id=?`, string(raw), job.ID); err != nil {
		return err
	}
	return tx.Commit()
}
