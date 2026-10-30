package netproxy

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testTunnelProxy(t *testing.T, label string, calls *atomic.Int32) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodConnect || r.Host != "telegram.example.test:443" || r.Header.Get("Proxy-Authorization") != "Basic dXNlcjpzZWNyZXQ=" {
			t.Errorf("unexpected CONNECT method=%s target=%s", r.Method, r.Host)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
		// Deliberately send tunnel data with the headers to exercise buffering.
		_, _ = io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n"+label)
		_, _ = io.Copy(conn, conn)
	}))
	t.Cleanup(server.Close)
	return server
}

func TestHTTPDialerUsesAuthSnapshotAndPreservesBufferedTunnel(t *testing.T) {
	manager := New()
	t.Cleanup(manager.CloseIdleConnections)
	var firstCalls, secondCalls atomic.Int32
	first := testTunnelProxy(t, "A", &firstCalls)
	second := testTunnelProxy(t, "B", &secondCalls)
	config := Config{Enabled: true, URL: first.URL, Username: "user", Password: "secret", Services: []string{"telegram_user"}}
	applyTestConfig(t, manager, config)
	old := manager.Dialer("telegram_user")
	config.URL = second.URL
	applyTestConfig(t, manager, config)
	for _, operation := range []struct {
		dial func(context.Context, string, string) (net.Conn, error)
		want string
	}{{old, "A"}, {manager.Dialer("telegram_user"), "B"}} {
		ctx, cancel := context.WithCancel(context.Background())
		conn, err := operation.dial(ctx, "tcp", "telegram.example.test:443")
		cancel() // A completed dial must not tie the live tunnel to this context.
		if err != nil {
			t.Fatal(err)
		}
		_ = conn.SetDeadline(time.Now().Add(time.Second))
		data := make([]byte, 1)
		_, err = io.ReadFull(conn, data)
		if err == nil && string(data) != operation.want {
			_ = conn.Close()
			t.Fatalf("lost buffered data or used the wrong policy snapshot: %q", data)
		}
		if err == nil {
			_, err = io.WriteString(conn, "x")
		}
		if err == nil {
			_, err = io.ReadFull(conn, data[0:1])
		}
		_ = conn.Close()
		if err != nil || string(data) != "x" {
			t.Fatalf("tunnel stopped after dial context cancellation: %q %v", data, err)
		}
	}
	if firstCalls.Load() != 1 || secondCalls.Load() != 1 {
		t.Fatal("operation switched route after a policy change")
	}
}

func TestHTTPDialerCancelsStalledHandshakeAndDoesNotLeakProxyResponse(t *testing.T) {
	for _, timeout := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "timeout"}[timeout], func(t *testing.T) {
			closed := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				conn, _, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
				_, _ = io.Copy(io.Discard, conn)
				close(closed)
			}))
			defer server.Close()
			manager := New()
			defer manager.CloseIdleConnections()
			applyTestConfig(t, manager, Config{Enabled: true, URL: server.URL, Services: []string{"telegram_user"}})
			ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
			defer cancel()
			want := context.DeadlineExceeded
			if !timeout {
				stop := time.AfterFunc(50*time.Millisecond, cancel)
				defer stop.Stop()
				want = context.Canceled
			}
			conn, err := manager.Dialer("telegram_user")(ctx, "tcp", "telegram.example.test:443")
			if conn != nil || !errors.Is(err, want) {
				t.Fatalf("handshake ignored cancellation: %v", err)
			}
			select {
			case <-closed:
			case <-time.After(time.Second):
				t.Fatal("canceled handshake left a connection open")
			}
		})
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusProxyAuthRequired)
		_, _ = io.WriteString(w, "private-remote-content")
	}))
	defer server.Close()
	manager := New()
	defer manager.CloseIdleConnections()
	applyTestConfig(t, manager, Config{Enabled: true, URL: server.URL, Password: "private-password", Username: "user", Services: []string{"telegram_user"}})
	conn, err := manager.Dialer("telegram_user")(context.Background(), "tcp", "telegram.example.test:443")
	if conn != nil || err == nil || !strings.Contains(err.Error(), "407") || strings.Contains(err.Error(), "private-") {
		t.Fatalf("proxy refusal was ignored or leaked credentials: %v", err)
	}
}

func TestHTTPSDialerVerifiesProxyCertificate(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { requests.Add(1) }))
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.StartTLS()
	defer server.Close()
	manager := New()
	defer manager.CloseIdleConnections()
	applyTestConfig(t, manager, Config{Enabled: true, URL: server.URL, Services: []string{"telegram_user"}})
	conn, err := manager.Dialer("telegram_user")(context.Background(), "tcp", "telegram.example.test:443")
	if conn != nil || err == nil || requests.Load() != 0 {
		t.Fatal("untrusted HTTPS proxy certificate was accepted")
	}
}

