package p115

import (
	"sync"
	"time"
)

// Playback metadata has its own credential-scoped throttling and backoff. It
// neither waits behind background scans nor lets one account throttle another.
// Children share the HTTP transport for connection reuse, never credentials.
type playbackReadPool struct {
	mu       sync.Mutex
	clients  map[string]*ShareClient
	overflow *ShareClient
}

func (c *ShareClient) playbackReader(cookie string) *ShareClient {
	key := playbackCredentialHash(cookie)
	c.playbackReaders.mu.Lock()
	defer c.playbackReaders.mu.Unlock()
	if child := c.playbackReaders.clients[key]; child != nil {
		return child
	}
	if len(c.playbackReaders.clients) >= 128 && c.playbackReaders.overflow != nil {
		return c.playbackReaders.overflow
	}
	child := &ShareClient{HTTP: c.HTTP, WebURL: c.WebURL, AppURL: c.AppURL, RequestInterval: c.RequestInterval, ReadRetryDelays: append([]time.Duration(nil), c.ReadRetryDelays...), requestGate: make(chan struct{}, 1)}
	if len(c.playbackReaders.clients) >= 128 {
		c.playbackReaders.overflow = child
		return child
	}
	if c.playbackReaders.clients == nil {
		c.playbackReaders.clients = make(map[string]*ShareClient)
	}
	c.playbackReaders.clients[key] = child
	return child
}
