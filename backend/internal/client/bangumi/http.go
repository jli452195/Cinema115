package bangumi

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func (c *Client) fetch(ctx context.Context, spec requestSpec, old cacheEntry) (cacheEntry, bool, error) {
	select {
	case c.gate <- struct{}{}:
	case <-ctx.Done():
		return cacheEntry{}, false, ctx.Err()
	}
	defer func() { <-c.gate }()
	if delay := c.lastRequest.Add(c.interval).Sub(c.now()); delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return cacheEntry{}, false, ctx.Err()
		}
	}
	// A preceding request may have published site-wide Retry-After while this
	// one waited for the gate. Recheck before issuing any network request.
	c.mu.Lock()
	retry := c.cooldown
	c.mu.Unlock()
	if c.now().Before(retry) {
		return cacheEntry{}, false, &UpstreamError{Kind: ErrCooldown, RetryAfterAt: retry}
	}
	req, err := http.NewRequestWithContext(ctx, spec.method, c.base+spec.path, bytes.NewReader(spec.body))
	if err != nil {
		return cacheEntry{}, false, &UpstreamError{Kind: ErrInput, Cause: err}
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")
	if spec.method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
	}
	conditional := false
	if spec.method == http.MethodGet && len(old.data) > 0 {
		if old.etag != "" {
			req.Header.Set("If-None-Match", old.etag)
			conditional = true
		}
		if old.modified != "" {
			req.Header.Set("If-Modified-Since", old.modified)
			conditional = true
		}
	}
	sent := c.now()
	c.lastRequest = sent
	response, err := c.http.Do(req)
	if err != nil {
		return cacheEntry{}, false, networkError(ctx, err)
	}
	defer response.Body.Close()
	received := c.now()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusNotModified {
		kind := ErrUnavailable
		switch response.StatusCode {
		case http.StatusNotFound:
			kind = ErrNotFound
		case http.StatusUnauthorized, http.StatusForbidden:
			kind = ErrAccess
		case http.StatusTooManyRequests:
			kind = ErrCooldown
		case http.StatusBadRequest, http.StatusUnprocessableEntity:
			kind = ErrInvalid
		}
		retry := retryAfter(response.Header.Get("Retry-After"), received)
		if response.StatusCode == http.StatusTooManyRequests && !retry.After(received) {
			retry = received.Add(time.Minute)
		}
		if retry.After(received) && (response.StatusCode == http.StatusTooManyRequests || response.StatusCode == http.StatusServiceUnavailable) {
			c.mu.Lock()
			if retry.After(c.cooldown) {
				c.cooldown = retry
			}
			c.mu.Unlock()
		}
		return cacheEntry{}, false, &UpstreamError{Kind: kind, Status: response.StatusCode, RetryAfterAt: retry}
	}
	cacheControl := response.Header.Get("Cache-Control")
	if len(cacheControl) > 4096 {
		cacheControl = "no-store"
	}
	var data []byte
	if response.StatusCode == http.StatusNotModified {
		if !conditional || len(old.data) == 0 {
			return cacheEntry{}, false, &UpstreamError{Kind: ErrInvalid, Status: response.StatusCode}
		}
		data = old.data
		if cacheControl == "" {
			cacheControl = old.cacheControl
		}
	} else {
		body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
		if err != nil {
			return cacheEntry{}, false, networkError(ctx, err)
		}
		if len(body) > maxResponseBytes {
			return cacheEntry{}, false, &UpstreamError{Kind: ErrInvalid}
		}
		data, err = spec.normalize(body)
		if err != nil {
			return cacheEntry{}, false, &UpstreamError{Kind: ErrInvalid, Status: response.StatusCode, Cause: err}
		}
		if len(data) > maxResponseBytes {
			return cacheEntry{}, false, &UpstreamError{Kind: ErrInvalid}
		}
	}
	if err := ctx.Err(); err != nil {
		return cacheEntry{}, false, err
	}
	received = c.now()
	etag, modified := response.Header.Get("ETag"), response.Header.Get("Last-Modified")
	if response.StatusCode == http.StatusNotModified {
		if etag == "" {
			etag = old.etag
		}
		if modified == "" {
			modified = old.modified
		}
	}
	// Excessive or malformed validators are optional metadata, never required
	// for a successful body and never forwarded into later request headers.
	if len(etag) > 1024 || strings.ContainsAny(etag, "\r\n") {
		etag = ""
	}
	if len(modified) > 128 {
		modified = ""
	}
	if _, err := http.ParseTime(modified); err != nil {
		modified = ""
	}
	lifetime, store := freshness(cacheControl, response.Header, spec.ttl, sent, received)
	return cacheEntry{data: data, version: payloadVersion(data), etag: etag, modified: modified, cacheControl: cacheControl, fetched: received, expires: received.Add(lifetime), retainUntil: received.Add(retention), lastAccess: received}, store, nil
}

