package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"strm115/backend/internal/client/emby"
)

type EmbyProxyConfig struct {
	Enabled                bool   `json:"enabled"`
	Debug                  bool   `json:"debug"`
	Prewarm                bool   `json:"prewarm"`
	DirectLinkCacheMinutes int    `json:"directLinkCacheMinutes"`
	Port                   int    `json:"port"`
	URL                    string `json:"url"`
	AccountID              string `json:"accountId"`
	EmbyPathPrefix         string `json:"embyPathPrefix"`
}

type EmbyProxyStatus struct {
	Config    EmbyProxyConfig `json:"config"`
	Listening bool            `json:"listening"`
	Error     string          `json:"error"`
}

type directLinkCacheHandler interface {
	DirectLinkStatus() DirectLinkCacheStatus
	ClearDirectLinks(string)
}

func (s *EmbyProxy) DirectLinkStatus() DirectLinkCacheStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	if h, ok := s.handler.(directLinkCacheHandler); ok {
		return h.DirectLinkStatus()
	}
	return DirectLinkCacheStatus{Entries: []DirectLinkCacheEntry{}}
}
func (s *EmbyProxy) ClearDirectLinks(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if h, ok := s.handler.(directLinkCacheHandler); ok {
		h.ClearDirectLinks(id)
	}
}

type embyProxyLifecycleKey struct{}

// EmbyProxyStopping distinguishes listener shutdown from cancellation of an individual HTTP request.
func EmbyProxyStopping(ctx context.Context) bool {
	lifecycle, ok := ctx.Value(embyProxyLifecycleKey{}).(context.Context)
	return ok && lifecycle.Err() != nil
}

// EmbyProxy owns the independent listener. A failed bind/save leaves the active listener intact.
type EmbyProxy struct {
	Logs         *EmbyProxyLogs
	mu           sync.Mutex
	store        *SessionStore
	accounts     *P115Accounts
	factory      func(EmbyProxyConfig) http.Handler
	config       EmbyProxyConfig
	handler      http.Handler
	server       *http.Server
	serverCancel context.CancelFunc
	ctx          context.Context
	closed       bool
	lastError    string
	wg           sync.WaitGroup
}

func NewEmbyProxy(ctx context.Context, store *SessionStore, accounts *P115Accounts, factory func(EmbyProxyConfig) http.Handler, logs ...*EmbyProxyLogs) (*EmbyProxy, error) {
	s := &EmbyProxy{store: store, accounts: accounts, factory: factory, ctx: ctx, config: EmbyProxyConfig{Port: 8097, DirectLinkCacheMinutes: 9}}
	if len(logs) > 0 {
		s.Logs = logs[0]
	}
	if raw := store.Value(); raw != "" {
		if err := json.Unmarshal([]byte(raw), &s.config); err != nil {
			return nil, fmt.Errorf("读取 Emby 302 配置: %w", err)
		}
	}
	// Startup listener failures remain repairable from the management page.
	if s.config.Enabled {
		if err := s.Apply(ctx, s.config); err != nil {
			s.lastError = err.Error()
		}
	}
	return s, nil
}

func (s *EmbyProxy) Status() EmbyProxyStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return EmbyProxyStatus{Config: s.config, Listening: s.server != nil, Error: s.lastError}
}

func normalizeEmbyProxy(c EmbyProxyConfig) (EmbyProxyConfig, error) {
	if c.DirectLinkCacheMinutes < 0 || c.DirectLinkCacheMinutes > 30 {
		return c, fmt.Errorf("直链缓存时间须为 0～30 分钟，0 表示关闭")
	}
	if c.Port < 1024 || c.Port > 65535 {
		return c, fmt.Errorf("反代端口须为 1024～65535，且不能使用已占用的端口")
	}
	var err error

	if c.Enabled || c.URL != "" {
		c.URL, err = emby.NormalizeURL(c.URL)
		if err != nil {
			return c, err
		}
		u, _ := url.Parse(c.URL)
		// Conservatively reject all targets using our listener port, including NAS aliases.
		if u.Port() == strconv.Itoa(c.Port) {
			return c, fmt.Errorf("Emby 上游端口不能与反代监听端口相同")
		}
	}
	c.EmbyPathPrefix = strings.TrimRight(strings.ReplaceAll(strings.TrimSpace(c.EmbyPathPrefix), "\\", "/"), "/")
	if strings.ContainsAny(c.EmbyPathPrefix, "\r\n\x00") {
		return c, fmt.Errorf("Emby 媒体路径无效")
	}
	return c, nil
}

