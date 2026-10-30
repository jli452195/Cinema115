package netproxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"

	"golang.org/x/net/proxy"
)

const proxyConnectTimeout = 10 * time.Second

// Dialer fixes the current policy for one operation, including reconnects and DC
// migration. It never inherits environment settings or falls back after errors.
func (m *Manager) Dialer(scope string) func(context.Context, string, string) (net.Conn, error) {
	direct := &net.Dialer{Timeout: proxyConnectTimeout, KeepAlive: 30 * time.Second}
	policy := m.state.Load()
	if policy == nil || !KnownService(scope) || !policy.selected[scope] {
		return direct.DialContext
	}
	u := *policy.proxy
	port := u.Port()
	if port == "" {
		port = map[string]string{"http": "80", "https": "443", "socks5": "1080", "socks5h": "1080"}[u.Scheme]
	}
	proxyAddress := net.JoinHostPort(u.Hostname(), port)
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return nil, fmt.Errorf("代理目标地址无效: %w", err)
		}
		if alwaysDirect(host) {
			return direct.DialContext(ctx, network, address)
		}
		if network != "tcp" && network != "tcp4" && network != "tcp6" {
			return nil, fmt.Errorf("代理不支持网络类型 %q", network)
		}
		ctx, cancel := context.WithTimeout(ctx, proxyConnectTimeout)
		defer cancel()
		if u.Scheme == "socks5" || u.Scheme == "socks5h" {
			var auth *proxy.Auth
			if u.User != nil {
				password, _ := u.User.Password()
				auth = &proxy.Auth{User: u.User.Username(), Password: password}
			}
			dialer, err := proxy.SOCKS5("tcp", proxyAddress, auth, direct)
			if err != nil {
				return nil, fmt.Errorf("创建 SOCKS5 代理连接: %w", err)
			}
			conn, err := dialer.(proxy.ContextDialer).DialContext(ctx, network, address)
			if err != nil && ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return conn, err
		}
		return dialHTTPProxy(ctx, direct, &u, proxyAddress, address)
	}
}

// CONNECT reads may include the first tunnel bytes. Keep the buffered reader so
// a successful handshake never discards protocol data.
type bufferedProxyConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedProxyConn) Read(b []byte) (int, error) { return c.reader.Read(b) }

func dialHTTPProxy(ctx context.Context, dialer *net.Dialer, u *url.URL, proxyAddress, address string) (net.Conn, error) {
	raw, err := dialer.DialContext(ctx, "tcp", proxyAddress)
	if err != nil {
		return nil, fmt.Errorf("连接 HTTP 代理: %w", err)
	}
	if deadline, ok := ctx.Deadline(); ok {
		if err := raw.SetDeadline(deadline); err != nil {
			_ = raw.Close()
			return nil, fmt.Errorf("设置代理连接超时: %w", err)
		}
	}
	// Cancellation must interrupt a stalled handshake and finish before the
	// connection is handed to MTProto; canceling the dial context later is safe.
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = raw.Close(); close(done) })
	conn := raw
	tunnel, err := func() (net.Conn, error) {
		if u.Scheme == "https" {
			secure := tls.Client(raw, &tls.Config{ServerName: u.Hostname(), MinVersion: tls.VersionTLS12})
			if err := secure.HandshakeContext(ctx); err != nil {
				return nil, fmt.Errorf("验证 HTTPS 代理: %w", err)
			}
			conn = secure
		}
		request := &http.Request{Method: http.MethodConnect, URL: &url.URL{Host: address}, Host: address, Header: make(http.Header)}
		if u.User != nil {
			password, _ := u.User.Password()
			request.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(u.User.Username()+":"+password)))
		}
		if err := request.Write(conn); err != nil {
			return nil, fmt.Errorf("发送代理隧道请求: %w", err)
		}
		reader := bufio.NewReader(conn)
		response, err := http.ReadResponse(reader, request)
		if err != nil {
			return nil, fmt.Errorf("读取代理隧道响应: %w", err)
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			// Never include the proxy's response body/status text or credentials.
			return nil, fmt.Errorf("HTTP 代理拒绝隧道（HTTP %d）", response.StatusCode)
		}
		return &bufferedProxyConn{Conn: conn, reader: reader}, nil
	}()
	if !stop() {
		<-done
	}
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	if err == nil {
		err = conn.SetDeadline(time.Time{})
	}
	if err != nil {
		_ = raw.Close()
		return nil, err
	}
	return tunnel, nil
}
