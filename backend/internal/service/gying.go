package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"sync"

	"strm115/backend/internal/client/gying"
)

const DefaultGyingBaseURL = "https://www.xn--wcv59z.com"

type GyingConfigInput struct {
	BaseURL  string `json:"baseUrl"`
	Username string `json:"username"`
	Password string `json:"password"`
}

type GyingView struct {
	BaseURL    string `json:"baseUrl"`
	Username   string `json:"username"`
	Configured bool   `json:"configured"`
	HasSession bool   `json:"hasSession"`
}

type gyingDocument struct {
	Version  int                   `json:"version"`
	BaseURL  string                `json:"baseUrl"`
	Username string                `json:"username"`
	Password string                `json:"password"`
	Cookies  []gying.SessionCookie `json:"cookies"`
}

type GyingService struct {
	mu     sync.RWMutex
	opMu   sync.Mutex
	store  *SessionStore
	doc    gyingDocument
	client *gying.Client
}

func NewGyingService(store *SessionStore) (*GyingService, error) {
	s := &GyingService{store: store, doc: gyingDocument{Version: 1, Cookies: []gying.SessionCookie{}}}
	if strings.TrimSpace(store.Value()) == "" {
		return s, nil
	}
	if err := json.Unmarshal([]byte(store.Value()), &s.doc); err != nil || s.doc.Version != 1 {
		return nil, fmt.Errorf("观影配置无法解析")
	}
	client, err := gying.New(s.doc.BaseURL, nil)
	if err != nil {
		return nil, fmt.Errorf("恢复观影配置: %w", err)
	}
	if err := client.RestoreSession(s.doc.Cookies); err != nil {
		return nil, fmt.Errorf("恢复观影会话: %w", err)
	}
	s.client = client
	return s, nil
}

func (s *GyingService) View() GyingView {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return GyingView{BaseURL: s.doc.BaseURL, Username: s.doc.Username, Configured: s.client != nil && s.doc.Password != "", HasSession: len(s.doc.Cookies) > 0}
}

func validateGyingConfig(input GyingConfigInput) (GyingConfigInput, error) {
	input.BaseURL = strings.TrimRight(strings.TrimSpace(input.BaseURL), "/")
	input.Username = strings.TrimSpace(input.Username)
	if input.BaseURL == "" {
		input.BaseURL = DefaultGyingBaseURL
	}
	if input.Username == "" || len([]rune(input.Username)) > 100 || len(input.Password) > 512 || strings.ContainsAny(input.Username, "\r\n\x00") {
		return GyingConfigInput{}, gying.ErrInput
	}
	u, err := url.Parse(input.BaseURL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Port() != "" || u.Path != "" && u.Path != "/" || u.RawQuery != "" || u.Fragment != "" {
		return GyingConfigInput{}, fmt.Errorf("%w: 站点地址必须是无路径和端口的 HTTPS 域名", gying.ErrInput)
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if host == "localhost" || strings.HasSuffix(host, ".local") || net.ParseIP(host) != nil {
		return GyingConfigInput{}, fmt.Errorf("%w: 站点地址不能使用本机、内网名称或 IP", gying.ErrInput)
	}
	return input, nil
}

// Configure validates credentials against the remote site before replacing
// the encrypted configuration. A blank password retains the saved password.
func (s *GyingService) Configure(ctx context.Context, input GyingConfigInput) (GyingView, error) {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	s.mu.RLock()
	current := s.doc
	s.mu.RUnlock()
	if input.Password == "" && input.BaseURL == current.BaseURL && strings.TrimSpace(input.Username) == current.Username {
		input.Password = current.Password
	}
	input, err := validateGyingConfig(input)
	if err != nil {
		return GyingView{}, err
	}
	if input.Password == "" {
		return GyingView{}, fmt.Errorf("%w: 请输入密码", gying.ErrInput)
	}
	client, err := gying.New(input.BaseURL, nil)
	if err != nil {
		return GyingView{}, err
	}
	if err := client.Login(ctx, input.Username, input.Password); err != nil {
		return GyingView{}, err
	}
	doc := gyingDocument{Version: 1, BaseURL: input.BaseURL, Username: input.Username, Password: input.Password, Cookies: client.SnapshotSession()}
	if err := s.save(doc); err != nil {
		return GyingView{}, err
	}
	s.mu.Lock()
	s.doc, s.client = doc, client
	s.mu.Unlock()
	return s.View(), nil
}

func (s *GyingService) save(doc gyingDocument) error {
	raw, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("编码观影配置: %w", err)
	}
	if err := s.store.Save(string(raw)); err != nil {
		return fmt.Errorf("保存观影配置: %w", err)
	}
	return nil
}

func (s *GyingService) Clear() error {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	if err := s.store.Save(""); err != nil {
		return err
	}
	s.mu.Lock()
	s.doc = gyingDocument{Version: 1, Cookies: []gying.SessionCookie{}}
	s.client = nil
	s.mu.Unlock()
	return nil
}

func (s *GyingService) Search(ctx context.Context, originalTitle, imdbID string) (gying.SearchResponse, error) {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	s.mu.RLock()
	client, doc := s.client, s.doc
	s.mu.RUnlock()
	if client == nil || doc.Password == "" {
		return gying.SearchResponse{}, gying.ErrNotConfigured
	}
	result, err := client.SearchByTitle(ctx, originalTitle, imdbID)
	if errors.Is(err, gying.ErrForbidden) {
		if loginErr := client.Login(ctx, doc.Username, doc.Password); loginErr != nil {
			return gying.SearchResponse{}, fmt.Errorf("观影会话失效且重新登录失败: %w", loginErr)
		}
		result, err = client.SearchByTitle(ctx, originalTitle, imdbID)
	}
	if err != nil {
		return gying.SearchResponse{}, err
	}
	doc.Cookies = client.SnapshotSession()
	if err := s.save(doc); err != nil {
		return gying.SearchResponse{}, err
	}
	s.mu.Lock()
	s.doc = doc
	s.mu.Unlock()
	return result, nil
}