func networkError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() || errors.Is(err, context.DeadlineExceeded) {
		return &UpstreamError{Kind: ErrTimeout, Cause: err}
	}
	return &UpstreamError{Kind: ErrNetwork, Cause: err}
}

// Freshness is capped locally and subtracts the current upstream age. Hitting
// a CDN must not grant a new full max-age to an already aged response.
func freshness(control string, headers http.Header, capTTL time.Duration, sent, received time.Time) (time.Duration, bool) {
	ttl := capTTL
	store := true
	for _, directive := range strings.Split(control, ",") {
		parts := strings.SplitN(strings.TrimSpace(directive), "=", 2)
		switch strings.ToLower(parts[0]) {
		case "no-store":
			store = false
		case "no-cache":
			ttl = 0
		case "max-age", "s-maxage":
			if len(parts) != 2 {
				ttl = 0
				continue
			}
			age, ok := headerSeconds(strings.Trim(parts[1], `"`))
			if !ok {
				ttl = 0
				continue
			}
			if age < ttl {
				ttl = age
			}
		}
	}
	age := time.Duration(0)
	if raw := headers.Get("Age"); raw != "" {
		var ok bool
		age, ok = headerSeconds(raw)
		if !ok {
			age = 365 * 24 * time.Hour
		}
	}
	delay := received.Sub(sent)
	if delay > 0 {
		age += delay
	}
	if date, err := http.ParseTime(headers.Get("Date")); err == nil {
		if apparent := received.Sub(date); apparent > age {
			age = apparent
		}
	}
	return ttl - age, store
}

// Clamp very large positive ages to one year, already beyond any fresh TTL;
// this avoids duration overflow without making an ancient response fresh.
func headerSeconds(raw string) (time.Duration, bool) {
	if raw == "" {
		return 0, false
	}
	for _, c := range raw {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	seconds, err := strconv.ParseUint(raw, 10, 64)
	const ceiling = 365 * 24 * 60 * 60
	if err != nil || seconds > ceiling {
		seconds = ceiling
	}
	return time.Duration(seconds) * time.Second, true
}

func retryAfter(raw string, now time.Time) time.Time {
	if raw == "" {
		return time.Time{}
	}
	digits := true
	for _, c := range raw {
		if c < '0' || c > '9' {
			digits = false
			break
		}
	}
	if digits {
		seconds, err := strconv.ParseUint(raw, 10, 64)
		maxTime := time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)
		maxSeconds := uint64(maxTime.Sub(now) / time.Second)
		// time.Sub itself saturates at the maximum Duration (roughly 292
		// years). Values above that are represented as a far future date.
		if err != nil || seconds > maxSeconds {
			return maxTime
		}
		return now.Add(time.Duration(seconds) * time.Second)
	}
	date, err := http.ParseTime(raw)
	if err != nil {
		return time.Time{}
	}
	return date
}
