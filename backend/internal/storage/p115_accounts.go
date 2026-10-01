package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strm115/backend/internal/model"
)

var ErrAccountRevision = errors.New("115 账号存储版本已变化")

// 存储层只接触密文，不持有加密密钥或 Cookie 明文。
type P115AccountRow struct {
	Summary                string
	LoginClient            string
	Ownership, OwnerUserID string
	Primary                bool
	UID                    string
	PlaybackParentCID      string
	ID, Name               string
	Cookie                 []byte
	Revision               uint64
}
type P115AccountState struct {
	// 仅供旧加密文件首次导入时保留分享使用账号，运行时不作回退。
	LegacyShareAccountID string
	Revision             uint64
	Accounts             []P115AccountRow
}

func (s *Store) LoadP115Accounts(ctx context.Context) (P115AccountState, error) {
	var state P115AccountState
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return state, err
	}
	defer tx.Rollback()
	err = tx.QueryRowContext(ctx, "SELECT revision FROM p115_account_state WHERE id=1").Scan(&state.Revision)
	if errors.Is(err, sql.ErrNoRows) {
		return state, nil
	}
	if err != nil {
		return state, fmt.Errorf("读取 115 账号存储版本: %w", err)
	}
	rows, err := tx.QueryContext(ctx, "SELECT id,name,cookie_enc,revision,playback_parent_cid,is_primary,uid,ownership,owner_user_id,login_client,summary FROM p115_accounts ORDER BY position,id")
	if err != nil {
		return state, err
	}
	defer rows.Close()
	state.Accounts = make([]P115AccountRow, 0, 20)
	for rows.Next() {
		var row P115AccountRow
		if err := rows.Scan(&row.ID, &row.Name, &row.Cookie, &row.Revision, &row.PlaybackParentCID, &row.Primary, &row.UID, &row.Ownership, &row.OwnerUserID, &row.LoginClient, &row.Summary); err != nil {
			return state, err
		}
		state.Accounts = append(state.Accounts, row)
	}
	if err := rows.Err(); err != nil {
		return state, err
	}
	return state, nil
}

// 账号变更与首次导入标记在同一事务中提交；失败不会留下半份配置。
func (s *Store) SaveP115Accounts(ctx context.Context, state P115AccountState, member ...model.MemberPlayback) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var revision uint64
	err = tx.QueryRowContext(ctx, "SELECT revision FROM p115_account_state WHERE id=1").Scan(&revision)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if revision != state.Revision {
		return ErrAccountRevision
	}
	if len(state.Accounts) > 120 {
		return errors.New("115 账号数量超出限制")
	}
	// A primary identity replacement inserts a new ID. Release the old unique
	// primary role inside this same transaction before inserting its replacement.
	primaryID := ""
	for _, row := range state.Accounts {
		if row.Primary {
			if primaryID != "" {
				return errors.New("115 主账号配置重复")
			}
			primaryID = row.ID
		}
	}
	if _, err := tx.ExecContext(ctx, "UPDATE p115_accounts SET is_primary=0 WHERE is_primary=1 AND id<>?", primaryID); err != nil {
		return fmt.Errorf("更新主账号身份: %w", err)
	}
	ids := make(map[string]bool, len(state.Accounts))
	for position, row := range state.Accounts {
		if row.ID == "" || ids[row.ID] {
			return errors.New("115 账号编号无效")
		}
		ids[row.ID] = true
		if row.Summary == "" {
			row.Summary = "{}"
		}
		if row.Ownership == "" {
			row.Ownership = "admin"
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO p115_accounts(id,name,cookie_enc,revision,position,playback_parent_cid,is_primary,uid,ownership,owner_user_id,login_client,summary) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET name=excluded.name,cookie_enc=excluded.cookie_enc,revision=excluded.revision,position=excluded.position,playback_parent_cid=excluded.playback_parent_cid,is_primary=excluded.is_primary,uid=excluded.uid,ownership=excluded.ownership,owner_user_id=excluded.owner_user_id,login_client=excluded.login_client,summary=excluded.summary
WHERE name<>excluded.name OR revision<>excluded.revision OR position<>excluded.position OR playback_parent_cid<>excluded.playback_parent_cid OR is_primary<>excluded.is_primary OR uid<>excluded.uid OR ownership<>excluded.ownership OR owner_user_id<>excluded.owner_user_id OR login_client<>excluded.login_client OR summary<>excluded.summary`, row.ID, row.Name, row.Cookie, row.Revision, position, row.PlaybackParentCID, row.Primary, row.UID, row.Ownership, row.OwnerUserID, row.LoginClient, row.Summary)
		if err != nil {
			return fmt.Errorf("保存 115 账号: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO p115_account_state(id,revision) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET revision=excluded.revision`, revision+1); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, "SELECT id FROM p115_accounts")
	if err != nil {
		return err
	}
	remove := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		if !ids[id] {
			remove = append(remove, id)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range remove {
		if _, err := tx.ExecContext(ctx, "DELETE FROM p115_accounts WHERE id=?", id); err != nil {
			return err
		}
	}
	if revision == 0 {
		if state.LegacyShareAccountID != "" {
			if !ids[state.LegacyShareAccountID] {
				return errors.New("旧分享账号配置不存在")
			}
			if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO p115_feature_migration(feature,account_id) VALUES('share',?)", state.LegacyShareAccountID); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO data_migrations(name,imported_count,completed_at) VALUES('p115_accounts_v1',?,strftime('%Y-%m-%dT%H:%M:%SZ','now'))", len(state.Accounts)); err != nil {
			return err
		}
	}
	if len(member) > 0 {
		raw, err := json.Marshal(member[0])
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO member_playback(user_id,record) VALUES(?,?) ON CONFLICT(user_id) DO UPDATE SET record=excluded.record`, member[0].UserID, string(raw)); err != nil {
			return fmt.Errorf("保存个人网盘关联: %w", err)
		}
	}
	return tx.Commit()
}

func (s *Store) PendingP115ShareAccount(ctx context.Context) (string, error) {
	var id string
	err := s.db.QueryRowContext(ctx, "SELECT account_id FROM p115_feature_migration WHERE feature='share'").Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return id, err
}
func (s *Store) FinishP115ShareAccountMigration(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM p115_feature_migration WHERE feature='share'")
	return err
}
