package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"sync"
	"time"
)

type DirectLinkValue struct {
	URL    string
	Report PlaybackResolution
}
type DirectLinkCacheEntry struct {
	ID             string    `json:"id"`
	User           string    `json:"user"`
	MediaSource    string    `json:"mediaSource"`
	Account        string    `json:"account"`
	Mode           string    `json:"mode"`
	CDN            string    `json:"cdn"`
	ExpiresAt      time.Time `json:"expiresAt"`
	Hits           uint64    `json:"hits"`
	URL            string    `json:"url"`
	CreatedAt      time.Time `json:"createdAt"`
	TraceID        string    `json:"traceId"`
	RequestHeaders string    `json:"requestHeaders"`
	LookupHeaders  string    `json:"lookupHeaders"`
}
type DirectLinkCacheStatus struct {
	Entries []DirectLinkCacheEntry `json:"entries"`
	Hits    uint64                 `json:"hits"`
	Misses  uint64                 `json:"misses"`
}
type directLinkEntry struct {
	value DirectLinkValue
	view  DirectLinkCacheEntry
}
type directLinkFlight struct {
	done    chan struct{}
	value   DirectLinkValue
	err     error
	expires time.Time
}

// One cache per immutable proxy configuration. Full URLs are administrator-only.
type DirectLinkCache struct {
	mu           sync.Mutex
	entries      map[string]directLinkEntry
	flights      map[string]*directLinkFlight
	generation   uint64
	hits, misses uint64
}

func DirectLinkKey(parts ...string) string {
	data, _ := json.Marshal(parts)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
func (c *DirectLinkCache) prune(now time.Time) {
	for key, entry := range c.entries {
		if !now.Before(entry.view.ExpiresAt) {
			delete(c.entries, key)
		}
	}
}
func (c *DirectLinkCache) Status() DirectLinkCacheStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.prune(time.Now())
	out := DirectLinkCacheStatus{Entries: make([]DirectLinkCacheEntry, 0, len(c.entries)), Hits: c.hits, Misses: c.misses}
	for _, entry := range c.entries {
		out.Entries = append(out.Entries, entry.view)
	}
	sort.Slice(out.Entries, func(i, j int) bool { return out.Entries[i].ExpiresAt.Before(out.Entries[j].ExpiresAt) })
	return out
}
func (c *DirectLinkCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.generation++
	c.entries = nil
	// Existing producers can finish for their callers but cannot refill the cleared cache.
	c.flights = nil
	c.hits, c.misses = 0, 0
}
func (c *DirectLinkCache) Remove(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.generation++
	delete(c.flights, key)
	delete(c.entries, key)
}
func (c *DirectLinkCache) Get(ctx context.Context, key string, ttl time.Duration, view DirectLinkCacheEntry, fetch func(context.Context) (DirectLinkValue, error)) (DirectLinkValue, bool, time.Time, error) {
	if err := ctx.Err(); err != nil {
		return DirectLinkValue{}, false, time.Time{}, err
	}
	c.mu.Lock()
	c.prune(time.Now())
	if entry, ok := c.entries[key]; ok && ttl > 0 {
		entry.view.Hits++
		c.hits++
		c.entries[key] = entry
		c.mu.Unlock()
		return entry.value, true, entry.view.ExpiresAt, nil
	}
	if flight, ok := c.flights[key]; ok {
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			return DirectLinkValue{}, false, time.Time{}, ctx.Err()
		case <-flight.done:
			c.mu.Lock()
			if flight.err == nil {
				c.hits++
				if e, ok := c.entries[key]; ok {
					e.view.Hits++
					c.entries[key] = e
				}
			}
			c.mu.Unlock()
			return flight.value, true, flight.expires, flight.err
		}
	}
	if c.flights == nil {
		c.flights = make(map[string]*directLinkFlight)
	}
	flight := &directLinkFlight{done: make(chan struct{})}
	c.flights[key] = flight
	generation := c.generation
	c.misses++
	c.mu.Unlock()
	value, err := fetch(ctx)
	if err == nil {
		err = ctx.Err()
	}
	if err == nil && value.URL == "" {
		err = fmt.Errorf("直链为空")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var expires time.Time
	if ttl > 0 {
		expires = time.Now().Add(ttl)
	}
	if err == nil && ttl > 0 && generation == c.generation {
		if c.entries == nil {
			c.entries = make(map[string]directLinkEntry)
		}
		if len(c.entries) >= 512 {
			oldest := ""
			for k, e := range c.entries {
				if oldest == "" || e.view.ExpiresAt.Before(c.entries[oldest].view.ExpiresAt) {
					oldest = k
				}
			}
			delete(c.entries, oldest)
		}
		view.ID = key
		view.URL = value.URL
		view.CreatedAt = expires.Add(-ttl)
		view.ExpiresAt = expires
		view.Account = value.Report.AccountLabel
		view.Mode = value.Report.Mode
		if parsed, parseErr := url.Parse(value.URL); parseErr == nil {
			view.CDN = parsed.Hostname()
		}
		c.entries[key] = directLinkEntry{value: value, view: view}
	}
	flight.value, flight.err, flight.expires = value, err, expires
	if c.flights[key] == flight {
		delete(c.flights, key)
	}
	close(flight.done)
	return value, false, expires, err
}
