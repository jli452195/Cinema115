package p115

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPlaybackRangeReusesConnectionAndResumesTLS(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "" || r.Header.Get("Range") != "bytes=1-3" {
			t.Error("wrong range or leaked credentials")
		}
		w.Header().Set("Content-Range", "bytes 1-3/100")
		w.WriteHeader(http.StatusPartialContent)
		w.(http.Flusher).Flush() // Exercise chunked EOF, necessary for connection reuse.
		_, _ = io.WriteString(w, "abc")
	}))
	server.TLS = &tls.Config{MaxVersion: tls.VersionTLS12}
	server.StartTLS()
	defer server.Close()
	transport := newPlaybackTransport()
	defer transport.CloseIdleConnections()
	transport.Proxy = nil
	transport.TLSClientConfig.RootCAs = x509.NewCertPool()
	transport.TLSClientConfig.RootCAs.AddCert(server.Certificate())
	transport.TLSClientConfig.ServerName = "127.0.0.1"
	transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	client := NewShareClient()
	client.HTTP.Transport = transport
	var timing PlaybackTiming
	ctx := WithPlaybackTiming(context.Background(), func(v PlaybackTiming) {
		if v.HTTP {
			timing = v
		}
	})
	for i := 0; i < 3; i++ {
		if i == 2 {
			transport.CloseIdleConnections()
		}
		hash, err := client.instantRangeHash(ctx, "https://cdn.115cdn.net/video", 1, 3, 100)
		if err != nil || hash != "A9993E364706816ABA3E25717850C26C9CD0D89D" {
			t.Fatalf("range %d: %s, %v", i, hash, err)
		}
		if i == 1 && !timing.Reused {
			t.Fatal("range connection was not reused")
		}
		if i == 2 && (timing.Reused || !timing.TLSResumed) {
			t.Fatal("new connection did not resume TLS")
		}
	}
}
