package service

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
)

type proxyTestStore struct {
	*storage.Store
	saveErr error
}

func (s *proxyTestStore) SaveEmbyProxyConfig(ctx context.Context, raw string, settings *model.MemberPlaybackSettings) error {
	if s.saveErr != nil {
		return s.saveErr
	}
	return s.Store.SaveEmbyProxyConfig(ctx, raw, settings)
}

func openProxyTestStore(t *testing.T, path string) *storage.Store {
	t.Helper()
	store, err := storage.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func proxyFreePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port
}

func TestEmbyProxyListenerTransactionsAndRestart(t *testing.T) {
	accounts, err := newP115Accounts(&failingAccountStore{}, "")
	if err != nil {
		t.Fatal(err)
	}
	id, err := accounts.AddPrimary("独立账号")
	if err != nil {
		t.Fatal(err)
	}
	cookie := "private-cookie"
	if err := accounts.Update(id, 1, "独立账号", &cookie); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "proxy.db")
	store := &proxyTestStore{Store: openProxyTestStore(t, path)}
	factory := func(c EmbyProxyConfig) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, c.AccountID) })
	}
	s, err := NewEmbyProxy(context.Background(), store, nil, accounts, factory)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c := EmbyProxyConfig{Enabled: true, Port: proxyFreePort(t), URL: "http://127.0.0.1:8096"}
	if err := s.Apply(context.Background(), c); err != nil {
		t.Fatal("没有使用主账号", err)
	}
	c.AccountID = id
	if err := s.Apply(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	check := func(port int) {
		t.Helper()
		client := &http.Client{Timeout: 2 * time.Second}
		resp, err := client.Get("http://127.0.0.1:" + strconv.Itoa(port))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil || string(body) != id {
			t.Fatal("监听未提供配置的处理器")
		}
	}
	check(c.Port)
	// 在同一端口切换调试开关时立即生效，并与其他配置一起保存。
	c.Debug = true
	if err := s.Apply(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if !s.Status().Config.Debug {
		t.Fatal("debug setting was not saved")
	}
	check(c.Port)
	busy, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	changed := c
	changed.Port = busy.Addr().(*net.TCPAddr).Port
	if err := s.Apply(context.Background(), changed); err == nil {
		t.Fatal("未识别端口冲突")
	}
	if s.Status().Config != c || !s.Status().Listening {
		t.Fatal("端口冲突破坏原配置")
	}
	check(c.Port)
	// 保存失败时保留原监听和配置，释放新绑定的端口。
	store.saveErr = errors.New("模拟反代配置保存失败")
	changed.Port = proxyFreePort(t)
	if err := s.Apply(context.Background(), changed); !errors.Is(err, store.saveErr) {
		t.Fatal("未报告保存失败", err)
	}
	store.saveErr = nil
	if s.Status().Config != c {
		t.Fatal("保存失败改变了配置")
	}
	check(c.Port)
	if err := s.Apply(context.Background(), changed); err != nil {
		t.Fatal(err)
	}
	check(changed.Port)
	s.Close()
	if _, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(changed.Port), 100*time.Millisecond); err == nil {
		t.Fatal("关闭后端口未释放")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	restoredStore := &proxyTestStore{Store: openProxyTestStore(t, path), saveErr: errors.New("启动恢复不应保存配置")}
	restarted, err := NewEmbyProxy(context.Background(), restoredStore, func() (string, error) {
		return "", errors.New("已有 SQLite 配置时不应读取旧文件")
	}, accounts, factory)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if !restarted.Status().Listening || restarted.Status().Config != changed {
		t.Fatal("重启没有恢复监听")
	}
	restoredStore.saveErr = nil
	check(changed.Port)
	// 旧监听正在退出时仍能重新启用同一端口。
	resumeShutdown := make(chan struct{})
	defer close(resumeShutdown)
	restarted.mu.Lock()
	oldCancel := restarted.serverCancel
	restarted.serverCancel = func() { <-resumeShutdown; oldCancel() }
	restarted.mu.Unlock()
	changed.Enabled = false
	if err := restarted.Apply(context.Background(), changed); err != nil {
		t.Fatal(err)
	}
	changed.Enabled = true
	if err := restarted.Apply(context.Background(), changed); err != nil {
		t.Fatal("旧请求仍在退出时无法重新监听同一端口", err)
	}
	check(changed.Port)
	if err := accounts.Delete(id, 2); err != nil {
		t.Fatal(err)
	}
	changed.Enabled = false
	if err := restarted.Apply(context.Background(), changed); err != nil {
		t.Fatal("账号删除后不能关闭反代", err)
	}
	if restarted.Status().Listening {
		t.Fatal("关闭后状态仍为监听")
	}
}

func TestEmbyProxyRejectsInvalidPortsAndLoop(t *testing.T) {
	for _, port := range []int{0, 80, 65536} {
		if _, err := normalizeEmbyProxy(EmbyProxyConfig{Port: port}); err == nil {
			t.Fatal("接受了无效端口", port)
		}
	}
	_, err := normalizeEmbyProxy(EmbyProxyConfig{Enabled: true, Port: 8097, URL: "http://nas:8097"})
	if err == nil || !strings.Contains(err.Error(), "上游端口") {
		t.Fatal("未阻止上游反代循环", err)
	}
}
