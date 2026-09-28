package service

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"strm115/backend/internal/storage"
)

func TestP115SQLiteEncryptedMigrationAndRecovery(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	old, err := NewSessionStore(filepath.Join(dir, "p115-accounts.enc"), "secret")
	if err != nil {
		t.Fatal(err)
	}
	id := "second"
	cookie := "UID=987_A; SEID=private-cookie"
	raw, err := json.Marshal(accountDocument{Version: 1, LegacyActiveID: id, Accounts: []P115Account{
		{ID: "default", Name: "默认账号", Revision: 1},
		{ID: id, Name: "第二账号", Cookie: cookie, Revision: 2},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := old.Save(string(raw)); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(dir, "p115-accounts.enc"))
	if err != nil {
		t.Fatal(err)
	}
	db, err := storage.Open(ctx, filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s, err := OpenP115AccountsSQLite(ctx, db, dir, "secret")
	if err != nil {
		t.Fatal(err)
	}
	if s.CookieFor(id) != cookie || len(s.List()) != 2 {
		t.Fatal("迁移未保留账号和当前选择")
	}
	state, err := db.LoadP115Accounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range state.Accounts {
		if bytes.Contains(row.Cookie, []byte("SEID")) || bytes.Contains(row.Cookie, []byte(cookie)) {
			t.Fatal("数据库出现明文 Cookie")
		}
	}
	if _, err := OpenP115AccountsSQLite(ctx, db, dir, "wrong"); err == nil {
		t.Fatal("错误密钥不能读取")
	}
	if err := s.Update(id, 2, "重命名", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Update(id, 3, "重命名", new(string)); err != nil {
		t.Fatal(err)
	}
	restarted, err := OpenP115AccountsSQLite(ctx, db, dir, "secret")
	if err != nil {
		t.Fatal(err)
	}
	if restarted.CookieFor(id) != "" || restarted.List()[1].Name != "重命名" {
		t.Fatal("数据库更新未持久化或错误重导入旧文件")
	}
	after, err := os.ReadFile(filepath.Join(dir, "p115-accounts.enc"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("不应修改旧账号备份")
	}
	// 已迁移后即使旧文件损坏，也必须只使用数据库。
	if err := os.WriteFile(filepath.Join(dir, "p115-accounts.enc"), []byte("damaged"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenP115AccountsSQLite(ctx, db, dir, "secret"); err != nil {
		t.Fatal(err)
	}
	// 数据库关闭模拟保存失败，内存状态必须保留。
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := restarted.Update(id, 4, "不能保存", &cookie); err == nil || restarted.CookieFor(id) != "" {
		t.Fatal("持久化失败改变内存状态")
	}
}

func TestP115SQLiteSingleAccountAndCorruptLegacy(t *testing.T) {
	for _, corrupt := range []bool{false, true} {
		dir := t.TempDir()
		ctx := context.Background()
		db, err := storage.Open(ctx, filepath.Join(dir, "test.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Close() })
		old, err := NewSessionStore(filepath.Join(dir, "p115-session.enc"), "secret")
		if err != nil {
			t.Fatal(err)
		}
		if err := old.Save("single-cookie"); err != nil {
			t.Fatal(err)
		}
		if corrupt {
			if err := os.WriteFile(filepath.Join(dir, "p115-accounts.enc"), []byte("damaged"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		s, err := OpenP115AccountsSQLite(ctx, db, dir, "secret")
		if corrupt {
			if err == nil {
				t.Fatal("多账号文件损坏时不能回退单账号")
			}
			state, err := db.LoadP115Accounts(ctx)
			if err != nil || state.Revision != 0 || len(state.Accounts) != 0 {
				t.Fatal("失败导入不应提交数据")
			}
		} else if err != nil || s.CookieFor("default") != "single-cookie" {
			t.Fatal("单账号升级失败", err)
		}
	}
}

func TestShareAccountSelectionAndSnapshot(t *testing.T) {
	accounts, err := newP115Accounts(&failingAccountStore{}, "active-cookie")
	if err != nil {
		t.Fatal(err)
	}
	id, err := accounts.Add("固定账号")
	if err != nil {
		t.Fatal(err)
	}
	cookie := "selected-cookie"
	if err := accounts.Update(id, 1, "固定账号", &cookie); err != nil {
		t.Fatal(err)
	}
	s, db, input := setupShareTasks(t, generateFunc(func(ctx context.Context, _ GenerateInput) (*GenerationResult, error) {
		if generationCookie(ctx, func() string { return accounts.CookieFor("") }) != cookie {
			t.Fatal("生成未使用所选账号")
		}
		return &GenerationResult{}, nil
	}))
	s.Settings.ValidateAccount = accounts.ValidateSelection
	s.AccountCookie = accounts.CookieFor
	value := s.Settings.Value()
	value.AccountID = id
	if err := s.Settings.Save(value); err != nil {
		t.Fatal(err)
	}
	// 网页、Telegram 使用的生成入口都必须按服务器所选配置固定 Cookie。
	if _, err := s.Generate(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	if len(taskHistory(t, db)) != 1 {
		t.Fatal("未保存任务")
	}
	loaded, err := NewAutomationStore(s.Settings.path, s.Settings.root, "")
	if err != nil || loaded.Value().AccountID != id {
		t.Fatal("选择未持久化")
	}
	value.AccountID = "missing"
	if err := s.Settings.Save(value); err == nil || s.Settings.Value().AccountID != id {
		t.Fatal("无效选择不能覆盖原配置")
	}
	if err := accounts.Delete(id, 2); err != nil {
		t.Fatal(err)
	}
	if accounts.CookieFor(id) != "" || accounts.CookieFor("") != "" {
		t.Fatal("缺失配置不能自动换用当前账号")
	}
}
