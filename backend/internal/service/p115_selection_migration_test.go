package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"strm115/backend/internal/storage"
)

func TestExplicitShareSelectionMigration(t *testing.T) {
	for _, selected := range []string{"", "chosen"} {
		t.Run("旧配置-"+selected, func(t *testing.T) {
			ctx := context.Background()
			dir := t.TempDir()
			db, err := storage.Open(ctx, filepath.Join(dir, "test.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			// 此处只测试一次性业务选择迁移，密文字段不需要解密。
			if err := db.SaveP115Accounts(ctx, storage.P115AccountState{LegacyShareAccountID: "legacy", Accounts: []storage.P115AccountRow{{ID: "legacy", Name: "旧账号", Cookie: []byte("cipher"), Revision: 1}}}); err != nil {
				t.Fatal(err)
			}
			settings, err := NewAutomationStore(filepath.Join(dir, "automation.json"), dir, "http://nas:8115")
			if err != nil {
				t.Fatal(err)
			}
			value := settings.Value()
			value.AccountID = selected
			if err := settings.Save(value); err != nil {
				t.Fatal(err)
			}
			if err := MigrateP115ShareSelection(ctx, db, settings); err != nil {
				t.Fatal(err)
			}
			want := selected
			if want == "" {
				want = "legacy"
			}
			if settings.Value().AccountID != want {
				t.Fatal("升级改变了分享实际使用的账号")
			}
			reopened, err := NewAutomationStore(settings.path, dir, "")
			if err != nil || reopened.Value().AccountID != want {
				t.Fatal("分享选择未持久化")
			}
			// 之后用户清空配置或文件被重置，重启也不能再次采用旧全局账号。
			value.AccountID = ""
			if err := settings.Save(value); err != nil {
				t.Fatal(err)
			}
			if err := MigrateP115ShareSelection(ctx, db, settings); err != nil || settings.Value().AccountID != "" {
				t.Fatal("迁移不能充当运行时默认值")
			}
		})
	}
}

func TestShareSelectionMigrationFailureCanRetry(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := storage.Open(ctx, filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.SaveP115Accounts(ctx, storage.P115AccountState{LegacyShareAccountID: "legacy", Accounts: []storage.P115AccountRow{{ID: "legacy", Name: "账号", Cookie: []byte("cipher"), Revision: 1}}}); err != nil {
		t.Fatal(err)
	}
	settings, err := NewAutomationStore(filepath.Join(dir, "automation.json"), dir, "http://nas:8115")
	if err != nil {
		t.Fatal(err)
	}
	blocked := filepath.Join(dir, "blocked")
	if err := os.WriteFile(blocked, []byte("file"), 0600); err != nil {
		t.Fatal(err)
	}
	settings.path = filepath.Join(blocked, "config.json")
	if err := MigrateP115ShareSelection(ctx, db, settings); err == nil {
		t.Fatal("写入失败不能标记完成")
	}
	if id, err := db.PendingP115ShareAccount(ctx); err != nil || id != "legacy" || settings.Value().AccountID != "" {
		t.Fatal("迁移失败必须保留重试信息")
	}
	settings.path = filepath.Join(dir, "automation.json")
	if err := MigrateP115ShareSelection(ctx, db, settings); err != nil {
		t.Fatal(err)
	}
	if id, err := db.PendingP115ShareAccount(ctx); err != nil || id != "" {
		t.Fatal("成功后必须移除升级候选")
	}
}

func TestFreshInstallHasNoImplicitFeatureAccount(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := storage.Open(ctx, filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	accounts, err := OpenP115AccountsSQLite(ctx, db, dir, "secret")
	if err != nil {
		t.Fatal(err)
	}
	settings, err := NewAutomationStore(filepath.Join(dir, "automation.json"), dir, "http://nas:8115")
	if err != nil {
		t.Fatal(err)
	}
	if err := MigrateP115ShareSelection(ctx, db, settings); err != nil {
		t.Fatal(err)
	}
	if len(accounts.List()) != 0 || settings.Value().AccountID != "" {
		t.Fatal("新安装不能创建默认账号或选择")
	}
	settings.ValidateAccount = accounts.ValidateSelection
	if err := settings.Save(settings.Value()); err == nil {
		t.Fatal("未选择账号不能保存功能设置")
	}
}
