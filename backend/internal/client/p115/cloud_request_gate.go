package p115

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"
)

const (
	cloudRequestInterval = time.Second
	cloudRequestWait     = 3 * time.Second
	cloudRequestCooldown = 30 * time.Second
	cloudRequestUIDLimit = 512
	cloudRequestWaiters  = 8
)

// CloudRequestDeferredError 表示本次 HTTP 请求未发送。组合操作中的先前请求可能已经执行。
type CloudRequestDeferredError struct {
	RetryAt time.Time
	cause   error
}

func (e *CloudRequestDeferredError) Error() string {
	if e.cause != nil {
		return e.cause.Error()
	}
	return "115 请求已延后，尚未发送"
}

func (e *CloudRequestDeferredError) Unwrap() error { return e.cause }

// CloudRequestRetryAt 返回本地延后或上游拒绝后的最早重试时间。
func CloudRequestRetryAt(err error) time.Time {
	var deferred *CloudRequestDeferredError
	if errors.As(err, &deferred) {
		return deferred.RetryAt
	}
	var upstream *HTTPError
	if errors.As(err, &upstream) {
		if delay := cloudCooldown(upstream.Status, upstream.RetryAfter); delay > 0 {
			return time.Now().Add(delay)
		}
	}
	return time.Time{}
}

type cloudRequestGateKey struct{}

// WithRequestGate 让一个离线处理流程中使用其他 115 客户端发出的请求也共享此限速。
func (c *CloudClient) WithRequestGate(ctx context.Context) context.Context {
	return context.WithValue(ctx, cloudRequestGateKey{}, &c.requestGate)
}

// RequestCooldown 读取当前 UID 的上游冷却期限。
func (c *CloudClient) RequestCooldown(uid string) time.Time {
	g := &c.requestGate
	g.mu.Lock()
	defer g.mu.Unlock()
	if state := g.uids[uid]; state != nil && state.cooldown.After(time.Now()) {
		return state.cooldown
	}
	return time.Time{}
}

func cloudRequestGateFrom(ctx context.Context) *cloudRequestGate {
	gate, _ := ctx.Value(cloudRequestGateKey{}).(*cloudRequestGate)
	return gate
}

func cloudCooldown(status int, retryAfter time.Duration) time.Duration {
	if status == http.StatusMethodNotAllowed || status == http.StatusTooManyRequests {
		return max(cloudRequestCooldown, retryAfter)
	}
	return 0
}

type cloudRequestGate struct {
	mu   sync.Mutex
	uids map[string]*cloudUIDRequests
}

type cloudUIDRequests struct {
	turn     chan struct{}
	users    int
	next     time.Time
	cooldown time.Time
}

type cloudRequestLease struct {
	gate  *cloudRequestGate
	state *cloudUIDRequests
}

func (g *cloudRequestGate) acquire(ctx context.Context, cookie string) (*cloudRequestLease, error) {
	if g == nil {
		return nil, nil
	}
	// 真实 UID 相同的凭据共享节奏；未提供可解析 UID 的请求共用一个保守分组。
	uid := CookieUserID(cookie)
	now := time.Now()
	g.mu.Lock()
	state := g.uids[uid]
	if state == nil {
		for key, candidate := range g.uids {
			if candidate.users == 0 && !candidate.next.After(now) && !candidate.cooldown.After(now) {
				delete(g.uids, key)
			}
		}
		if len(g.uids) >= cloudRequestUIDLimit {
			g.mu.Unlock()
			return nil, &CloudRequestDeferredError{RetryAt: now.Add(cloudRequestInterval), cause: ErrRateQueueFull}
		}
		if g.uids == nil {
			g.uids = make(map[string]*cloudUIDRequests)
		}
		state = &cloudUIDRequests{turn: make(chan struct{}, 1)}
		g.uids[uid] = state
	}
	if state.cooldown.After(now) {
		deferred := &CloudRequestDeferredError{RetryAt: state.cooldown, cause: ErrShareThrottled}
		g.mu.Unlock()
		return nil, deferred
	}
	if state.users >= cloudRequestWaiters+1 {
		g.mu.Unlock()
		return nil, &CloudRequestDeferredError{RetryAt: now.Add(cloudRequestInterval), cause: ErrRateQueueFull}
	}
	state.users++
	g.mu.Unlock()

	waitCtx, cancel := context.WithTimeout(ctx, cloudRequestWait)
	defer cancel()
	select {
	case state.turn <- struct{}{}:
	case <-waitCtx.Done():
		g.mu.Lock()
		state.users--
		g.mu.Unlock()
		return nil, cloudQueueDeferred(ctx, time.Time{})
	}
	lease := &cloudRequestLease{gate: g, state: state}
	g.mu.Lock()
	next, cooldown := state.next, state.cooldown
	g.mu.Unlock()
	if cooldown.After(time.Now()) {
		lease.release()
		return nil, &CloudRequestDeferredError{RetryAt: cooldown, cause: ErrShareThrottled}
	}
	if err := waitRequest(waitCtx, next); err != nil {
		lease.release()
		return nil, cloudQueueDeferred(ctx, next)
	}
	if waitCtx.Err() != nil {
		lease.release()
		return nil, cloudQueueDeferred(ctx, next)
	}
	g.mu.Lock()
	state.next = time.Now().Add(cloudRequestInterval)
	g.mu.Unlock()
	return lease, nil
}

func cloudQueueDeferred(ctx context.Context, retryAt time.Time) *CloudRequestDeferredError {
	if earliest := time.Now().Add(cloudRequestInterval); retryAt.Before(earliest) {
		retryAt = earliest
	}
	cause := ctx.Err()
	if cause == nil {
		cause = ErrRateQueueFull
	}
	return &CloudRequestDeferredError{RetryAt: retryAt, cause: cause}
}

func (l *cloudRequestLease) observe(status int, retryAfter time.Duration) {
	delay := cloudCooldown(status, retryAfter)
	if l == nil || delay == 0 {
		return
	}
	until := time.Now().Add(delay)
	l.gate.mu.Lock()
	l.state.cooldown = until
	l.gate.mu.Unlock()
}

func (l *cloudRequestLease) release() {
	if l == nil {
		return
	}
	l.gate.mu.Lock()
	<-l.state.turn
	l.state.users--
	l.gate.mu.Unlock()
}
