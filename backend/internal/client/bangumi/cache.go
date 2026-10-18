package bangumi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"sync"
	"time"
)

type requestSpec struct {
	key, method, path string
	body              []byte
	ttl               time.Duration
	normalize         func([]byte) ([]byte, error)
}

type cacheEntry struct {
	data                                      []byte // immutable, normalized JSON; never exposed to callers
	version, etag, modified, cacheControl     string
	fetched, expires, retainUntil, lastAccess time.Time
	retryAt                                   time.Time
	lastError                                 error
	failures                                  int
}

type readResult struct {
	data []byte
	meta CacheMeta
}
type flight struct {
	done     chan struct{}
	result   readResult
	err      error
	canceled bool
}

// Client is safe to share across all anime discovery handlers. Its owner must
// construct one instance at API wiring time; there are no background workers.
type Client struct {
	http        *http.Client
	base        string
	now         func() time.Time
	interval    time.Duration
	gate        chan struct{}
	lastRequest time.Time // guarded by gate
	mu          sync.Mutex
	cache       map[string]cacheEntry
	flights     map[string]*flight
	cacheBytes  int
	cooldown    time.Time
}

func New() *Client {
	return &Client{http: newHTTPClient(), base: baseURL, now: time.Now, interval: time.Second, gate: make(chan struct{}, 1), cache: make(map[string]cacheEntry, maxCacheEntries), flights: make(map[string]*flight, maxFlights)}
}

func entryResult(e cacheEntry, now time.Time, err error, retry time.Time) (readResult, error) {
	if len(e.data) == 0 {
		return readResult{}, err
	}
	out := readResult{data: e.data, meta: CacheMeta{FetchedAt: e.fetched.UTC().UnixMilli(), ExpiresAt: e.expires.UTC().UnixMilli(), Stale: !now.Before(e.expires) || err != nil, DataVersion: e.version}}
	if err != nil {
		out.meta.Issue = issueFrom(err, retry)
	}
	return out, nil
}

// The first caller does I/O synchronously. Waiters have independent contexts;
// a canceled leader can be replaced by a live waiter, while actual upstream
// failures are shared without an immediate retry. No goroutine is detached.
func (c *Client) cachedRead(ctx context.Context, spec requestSpec) (readResult, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	for {
		if err := ctx.Err(); err != nil {
			return readResult{}, err
		}
		now := c.now()
		c.mu.Lock()
		old := c.cache[spec.key]
		if !old.retainUntil.IsZero() && !now.Before(old.retainUntil) {
			c.deleteLocked(spec.key)
			old = cacheEntry{}
		}
		if len(old.data) > 0 && now.Before(old.expires) {
			old.lastAccess = now
			c.cache[spec.key] = old
			c.mu.Unlock()
			return entryResult(old, now, nil, time.Time{})
		}
		if active, ok := c.flights[spec.key]; ok {
			c.mu.Unlock()
			select {
			case <-ctx.Done():
				return readResult{}, ctx.Err()
			case <-active.done:
				if err := ctx.Err(); err != nil {
					return readResult{}, err
				}
				if active.canceled {
					continue
				}
				return active.result, active.err
			}
		}
		if now.Before(c.cooldown) {
			err := &UpstreamError{Kind: ErrCooldown, RetryAfterAt: c.cooldown}
			retry := c.cooldown
			c.mu.Unlock()
			return entryResult(old, now, err, retry)
		}
		if old.lastError != nil && now.Before(old.retryAt) {
			c.mu.Unlock()
			return entryResult(old, now, old.lastError, old.retryAt)
		}
		if len(c.flights) >= maxFlights {
			c.mu.Unlock()
			return entryResult(old, now, ErrBusy, time.Time{})
		}
		active := &flight{done: make(chan struct{})}
		c.flights[spec.key] = active
		c.mu.Unlock()
		entry, store, err := c.fetch(ctx, spec, old)
		if ctx.Err() != nil {
			// Cancellation is not an outage and never replaces a good snapshot
			// or installs failure backoff for another caller.
			err = ctx.Err()
		}
		c.mu.Lock()
		now = c.now()
		active.canceled = ctx.Err() != nil
		if active.canceled {
			err = ctx.Err()
		}
		if err == nil {
			if store {
				c.putLocked(spec.key, entry, now)
			} else {
				c.deleteLocked(spec.key)
			}
			active.result, active.err = entryResult(entry, now, nil, time.Time{})
		} else if active.canceled {
			active.err = err
		} else {
			if !old.retainUntil.IsZero() && !now.Before(old.retainUntil) {
				old = cacheEntry{}
			}
			old.lastAccess = now
			old.failures++
			old.lastError = err
			old.retryAt = failureRetry(now, old.failures, err)
			if old.retainUntil.IsZero() {
				old.retainUntil = now.Add(retention)
			}
			c.putLocked(spec.key, old, now)
			active.result, active.err = entryResult(old, now, err, old.retryAt)
		}
		delete(c.flights, spec.key)
		close(active.done)
		c.mu.Unlock()
		return active.result, active.err
	}
}

func failureRetry(now time.Time, failures int, err error) time.Time {
	backoff := []time.Duration{15 * time.Second, 30 * time.Second, time.Minute, 5 * time.Minute}
	if failures > len(backoff) {
		failures = len(backoff)
	}
	retry := now.Add(backoff[failures-1])
	if errors.Is(err, ErrNotFound) || errors.Is(err, ErrAccess) {
		retry = now.Add(5 * time.Minute)
	}
	var upstream *UpstreamError
	if errors.As(err, &upstream) && upstream.RetryAfterAt.After(retry) {
		retry = upstream.RetryAfterAt
	}
	return retry
}

func (c *Client) deleteLocked(key string) {
	c.cacheBytes -= len(c.cache[key].data)
	delete(c.cache, key)
}

func (c *Client) putLocked(key string, e cacheEntry, now time.Time) {
	c.deleteLocked(key)
	for k, entry := range c.cache {
		if !now.Before(entry.retainUntil) {
			c.deleteLocked(k)
		}
	}
	for len(c.cache) >= maxCacheEntries || c.cacheBytes+len(e.data) > maxCacheBytes {
		var oldest string
		var accessed time.Time
		for k, entry := range c.cache {
			if oldest == "" || entry.lastAccess.Before(accessed) {
				oldest, accessed = k, entry.lastAccess
			}
		}
		if oldest == "" {
			return
		}
		c.deleteLocked(oldest)
	}
	c.cache[key] = e
	c.cacheBytes += len(e.data)
}

func payloadVersion(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
