package netproxy

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func applyTestConfig(t *testing.T, manager *Manager, config Config) {
	t.Helper()
	_, policy, err := Prepare(config)
	if err != nil {
		t.Fatal(err)
	}
	manager.Apply(policy)
}

func TestLiveRoutingUsesSelectedProxyAndKeepsLocalAnd115Direct(t *testing.T) {
	var calls atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Host != "metadata.example.test" || r.Header.Get("Proxy-Authorization") != "Basic dXNlcjpzZWNyZXQ=" {
			t.Errorf("unexpected proxy request host=%q authPresent=%t", r.URL.Host, r.Header.Get("Proxy-Authorization") != "")
		}
		_, _ = io.WriteString(w, "proxied")
	}))
	defer proxy.Close()
	manager := New()
	defer manager.CloseIdleConnections()
	transport := manager.Transport("tmdb")
	applyTestConfig(t, manager, Config{Enabled: true, URL: proxy.URL, Username: "user", Password: "secret", Services: []string{"tmdb"}})
	client := &http.Client{Transport: transport}
	resp, err := client.Get("http://metadata.example.test/catalog")
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil || string(data) != "proxied" || calls.Load() != 1 {
		t.Fatalf("request did not use proxy: %q %v", data, err)
	}
	for _, endpoint := range []string{"https://webapi.115.com/files", "https://cdn.example.115cdn.net/video", "https://115cdn.com/s/test", "https://anxia.com/", "http://127.0.0.1:8096", "http://192.168.0.3:8096", "http://[::1]/"} {
		req, _ := http.NewRequest(http.MethodGet, endpoint, nil)
		u, err := transport.Proxy(req)
		if err != nil || u != nil {
			t.Fatalf("protected origin would use a proxy: %s", endpoint)
		}
	}
	req, _ := http.NewRequest(http.MethodGet, "https://mikanani.me/", nil)
	if u, err := manager.Transport("mikan").Proxy(req); err != nil || u != nil {
		t.Fatal("unselected scope uses proxy")
	}
	applyTestConfig(t, manager, Config{URL: proxy.URL, Services: []string{"tmdb"}})
	if manager.Transport("tmdb") != transport {
		t.Fatal("configuration replacement rebuilt the provider pool")
	}
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "direct") }))
	defer local.Close()
	resp, err = client.Get(local.URL)
	if err != nil {
		t.Fatal(err)
	}
	data, err = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil || string(data) != "direct" || calls.Load() != 1 {
		t.Fatal("disabling proxy did not preserve direct requests")
	}
	public, _ := http.NewRequest(http.MethodGet, "https://api.themoviedb.org/3", nil)
	if u, _ := transport.Proxy(public); u != nil {
		t.Fatal("disabled scope still routes through proxy")
	}
}

func TestConfigValidationAndNoEnvironmentFallback(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://environment.invalid:8888")
	t.Setenv("HTTPS_PROXY", "http://environment.invalid:8888")
	manager := New()
	defer manager.CloseIdleConnections()
	if manager.direct.Proxy != nil || manager.Transport("115").Proxy != nil {
		t.Fatal("direct transports inherit environment proxy")
	}
	request, _ := http.NewRequest(http.MethodGet, "https://api.github.com/", nil)
	if u, _ := manager.Transport("github").Proxy(request); u != nil {
		t.Fatal("disabled policy inherited environment proxy")
	}
	for _, endpoint := range []string{"http://user:secret@proxy:7890", "ftp://proxy:7890", "http://proxy:0", "http://proxy:65536", "http://proxy/path", "http://proxy?key=x", "http://proxy#fragment", "http://proxy:", "http://", "http://proxy:bad"} {
		if _, _, err := Prepare(Config{Enabled: true, URL: endpoint, Services: []string{"tmdb"}}); err == nil {
			t.Errorf("accepted invalid proxy %q", endpoint)
		}
	}
	for _, scheme := range []string{"http", "https", "socks5", "socks5h"} {
		config, policy, err := Prepare(Config{Enabled: true, URL: scheme + "://[::1]:7890/", Services: []string{"mikan", "tmdb", "mikan"}})
		if err != nil || len(config.Services) != 2 || config.URL != scheme+"://[::1]:7890" || policy.proxy == nil {
			t.Fatalf("valid proxy rejected: %#v %v", config, err)
		}
	}
	for _, config := range []Config{
		{Enabled: true, URL: "socks5://proxy:1080", Username: strings.Repeat("a", 256), Services: []string{"telegram_user"}},
		{Enabled: true, URL: "socks5h://proxy:1080", Username: "user", Password: strings.Repeat("a", 256), Services: []string{"telegram_user"}},
		{Enabled: true, URL: "http://proxy:7890", Services: []string{"115"}},
		{Enabled: true, URL: "http://proxy:7890"},
		{Enabled: true, Services: []string{"tmdb"}},
		{URL: "http://proxy:7890", Password: "secret"},
	} {
		if _, _, err := Prepare(config); err == nil {
			t.Fatal("accepted invalid selection or authentication")
		}
	}
}

func TestTelegramUserAndBotProxySelectionsRemainIndependent(t *testing.T) {
	manager := New()
	defer manager.CloseIdleConnections()
	request, _ := http.NewRequest(http.MethodGet, "https://api.telegram.org/", nil)
	for _, selected := range []string{"telegram_user", "telegram_bot"} {
		applyTestConfig(t, manager, Config{Enabled: true, URL: "http://proxy.example:7890", Services: []string{selected}})
		for _, scope := range []string{"telegram_user", "telegram_bot"} {
			u, err := manager.Transport(scope).Proxy(request)
			if err != nil || (u != nil) != (scope == selected) {
				t.Fatal("user and Bot proxy scopes were coupled", scope, selected, err)
			}
		}
	}
}

func TestPolicySnapshotsAreImmutableDuringConcurrentUpdates(t *testing.T) {
	manager := New()
	defer manager.CloseIdleConnections()
	_, policy, err := Prepare(Config{Enabled: true, URL: "http://proxy:7890", Services: []string{"tmdb"}})
	if err != nil {
		t.Fatal(err)
	}
	manager.Apply(policy)
	transport := manager.Transport("tmdb")
	request, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://api.themoviedb.org/3", nil)
	u, _ := transport.Proxy(request)
	u.Host = "changed.invalid"
	u2, _ := transport.Proxy(request)
	if u2.Host != "proxy:7890" {
		t.Fatal("caller mutated live policy URL")
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				manager.Apply(policy)
				u, err := manager.Transport("tmdb").Proxy(request)
				if err != nil || u == nil || !strings.HasPrefix(u.String(), "http://proxy:") {
					t.Error("inconsistent policy snapshot")
					return
				}
			}
		}()
	}
	wg.Wait()
}
