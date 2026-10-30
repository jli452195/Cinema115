package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"strm115/backend/internal/netproxy"
)

type NetworkProxyInput struct {
	Enabled  bool     `json:"enabled"`
	URL      string   `json:"url"`
	Username string   `json:"username"`
	Password *string  `json:"password"`
	Services []string `json:"services"`
}

type NetworkProxyView struct {
	Enabled            bool               `json:"enabled"`
	URL                string             `json:"url"`
	Username           string             `json:"username"`
	PasswordConfigured bool               `json:"passwordConfigured"`
	Services           []string           `json:"services"`
	AvailableServices  []netproxy.Service `json:"availableServices"`
}

type networkProxyDocument struct {
	Version int             `json:"version"`
	Config  netproxy.Config `json:"config"`
}

type NetworkProxy struct {
	mu      sync.Mutex
	store   accountSecretStore
	runtime *netproxy.Manager
	config  netproxy.Config
	// Tests may supply a local client; production only probes fixed service URLs.
	probeClient func(*netproxy.Manager, string) *http.Client
}

func NewNetworkProxy(store accountSecretStore, runtime *netproxy.Manager) (*NetworkProxy, error) {
	config := netproxy.DefaultConfig()
	if raw := store.Value(); raw != "" {
		var doc networkProxyDocument
		if err := json.Unmarshal([]byte(raw), &doc); err != nil || doc.Version != 1 {
			return nil, errors.New("网络代理配置无法读取")
		}
		config = doc.Config
	}
	config, policy, err := netproxy.Prepare(config)
	if err != nil {
		return nil, fmt.Errorf("读取网络代理配置: %w", err)
	}
	runtime.Apply(policy)
	return &NetworkProxy{store: store, runtime: runtime, config: config}, nil
}

func networkProxyView(config netproxy.Config) NetworkProxyView {
	return NetworkProxyView{Enabled: config.Enabled, URL: config.URL, Username: config.Username,
		PasswordConfigured: config.Password != "", Services: append([]string{}, config.Services...), AvailableServices: netproxy.Services()}
}

func (s *NetworkProxy) View() NetworkProxyView {
	s.mu.Lock()
	defer s.mu.Unlock()
	return networkProxyView(s.config)
}

func (s *NetworkProxy) prepareLocked(input NetworkProxyInput) (netproxy.Config, *netproxy.Policy, error) {
	password := s.config.Password
	if input.Password != nil {
		password = *input.Password
	}
	return netproxy.Prepare(netproxy.Config{Enabled: input.Enabled, URL: input.URL,
		Username: input.Username, Password: password, Services: input.Services})
}

func (s *NetworkProxy) Save(ctx context.Context, input NetworkProxyInput) (NetworkProxyView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return NetworkProxyView{}, err
	}
	config, policy, err := s.prepareLocked(input)
	if err != nil {
		return NetworkProxyView{}, fmt.Errorf("%w: %w", netproxy.ErrConfig, err)
	}
	raw, err := json.Marshal(networkProxyDocument{Version: 1, Config: config})
	if err != nil {
		return NetworkProxyView{}, fmt.Errorf("编码网络代理设置: %w", err)
	}
	if err := s.store.Save(string(raw)); err != nil {
		return NetworkProxyView{}, fmt.Errorf("保存网络代理设置: %w", err)
	}
	s.config = config
	s.runtime.Apply(policy)
	return networkProxyView(config), nil
}

type NetworkProxyTest struct {
	Service   string `json:"service"`
	Proxied   bool   `json:"proxied"`
	Reachable bool   `json:"reachable"`
	Status    int    `json:"status,omitempty"`
	ElapsedMS int64  `json:"elapsedMs"`
	Message   string `json:"message"`
}

var networkProxyProbeURLs = map[string]string{
	"tmdb":         "https://api.themoviedb.org/3/configuration",
	"github":       "https://api.github.com/rate_limit",
	"mikan":        "https://mikanani.me/",
	"anibt":        "https://anibt.net/",
	"garden":       "https://api.animes.garden/",
	"bangumi":      "https://api.bgm.tv/v0/subjects/8",
	"assrt":        "https://api.assrt.net/",
	"gying":        DefaultGyingBaseURL,
	"woniu":        "https://www.wn4k.com/",
	"telegram_bot": "https://api.telegram.org/",
}

// Test uses only the supplied form snapshot and a short-lived connection pool.
// It never saves the form, changes live routing, follows redirects, or polls.
func (s *NetworkProxy) Test(ctx context.Context, input NetworkProxyInput, service string) (NetworkProxyTest, error) {
	endpoint, ok := networkProxyProbeURLs[service]
	if !ok && service != "telegram_user" {
		return NetworkProxyTest{}, fmt.Errorf("%w：请选择有效的测试服务", netproxy.ErrConfig)
	}
	s.mu.Lock()
	config, policy, err := s.prepareLocked(input)
	s.mu.Unlock()
	if err != nil {
		return NetworkProxyTest{}, fmt.Errorf("%w: %w", netproxy.ErrConfig, err)
	}
	result := NetworkProxyTest{Service: service}
	for _, id := range config.Services {
		if config.Enabled && id == service {
			result.Proxied = true
		}
	}
	runtime := netproxy.New()
	runtime.Apply(policy)
	defer runtime.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	started := time.Now()
	if service == "telegram_user" {
		// Same native TCP route as the user client; Bot HTTPS cannot verify it.
		conn, err := runtime.Dialer(service)(ctx, "tcp", "149.154.167.41:443")
		result.ElapsedMS = time.Since(started).Milliseconds()
		if err != nil {
			return networkProxyProbeFailure(ctx, result, err)
		}
		_ = conn.Close()
		result.Reachable = true
		result.Message = "Telegram 数据中心 TCP／代理隧道已连通，仅验证网络连接，不代表用户登录或频道访问成功"
		return result, nil
	}
	client := &http.Client{Transport: runtime.Transport(service), Timeout: 12 * time.Second}
	if s.probeClient != nil {
		client = s.probeClient(runtime, service)
	}
	copy := *client
	copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return NetworkProxyTest{}, fmt.Errorf("创建代理连通性请求: %w", err)
	}
	req.Header.Set("User-Agent", "Cinema115-Network-Test/1.0")
	resp, err := copy.Do(req)
	result.ElapsedMS = time.Since(started).Milliseconds()
	if err != nil {
		return networkProxyProbeFailure(ctx, result, err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	result.Status = resp.StatusCode
	result.Reachable = resp.StatusCode >= 200 && resp.StatusCode < 400 || resp.StatusCode == 401 || resp.StatusCode == 404
	if result.Reachable {
		result.Message = "已连接到上游服务"
		if resp.StatusCode == 401 || resp.StatusCode == 404 {
			result.Message += "，仅验证网络连通，未验证服务凭据或业务接口"
		}
	} else {
		result.Message = "上游返回 HTTP " + strconv.Itoa(resp.StatusCode)
	}
	return result, nil
}

func networkProxyProbeFailure(ctx context.Context, result NetworkProxyTest, err error) (NetworkProxyTest, error) {
	// Proxy errors may contain credentials. Never forward their raw text.
	result.Message = "连接失败，请检查代理地址、认证信息和 NAS 网络"
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		result.Message = "连接超时，请检查代理服务和 NAS 网络"
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return NetworkProxyTest{}, ctx.Err()
	}
	return result, nil
}
