package bot

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func configTestManager(t *testing.T, handler http.HandlerFunc) (*Manager, string) {
	t.Helper()
	dir := t.TempDir()
	m, err := NewManager(dir, "test-secret", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	factory := m.newWorker
	m.newWorker = func(cfg BotConfig) (*Telegram, error) {
		worker, err := factory(cfg)
		if worker != nil {
			worker.BaseURL = server.URL
		}
		return worker, err
	}
	return m, dir
}

func identityHandler(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	if strings.HasPrefix(r.URL.Path, "/bot999:") {
		w.WriteHeader(401)
		return
	}
	switch {
	case strings.HasSuffix(r.URL.Path, "/getMe"):
		w.Write([]byte(`{"ok":true,"result":{"id":123,"is_bot":true,"first_name":"测试机器人","username":"cinema_test_bot"}}`))
	case strings.HasSuffix(r.URL.Path, "/getWebhookInfo"):
		w.Write([]byte(`{"ok":true,"result":{"url":""}}`))
	default:
		t.Error("连接测试或保存不应收取和发送消息", r.URL.Path)
		w.WriteHeader(500)
	}
}

func TestBotConfigEncryptedAndInvalidSavePreservesOriginal(t *testing.T) {
	m, dir := configTestManager(t, func(w http.ResponseWriter, r *http.Request) { identityHandler(t, w, r) })
	ctx := context.Background()
	identity, err := m.Test(ctx, "123:valid-token")
	if err != nil || identity.Username != "cinema_test_bot" {
		t.Fatal(identity, err)
	}
	if m.View().Configured {
		t.Fatal("测试连接不应保存配置")
	}
	if err := m.Save(ctx, BotConfig{Token: "123:valid-token", AllowedChatIDs: "42\n-100123", Enabled: false}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "telegram-config.enc"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "valid-token") {
		t.Fatal("令牌明文落盘")
	}
	view, _ := json.Marshal(m.View())
	if strings.Contains(string(view), "valid-token") || strings.Contains(string(view), `"token"`) {
		t.Fatal("接口泄露令牌")
	}
	if err := m.Save(ctx, BotConfig{Token: "999:bad-token", AllowedChatIDs: "42", Enabled: true}); err == nil {
		t.Fatal("错误令牌被保存")
	}
	if m.config.Token != "123:valid-token" || m.View().Enabled {
		t.Fatal("错误保存覆盖原配置")
	}
	if err := m.Save(ctx, BotConfig{AllowedChatIDs: "42", Enabled: false}); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewManager(dir, "test-secret", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.config.Token != "123:valid-token" || reopened.View().Source != "web" {
		t.Fatal("重启未恢复网页配置或被环境覆盖")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestBotManagerReplacesWorkerAndStops(t *testing.T) {
	var active, maximum atomic.Int32
	m, _ := configTestManager(t, func(w http.ResponseWriter, r *http.Request) { identityHandler(t, w, r) })
	factory := m.newWorker
	m.newWorker = func(cfg BotConfig) (*Telegram, error) {
		worker, err := factory(cfg)
		if err != nil {
			return nil, err
		}
		worker.HTTP.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if !strings.HasSuffix(r.URL.Path, "/getUpdates") {
				return http.DefaultTransport.RoundTrip(r)
			}
			count := active.Add(1)
			defer active.Add(-1)
			for prior := maximum.Load(); count > prior && !maximum.CompareAndSwap(prior, count); prior = maximum.Load() {
			}
			timer := time.NewTimer(10 * time.Millisecond)
			defer timer.Stop()
			select {
			case <-r.Context().Done():
				return nil, r.Context().Err()
			case <-timer.C:
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ok":true,"result":[]}`)), Header: make(http.Header)}, nil
		})
		return worker, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- m.Run(ctx) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(2 * time.Second):
			t.Error("接收器未退出")
		}
	}()
	wait := func(check func() bool) {
		t.Helper()
		deadline := time.After(2 * time.Second)
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		for !check() {
			select {
			case <-deadline:
				t.Fatal("状态更新超时")
			case <-ticker.C:
			}
		}
	}
	if err := m.Save(ctx, BotConfig{Token: "123:valid-token", AllowedChatIDs: "42", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	wait(func() bool { return m.View().Connected })
	if err := m.Save(ctx, BotConfig{AllowedChatIDs: "43", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	wait(func() bool { return m.View().Connected && m.View().AllowedChatIDs == "43" })
	if err := m.Save(ctx, BotConfig{AllowedChatIDs: "43", Enabled: false}); err != nil {
		t.Fatal(err)
	}
	wait(func() bool { return !m.View().Running && !m.View().Connected })
	if maximum.Load() > 1 {
		t.Fatal("出现并行长轮询接收器", maximum.Load())
	}
}

func TestBotConfigurationIgnoresEnvironment(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "123:legacy-token")
	t.Setenv("TELEGRAM_ALLOWED_CHAT_IDS", "42")
	m, err := NewManager(t.TempDir(), "test-secret", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if view := m.View(); view.Configured || view.Enabled || view.Source != "none" {
		t.Fatal("不应从环境变量启用 Bot")
	}
}
