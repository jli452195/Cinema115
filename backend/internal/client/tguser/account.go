package tguser

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

var ErrBusy = errors.New("Telegram 正在处理另一项操作，请稍后再试")
var ErrConfig = errors.New("请先配置 Telegram API ID 和 API Hash")
var ErrBotConfig = errors.New("请先在 Telegram 搜索配置中填写资源 Bot")
var ErrLogin = errors.New("请先在 Telegram 用户账号页面登录并检查连接")
var ErrInput = errors.New("输入格式无效")

type Store interface {
	Value() string
	Save(string) error
}
type Config struct {
	APIID   int    `json:"apiId"`
	APIHash string `json:"apiHash"`
	Bot     string `json:"bot"`
}
type View struct {
	APIID         int    `json:"apiId"`
	Configured    bool   `json:"configured"`
	BotConfigured bool   `json:"botConfigured"`
	HasSession    bool   `json:"hasSession"`
	Authorized    bool   `json:"authorized"`
	Name          string `json:"name"`
	Username      string `json:"username"`
	Stage         string `json:"stage"`
	Bot           string `json:"bot"`
}
type Client struct {
	configStore, sessionStore       Store
	gate                            chan struct{}
	mu                              sync.Mutex
	config                          Config
	channels                        channelSettings
	channelCatalog                  *channelCatalog
	channelSearches                 map[string]*channelSearch
	channelRetryAt                  time.Time
	userID                          int64
	identityKnown                   bool
	authorized                      bool
	name, username, phone, codeHash string
	expires                         time.Time
	passwordNeeded                  bool
	results                         map[string]storedAction
	searchCache                     map[string]cachedSearch
	cooldown                        time.Time
	searches                        map[string]*searchJob
	queue                           []*searchJob
	workerRunning                   bool
	closed                          bool
	workerContext                   context.Context
	stopWorker                      context.CancelFunc
	workers                         sync.WaitGroup
	searchTiming                    searchTiming
	runOperation                    func(context.Context, func(context.Context, *telegram.Client) error) error
	// ObserveSearch receives summaries only; never credentials or raw Bot messages.
	ObserveSearch func(SearchDiagnostic)
}
type encryptedSession struct{ Store }

func (s encryptedSession) LoadSession(ctx context.Context) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	value := s.Value()
	if value == "" {
		return nil, session.ErrNotFound
	}
	return base64.StdEncoding.DecodeString(value)
}
func (s encryptedSession) StoreSession(ctx context.Context, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.Save(base64.StdEncoding.EncodeToString(data))
}
func New(configStore, sessionStore Store) (*Client, error) {
	c := &Client{configStore: configStore, sessionStore: sessionStore, gate: make(chan struct{}, 1), results: make(map[string]storedAction)}
	c.workerContext, c.stopWorker = context.WithCancel(context.Background())
	c.searches = make(map[string]*searchJob)
	c.searchTiming = defaultSearchTiming
	if value := configStore.Value(); value != "" {
		var saved accountSettings
		if err := json.Unmarshal([]byte(value), &saved); err != nil {
			return nil, errors.New("Telegram 用户配置无法读取")
		}
		c.config, c.channels = saved.Config, saved.Channels
	}
	return c, nil
}
func (c *Client) lock() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || len(c.searches) > 0 {
		return ErrBusy
	}
	select {
	case c.gate <- struct{}{}:
		return nil
	default:
		return ErrBusy
	}
}
func (c *Client) unlock() { <-c.gate }
func (c *Client) View() View {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.viewLocked()
}

func (c *Client) viewLocked() View {
	stage := "idle"
	if time.Now().Before(c.expires) {
		if c.passwordNeeded {
			stage = "password"
		} else if c.codeHash != "" {
			stage = "code"
		}
	}
	configured := c.config.APIID > 0 && c.config.APIHash != ""
	return View{APIID: c.config.APIID, Configured: configured, BotConfigured: configured && validBotUsername(c.config.Bot), HasSession: c.sessionStore.Value() != "", Authorized: c.authorized, Name: c.name, Username: c.username, Stage: stage, Bot: c.config.Bot}
}

