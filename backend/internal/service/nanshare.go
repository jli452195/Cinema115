package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"strm115/backend/internal/client/nanshare"
)

type NanShareConfigInput struct {
	BaseURL  string `json:"baseUrl"`
	Username string `json:"username"`
	Password string `json:"password"`
}

type NanShareCheck struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

type NanShareView struct {
	AutoLogin  bool          `json:"autoLogin"`
	BaseURL    string        `json:"baseUrl"`
	Username   string        `json:"username"`
	Configured bool          `json:"configured"`
	CheckedAt  string        `json:"checkedAt"`
	Session    NanShareCheck `json:"session"`
	RE0        NanShareCheck `json:"re0"`
	Share      NanShareCheck `json:"share"`
}

type nanShareDocument struct {
	ConfigID string `json:"configId,omitempty"`
	Password string `json:"password,omitempty"`
	Version  int    `json:"version"`
	BaseURL  string `json:"baseUrl"`
	Username string `json:"username"`
	Token    string `json:"token"`
}

type NanShareService struct {
	mu       sync.RWMutex
	opMu     sync.Mutex
	authMu   sync.Mutex
	store    *SessionStore
	doc      nanShareDocument
	view     NanShareView
	searchMu sync.Mutex
	searches map[string]*nanShareSearch
}

func NewNanShareService(store *SessionStore) (*NanShareService, error) {
	s := &NanShareService{store: store, doc: nanShareDocument{Version: 1}}
	if store.Value() == "" {
		return s, nil
	}
	if json.Unmarshal([]byte(store.Value()), &s.doc) != nil || s.doc.Version != 1 || s.doc.Token == "" || s.doc.Username == "" {
		return nil, fmt.Errorf("NanShare 配置无法解析")
	}
	if _, err := nanshare.New(s.doc.BaseURL, s.doc.Token); err != nil {
		return nil, fmt.Errorf("恢复 NanShare 配置: %w", err)
	}
	s.view = NanShareView{BaseURL: s.doc.BaseURL, Username: s.doc.Username, Configured: true, AutoLogin: s.doc.Password != ""}
	return s, nil
}

func (s *NanShareService) View() NanShareView {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.view
}

func (s *NanShareService) Configure(ctx context.Context, input NanShareConfigInput) (NanShareView, error) {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	s.authMu.Lock()
	defer s.authMu.Unlock()
	base, err := nanshare.NormalizeBaseURL(input.BaseURL)
	if err != nil {
		return NanShareView{}, err
	}
	input.Username = strings.TrimSpace(input.Username)
	if input.Username == "" || input.Password == "" || len(input.Username) > 100 || len(input.Password) > 4096 || strings.ContainsAny(input.Username, "\r\n\x00") {
		return NanShareView{}, nanshare.ErrInput
	}
	client, err := nanshare.New(base, "")
	if err != nil {
		return NanShareView{}, err
	}
	token, _, err := client.Login(ctx, input.Username, input.Password)
	if err != nil {
		return NanShareView{}, err
	}
	client, err = nanshare.New(base, token)
	if err != nil {
		return NanShareView{}, err
	}
	username, err := client.Session(ctx)
	if err != nil {
		return NanShareView{}, err
	}
	if input.Username != "" && username != input.Username {
		return NanShareView{}, fmt.Errorf("%w：会话账号与填写的用户名不一致", nanshare.ErrInput)
	}
	var identity [16]byte
	if _, err := rand.Read(identity[:]); err != nil {
		return NanShareView{}, fmt.Errorf("创建 NanShare 配置编号: %w", err)
	}
	doc := nanShareDocument{Version: 1, BaseURL: base, Username: username, Token: token, Password: input.Password, ConfigID: hex.EncodeToString(identity[:])}
	encoded, err := json.Marshal(doc)
	if err != nil {
		return NanShareView{}, fmt.Errorf("编码 NanShare 配置: %w", err)
	}
	if err := s.store.Save(string(encoded)); err != nil {
		return NanShareView{}, fmt.Errorf("保存 NanShare 会话: %w", err)
	}
	view := NanShareView{BaseURL: base, Username: username, Configured: true, AutoLogin: doc.Password != "", Session: NanShareCheck{OK: true, Message: "登录验证通过"}}
	// Save the login even if an optional remote feature is unconfigured.
	s.mu.Lock()
	s.doc = doc
	s.view = view
	s.mu.Unlock()
	view = probeNanShare(ctx, client, view)
	s.mu.Lock()
	s.view = view
	s.mu.Unlock()
	return view, nil
}