func (s *EmbyProxy) Apply(ctx context.Context, input EmbyProxyConfig) (resultErr error) {
	ctx = s.Logs.Trace(ctx)
	defer func() {
		if resultErr != nil {
			s.Logs.Add(ctx, "ERROR", "反代配置未生效，请检查页面错误提示；原配置与监听保持不变")
		}
	}()
	c, err := normalizeEmbyProxy(input)
	if err != nil {
		return err
	}
	// Disabling must work even after the configured account has been deleted.
	if c.Enabled {
		if err := s.accounts.ValidateSelection(c.AccountID); err != nil {
			return err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.ctx.Err() != nil {
		return fmt.Errorf("反代服务正在退出")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("保存反代配置取消: %w", err)
	}
	var listener net.Listener
	if c.Enabled && (s.server == nil || c.Port != s.config.Port) {
		listener, err = (&net.ListenConfig{}).Listen(ctx, "tcp", ":"+strconv.Itoa(c.Port))
		if err != nil {
			return fmt.Errorf("无法监听端口 %d，请检查占用或权限", c.Port)
		}
	}
	data, err := json.Marshal(c)
	if err == nil {
		err = s.store.Save(string(data))
	}
	if err != nil {
		if listener != nil {
			_ = listener.Close()
		}
		return fmt.Errorf("保存反代配置: %w", err)
	}
	old, oldCancel := s.server, s.serverCancel
	s.config, s.lastError = c, ""
	previousHandler := s.handler
	s.handler = s.factory(c)
	if closer, ok := previousHandler.(interface{ Close() }); ok {
		closer.Close()
	}
	if !c.Enabled {
		s.server = nil
		s.serverCancel = nil
	}
	if listener != nil {
		serverContext, serverCancel := context.WithCancel(s.ctx)
		server := &http.Server{Handler: s, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 64 << 10, BaseContext: func(net.Listener) context.Context {
			return context.WithValue(serverContext, embyProxyLifecycleKey{}, serverContext)
		}}
		s.server = server
		s.serverCancel = serverCancel
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer serverCancel()
			err := server.Serve(listener)
			s.mu.Lock()
			defer s.mu.Unlock()
			if s.server == server {
				s.server = nil
				if err != nil && !errors.Is(err, http.ErrServerClosed) {
					s.lastError = "反代监听已停止，请重新保存配置"
					s.Logs.Add(s.ctx, "ERROR", s.lastError)
				}
			}
		}()
	}
	if old != nil && old != s.server {
		s.wg.Add(1)
		go func() { defer s.wg.Done(); stopEmbyProxy(old, oldCancel) }()
	}
	if c.Enabled {
		s.Logs.Add(ctx, "INFO", fmt.Sprintf("反代配置生效，监听端口 %d", c.Port))
	} else {
		s.Logs.Add(ctx, "INFO", "反代已停用")
	}
	return nil
}

func (s *EmbyProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	h, enabled := s.handler, s.config.Enabled && !s.closed
	s.mu.Unlock()
	if h == nil || !enabled {
		http.Error(w, "Emby 302 已停用", http.StatusServiceUnavailable)
		return
	}
	h.ServeHTTP(w, r)
}

func stopEmbyProxy(server *http.Server, cancelRequests context.CancelFunc) {
	// Also terminate hijacked WebSocket connections through the request context.
	if cancelRequests != nil {
		cancelRequests()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		_ = server.Close()
	}
}

func (s *EmbyProxy) Close() {
	s.mu.Lock()
	s.closed = true
	server := s.server
	cancelRequests := s.serverCancel
	handler := s.handler
	s.server = nil
	s.serverCancel = nil
	s.mu.Unlock()
	if closer, ok := handler.(interface{ Close() }); ok {
		closer.Close()
	}
	if server != nil {
		stopEmbyProxy(server, cancelRequests)
	}
	s.wg.Wait()
}
