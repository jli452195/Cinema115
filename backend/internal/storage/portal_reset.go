package storage

import (
	"context"
	"fmt"
)

// ResetPortal is an explicit offline test-data reset, never a login migration.
// The caller must back up the database and stop the application first.
func (s *Store) ResetPortal(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, query := range []string{
		`DELETE FROM playback_copies WHERE account_id IN (SELECT id FROM p115_accounts WHERE ownership='member' OR owner_user_id<>'')`,
		`DELETE FROM p115_accounts WHERE ownership='member' OR owner_user_id<>''`,
		`UPDATE p115_account_state SET revision=revision+1`,
		`DELETE FROM playback_settings`,
		`DELETE FROM user_follows`,
		`DELETE FROM website_notifications`,
		`DROP TABLE IF EXISTS member_playback`,
		`DROP TABLE IF EXISTS member_deletions`,
		`DROP TABLE IF EXISTS playback_child_creations`,
		`DROP TABLE IF EXISTS website_user_cleanup`,
		`DROP TABLE IF EXISTS website_invites`,
		`DROP TABLE IF EXISTS website_users`,
		`DROP TABLE IF EXISTS portal_invites`,
		`DROP TABLE IF EXISTS emby_profiles`,
	} {
		if _, err := tx.ExecContext(ctx, query); err != nil {
			return fmt.Errorf("重置个人中心测试数据: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return s.initialize(ctx)
}
