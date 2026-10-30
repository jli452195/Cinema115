package tguser

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/netproxy"
)

func TestAccountConnectionUsesTelegramUserProxy(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	requests := make(chan *http.Request, 1)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case requests <- r:
		default:
		}
		// Stop before MTProto login; the test must never contact Telegram.
		w.WriteHeader(http.StatusBadGateway)
		cancel()
	}))
	defer proxy.Close()
	_, policy, err := netproxy.Prepare(netproxy.Config{
		Enabled: true, URL: proxy.URL, Services: []string{"telegram_user"},
	})
	if err != nil {
		t.Fatal(err)
	}
	manager := netproxy.New()
	manager.Apply(policy)
	original := netproxy.Default
	netproxy.Default = manager
	t.Cleanup(func() {
		netproxy.Default = original
		manager.CloseIdleConnections()
	})
	c := testClient(t)
	if err := c.Save(Config{APIID: 123, APIHash: strings.Repeat("a", 32)}); err != nil {
		t.Fatal(err)
	}
	// The SDK may return nil when cancellation ends its connection loop before
	// the operation callback starts; login state must still remain unchanged.
	_ = c.Check(ctx)
	select {
	case req := <-requests:
		if req.Method != http.MethodConnect {
			t.Fatalf("MTProto requires a CONNECT tunnel, got %s", req.Method)
		}
		host, port, err := net.SplitHostPort(req.Host)
		if err != nil || net.ParseIP(host) == nil || port != "443" {
			t.Fatalf("invalid Telegram DC target %q: %v", req.Host, err)
		}
	default:
		t.Fatal("the account connection bypassed the Telegram user proxy scope")
	}
	if c.View().Authorized {
		t.Fatal("failed network connection must not change the login state")
	}
}
