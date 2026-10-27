package service

import (
	"context"
	"fmt"
	"strings"

	"strm115/backend/internal/model"
)

type poolCopyPreferenceKey struct{}

// Only examines committed local indices. Hints never create trusted origins,
// bypass live validation, or trigger cloud searches for candidate sorting.
func (s *PlaybackCache) WithPoolCopyPreference(ctx context.Context, origin, hash string) context.Context {
	m, _ := s.lookupMaterial(origin, hash)
	if m.ID == "" || m.VerifiedAt.IsZero() {
		return ctx
	}
	uids := make(map[string]bool)
	for _, copy := range s.entriesByHash(m.SHA1) {
		if copy.Status == "ready" && copy.UID != "" && copy.File.ID != "" && copy.File.Size == m.Size && strings.EqualFold(copy.File.SHA1, m.SHA1) && copy.MaterialID == m.ID {
			uids[copy.UID] = true
		}
	}
	return context.WithValue(ctx, poolCopyPreferenceKey{}, uids)
}

// Reads local state under the pool lock. A pending first allocation or switch
// also pins concurrent preparations, but only a successful commit persists it.
func poolUserTarget(d model.PlaybackPoolDocument, user string, accounts []P115Account, now int64) (model.PlaybackPoolAssignment, bool) {
	target := d.Assignments[user]
	var pending model.PlaybackPoolAssignment
	for key, l := range d.Leases {
		if !l.UserTarget || l.Issued || l.UserID != user || key != l.Key || l.Stopped || l.ExpiresAt <= now || l.Generation < target.Generation {
			continue
		}
		candidate := model.PlaybackPoolAssignment{AccountID: l.AccountID, UID: l.UID, Generation: l.Generation}
		if (pending.UID == "" || candidate.Generation < pending.Generation) && poolTargetPinned(d, candidate, accounts, now) {
			pending = candidate
		}
	}
	if pending.UID != "" {
		return pending, true
	}
	return target, false
}

func poolTargetPinned(d model.PlaybackPoolDocument, target model.PlaybackPoolAssignment, accounts []P115Account, now int64) bool {
	for _, a := range accounts {
		if a.ID != target.AccountID || a.UID != target.UID || !eligiblePoolAccount(a) || d.Modes[a.ID] == "disabled" {
			continue
		}
		h := d.Health[a.ID]
		return h.Revision != a.Revision || !poolHealthAllowsSwitch(h) || h.State == "cooldown" && h.Until <= now
	}
	return false
}

func (l *PoolLease) assignmentCurrent(d model.PlaybackPoolDocument) bool {
	current := d.Assignments[l.Record.UserID]
	return !l.trackAssignment || current == l.assignment || l.assignment.UID == "" && current.AccountID == l.Account.ID && current.UID == l.Account.UID
}

func (l *PoolLease) commitAssignment(d *model.PlaybackPoolDocument) error {
	if !l.assignmentCurrent(*d) {
		return ErrAccountChanged
	}
	if !l.trackAssignment || l.Record.Issued {
		// An already issued old session may reuse its own full slot. Refreshing
		// it must not move the user's newer default back and forth.
		return nil
	}
	current := d.Assignments[l.Record.UserID]
	if current.AccountID != l.Account.ID || current.UID != l.Account.UID {
		generation := l.Record.Generation
		if generation <= current.Generation {
			d.Generation++ // A recovered older preparation must not rewind the fence.
			generation = d.Generation
		}
		d.Assignments[l.Record.UserID] = model.PlaybackPoolAssignment{AccountID: l.Account.ID, UID: l.Account.UID, Generation: generation}
	}
	return nil
}

func poolHealthAllowsSwitch(h model.PlaybackPoolHealth) bool {
	return h.State == "invalid_credentials" || h.State == "invalid_directory" || h.State == "cooldown" && h.Failures >= 2
}

func (p *DynamicPool) canSwitchAccount(a P115Account) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	h := p.doc.Health[a.ID]
	return h.Revision == a.Revision && poolHealthAllowsSwitch(h)
}

// This is an API retry hint, never an authorization or a cached health grant.
type PoolRecoveryError struct {
	Reason     string
	RetryAfter int
}

func (e *PoolRecoveryError) Error() string {
	return fmt.Sprintf("%s，保留原 UID，请在 %d 秒后重试", e.Reason, e.RetryAfter)
}
func (e *PoolRecoveryError) Unwrap() error { return ErrPoolCooling }

func poolRecoveryError(h model.PlaybackPoolHealth, now int64) error {
	reason := "共享小号暂时不可用"
	switch h.Fault {
	case "network":
		reason = "115 网络请求失败，等待原账号恢复检查"
	case "upstream_5xx":
		reason = "115 服务暂时异常，等待原账号恢复检查"
	case "http_429", "uid_throttled":
		reason = "115 请求受限，等待冷却结束"
	}
	return &PoolRecoveryError{Reason: reason, RetryAfter: max(1, int((h.Until-now+999)/1000))}
}

func (p *DynamicPool) accountRecoveryError(a P115Account) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return poolRecoveryError(p.doc.Health[a.ID], p.now().UnixMilli())
}
