package service

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

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
	id, err := accounts.Add("独立账号")
	if err != nil {
		t.Fatal(err)
	}
	cookie := "private-cookie"
	if err := accounts.Update(id, 1, "独立账号", &cookie); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	store, err := NewSessionStore(filepath.Join(dir, "proxy.enc"), "secret")
	if err != nil {
		t.Fatal(err)
	}
	factory := func(c EmbyProxyConfig) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, c.AccountID) })
	}
	s, err := NewEmbyProxy(context.Background(), store, accounts, factory)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c := EmbyProxyConfig{Enabled: true, Port: proxyFreePort(t), URL: "http://127.0.0.1:8096", STRMBaseURL: "http://cinema:8115"}
	if err := s.Apply(context.Background(), c); err == nil {
		t.Fatal("自动使用了唯一账号")
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
	// A persistence failure must not displace the old listener or change its configuration.
	goodPath := store.path
	blocker := filepath.Join(dir, "not-a-directory")
	if err := os.WriteFile(blocker, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	store.path = filepath.Join(blocker, "proxy.enc")
	changed.Port = proxyFreePort(t)
	if err := s.Apply(context.Background(), changed); err == nil {
		t.Fatal("未报告保存失败")
	}
	store.path = goodPath
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
	restoredStore, err := NewSessionStore(goodPath, "secret")
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := NewEmbyProxy(context.Background(), restoredStore, accounts, factory)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if !restarted.Status().Listening || restarted.Status().Config != changed {
		t.Fatal("重启没有恢复监听")
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
	_, err := normalizeEmbyProxy(EmbyProxyConfig{Enabled: true, Port: 8097, URL: "http://nas:8097", STRMBaseURL: "http://nas:8115"})
	if err == nil || !strings.Contains(err.Error(), "上游端口") {
		t.Fatal("未阻止上游反代循环", err)
	}
}
