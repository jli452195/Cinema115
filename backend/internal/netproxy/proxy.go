// Package netproxy controls outbound routing without changing the process
// default transport or any provider's rate limits, credentials, or retry policy.
package netproxy

import (
	"errors"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

var ErrConfig = errors.New("代理配置无效")

type Service struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

var services = []Service{
	{"tmdb", "TMDB"}, {"github", "GitHub 字幕库"}, {"mikan", "蜜柑"},
	{"anibt", "AniBT"}, {"garden", "AnimeGarden"}, {"bangumi", "Bangumi"},
	{"assrt", "射手网 Assrt"}, {"gying", "观影"}, {"woniu", "蜗牛"},
	{"telegram_bot", "Telegram 机器人"},
	{"telegram_user", "Telegram 用户与频道搜索"},
}

type Config struct {
	Enabled  bool     `json:"enabled"`
	URL      string   `json:"url"`
	Username string   `json:"username"`
	Password string   `json:"password,omitempty"`
	Services []string `json:"services"`
}

func Services() []Service { return append([]Service(nil), services...) }

func KnownService(id string) bool {
	for _, service := range services {
		if service.ID == id {
			return true
		}
	}
	return false
}

func DefaultConfig() Config {
	return Config{Services: []string{"tmdb", "github", "mikan", "anibt", "garden", "bangumi"}}
}

// Policy is immutable after preparation. It contains credentials only in memory;
// callers must never serialize it or return its proxy URL to a browser.
type Policy struct {
	proxy    *url.URL
	selected map[string]bool
}

func Prepare(input Config) (Config, *Policy, error) {
	input.URL, input.Username = strings.TrimSpace(input.URL), strings.TrimSpace(input.Username)
	if len(input.URL) > 2048 || len(input.Username) > 256 || len(input.Password) > 1024 ||
		strings.ContainsAny(input.Username+input.Password, "\x00\r\n") || input.Password != "" && input.Username == "" {
		return Config{}, nil, ErrConfig
	}
	selected := make(map[string]bool, len(input.Services))
	for _, id := range input.Services {
		if !KnownService(id) {
			return Config{}, nil, ErrConfig
		}
		selected[id] = true
	}
	input.Services = make([]string, 0, len(selected))
	for _, service := range services {
		if selected[service.ID] {
			input.Services = append(input.Services, service.ID)
		}
	}
	policy := &Policy{selected: make(map[string]bool)}
	if input.URL == "" {
		if input.Enabled {
			return Config{}, nil, errors.New("请填写代理地址")
		}
		return input, policy, nil
	}
	u, err := url.Parse(input.URL)
	if err != nil || u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" ||
		u.RawPath != "" || u.Path != "" && u.Path != "/" || u.Hostname() == "" ||
		(u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "socks5" && u.Scheme != "socks5h") ||
		strings.ContainsAny(u.Host, " \t\r\n\x00") {
		return Config{}, nil, errors.New("请输入有效的 HTTP、HTTPS 或 SOCKS5 代理地址，认证信息请单独填写")
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return Config{}, nil, errors.New("代理端口必须为 1～65535")
		}
	} else if strings.HasSuffix(u.Host, ":") {
		return Config{}, nil, errors.New("请填写有效代理端口")
	}
	u.Path = ""
	if (u.Scheme == "socks5" || u.Scheme == "socks5h") && (len(input.Username) > 255 || len(input.Password) > 255) {
		return Config{}, nil, errors.New("SOCKS5 用户名和密码不能超过 255 字节")
	}
	input.URL = u.String()
	if input.Enabled {
		if len(selected) == 0 {
			return Config{}, nil, errors.New("请至少选择一项使用代理的服务")
		}
		policy.proxy, policy.selected = u, selected
		if input.Username != "" {
			policy.proxy.User = url.UserPassword(input.Username, input.Password)
		}
	}
	return input, policy, nil
}

// Manager keeps stable connection pools. Every request reads one policy snapshot;
// changing settings never replays a request or switches its route on failure.
type Manager struct {
	state      atomic.Pointer[Policy]
	mu         sync.Mutex
	transports map[string]*http.Transport
	direct     *http.Transport
}

func New() *Manager {
	direct := baseTransport()
	direct.Proxy = nil
	return &Manager{transports: make(map[string]*http.Transport), direct: direct}
}

func baseTransport() *http.Transport {
	if transport, ok := http.DefaultTransport.(*http.Transport); ok {
		return transport.Clone()
	}
	return &http.Transport{ForceAttemptHTTP2: true}
}

func (m *Manager) Apply(policy *Policy) {
	m.state.Store(policy)
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, transport := range m.transports {
		transport.CloseIdleConnections()
	}
}

func alwaysDirect(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()) {
		return true
	}
	for _, domain := range []string{"115.com", "115cdn.com", "115cdn.net", "anxia.com"} {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return true
		}
	}
	return false
}

func (m *Manager) Transport(scope string) *http.Transport {
	if !KnownService(scope) {
		return m.direct
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if transport := m.transports[scope]; transport != nil {
		return transport
	}
	transport := baseTransport()
	transport.Proxy = func(req *http.Request) (*url.URL, error) {
		policy := m.state.Load()
		if policy == nil || !policy.selected[scope] || alwaysDirect(req.URL.Hostname()) {
			return nil, nil
		}
		// The Transport must not receive the immutable policy URL itself.
		proxy := *policy.proxy
		return &proxy, nil
	}
	m.transports[scope] = transport
	return transport
}

func (m *Manager) CloseIdleConnections() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.direct.CloseIdleConnections()
	for _, transport := range m.transports {
		transport.CloseIdleConnections()
	}
}

// Default is the application's shared policy manager. Provider clients opt into
// an explicit scope; 115 and local integrations always use DirectTransport.
var Default = New()

func Transport(scope string) *http.Transport { return Default.Transport(scope) }
func DirectTransport() *http.Transport       { return Default.direct }
