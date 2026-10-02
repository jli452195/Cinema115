package service

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"strm115/backend/internal/model"
)

type PoolAdmissionReason string

const (
	PoolCapacityFull PoolAdmissionReason = "uid_capacity_full"
	PoolRecoveryBusy PoolAdmissionReason = "recovery_preparing"
	PoolDraining     PoolAdmissionReason = "draining"
	PoolMixedBlocked PoolAdmissionReason = "mixed_unavailable"
	PoolWaiterLimit  PoolAdmissionReason = "session_waiter_limit"
	PoolRequestLimit PoolAdmissionReason = "session_request_limit"
	PoolLeaseLimit   PoolAdmissionReason = "lease_record_limit"
)

// Diagnostic is captured under the admission lock, never by a later recount.
// Error() is safe for clients; only administrator logs use Diagnostic.
type PoolAdmissionError struct {
	Reason     PoolAdmissionReason
	Diagnostic string
}

func (e *PoolAdmissionError) Unwrap() error { return ErrPoolBusy }
func (e *PoolAdmissionError) Error() string {
	switch e.Reason {
	case PoolCapacityFull:
		return "共享小号会话额度已满，请等待已有会话释放后重试"
	case PoolRecoveryBusy:
		return "共享小号正在恢复，已有播放请求准备中，请稍后重试"
	case PoolDraining:
		return "共享小号已停止新分配，请联系管理员"
	case PoolMixedBlocked:
		return "共享小号因容量、恢复或维护等不同原因暂不可用，请稍后重试或联系管理员"
	default:
		return "共享池请求或记录达到系统保护上限，请稍后重试"
	}
}

type poolOccupancy struct {
	total, preparing, active, residual int
	next                               int64
}

func poolUIDOccupancy(d model.PlaybackPoolDocument, uid string, now int64) poolOccupancy {
	v := poolOccupancy{}
	for key, l := range d.Leases {
		if l.UID != uid || l.ExpiresAt <= now {
			continue
		}
		v.total++
		if key != l.Key || l.Stopped {
			v.residual++
		} else if !l.Issued {
			v.preparing++
		} else {
			v.active++
		}
		if v.next == 0 || l.ExpiresAt < v.next {
			v.next = l.ExpiresAt
		}
	}
	return v
}

// Called with mu held; all inputs are local, no network or writes. The
// diagnostic lists at most eight UIDs, independently of pool size.
func (p *DynamicPool) admissionError(d model.PlaybackPoolDocument, reason PoolAdmissionReason, blocked map[string]map[string]bool) error {
	uids := make([]string, 0, len(blocked))
	for uid := range blocked {
		uids = append(uids, uid)
	}
	sort.Strings(uids)
	now := p.now().UnixMilli()
	parts := make([]string, 0, min(len(uids), 8)+1)
	parts = append(parts, fmt.Sprintf("原因码=%s，请求处理中=%d/128，等待者=%d/128，租约记录=%d/4096", reason, len(p.busy), p.waiting, len(d.Leases)))
	for _, uid := range uids[:min(len(uids), 8)] {
		v := poolUIDOccupancy(d, uid, now)
		reasons := make([]string, 0, len(blocked[uid]))
		for r := range blocked[uid] {
			reasons = append(reasons, r)
		}
		sort.Strings(reasons)
		next := "无"
		if v.next > 0 {
			next = time.UnixMilli(v.next).UTC().Format(time.RFC3339Nano)
		}
		parts = append(parts, fmt.Sprintf("UID=%s，排除=%s，占用=%d/%d，准备=%d，活跃=%d，残余=%d，最近到期=%s（仅当前租约估计，续租可能延后）", PlaybackLogName(uid), strings.Join(reasons, "/"), v.total, d.Settings.MaxSessions, v.preparing, v.active, v.residual, next))
	}
	if len(uids) > 8 {
		parts = append(parts, fmt.Sprintf("另有 %d 个 UID 未展开", len(uids)-8))
	}
	return &PoolAdmissionError{Reason: reason, Diagnostic: strings.Join(parts, "；")}
}

func blockPoolUID(blocked map[string]map[string]bool, uid, reason string) {
	if blocked[uid] == nil {
		blocked[uid] = make(map[string]bool)
	}
	blocked[uid][reason] = true
}

func poolBlockedReason(blocked map[string]map[string]bool) PoolAdmissionReason {
	var result PoolAdmissionReason
	for _, reasons := range blocked {
		for reason := range reasons {
			r := PoolAdmissionReason(reason)
			if r != PoolCapacityFull && r != PoolRecoveryBusy && r != PoolDraining {
				return PoolMixedBlocked
			}
			if result != "" && result != r {
				return PoolMixedBlocked
			}
			result = r
		}
	}
	if result == "" {
		return PoolMixedBlocked
	}
	return result
}
