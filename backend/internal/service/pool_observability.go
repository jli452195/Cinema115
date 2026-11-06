package service

import (
	"context"
	"fmt"
	"sort"
	"strings"
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

// 在健康事务的池锁内延续异常周期；事件沿用现有持久队列。
func (p *DynamicPool) recordHealthEvent(d *model.PlaybackPoolDocument, id string, old, current model.PlaybackPoolHealth) {
	current.FailureSince, current.AlertBotID, current.AlertEventID = old.FailureSince, old.AlertBotID, old.AlertEventID
	if current.State != "ready" && (current.FailureSince == 0 || old.State == "ready" || old.Revision != current.Revision) {
		current.FailureSince = current.CheckedAt
		current.AlertBotID, current.AlertEventID = "", 0
	}
	d.Health[id] = current
	if old.State == current.State && (old.Revision == current.Revision || current.State == "ready") {
		return
	}
	// 首次检查成功不产生恢复事件。
	if old.State == "" && current.State == "ready" {
		return
	}
	now := p.now().UnixMilli()
	var recoveryOf uint64
	if current.State == "ready" && current.AlertBotID == d.Settings.NotificationBotID {
		recoveryOf = current.AlertEventID
	}
	d.EventSequence++
	d.Events = append(d.Events, model.PlaybackPoolEvent{ID: d.EventSequence, AccountID: id, State: current.State, At: now, BotID: d.Settings.NotificationBotID, Since: current.FailureSince, RecoveryOf: recoveryOf})
	if len(d.Events) > 100 {
		d.Events = append([]model.PlaybackPoolEvent(nil), d.Events[len(d.Events)-100:]...)
	}
}

type PoolNotifier interface {
	// 仅写入本地持久队列，不在此执行网络发送。
	NotifyPoolHealth(context.Context, string, string, string) error
}

// 复用健康工作循环，每轮最多入队一条；发送端再次核对异常周期及恢复配对。
func (s *MemberPlaybackService) flushPoolNotifications(ctx context.Context) {
	if s.PoolNotifier == nil || s.Pool == nil {
		return
	}
	settings := s.Pool.Settings()
	if settings.NotificationBotID == "" {
		return
	}
	events := s.Pool.Events()
	seen := make(map[string]bool, len(events))
	for i := len(events) - 1; i >= 0; i-- {
		event := events[i]
		if seen[event.AccountID] {
			continue
		}
		seen[event.AccountID] = true
		if event.Delivered || event.BotID != settings.NotificationBotID {
			continue
		}
		account, err := s.accounts.Get(event.AccountID)
		if err != nil || account.Primary || account.Ownership == "member" || account.OwnerUserID != "" || IsMemberAccount(account.ID) {
			continue
		}
		if queued, err := s.enqueuePoolNotification(ctx, event, account); queued || err != nil {
			return
		}
	}
}

func (s *MemberPlaybackService) enqueuePoolNotification(ctx context.Context, event model.PlaybackPoolEvent, account P115Account) (bool, error) {
	p := s.Pool
	p.mu.Lock()
	defer p.mu.Unlock()
	h := p.doc.Health[event.AccountID]
	now := p.now().UnixMilli()
	latest := -1
	for i := len(p.doc.Events) - 1; i >= 0; i-- {
		if p.doc.Events[i].AccountID == event.AccountID {
			latest = i
			break
		}
	}
	if latest < 0 || p.doc.Events[latest].ID != event.ID || p.doc.Events[latest].Delivered ||
		p.doc.Settings.NotificationBotID != event.BotID || h.Revision != account.Revision ||
		h.State != event.State || h.FailureSince != event.Since || p.doc.Modes[event.AccountID] == "disabled" {
		return false, nil
	}
	if event.State == "ready" {
		if event.RecoveryOf == 0 || h.AlertBotID != event.BotID {
			return false, nil
		}
	} else if h.AlertBotID == event.BotID || (h.State == "cooldown" || h.State == "throttled") &&
		(now-h.FailureSince < time.Minute.Milliseconds() || h.State == "cooldown" && h.Failures < 2 || h.State == "throttled" && h.Until <= now) {
		return false, nil
	}
	name := strings.Join(strings.Fields(account.Name), " ")
	if name == "" {
		name = "未命名小号"
	}
	message := poolHealthMessage(event.State)
	switch h.Fault {
	case "network":
		message = "持续异常：网络请求失败或超时，冷却后复查"
	case "upstream_5xx":
		message = "持续异常：115 接口返回 5xx，冷却后复查"
	case "http_429":
		message = "持续限流：HTTP 429，共享池全局冷却"
	case "uid_throttled":
		message = "持续限流：当前 UID 请求受限，冷却后复查"
	}
	if event.State == "ready" {
		message = "已恢复：登录与播放目录核验通过"
	}
	text := fmt.Sprintf("动态播放池：%s（%s）\n%s\n核验时间：%s", name, account.ID, message, time.UnixMilli(h.CheckedAt).UTC().Format(time.RFC3339))
	if h.FailureSince > 0 {
		end := now
		if event.State == "ready" {
			end = h.CheckedAt
		}
		text += fmt.Sprintf("\n异常持续约 %d 秒", max(0, (end-h.FailureSince)/1000))
	}
	key := fmt.Sprintf("pool-health:%d", event.ID)
	if err := s.PoolNotifier.NotifyPoolHealth(ctx, event.BotID, key, text); err != nil {
		return false, err
	}
	d := p.clone()
	d.Events[latest].Delivered = true
	if event.State == "ready" {
		h.AlertBotID, h.AlertEventID = "", 0
	} else {
		h.AlertBotID, h.AlertEventID = event.BotID, event.ID
	}
	d.Health[event.AccountID] = h
	return true, p.save(ctx, d)
}
