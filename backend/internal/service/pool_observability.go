package service

import (
	"context"
	"fmt"
	"sort"
	"time"

	"strm115/backend/internal/model"
)

// Counters reset on restart; latency keeps the latest 256 completed redirects.
// No request identity, credential or URL is stored in metrics.
type PoolMetrics struct {
	StartedAt         int64  `json:"startedAt"`
	Requests          uint64 `json:"requests"`
	Succeeded         uint64 `json:"succeeded"`
	Failed            uint64 `json:"failed"`
	Switches          uint64 `json:"switches"`
	SwitchedRequests  uint64 `json:"switchedRequests"`
	DeviceConflicts   uint64 `json:"deviceConflicts"`
	CapacityDenials   uint64 `json:"capacityDenials"`
	CapacityTransfers uint64 `json:"capacityTransfers"`
	CopyReuses        uint64 `json:"copyReuses"`
	Allocations       uint64 `json:"allocations"`
	Reused            uint64 `json:"reused"`
	CopyPreferred     uint64 `json:"copyPreferred"`
	PeakUIDSessions   int    `json:"peakUidSessions"`
	P50MS             int64  `json:"p50Ms"`
	P95MS             int64  `json:"p95Ms"`
	Samples           int    `json:"samples"`
	Waiting           int    `json:"waiting"`
	CopyResolutions   uint64 `json:"copyResolutions"`
	Resolved          uint64 `json:"resolved"`
	DirectLinkHits    uint64 `json:"directLinkHits"`
}

func (p *DynamicPool) ObserveResolution(copyHit, linkHit bool, reused ...bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.metrics.Resolved++
	if copyHit && (linkHit || len(reused) > 0 && reused[0]) {
		p.metrics.CopyReuses++
	}
	if copyHit {
		p.metrics.CopyResolutions++
	}
	if linkHit {
		p.metrics.DirectLinkHits++
	}
}

func (p *DynamicPool) ObserveRedirect(elapsed time.Duration, success bool, switches int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.metrics.Requests++
	if success {
		p.metrics.Succeeded++
	} else {
		p.metrics.Failed++
	}
	p.metrics.Switches += uint64(max(0, switches))
	if switches > 0 {
		p.metrics.SwitchedRequests++
	}
	if len(p.latencies) < 256 {
		p.latencies = append(p.latencies, elapsed.Milliseconds())
	} else {
		p.latencies[p.latencyNext] = elapsed.Milliseconds()
		p.latencyNext = (p.latencyNext + 1) % 256
	}
}

func (p *DynamicPool) Metrics() PoolMetrics {
	p.mu.Lock()
	defer p.mu.Unlock()
	v := p.metrics
	v.Waiting = p.waiting
	samples := append([]int64(nil), p.latencies...)
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	v.Samples = len(samples)
	if len(samples) > 0 {
		v.P50MS = samples[(len(samples)*50+99)/100-1]
		v.P95MS = samples[(len(samples)*95+99)/100-1]
	}
	return v
}

func (p *DynamicPool) Events() []model.PlaybackPoolEvent {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]model.PlaybackPoolEvent{}, p.doc.Events...)
}

func poolHealthMessage(state string) string {
	switch state {
	case "ready":
		return "最近检查通过"
	case "invalid_credentials":
		return "登录凭据失效，请重新登录小号"
	case "invalid_directory":
		return "播放目录失效，请重新配置目录"
	case "throttled":
		return "115 限流冷却，等待恢复"
	case "cooldown":
		return "上游暂时不可用，冷却后重新检查"
	default:
		return "等待检查"
	}
}

// Called under the pool lock, as part of the same persisted health transaction.
func (p *DynamicPool) recordHealthEvent(d *model.PlaybackPoolDocument, id string, old, current model.PlaybackPoolHealth) {
	if old.State == current.State && old.Revision == current.Revision {
		return
	}
	// Initial success is not a recovery alert.
	if old.State == "" && current.State == "ready" {
		return
	}
	now := p.now().UnixMilli()
	for i := len(d.Events) - 1; i >= 0; i-- {
		e := d.Events[i]
		if e.AccountID == id && e.State == current.State && now-e.At < int64(10*time.Minute/time.Millisecond) {
			return
		}
	}
	d.EventSequence++
	d.Events = append(d.Events, model.PlaybackPoolEvent{ID: d.EventSequence, AccountID: id, State: current.State, At: now, BotID: d.Settings.NotificationBotID})
	if len(d.Events) > 100 {
		d.Events = append([]model.PlaybackPoolEvent(nil), d.Events[len(d.Events)-100:]...)
	}
}

type PoolNotifier interface {
	NotifyPoolHealth(context.Context, string, string, string) error
}

// Uses the existing managed health worker; never starts a detached goroutine.
// Notification delivery failures do not change playback health.
func (s *MemberPlaybackService) flushPoolNotifications(ctx context.Context) {
	if s.PoolNotifier == nil || s.Pool == nil {
		return
	}
	settings := s.Pool.Settings()
	if settings.NotificationBotID == "" {
		return
	}
	for _, event := range s.Pool.Events() {
		if event.Delivered || event.BotID != settings.NotificationBotID || event.At < time.Now().Add(-time.Hour).UnixMilli() {
			continue
		}
		account, err := s.accounts.Get(event.AccountID)
		if err != nil || account.Primary || account.Ownership == "member" || account.OwnerUserID != "" || IsMemberAccount(account.ID) {
			continue
		}
		// Only stable event ID and safe state; omit names that can contain user text.
		key := fmt.Sprintf("pool-health:%d", event.ID)
		if err := s.PoolNotifier.NotifyPoolHealth(ctx, event.BotID, key, "动态播放池："+poolHealthMessage(event.State)); err != nil {
			return
		}
		s.Pool.mu.Lock()
		d := s.Pool.clone()
		for i := range d.Events {
			if d.Events[i].ID == event.ID {
				d.Events[i].Delivered = true
			}
		}
		err = s.Pool.save(ctx, d)
		s.Pool.mu.Unlock()
		if err != nil {
			return
		}
		return // At most one enqueue per worker iteration.
	}
}
