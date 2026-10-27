package p115

import (
	"context"
	"errors"
	"maps"
	"net/url"
	"strings"
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
	Requests           uint64            `json:"requests"`
	Throttled          uint64            `json:"throttled"`
	Rejected           uint64            `json:"rejected"`
	WaitMS             int64             `json:"waitMs"`
	Waiting            int               `json:"waiting"`
	PeakWaiting        int               `json:"peakWaiting"`
	CoolingUntil       int64             `json:"coolingUntil"`
	BackgroundDeferred uint64            `json:"backgroundDeferred"`
	SourceRequests     uint64            `json:"sourceRequests"`
	Stages             map[string]uint64 `json:"stages"`
	Origins            map[string]uint64 `json:"origins"`
}

func (b *RequestBudget) Stats() RequestBudgetStats {
	b.mu.Lock()
	defer b.mu.Unlock()
	v := b.stats
	v.Stages = maps.Clone(b.stats.Stages)
	v.Origins = maps.Clone(b.stats.Origins)
	v.Waiting = b.waiting
	if b.cooling.After(b.now()) {
		v.CoolingUntil = b.cooling.UnixMilli()
	}
	return v
}

type requestBudgetKey struct{}
type backgroundBudgetKey struct{}
type sourceBudgetKey struct{}
type donorBudgetKey struct{}
type budgetStageKey struct{}
type budgetTraceKey struct{}
type BudgetTrace struct {
	mu      sync.Mutex
	stages  map[string]uint64
	origins map[string]uint64
}

func WithBudgetTrace(ctx context.Context) (context.Context, *BudgetTrace) {
	b := &BudgetTrace{stages: make(map[string]uint64), origins: make(map[string]uint64)}
	return context.WithValue(ctx, budgetTraceKey{}, b), b
}
func (b *BudgetTrace) Snapshot() (map[string]uint64, map[string]uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return maps.Clone(b.stages), maps.Clone(b.origins)
}

func WithDonorBudget(ctx context.Context) context.Context {
	return context.WithValue(ctx, donorBudgetKey{}, true)
}

// Fixed categories only: endpoint queries, file IDs and credentials never
// become metric keys. Counts measure API budget admissions, not CDN requests.
func budgetEndpointContext(ctx context.Context, endpoint string) context.Context {
	stage := "metadata"
	u, err := url.Parse(endpoint)
	if err == nil {
		path := strings.ToLower(u.Path)
		switch {
		case strings.Contains(path, "downurl"), strings.Contains(path, "download"):
			stage = "direct_link"
		case strings.Contains(path, "search"), strings.Contains(path, "get_info"), strings.Contains(path, "sha1"):
			stage = "file_lookup"
		case strings.Contains(path, "files/add"), strings.Contains(path, "files/delete"), strings.Contains(path, "rb/delete"):
			stage = "directory_write"
		}
	}
	return context.WithValue(ctx, budgetStageKey{}, stage)
}

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
		return errors.Join(ErrRateQueueFull, err)
	}
	b.mu.Lock()
	cooling := b.cooling.After(b.now())
	if !cooling && ctx.Err() == nil {
		b.stats.Requests++
		stage, _ := ctx.Value(budgetStageKey{}).(string)
		if stage == "" {
			stage = "other"
		}
		origin := "target"
		if source, _ := ctx.Value(sourceBudgetKey{}).(string); source == uid {
			b.stats.SourceRequests++
			origin = "source"
		}
		if donor, _ := ctx.Value(donorBudgetKey{}).(bool); donor {
			origin = "donor"
		}
		if background {
			origin = "probe"
		}
		if b.stats.Stages == nil {
			b.stats.Stages = make(map[string]uint64)
			b.stats.Origins = make(map[string]uint64)
		}
		b.stats.Stages[stage]++
		b.stats.Origins[origin]++
		if trace, _ := ctx.Value(budgetTraceKey{}).(*BudgetTrace); trace != nil {
			trace.mu.Lock()
			trace.stages[stage]++
			trace.origins[origin]++
			trace.mu.Unlock()
		}
	}
	b.mu.Unlock()
	if cooling {
		return ErrShareThrottled
	}
	return ctx.Err()
}
