package bot

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"

	"strm115/backend/internal/service"
	"strm115/backend/internal/storage"
)

var ErrBotMissing = errors.New("Bot 配置不存在，请刷新页面")
var ErrBotChanged = errors.New("Bot 配置已发生变化，请刷新后重试")

type BotProfile struct {
	ID                 string   `json:"id"`
	Name               string   `json:"name"`
	Token              string   `json:"token"`
	AllowedChatIDs     string   `json:"allowedChatIDs"`
	NotificationChatID string   `json:"notificationChatID"`
	Enabled            bool     `json:"enabled"`
	Identity           Identity `json:"identity"`
	Revision           uint64   `json:"revision"`
}

type BotProfileView struct {
	ID                 string   `json:"id"`
	Name               string   `json:"name"`
	Configured         bool     `json:"configured"`
	Enabled            bool     `json:"enabled"`
	AllowedChatIDs     string   `json:"allowedChatIDs"`
	NotificationChatID string   `json:"notificationChatID"`
	Identity           Identity `json:"identity"`
	Revision           uint64   `json:"revision"`
	Running            bool     `json:"running"`
	Connected          bool     `json:"connected"`
	LastError          string   `json:"lastError"`
}

type botProfileDocument struct {
	Version         int          `json:"version"`
	LegacyBindingID string       `json:"legacyBindingId,omitempty"`
	Profiles        []BotProfile `json:"profiles"`
}

type BotProfiles struct {
	mu    sync.RWMutex
	store *service.SessionStore
	value botProfileDocument
}

func OpenBotProfiles(dataDir, secret string) (*BotProfiles, error) {
	store, err := service.NewSessionStore(filepath.Join(dataDir, "telegram-bots.enc"), secret+"\x00telegram-bots")
	if err != nil {
		return nil, fmt.Errorf("读取 Telegram Bot 配置库: %w", err)
	}
	p := &BotProfiles{store: store, value: botProfileDocument{Version: 1, Profiles: []BotProfile{}}}
	if store.Value() != "" {
		if err := json.Unmarshal([]byte(store.Value()), &p.value); err != nil || p.value.Version != 1 {
			return nil, errors.New("Telegram Bot 配置库损坏")
		}
		if err := validateBotProfileDocument(p.value); err != nil {
			return nil, err
		}
		return p, nil
	}
	legacy, err := service.NewSessionStore(filepath.Join(dataDir, "telegram-config.enc"), secret+"\x00telegram-config")
	if err != nil {
		return nil, fmt.Errorf("读取旧 Telegram Bot 配置: %w", err)
	}
	if legacy.Value() != "" {
		var cfg BotConfig
		if err := json.Unmarshal([]byte(legacy.Value()), &cfg); err != nil {
			return nil, errors.New("旧 Telegram Bot 配置损坏")
		}
		if cfg.Token != "" {
			p.value.LegacyBindingID = "legacy"
			p.value.Profiles = append(p.value.Profiles, BotProfile{ID: "legacy", Name: "Bot 1", Token: cfg.Token, AllowedChatIDs: cfg.AllowedChatIDs, NotificationChatID: cfg.NotificationChatID, Enabled: cfg.Enabled, Identity: cfg.Identity, Revision: 1})
		}
	}
	if err := p.commit(p.value); err != nil {
		return nil, err
	}
	return p, nil
}

func validateBotProfileDocument(value botProfileDocument) error {
	if len(value.Profiles) > 20 {
		return errors.New("Telegram Bot 配置数量无效")
	}
	seen := make(map[string]bool, len(value.Profiles))
	for _, profile := range value.Profiles {
		if profile.ID == "" || seen[profile.ID] || profile.Revision == 0 || !validBotName(profile.Name) {
			return errors.New("Telegram Bot 配置库损坏")
		}
		seen[profile.ID] = true
	}
	if value.LegacyBindingID != "" && !seen[value.LegacyBindingID] {
		return errors.New("Telegram Bot 旧配置迁移目标缺失")
	}
	return nil
}

func validBotName(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && utf8.RuneCountInString(value) <= 50 && !strings.ContainsAny(value, "\r\n\x00")
}

func (p *BotProfiles) commit(value botProfileDocument) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("编码 Telegram Bot 配置库: %w", err)
	}
	if err := p.store.Save(string(raw)); err != nil {
		return fmt.Errorf("保存 Telegram Bot 配置库: %w", err)
	}
	p.value = value
	return nil
}

func (p *BotProfiles) copy() botProfileDocument {
	value := p.value
	value.Profiles = append([]BotProfile{}, value.Profiles...)
	return value
}

func (p *BotProfiles) List() []BotProfile {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return append([]BotProfile{}, p.value.Profiles...)
}

func (p *BotProfiles) Get(id string) (BotProfile, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	for _, profile := range p.value.Profiles {
		if profile.ID == id {
			return profile, nil
		}
	}
	return BotProfile{}, ErrBotMissing
}

func (p *BotProfiles) Add(name string) (string, error) {
	name = strings.TrimSpace(name)
	if !validBotName(name) {
		return "", fmt.Errorf("%w：名称应为 1～50 字", ErrConfig)
	}
	var nonce [12]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", fmt.Errorf("生成 Bot 配置编号: %w", err)
	}
	id := hex.EncodeToString(nonce[:])
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.value.Profiles) >= 20 {
		return "", fmt.Errorf("%w：最多保存 20 个 Bot 配置", ErrConfig)
	}
	value := p.copy()
	value.Profiles = append(value.Profiles, BotProfile{ID: id, Name: name, Revision: 1})
	if err := p.commit(value); err != nil {
		return "", err
	}
	return id, nil
}

func (p *BotProfiles) Update(profile BotProfile, revision uint64) error {
	profile.Name = strings.TrimSpace(profile.Name)
	if !validBotName(profile.Name) {
		return fmt.Errorf("%w：名称应为 1～50 字", ErrConfig)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	value := p.copy()
	for i, current := range value.Profiles {
		if current.ID != profile.ID {
			continue
		}
		if current.Revision != revision {
			return ErrBotChanged
		}
		profile.Revision = current.Revision + 1
		value.Profiles[i] = profile
		return p.commit(value)
	}
	return ErrBotMissing
}

func (p *BotProfiles) Delete(id string, revision uint64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	value := p.copy()
	for i, current := range value.Profiles {
		if current.ID != id {
			continue
		}
		if current.Revision != revision {
			return ErrBotChanged
		}
		value.Profiles = append(value.Profiles[:i], value.Profiles[i+1:]...)
		if value.LegacyBindingID == id {
			value.LegacyBindingID = ""
		}
		return p.commit(value)
	}
	return ErrBotMissing
}

func (p *BotProfiles) PendingLegacyBinding() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.value.LegacyBindingID
}

func (p *BotProfiles) FinishLegacyBinding() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.value.LegacyBindingID == "" {
		return nil
	}
	value := p.copy()
	value.LegacyBindingID = ""
	return p.commit(value)
}

func MigrateLegacyBindings(ctx context.Context, profiles *BotProfiles, automation *service.AutomationStore, db *storage.Store) error {
	id := profiles.PendingLegacyBinding()
	if id == "" {
		return nil
	}
	profile, err := profiles.Get(id)
	if err != nil {
		return err
	}
	if err := automation.MigrateTelegramBotSelection(id, profile.Enabled); err != nil {
		return fmt.Errorf("迁移分享 Telegram Bot 绑定: %w", err)
	}
	if err := db.MigrateFollowTelegramBot(ctx, id, profile.Enabled); err != nil {
		return err
	}
	return profiles.FinishLegacyBinding()
}
