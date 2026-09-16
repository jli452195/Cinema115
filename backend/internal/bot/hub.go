package bot

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"strm115/backend/internal/model"
	"strm115/backend/internal/service"
)

// Hub 管理 Bot 配置库及运行实例。每个 Bot 最多启动一个 getUpdates 消费者。
type Hub struct {
	mu            sync.RWMutex
	profiles      *BotProfiles
	dataDir       string
	generator     ShareGenerator
	settings      *service.AutomationStore
	runtimes      map[string]*Manager
	wake          chan struct{}
	Logs          *service.GenerationLogs
	Notifications NotificationStore
}

func NewHub(dataDir, secret string, generator ShareGenerator, settings *service.AutomationStore) (*Hub, error) {
	profiles, err := OpenBotProfiles(dataDir, secret)
	if err != nil {
		return nil, err
	}
	return &Hub{profiles: profiles, dataDir: dataDir, generator: generator, settings: settings, runtimes: make(map[string]*Manager), wake: make(chan struct{}, 1)}, nil
}

func (h *Hub) Profiles() *BotProfiles { return h.profiles }

func (h *Hub) Refresh() {
	select {
	case h.wake <- struct{}{}:
	default:
	}
}

func (h *Hub) newManager(profile BotProfile, receive bool) *Manager {
	cfg := BotConfig{Token: profile.Token, AllowedChatIDs: profile.AllowedChatIDs, NotificationChatID: profile.NotificationChatID, Enabled: receive, Identity: profile.Identity}
	m := &Manager{config: cfg, source: "web", wake: make(chan struct{}, 1)}
	m.newWorker = func(value BotConfig) (*Telegram, error) {
		return NewTelegram(value.Token, value.AllowedChatIDs, h.dataDir, h.generator, h.settings)
	}
	m.Logs, m.Notifications = h.Logs, h.Notifications
	return m
}

func (h *Hub) tester() *Manager {
	m := &Manager{wake: make(chan struct{}, 1)}
	m.newWorker = func(value BotConfig) (*Telegram, error) {
		return NewTelegram(value.Token, value.AllowedChatIDs, h.dataDir, h.generator, h.settings)
	}
	return m
}

func (h *Hub) Test(ctx context.Context, token string) (Identity, error) {
	return h.tester().Test(ctx, token)
}

func (h *Hub) List() []BotProfileView {
	profiles := h.profiles.List()
	h.mu.RLock()
	defer h.mu.RUnlock()
	result := make([]BotProfileView, 0, len(profiles))
	for _, profile := range profiles {
		view := BotProfileView{ID: profile.ID, Name: profile.Name, Configured: profile.Token != "", Enabled: profile.Enabled, AllowedChatIDs: profile.AllowedChatIDs, NotificationChatID: profile.NotificationChatID, Identity: profile.Identity, Revision: profile.Revision}
		if runtime := h.runtimes[profile.ID]; runtime != nil {
			state := runtime.View()
			view.Running, view.Connected, view.LastError = state.Running, state.Connected, state.LastError
		}
		result = append(result, view)
	}
	return result
}

func normalizeBotProfile(profile BotProfile) (BotProfile, error) {
	profile.Name = strings.TrimSpace(profile.Name)
	profile.Token = strings.TrimSpace(profile.Token)
	profile.AllowedChatIDs = normalizeChats(profile.AllowedChatIDs)
	profile.NotificationChatID = strings.TrimSpace(profile.NotificationChatID)
	if !validBotName(profile.Name) {
		return profile, fmt.Errorf("%w：名称应为 1～50 字", ErrConfig)
	}
	if profile.NotificationChatID != "" {
		id, err := strconv.ParseInt(profile.NotificationChatID, 10, 64)
		if err != nil || id == 0 {
			return profile, fmt.Errorf("%w：通知聊天 ID 必须是单个非零数字 ID", ErrConfig)
		}
		profile.NotificationChatID = strconv.FormatInt(id, 10)
	}
	if profile.Enabled && profile.Token == "" {
		return profile, fmt.Errorf("%w：启用前请填写 Bot Token", ErrConfig)
	}
	if profile.Enabled && profile.AllowedChatIDs == "" {
		return profile, fmt.Errorf("%w：启用前请填写允许的聊天 ID", ErrConfig)
	}
	if profile.Token != "" && (profile.Enabled || profile.AllowedChatIDs != "") {
		if _, err := NewTelegram(profile.Token, profile.AllowedChatIDs, "", nil, nil); err != nil {
			return profile, fmt.Errorf("%w：Token 格式或聊天 ID 不正确", ErrConfig)
		}
	}
	return profile, nil
}

func (h *Hub) Update(ctx context.Context, input BotProfile, revision uint64) error {
	current, err := h.profiles.Get(input.ID)
	if err != nil {
		return err
	}
	if input.Token == "" {
		input.Token = current.Token
	}
	input, err = normalizeBotProfile(input)
	if err != nil {
		return err
	}
	input.Identity = current.Identity
	for _, other := range h.profiles.List() {
		if other.ID != input.ID && input.Token != "" && other.Token == input.Token {
			return fmt.Errorf("%w：这个 Bot 已存在于配置库中", ErrConfig)
		}
	}
	if input.Token != "" && (input.Enabled || input.Token != current.Token) {
		identity, err := h.Test(ctx, input.Token)
		if err != nil {
			return err
		}
		if input.Enabled && identity.Webhook {
			return fmt.Errorf("%w：此 Bot 已配置 Webhook，请先在原服务中停用 Webhook，再启用", ErrConfig)
		}
		for _, other := range h.profiles.List() {
			if other.ID != input.ID && other.Identity.ID != "" && other.Identity.ID == identity.ID {
				return fmt.Errorf("%w：这个 Bot 已存在于配置库中", ErrConfig)
			}
		}
		input.Identity = identity
	}
	if err := h.profiles.Update(input, revision); err != nil {
		return err
	}
	h.Refresh()
	return nil
}

