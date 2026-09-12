package bot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"unicode"

	"strm115/backend/internal/service"
)

var ErrConfig = errors.New("Telegram 配置无效")

type Identity struct {
	ID       json.Number `json:"id"`
	Name     string      `json:"first_name"`
	Username string      `json:"username"`
	IsBot    bool        `json:"is_bot"`
	Webhook  bool        `json:"webhookConfigured"`
}

type BotConfig struct {
	Token          string   `json:"token"`
	AllowedChatIDs string   `json:"allowedChatIDs"`
	Enabled        bool     `json:"enabled"`
	Identity       Identity `json:"identity"`
}

type ConfigView struct {
	Configured     bool     `json:"configured"`
	Enabled        bool     `json:"enabled"`
	AllowedChatIDs string   `json:"allowedChatIDs"`
	Identity       Identity `json:"identity"`
	Source         string   `json:"source"`
	Running        bool     `json:"running"`
	Connected      bool     `json:"connected"`
	LastError      string   `json:"lastError"`
}

// Manager 持有加密配置，串行更换接收器；令牌不进入返回给页面的配置。
type Manager struct {
	mu                 sync.RWMutex
	saveMu             sync.Mutex
	config             BotConfig
	source             string
	running, connected bool
	lastError          string
	version            uint64
	store              *service.SessionStore
	wake               chan struct{}
	newWorker          func(BotConfig) (*Telegram, error)
}

func NewManager(dataDir, secret string, generator ShareGenerator, settings *service.AutomationStore) (*Manager, error) {
	store, err := service.NewSessionStore(filepath.Join(dataDir, "telegram-config.enc"), secret+"\x00telegram-config")
	if err != nil {
		return nil, fmt.Errorf("读取 Telegram 配置: %w", err)
	}
	m := &Manager{store: store, wake: make(chan struct{}, 1), source: "none"}
	m.newWorker = func(cfg BotConfig) (*Telegram, error) {
		return NewTelegram(cfg.Token, cfg.AllowedChatIDs, dataDir, generator, settings)
	}
	if saved := store.Value(); saved != "" {
		if err := json.Unmarshal([]byte(saved), &m.config); err != nil {
			return nil, fmt.Errorf("解析 Telegram 配置: %w", err)
		}
		m.source = "web"
	}
	return m, nil
}

func normalizeChats(raw string) string {
	return strings.Join(strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == '，' || r == ';' || unicode.IsSpace(r) }), ",")
}

func (m *Manager) View() ConfigView {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return ConfigView{Configured: m.config.Token != "", Enabled: m.config.Enabled, AllowedChatIDs: m.config.AllowedChatIDs, Identity: m.config.Identity, Source: m.source, Running: m.running, Connected: m.connected, LastError: m.lastError}
}

// Test 只读取 Bot 身份和 Webhook 状态，不拉取消息、不向聊天发送消息。
func (m *Manager) Test(ctx context.Context, token string) (Identity, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		m.mu.RLock()
		token = m.config.Token
		m.mu.RUnlock()
	}
	if token == "" {
		return Identity{}, fmt.Errorf("%w：请填写 Bot Token", ErrConfig)
	}
	worker, err := m.newWorker(BotConfig{Token: token, AllowedChatIDs: "1"})
	if err != nil {
		return Identity{}, fmt.Errorf("%w：Token 格式不正确", ErrConfig)
	}
	var identity Identity
	if err := worker.call(ctx, "getMe", struct{}{}, &identity); err != nil {
		return Identity{}, err
	}
	if !identity.IsBot || identity.ID == "" {
		return Identity{}, fmt.Errorf("Telegram 未返回有效的 Bot 身份")
	}
	var webhook struct {
		URL string `json:"url"`
	}
	if err := worker.call(ctx, "getWebhookInfo", struct{}{}, &webhook); err != nil {
		return Identity{}, err
	}
	identity.Webhook = webhook.URL != ""
	return identity, nil
}

func (m *Manager) Save(ctx context.Context, value BotConfig) error {
	m.saveMu.Lock()
	defer m.saveMu.Unlock()
	m.mu.RLock()
	old := m.config
	m.mu.RUnlock()
	value.Token = strings.TrimSpace(value.Token)
	if value.Token == "" {
		value.Token = old.Token
	}
	value.AllowedChatIDs = normalizeChats(value.AllowedChatIDs)
	if value.Enabled && value.Token == "" {
		return fmt.Errorf("%w：启用前请填写 Bot Token", ErrConfig)
	}
	if value.Enabled || value.AllowedChatIDs != "" {
		if value.Token == "" {
			return fmt.Errorf("%w：请先填写 Bot Token", ErrConfig)
		}
		if _, err := m.newWorker(value); err != nil {
			return fmt.Errorf("%w：Token 格式或聊天 ID 不正确，请填写数字聊天 ID", ErrConfig)
		}
	}
	value.Identity = old.Identity
	if value.Token != "" && (value.Enabled || value.Token != old.Token) {
		identity, err := m.Test(ctx, value.Token)
		if err != nil {
			return err
		}
		if value.Enabled && identity.Webhook {
			return fmt.Errorf("%w：此 Bot 已配置 Webhook，请先在原服务中停用 Webhook，再启用消息接收", ErrConfig)
		}
		value.Identity = identity
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("编码 Bot 配置: %w", err)
	}
	if err := m.store.Save(string(data)); err != nil {
		return fmt.Errorf("保存 Bot 配置: %w", err)
	}
	m.mu.Lock()
	m.config = value
	m.source = "web"
	m.version++
	m.connected = false
	m.lastError = ""
	m.mu.Unlock()
	select {
	case m.wake <- struct{}{}:
	default:
	}
	return nil
}

// Run 管理唯一接收器的完整生命周期，更换配置时先等待旧接收器退出。
func (m *Manager) Run(ctx context.Context) error {
	var cancel context.CancelFunc
	var done chan error
	stop := func() {
		if cancel != nil {
			cancel()
			<-done
			cancel = nil
			done = nil
		}
		m.mu.Lock()
		m.running = false
		m.connected = false
		m.mu.Unlock()
	}
	defer stop()
	apply := func() {
		stop()
		m.mu.RLock()
		cfg, version := m.config, m.version
		m.mu.RUnlock()
		if !cfg.Enabled {
			return
		}
		worker, err := m.newWorker(cfg)
		if err != nil || worker == nil {
			m.mu.Lock()
			m.lastError = "Bot 配置不完整，请检查 Token 和允许的聊天 ID"
			m.mu.Unlock()
			return
		}
		worker.OnStatus = func(connected bool, message string) {
			m.mu.Lock()
			defer m.mu.Unlock()
			if m.version == version {
				m.connected = connected
				m.lastError = message
			}
		}
		workerCtx, workerCancel := context.WithCancel(ctx)
		cancel = workerCancel
		done = make(chan error, 1)
		m.mu.Lock()
		m.running = true
		m.mu.Unlock()
		workerDone := done
		go func() { workerDone <- worker.Run(workerCtx) }()
	}
	apply()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-m.wake:
			apply()
		case err := <-done:
			cancel()
			cancel = nil
			done = nil
			m.mu.Lock()
			m.running = false
			m.connected = false
			if err != nil {
				m.lastError = "接收器已停止，请检查数据目录权限和处理进度文件"
			}
			m.mu.Unlock()
		}
	}
}