func probeNanShare(ctx context.Context, client *nanshare.Client, view NanShareView) NanShareView {
	view.CheckedAt = time.Now().UTC().Format(time.RFC3339)
	account, err := client.RE0Account(ctx)
	view.RE0 = NanShareCheck{OK: err == nil && account != "", Message: "RE0 搜索配置读取成功"}
	if err != nil {
		view.RE0.Message = err.Error()
	} else if account == "" {
		view.RE0.Message = "请在 NanShare 看板设置中明确选择 RE0 账号"
	}
	share, err := client.ShareConfig(ctx)
	view.Share = NanShareCheck{Message: "请在 NanShare 启用分享 STRM、生成模式和输出目录"}
	if err != nil {
		view.Share.Message = err.Error()
	} else if *share.Enabled && (*share.Normal || *share.Reshare) && len(share.Folders) > 0 {
		view.Share = NanShareCheck{OK: true, Message: "分享 STRM 配置读取成功（尚未验证任务执行）"}
	}
	return view
}

func (s *NanShareService) Verify(ctx context.Context) (NanShareView, error) {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	client, view, err := s.snapshot()
	if err != nil {
		return NanShareView{}, err
	}
	username, err := client.Session(ctx)
	view.CheckedAt = time.Now().UTC().Format(time.RFC3339)
	view.Session = NanShareCheck{OK: err == nil && username == view.Username, Message: "登录验证通过"}
	view.RE0 = NanShareCheck{}
	view.Share = NanShareCheck{}
	if err != nil {
		view.Session.Message = err.Error()
	} else if username != view.Username {
		view.Session.Message = "会话账号不一致，请重新登录"
	}
	if view.Session.OK {
		view = probeNanShare(ctx, client, view)
	}
	s.mu.Lock()
	s.view = view
	s.mu.Unlock()
	return view, nil
}

func (s *NanShareService) snapshot() (*nanshare.Client, NanShareView, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.doc.Token == "" {
		return nil, NanShareView{}, fmt.Errorf("%w：请先登录 NanShare", nanshare.ErrInput)
	}
	client, err := s.clientFor(s.doc)
	return client, s.view, err
}

func (s *NanShareService) SearchTest(ctx context.Context, kind string, id int64) (nanshare.SearchResult, error) {
	// A concurrent credential change never changes this request's snapshot.
	client, _, err := s.snapshot()
	if err != nil {
		return nanshare.SearchResult{}, err
	}
	account, err := client.RE0Account(ctx)
	if err != nil {
		return nanshare.SearchResult{}, err
	}
	if account == "" {
		return nanshare.SearchResult{}, fmt.Errorf("%w：请先在 NanShare 选择 RE0 搜索账号", nanshare.ErrInput)
	}
	return client.SearchTest(ctx, account, kind, id)
}

func (s *NanShareService) Clear() error {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	s.authMu.Lock()
	defer s.authMu.Unlock()
	if err := s.store.Save(""); err != nil {
		return fmt.Errorf("清除 NanShare 会话: %w", err)
	}
	s.mu.Lock()
	s.doc = nanShareDocument{Version: 1}
	s.view = NanShareView{}
	s.mu.Unlock()
	return nil
}

// Token renewal preserves the same configured identity and search receipts.
func sameNanShareConfig(a, b nanShareDocument) bool {
	a.Token = ""
	b.Token = ""
	return a == b
}

func (s *NanShareService) clientFor(doc nanShareDocument) (*nanshare.Client, error) {
	client, err := nanshare.New(doc.BaseURL, doc.Token)
	if err != nil {
		return nil, err
	}
	return client.WithRenewal(func(ctx context.Context, rejected string) (string, error) {
		return s.renewSession(ctx, doc, rejected)
	}), nil
}

func (s *NanShareService) renewSession(ctx context.Context, expected nanShareDocument, rejected string) (string, error) {
	s.authMu.Lock()
	defer s.authMu.Unlock()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	s.mu.RLock()
	current := s.doc
	s.mu.RUnlock()
	if !sameNanShareConfig(expected, current) {
		return "", nanshare.ErrChanged
	}
	if current.Token != rejected {
		return current.Token, nil
	}
	if current.Password == "" {
		return "", fmt.Errorf("%w：请用账号密码重新登录一次以启用自动登录", nanshare.ErrAuth)
	}
	client, err := nanshare.New(current.BaseURL, "")
	if err != nil {
		return "", err
	}
	token, username, err := client.Login(ctx, current.Username, current.Password)
	if err != nil {
		return "", fmt.Errorf("NanShare 自动登录失败: %w", err)
	}
	if username != current.Username {
		return "", fmt.Errorf("%w：自动登录返回的账号不一致", nanshare.ErrAuth)
	}
	verified, err := nanshare.New(current.BaseURL, token)
	if err != nil {
		return "", err
	}
	username, err = verified.Session(ctx)
	if err != nil {
		return "", err
	}
	if username != current.Username {
		return "", nanshare.ErrAuth
	}
	current.Token = token
	encoded, err := json.Marshal(current)
	if err != nil {
		return "", fmt.Errorf("编码 NanShare 会话: %w", err)
	}
	if err := s.store.Save(string(encoded)); err != nil {
		return "", fmt.Errorf("保存 NanShare 会话: %w", err)
	}
	s.mu.Lock()
	s.doc = current
	s.mu.Unlock()
	return token, nil
}
