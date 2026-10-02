package p115

import (
	"context"
	"errors"
	"sync"
	"time"
)

var ErrRateQueueFull = errors.New("115 请求队列已满，请稍后重试")

type RequestBudget struct {
	mu                          sync.Mutex
	next                        map[string]time.Time
	global                      time.Time
	waiting                     int
	uidInterval, globalInterval time.Duration
	cooling                     time.Time
	stats                       RequestBudgetStats
	lastBackground              time.Time
	clock                       func() time.Time
	wait                        func(context.Context, time.Time) error
}

func (b *RequestBudget) now() time.Time {
	if b.clock != nil {
		return b.clock()
	}
	return time.Now()
}
func (b *RequestBudget) waitUntil(ctx context.Context, at time.Time) error {
	if b.wait != nil {
		return b.wait(ctx, at)
	}
	return waitRequest(ctx, at)
}

type RequestBudgetStats struct {
	Requests           uint64 `json:"requests"`
	Throttled          uint64 `json:"throttled"`
	Rejected           uint64 `json:"rejected"`
	WaitMS             int64  `json:"waitMs"`
	Waiting            int    `json:"waiting"`
	PeakWaiting        int    `json:"peakWaiting"`
	CoolingUntil       int64  `json:"coolingUntil"`
	BackgroundDeferred uint64 `json:"backgroundDeferred"`
	SourceRequests     uint64 `json:"sourceRequests"`
}

func (b *RequestBudget) Stats() RequestBudgetStats {
	b.mu.Lock()
	defer b.mu.Unlock()
	v := b.stats
	v.Waiting = b.waiting
	if b.cooling.After(b.now()) {
		v.CoolingUntil = b.cooling.UnixMilli()
	}
	return v
}

type requestBudgetKey struct{}
type backgroundBudgetKey struct{}
type sourceBudgetKey struct{}

func WithBudgetSource(ctx context.Context, uid string) context.Context {
	return context.WithValue(ctx, sourceBudgetKey{}, uid)
}

func WithBackgroundBudget(ctx context.Context) context.Context {
	return context.WithValue(ctx, backgroundBudgetKey{}, true)
}

func WithRequestBudget(ctx context.Context, b *RequestBudget) context.Context {
	return context.WithValue(ctx, requestBudgetKey{}, b)
}

func (b *RequestBudget) Configure(uid, global time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.uidInterval, b.globalInterval = uid, global
}

func (b *RequestBudget) CoolUntil(until time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if until.After(b.cooling) {
		b.cooling = until
	}
}

// HTTP 429 can be an IP/global throttle. Conservatively pause all pool API
// traffic, so changing accounts does not multiply calls during Retry-After.
func ObserveRequestBudget(ctx context.Context, status int, retry time.Duration) {
	b, _ := ctx.Value(requestBudgetKey{}).(*RequestBudget)
	if b == nil || status != 429 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	until := b.now().Add(max(retry, 5*time.Second))
	b.stats.Throttled++
	if until.After(b.cooling) {
		b.cooling = until
	}
}

// One budget is shared across independent credential clients, keyed by actual
// UID. No credentials are retained. Waiting is bounded and cancellable.
func WaitRequestBudget(ctx context.Context, cookie string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b, _ := ctx.Value(requestBudgetKey{}).(*RequestBudget)
	if b == nil {
		return ctx.Err()
	}
	uid := CookieUserID(cookie)
	if uid == "" {
		return ctx.Err()
	}
	b.mu.Lock()
	background, _ := ctx.Value(backgroundBudgetKey{}).(bool)
	if background && b.waiting > 0 && b.now().Sub(b.lastBackground) < time.Minute {
		b.stats.BackgroundDeferred++
		b.mu.Unlock()
		return ErrRateQueueFull
	}
	if b.waiting >= 64 {
		b.stats.Rejected++
		b.mu.Unlock()
		return ErrRateQueueFull
	}
	if b.next == nil {
		b.next = make(map[string]time.Time)
	}
	now := b.now()
	if b.cooling.After(now) {
		b.mu.Unlock()
		return ErrShareThrottled
	}
	for k, t := range b.next {
		if t.Before(now) {
			delete(b.next, k)
		}
	}
	if len(b.next) >= 512 {
		b.stats.Rejected++
		b.mu.Unlock()
		return ErrRateQueueFull
	}
	at := now
	if t := b.next[uid]; t.After(at) {
		at = t
	}
	if b.global.After(at) {
		at = b.global
	}
	if at.Sub(now) > 3*time.Second {
		b.stats.Rejected++
		b.mu.Unlock()
		return ErrRateQueueFull
	}
	b.waiting++
	if background {
		b.lastBackground = now
	}
	b.stats.PeakWaiting = max(b.stats.PeakWaiting, b.waiting)
	uidInterval, globalInterval := b.uidInterval, b.globalInterval
	if uidInterval <= 0 {
		uidInterval = 250 * time.Millisecond
	}
	if globalInterval <= 0 {
		globalInterval = 50 * time.Millisecond
	}
	b.next[uid] = at.Add(uidInterval)
	b.global = at.Add(globalInterval)
	b.mu.Unlock()
	defer func() { b.mu.Lock(); b.waiting--; b.stats.WaitMS += b.now().Sub(now).Milliseconds(); b.mu.Unlock() }()
	if err := b.waitUntil(ctx, at); err != nil {
		return err
	}
	b.mu.Lock()
	cooling := b.cooling.After(b.now())
	if !cooling && ctx.Err() == nil {
		b.stats.Requests++
		if source, _ := ctx.Value(sourceBudgetKey{}).(string); source == uid {
			b.stats.SourceRequests++
		}
	}
	b.mu.Unlock()
	if cooling {
		return ErrShareThrottled
	}
	return ctx.Err()
}