var hashPattern = regexp.MustCompile(`^[a-fA-F0-9]{32}$`)
var phonePattern = regexp.MustCompile(`^\+[1-9][0-9]{6,14}$`)
var botPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{1,28}(?i:bot)$`)

func normalizeBotUsername(value string) string {
	return strings.TrimPrefix(strings.TrimSpace(value), "@")
}

func validBotUsername(value string) bool {
	return botPattern.MatchString(value)
}

func (c *Client) Save(input Config) error {
	if err := c.lock(); err != nil {
		return err
	}
	defer c.unlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	if input.APIHash == "" {
		input.APIHash = c.config.APIHash
	}
	input.Bot = normalizeBotUsername(input.Bot)
	if input.APIID < 1 || !hashPattern.MatchString(input.APIHash) || (input.Bot != "" && !validBotUsername(input.Bot)) {
		return ErrInput
	}
	if c.sessionStore.Value() != "" && (input.APIID != c.config.APIID || input.APIHash != c.config.APIHash) {
		return errors.New("更换 API 凭据前，请先退出当前 Telegram 用户账号")
	}
	if err := c.saveSettingsLocked(input, c.channels); err != nil {
		return err
	}
	c.clearSearchLocked()
	return nil
}

// 每项操作持有独占名额，Run 返回前关闭连接并回收内部协程；配置和登录会话分开加密保存。
func (c *Client) run(ctx context.Context, operation func(context.Context, *telegram.Client) error) error {
	if c.runOperation != nil {
		return c.runOperation(ctx, operation)
	}
	c.mu.Lock()
	cfg := c.config
	c.mu.Unlock()
	if cfg.APIID < 1 || cfg.APIHash == "" {
		return ErrConfig
	}
	client := telegram.NewClient(cfg.APIID, cfg.APIHash, telegram.Options{SessionStorage: encryptedSession{c.sessionStore}, NoUpdates: true})
	return client.Run(ctx, func(ctx context.Context) error { return operation(ctx, client) })
}
func (c *Client) status(ctx context.Context, client *telegram.Client) error {
	state, err := client.Auth().Status(ctx)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.authorized = state.Authorized
	c.identityKnown = true
	c.userID = 0
	if state.Authorized && state.User != nil {
		c.userID = state.User.ID
	}
	if c.channels.OwnerID != 0 && c.channels.OwnerID != c.userID {
		c.channelSearches = nil
		c.channelRetryAt = time.Time{}
		if err := c.saveSettingsLocked(c.config, channelSettings{}); err != nil {
			c.channelCatalog = nil
			return err
		}
	}
	if c.channelCatalog != nil && c.channelCatalog.ownerID != c.userID {
		c.channelCatalog = nil
	}
	if !state.Authorized {
		c.clearSearchLocked()
	}
	c.name = ""
	c.username = ""
	if state.User != nil {
		c.name = state.User.FirstName
		c.username = state.User.Username
	}
	if state.Authorized {
		c.codeHash = ""
		c.phone = ""
		c.passwordNeeded = false
		c.expires = time.Time{}
	}
	return nil
}
func (c *Client) Check(ctx context.Context) error {
	if err := c.lock(); err != nil {
		return err
	}
	defer c.unlock()
	return publicError(c.run(ctx, c.status))
}
func (c *Client) SendCode(ctx context.Context, phone string) error {
	if !phonePattern.MatchString(phone) {
		return ErrInput
	}
	if err := c.lock(); err != nil {
		return err
	}
	defer c.unlock()
	return publicError(c.run(ctx, func(ctx context.Context, client *telegram.Client) error {
		if err := c.status(ctx, client); err != nil {
			return err
		}
		if c.View().Authorized {
			return errors.New("当前已有登录账号，请先退出再更换账号")
		}
		sent, err := client.Auth().SendCode(ctx, phone, auth.SendCodeOptions{})
		if err != nil {
			return err
		}
		switch code := sent.(type) {
		case *tg.AuthSentCode:
			c.mu.Lock()
			c.phone = phone
			c.codeHash = code.PhoneCodeHash
			c.expires = time.Now().Add(10 * time.Minute)
			c.passwordNeeded = false
			c.mu.Unlock()
			return nil
		case *tg.AuthSentCodeSuccess:
			return c.status(ctx, client)
		default:
			return errors.New("Telegram 要求使用其他登录方式，请在官方客户端确认账号状态")
		}
	}))
}
func (c *Client) SignIn(ctx context.Context, code, password string) error {
	if err := c.lock(); err != nil {
		return err
	}
	defer c.unlock()
	c.mu.Lock()
	phone, hash, need, expires := c.phone, c.codeHash, c.passwordNeeded, c.expires
	c.mu.Unlock()
	if time.Now().After(expires) || phone == "" {
		return errors.New("登录流程已过期，请重新获取验证码")
	}
	return publicError(c.run(ctx, func(ctx context.Context, client *telegram.Client) error {
		var err error
		if need {
			if password == "" {
				return ErrInput
			}
			_, err = client.Auth().Password(ctx, password)
		} else {
			if code == "" {
				return ErrInput
			}
			_, err = client.Auth().SignIn(ctx, phone, code, hash)
		}
		if errors.Is(err, auth.ErrPasswordAuthNeeded) {
			c.mu.Lock()
			c.passwordNeeded = true
			c.mu.Unlock()
			return nil
		}
		if err != nil {
			return err
		}
		return c.status(ctx, client)
	}))
}
func (c *Client) Disconnect(ctx context.Context) error {
	if err := c.lock(); err != nil {
		return err
	}
	defer c.unlock()
	if c.sessionStore.Value() != "" {
		if err := c.run(ctx, func(ctx context.Context, client *telegram.Client) error {
			_, err := client.API().AuthLogOut(ctx)
			if auth.IsUnauthorized(err) {
				return nil
			}
			return err
		}); err != nil {
			return publicError(err)
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.authorized = false
	c.userID = 0
	c.identityKnown = true
	c.channelCatalog = nil
	c.channelSearches = nil
	c.channelRetryAt = time.Time{}
	c.name = ""
	c.username = ""
	c.phone = ""
	c.codeHash = ""
	c.passwordNeeded = false
	c.expires = time.Time{}
	c.clearSearchLocked()
	sessionErr := c.sessionStore.Save("")
	configErr := c.saveSettingsLocked(c.config, channelSettings{})
	if sessionErr != nil {
		return errors.New("清除 Telegram 登录会话失败")
	}
	return configErr
}
func publicError(err error) error {
	if err == nil {
		return nil
	}
	var searchErr *OperationError
	if errors.As(err, &searchErr) {
		return err
	}
	if errors.Is(err, ErrConfig) || errors.Is(err, ErrBotConfig) || errors.Is(err, ErrLogin) || errors.Is(err, ErrInput) || errors.Is(err, ErrBusy) {
		return err
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return errors.New("Telegram 请求超时，请检查网络后重试；搜索请勿立即重复发送")
	}
	for code, message := range map[string]string{"PHONE_CODE_INVALID": "验证码不正确", "PHONE_CODE_EXPIRED": "验证码已过期，请重新发送", "PHONE_NUMBER_INVALID": "手机号无效，请使用带国家区号的号码", "API_ID_INVALID": "API ID 或 API Hash 无效", "PHONE_NUMBER_BANNED": "该 Telegram 号码当前不可用", "SESSION_REVOKED": "Telegram 会话已撤销，请重新登录", "AUTH_KEY_UNREGISTERED": "Telegram 会话失效，请重新登录", "FLOOD_WAIT": "Telegram 请求频率受限，请稍后再试"} {
		if tgerr.Is(err, code) {
			return errors.New(message)
		}
	}
	if errors.Is(err, auth.ErrPasswordInvalid) {
		return errors.New("二步验证密码不正确")
	}
	// 不回传底层错误，避免认证参数、会话和网络地址进入日志或响应。
	return errors.New("Telegram 操作未完成，请检查账号状态与网络后重试")
}
