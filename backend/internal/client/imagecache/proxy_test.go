package imagecache

import (
	"context"
	"net/http"
	"testing"

	"strm115/backend/internal/netproxy"
)

func TestCoverClientsKeepIndependentDynamicProxyScopes(t *testing.T) {
	previous := netproxy.Default
	manager := netproxy.New()
	netproxy.Default = manager
	t.Cleanup(func() { manager.CloseIdleConnections(); netproxy.Default = previous })
	constructors := map[string]func(string) (*Cache, error){
		"tmdb":    func(root string) (*Cache, error) { return New(root, DefaultMaxBytes) },
		"bangumi": NewBangumi, "mikan": NewMikan, "anibt": NewAnibt,
		"garden": NewGarden, "gying": NewGying, "woniu": NewWoniu,
	}
	for selected, constructor := range constructors {
		cache, err := constructor(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		_, policy, err := netproxy.Prepare(netproxy.Config{Enabled: true, URL: "http://proxy.example:7890", Services: []string{selected}})
		if err != nil {
			t.Fatal(err)
		}
		manager.Apply(policy)
		req, _ := http.NewRequest("GET", "https://covers.example/poster.jpg", nil)
		transport := cache.http.Transport.(*http.Transport)
		proxy, err := transport.Proxy(req)
		if err != nil || proxy == nil || proxy.Host != "proxy.example:7890" {
			t.Fatal("cover used the wrong service policy", selected, proxy, err)
		}
		manager.Apply(nil)
		if proxy, err := transport.Proxy(req); err != nil || proxy != nil {
			t.Fatal("existing cover client ignored disabled policy", selected, proxy, err)
		}
	}
	emby, err := NewEmby(t.TempDir(), func(context.Context, string, string) (*http.Request, error) { return nil, nil })
	if err != nil {
		t.Fatal(err)
	}
	if emby.http.Transport.(*http.Transport).Proxy != nil {
		t.Fatal("Emby covers must remain explicitly direct")
	}
}
