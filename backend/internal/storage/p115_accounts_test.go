package storage

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestPlaybackDirectoryMigrationFromNineteen(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	if _, err := s.db.ExecContext(ctx, `INSERT INTO p115_accounts(id,name,cookie_enc,revision,position) VALUES('friend','friend',x'1234',2,0);
ALTER TABLE p115_accounts DROP COLUMN playback_parent_cid; PRAGMA user_version=19;`); err != nil {
		t.Fatal(err)
	}
	if err := s.initialize(ctx); err != nil {
		t.Fatal(err)
	}
	var cid string
	var revision int
	var cookie []byte
	if err := s.db.QueryRowContext(ctx, `SELECT playback_parent_cid,revision,cookie_enc FROM p115_accounts WHERE id='friend'`).Scan(&cid, &revision, &cookie); err != nil {
		t.Fatal(err)
	}
	if cid != "" || revision != 2 || string(cookie) != string([]byte{0x12, 0x34}) {
		t.Fatal("migration changed credentials or implicitly selected a directory")
	}
}

func TestP115AccountTransactionAndMigration(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	// 模拟现有版本 4 的数据库，升级账号表不能影响已有业务表。
	if _, err := s.db.ExecContext(ctx, "DROP TABLE follow_bot_jobs; DROP TABLE website_notifications; DROP TABLE follow_checks; DROP TABLE follow_settings; DROP TABLE user_follows; DROP TABLE follow_requests;  DROP TABLE telegram_notifications; DROP TABLE p115_feature_migration; DROP TABLE p115_account_state; DROP TABLE p115_accounts; PRAGMA user_version=4;"); err != nil {
		t.Fatal(err)
	}
	if err := s.initialize(ctx); err != nil {
		t.Fatal(err)
	}
	state := P115AccountState{LegacyShareAccountID: "a", Accounts: []P115AccountRow{{ID: "a", Name: "账号一", Cookie: []byte("ciphertext-a"), Revision: 1}, {ID: "b", Name: "账号二", Cookie: []byte("ciphertext-b"), Revision: 1}}}
	if err := s.SaveP115Accounts(ctx, state); err != nil {
		t.Fatal(err)
	}
	before, err := s.LoadP115Accounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SaveP115Accounts(ctx, state); !errors.Is(err, ErrAccountRevision) {
		t.Fatal("未拦截旧进程写入")
	}
	state.Revision = 1
	state.Accounts[0].Name = "失败的修改"
	state.Accounts[0].Revision++
	state.Accounts[1].Revision = 0
	if err := s.SaveP115Accounts(ctx, state); err == nil {
		t.Fatal("无效账号行应使事务回滚")
	}
	after, err := s.LoadP115Accounts(ctx)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("事务失败留下部分修改")
	}
	var count int
	if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM data_migrations WHERE name='p115_accounts_v1'").Scan(&count); err != nil || count != 1 {
		t.Fatal("迁移标记错误")
	}
	// 任意账号都可删除，包括旧版本指定的全局账号。
	state.Accounts[1].Revision = 1
	state.Accounts = state.Accounts[1:]
	if err := s.SaveP115Accounts(ctx, state); err != nil {
		t.Fatal(err)
	}
	after, err = s.LoadP115Accounts(ctx)
	if err != nil || len(after.Accounts) != 1 {
		t.Fatal("账号删除未提交")
	}
	if err := s.Check(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestRemoveGlobalAccountFromVersionFive(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	_, err := s.db.ExecContext(ctx, `DROP TABLE follow_bot_jobs; DROP TABLE website_notifications; DROP TABLE follow_checks; DROP TABLE follow_settings; DROP TABLE user_follows; DROP TABLE follow_requests;  DROP TABLE telegram_notifications; DROP TABLE p115_feature_migration; DROP TABLE p115_account_state;
CREATE TABLE p115_account_state(id INTEGER PRIMARY KEY,active_id TEXT,revision INTEGER);
INSERT INTO p115_accounts(id,name,cookie_enc,revision,position) VALUES('default','默认账号',x'1234',3,0),('other','自定义名称',x'5678',1,1);
INSERT INTO p115_account_state VALUES(1,'default',4);
PRAGMA user_version=5;`)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.initialize(ctx); err != nil {
		t.Fatal(err)
	}
	var columns int
	if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM pragma_table_info('p115_account_state') WHERE name='active_id'").Scan(&columns); err != nil || columns != 0 {
		t.Fatal("全局当前账号列未移除")
	}
	state, err := s.LoadP115Accounts(ctx)
	if err != nil || len(state.Accounts) != 2 || state.Accounts[0].Name != "账号 1" || state.Accounts[1].Name != "自定义名称" {
		t.Fatal("账号升级丢失信息")
	}
	if state.Accounts[0].Revision != 4 || string(state.Accounts[0].Cookie) != string([]byte{0x12, 0x34}) {
		t.Fatal("重命名没有保留原凭据或更新版本")
	}
	id, err := s.PendingP115ShareAccount(ctx)
	if err != nil || id != "default" {
		t.Fatal("未保留升级用的分享账号候选")
	}
	if err := s.FinishP115ShareAccountMigration(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.initialize(ctx); err != nil {
		t.Fatal(err)
	}
	if id, err := s.PendingP115ShareAccount(ctx); err != nil || id != "" {
		t.Fatal("迁移已完成后不能再次继承旧选择")
	}
}