func TestSOCKS5DialerUsesRemoteDNSAndAuthentication(t *testing.T) {
	for _, scheme := range []string{"socks5", "socks5h"} {
		t.Run(scheme, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			done := make(chan error, 1)
			go func() {
				conn, err := listener.Accept()
				if err != nil {
					done <- err
					return
				}
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
				done <- serveTestSOCKS5(conn)
			}()
			manager := New()
			defer manager.CloseIdleConnections()
			applyTestConfig(t, manager, Config{Enabled: true, URL: scheme + "://" + listener.Addr().String(), Username: "user", Password: "secret", Services: []string{"telegram_user"}})
			conn, err := manager.Dialer("telegram_user")(context.Background(), "tcp", "telegram.example.test:443")
			if err != nil {
				_ = listener.Close()
				<-done
				t.Fatal(err)
			}
			data := make([]byte, 2)
			_, err = io.ReadFull(conn, data)
			_ = conn.Close()
			if serverErr := <-done; err != nil || serverErr != nil || string(data) != "ok" {
				t.Fatal("invalid SOCKS5 tunnel", err, serverErr)
			}
		})
	}
}

func serveTestSOCKS5(conn net.Conn) error {
	r := bufio.NewReader(conn)
	read := func(want string) error {
		data := make([]byte, len(want))
		if _, err := io.ReadFull(r, data); err != nil {
			return err
		}
		if string(data) != want {
			return errors.New("unexpected SOCKS5 request")
		}
		return nil
	}
	if err := read(string([]byte{5, 2, 0, 2})); err != nil {
		return err
	}
	_, _ = conn.Write([]byte{5, 2})
	if err := read(string([]byte{1, 4}) + "user" + string([]byte{6}) + "secret"); err != nil {
		return err
	}
	_, _ = conn.Write([]byte{1, 0})
	if err := read(string([]byte{5, 1, 0, 3, 21}) + "telegram.example.test"); err != nil {
		return err
	}
	var port [2]byte
	if _, err := io.ReadFull(r, port[:]); err != nil || binary.BigEndian.Uint16(port[:]) != 443 {
		return errors.New("unexpected SOCKS5 target port")
	}
	_, err := conn.Write(append([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 1}, []byte("ok")...))
	return err
}

func TestSOCKS5DialerCancelsStalledHandshake(t *testing.T) {
	for _, phase := range []string{"greeting", "connect"} {
		t.Run(phase, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				conn, err := listener.Accept()
				if err != nil {
					done <- err
					return
				}
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
				header := make([]byte, 3)
				_, err = io.ReadFull(conn, header)
				if err == nil && phase == "connect" {
					_, err = conn.Write([]byte{5, 0})
					if err == nil {
						_, err = io.ReadFull(conn, make([]byte, 7+len("telegram.example.test")))
					}
				}
				cancel()
				if err == nil {
					_, err = io.Copy(io.Discard, conn)
				}
				done <- err
			}()
			manager := New()
			defer manager.CloseIdleConnections()
			applyTestConfig(t, manager, Config{Enabled: true, URL: "socks5://" + listener.Addr().String(), Services: []string{"telegram_user"}})
			conn, err := manager.Dialer("telegram_user")(ctx, "tcp", "telegram.example.test:443")
			_ = listener.Close()
			serverErr := <-done
			if conn != nil || !errors.Is(err, context.Canceled) || serverErr != nil {
				t.Fatal("stalled SOCKS5 handshake did not release its connection", err, serverErr)
			}
		})
	}
}

func TestNativeDialerKeepsPrivateTargetsAndUnselectedScopesDirect(t *testing.T) {
	t.Setenv("ALL_PROXY", "socks5://environment.invalid:1080")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	manager := New()
	defer manager.CloseIdleConnections()
	applyTestConfig(t, manager, Config{Enabled: true, URL: "http://unavailable.invalid:7890", Services: []string{"telegram_user"}})
	for _, scope := range []string{"telegram_user", "telegram_bot", "115"} {
		conn, err := manager.Dialer(scope)(context.Background(), "tcp", listener.Addr().String())
		if err != nil {
			t.Fatal("local target used proxy", scope, err)
		}
		_ = conn.Close()
		server, err := listener.Accept()
		if err != nil {
			t.Fatal(err)
		}
		_ = server.Close()
	}
}