func (h *Hub) ValidateSelection(id string, requireNotification bool) error {
	if id == "" {
		return fmt.Errorf("%w：请先选择此功能使用的 Telegram Bot 配置", ErrConfig)
	}
	profile, err := h.profiles.Get(id)
	if err != nil {
		return fmt.Errorf("%w：所选 Bot 配置已删除，请重新选择", ErrConfig)
	}
	if profile.Token == "" {
		return fmt.Errorf("%w：所选 Bot 尚未配置 Token", ErrConfig)
	}
	if !profile.Enabled {
		return fmt.Errorf("%w：所选 Bot 已停用，请先启用", ErrConfig)
	}
	if requireNotification && profile.NotificationChatID == "" {
		return fmt.Errorf("%w：所选 Bot 尚未配置独立通知聊天 ID", ErrConfig)
	}
	return nil
}

func (h *Hub) runSnapshot(ctx context.Context) error {
	workerCtx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	defer func() {
		cancel()
		wg.Wait()
	}()
	profiles := h.profiles.List()
	receiverID := ""
	if h.settings != nil {
		settings := h.settings.Value()
		if settings.TelegramEnabled {
			receiverID = settings.BotID
		}
	}
	runtimes := make(map[string]*Manager, len(profiles))
	for _, profile := range profiles {
		if profile.Enabled && profile.Token != "" {
			runtimes[profile.ID] = h.newManager(profile, profile.ID == receiverID)
		}
	}
	h.mu.Lock()
	h.runtimes = runtimes
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		h.runtimes = make(map[string]*Manager)
		h.mu.Unlock()
	}()
	errCh := make(chan error, max(1, len(runtimes)))
	for id, runtime := range runtimes {
		wg.Add(1)
		go func(m *Manager) { defer wg.Done(); m.RunNotifications(workerCtx) }(runtime)
		if id == receiverID {
			wg.Add(1)
			go func(m *Manager) {
				defer wg.Done()
				if err := m.Run(workerCtx); err != nil && workerCtx.Err() == nil {
					select {
					case errCh <- err:
					default:
					}
				}
			}(runtime)
		}
	}
	select {
	case <-ctx.Done():
		return nil
	case err := <-errCh:
		return err
	}
}

func (h *Hub) Run(ctx context.Context) error {
	for ctx.Err() == nil {
		runCtx, cancel := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() { done <- h.runSnapshot(runCtx) }()
		select {
		case <-ctx.Done():
			cancel()
			<-done
			return nil
		case <-h.wake:
			cancel()
			<-done
		case err := <-done:
			cancel()
			if err != nil && h.Logs != nil {
				h.Logs.Append("Telegram", "", "Bot", "ERROR", "Bot 运行失败，稍后重试："+err.Error())
			}
			timer := time.NewTimer(time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil
			case <-h.wake:
				if !timer.Stop() {
					<-timer.C
				}
			case <-timer.C:
			}
		}
	}
	return nil
}

func (h *Hub) NotifyShareSuccess(ctx context.Context, botConfigID, taskID string, input service.GenerateInput, result *service.GenerationResult, generationErr error) error {
	if botConfigID == "" {
		return nil
	}
	if err := h.ValidateSelection(botConfigID, true); err != nil {
		return err
	}
	profile, _ := h.profiles.Get(botConfigID)
	m := h.newManager(profile, false)
	return m.NotifyShareSuccess(ctx, taskID, input, result, generationErr)
}

func (h *Hub) NotifyShareBatchSuccess(ctx context.Context, botConfigID, taskID string, inputs []service.GenerateInput, outcomes []service.BatchOutcome) error {
	if botConfigID == "" {
		return nil
	}
	if err := h.ValidateSelection(botConfigID, true); err != nil {
		return err
	}
	profile, _ := h.profiles.Get(botConfigID)
	return h.newManager(profile, false).NotifyShareBatchSuccess(ctx, taskID, inputs, outcomes)
}

func (h *Hub) PrepareFollowNotification(f model.Follow, baseURL, botConfigID string) (*model.TelegramNotification, error) {
	if botConfigID == "" {
		return nil, nil
	}
	if err := h.ValidateSelection(botConfigID, true); err != nil {
		return nil, err
	}
	profile, _ := h.profiles.Get(botConfigID)
	return h.newManager(profile, false).PrepareFollowNotification(f, baseURL)
}

func (h *Hub) FollowNotificationsConfigured(botConfigID string) bool {
	return h.ValidateSelection(botConfigID, true) == nil
}

func (h *Hub) Add(name string) (string, error) {
	id, err := h.profiles.Add(name)
	if err == nil {
		h.Refresh()
	}
	return id, err
}

func (h *Hub) Delete(id string, revision uint64) error {
	err := h.profiles.Delete(id, revision)
	if err == nil {
		h.Refresh()
	}
	return err
}

func (h *Hub) ClearToken(id string, revision uint64) error {
	profile, err := h.profiles.Get(id)
	if err != nil {
		return err
	}
	profile.Token = ""
	profile.Enabled = false
	profile.Identity = Identity{}
	if err := h.profiles.Update(profile, revision); err != nil {
		return err
	}
	h.Refresh()
	return nil
}

var _ service.ShareSuccessNotifier = (*Hub)(nil)
var _ service.ShareBatchSuccessNotifier = (*Hub)(nil)

func isBotConfigError(err error) bool {
	return errors.Is(err, ErrConfig) || errors.Is(err, ErrBotMissing) || errors.Is(err, ErrBotChanged)
}
