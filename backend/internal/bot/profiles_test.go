package bot

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"strm115/backend/internal/service"
	"strm115/backend/internal/storage"
)

func TestBotProfilesMigrateLegacyOnceAndKeepSecretsEncrypted(t *testing.T) {
	dir, secret := t.TempDir(), "test-secret"
	legacy, err := service.NewSessionStore(filepath.Join(dir, "telegram-config.enc"), secret+"\x00telegram-config")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(BotConfig{Token: "123:private-token", AllowedChatIDs: "42", NotificationChatID: "42", Enabled: true, Identity: Identity{ID: "123", Username: "legacy_bot", IsBot: true}})
	if err := legacy.Save(string(raw)); err != nil {
		t.Fatal(err)
	}
	profiles, err := OpenBotProfiles(dir, secret)
	if err != nil {
		t.Fatal(err)
	}
	list := profiles.List()
	if len(list) != 1 || list[0].ID != "legacy" || list[0].Token != "123:private-token" || profiles.PendingLegacyBinding() != "legacy" {
		t.Fatalf("旧 Bot 没有完整迁移：%+v", list)
	}
	encrypted, err := os.ReadFile(filepath.Join(dir, "telegram-bots.enc"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encrypted), "private-token") {
		t.Fatal("Bot Token 明文落盘")
	}
	db, err := storage.Open(context.Background(), filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	automation, err := service.NewAutomationStore(filepath.Join(dir, "automation.json"), dir, "http://example.com")
	if err != nil {
		t.Fatal(err)
	}
	initial := automation.Value()
	initial.Generation.OutputDir = "strm"
	if err := automation.Save(initial); err != nil {
		t.Fatal(err)
	}
	if err := MigrateLegacyBindings(context.Background(), profiles, automation, db); err != nil {
		t.Fatal(err)
	}
	if got := automation.Value(); got.BotID != "legacy" || !got.TelegramEnabled {
		t.Fatalf("分享功能没有固化旧 Bot：%+v", got)
	}
	follow, err := db.FollowSettings(context.Background())
	if err != nil || follow.BotID != "legacy" || !follow.TelegramEnabled {
		t.Fatalf("追更功能没有固化旧 Bot：%+v %v", follow, err)
	}
	if profiles.PendingLegacyBinding() != "" {
		t.Fatal("一次性迁移标记未清除")
	}
	settings := automation.Value()
	settings.BotID, settings.TelegramEnabled = "", false
	if err := automation.Save(settings); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenBotProfiles(dir, secret)
	if err != nil {
		t.Fatal(err)
	}
	if err := MigrateLegacyBindings(context.Background(), reopened, automation, db); err != nil {
		t.Fatal(err)
	}
	if automation.Value().BotID != "" {
		t.Fatal("重启后再次使用旧 Bot 回填功能配置")
	}
}

func TestBotProfileSelectionNeverFallsBack(t *testing.T) {
	hub, err := NewHub(t.TempDir(), "secret", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	id, err := hub.Add("通知 Bot")
	if err != nil {
		t.Fatal(err)
	}
	profile, _ := hub.Profiles().Get(id)
	profile.Token, profile.AllowedChatIDs, profile.NotificationChatID, profile.Enabled = "123:token", "42", "42", true
	if err := hub.Profiles().Update(profile, profile.Revision); err != nil {
		t.Fatal(err)
	}
	if err := hub.ValidateSelection(id, true); err != nil {
		t.Fatal(err)
	}
	current, _ := hub.Profiles().Get(id)
	if err := hub.Delete(id, current.Revision); err != nil {
		t.Fatal(err)
	}
	if err := hub.ValidateSelection(id, true); err == nil {
		t.Fatal("删除 Bot 后错误回退到其他配置")
	}
}

func TestHubRejectsDuplicateBotTokenWithoutIdentity(t *testing.T) {
	hub, err := NewHub(t.TempDir(), "secret", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	firstID, err := hub.Add("第一个 Bot")
	if err != nil {
		t.Fatal(err)
	}
	first, _ := hub.Profiles().Get(firstID)
	first.Token = "123:shared-token"
	if err := hub.Profiles().Update(first, first.Revision); err != nil {
		t.Fatal(err)
	}
	secondID, err := hub.Add("第二个 Bot")
	if err != nil {
		t.Fatal(err)
	}
	second, _ := hub.Profiles().Get(secondID)
	second.Token = "123:shared-token"
	if err := hub.Update(context.Background(), second, second.Revision); !errors.Is(err, ErrConfig) {
		t.Fatal("重复 Token 未被拒绝", err)
	}
}
