package p115

import (
	"crypto/tls"
	"net"
	"net/http"
	"time"
)

// Shared by 115 clients and their request-local HTTP wrappers, independently
// of unrelated providers' idle connections. No cookie jar or request headers
// are stored here; account credentials remain on each individual request.
var sharedPlaybackTransport = newPlaybackTransport()

func newPlaybackTransport() *http.Transport {
	return &http.Transport{
		Proxy:                 nil,
		DialContext:           (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          128,
		MaxIdleConnsPerHost:   8,
		IdleConnTimeout:       3 * time.Minute,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
		TLSClientConfig: &tls.Config{
			ClientSessionCache: tls.NewLRUClientSessionCache(64),
		},
	}
}

// Preserve injected transports while keeping request-local wrappers away from
// environment proxies when no transport was supplied.
func (c *ShareClient) requestTransport() http.RoundTripper {
	if c.HTTP != nil && c.HTTP.Transport != nil {
		return c.HTTP.Transport
	}
	return sharedPlaybackTransport
}
