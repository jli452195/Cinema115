package service

import (
	"context"
	"errors"
	"time"

	"strm115/backend/internal/model"
)

// Browsing timestamps and future timestamps never prove playback. Renewal is
// anchored to this evidence, not to the time a repeated snapshot is polled.
func freshPlaybackEvidence(at int64, now time.Time) bool {
	return at > 0 && at <= now.UnixMilli() && at >= now.Add(-90*time.Second).UnixMilli()
}

func handoffIdle(l model.UserPlaybackLease, now time.Time) bool {
	if !l.Issued || l.IssuedAt == 0 || l.ObservationKey == "" || l.ExpiresAt <= now.UnixMilli() {
		return false
	}
	if l.HandoffAt != 0 && now.UnixMilli()-l.HandoffAt < 30000 {
		return false
	}
	silence := now.UnixMilli() - max(l.LastPlaybackAt, l.IssuedAt)
	if now.UnixMilli()-l.IssuedAt <= 60000 {
		return silence >= 30000
	}
	return silence >= 90000
}

func (p *UserPlayback) lockDevice(ctx context.Context, key string) error {
	p.mu.Lock()
	if p.deviceWaiters >= 128 {
		p.mu.Unlock()
		return ErrUserPlaybackBusy
	}
	p.deviceWaiters++
	p.mu.Unlock()
	defer func() { p.mu.Lock(); p.deviceWaiters--; p.mu.Unlock() }()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		p.mu.Lock()
		if !p.devices[key] {
			if len(p.devices) >= 128 {
				p.mu.Unlock()
				return ErrUserPlaybackBusy
			}
			p.devices[key] = true
			p.mu.Unlock()
			return nil
		}
		changed := p.deviceChanged
		p.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		}
	}
}

func (p *UserPlayback) unlockDevice(key string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.devices, key)
	close(p.deviceChanged)
	p.deviceChanged = make(chan struct{})
}

// A pending successor shares exactly one still-present predecessor slot. If
// the predecessor disappears/expires, the pending record counts on its own.
func borrowedPoolSlot(d model.PlaybackPoolDocument, l model.PlaybackPoolLease, now int64) bool {
	old, ok := d.Leases[l.BorrowedKey]
	return !l.Issued && l.BorrowedKey != "" && ok && old.Issued && old.Generation == l.BorrowedGeneration && old.UID == l.UID && old.ExpiresAt > now
}

func poolHandoffCandidate(d model.PlaybackPoolDocument, req PoolRequest, user string, now int64) model.PlaybackPoolLease {
	if req.handoff == nil {
		return model.PlaybackPoolLease{}
	}
	var result model.PlaybackPoolLease
	for key, l := range d.Leases {
		if key != l.Key || l.Stopped || !l.Issued || l.ExpiresAt <= now || l.UserID != user || l.UserGeneration != req.handoff.Generation || l.SessionKey != req.handoff.EventKey {
			continue
		}
		if result.Key != "" {
			return model.PlaybackPoolLease{}
		}
		result = l
	}
	for _, l := range d.Leases {
		if l.BorrowedKey == result.Key && l.BorrowedGeneration == result.Generation && l.ExpiresAt > now {
			return model.PlaybackPoolLease{}
		}
	}
	return result
}

// Lock order is user -> pool. No network calls occur while these locks are held.
// Both documents are committed before either in-memory state is published.
func (a *MediaAuthorization) commitHandoff(ctx context.Context) error {
	u := a.userLease.pool
	u.mu.Lock()
	defer u.mu.Unlock()
	d := u.clone()
	now := u.now()
	current, ok := d.Leases[a.userLease.Record.Key]
	if !ok || current.Generation != a.userLease.Record.Generation || current.ExpiresAt <= now.UnixMilli() {
		return ErrUserPlaybackStopped
	}
	if a.userLease.policyRevision != d.PolicyRevision {
		return ErrUserPlaybackStopped
	}
	previous := a.userLease.handoff
	alreadyIssued := current.Issued
	confirmed := freshPlaybackEvidence(current.LastPlaybackAt, now)
	old, ok := d.Leases[previous.Key]
	if confirmed && (!ok || old.Generation != previous.Generation || old.LastPlaybackAt != previous.LastPlaybackAt || old.IssuedAt != previous.IssuedAt || old.ExpiresAt <= now.UnixMilli() || u.refs[old.Generation] != 0) {
		return ErrUserPlaybackStopped
	}
	if confirmed {
		// Revalidate uniqueness under the commit lock; a parallel issued session
		// must not be silently retired or ignored.
		for key, other := range d.Leases {
			if key != current.Key && key != old.Key && other.Issued && other.ExpiresAt > now.UnixMilli() && other.Device == old.Device && other.UserID == old.UserID && other.ServerID == old.ServerID {
				return ErrUserPlaybackStopped
			}
		}
		delete(d.Leases, old.Key)
		d.Stopped[old.EventKey] = now.Add(24 * time.Hour).UnixMilli()
	}
	current.Issued = true
	if current.FirstIssuedAt == 0 {
		current.FirstIssuedAt = now.UnixMilli()
	}
	// An old stop without a session ID can arrive after the successor starts.
	// When both have the same fallback identity, never attribute it to the new one.
	if confirmed && current.EventFallback == old.EventFallback {
		if old.EventFallback != "" {
			d.Stopped["fallback:"+old.EventFallback] = now.Add(5 * time.Minute).UnixMilli()
		}
		current.EventFallback = ""
	}
	if !alreadyIssued {
		current.IssuedAt, current.UpdatedAt = now.UnixMilli(), now.UnixMilli()
		current.ExpiresAt = now.Add(userPlaybackIdleTTL).UnixMilli()
	}
	if confirmed {
		current.HandoffAt = now.UnixMilli()
	}
	d.Leases[current.Key] = current
	if a.poolLease == nil {
		if err := u.save(ctx, d); err != nil {
			return err
		}
	} else {
		p := a.poolLease.pool
		p.mu.Lock()
		defer p.mu.Unlock()
		pd := p.clone()
		lease, ok := pd.Leases[a.poolLease.Record.Key]
		if !ok || lease.Generation != a.poolLease.Record.Generation || lease.ExpiresAt <= now.UnixMilli() || pd.Modes[lease.AccountID] == "disabled" {
			return ErrAccountChanged
		}
		health := pd.Health[lease.AccountID]
		if alreadyIssued && (health.State != "ready" || health.Revision != lease.Revision) {
			return ErrAccountChanged
		}
		if health.Sequence != a.poolLease.healthSequence && health.State != "ready" && health.Revision == lease.Revision {
			return ErrAccountChanged
		}
		if confirmed && lease.BorrowedKey != "" && !borrowedPoolSlot(pd, lease, now.UnixMilli()) {
			return ErrAccountChanged
		}
		if !confirmed && lease.BorrowedKey != "" {
			// An unconfirmed URL may be prefetch. It cannot inherit capacity by
			// retiring the predecessor; convert to an independent slot atomically.
			used := 0
			for key, other := range pd.Leases {
				if key != lease.Key && other.UID == lease.UID && other.ExpiresAt > now.UnixMilli() && !borrowedPoolSlot(pd, other, now.UnixMilli()) {
					used++
				}
			}
			if used >= pd.Settings.MaxSessions {
				return ErrPoolBusy
			}
		}
		for key, prior := range pd.Leases {
			if !confirmed || prior.UserID != old.UserID || prior.SessionKey != old.EventKey || prior.UserGeneration != old.Generation {
				continue
			}
			if key == lease.BorrowedKey && prior.Generation == lease.BorrowedGeneration || key == prior.Key && !prior.Stopped && prior.UID == lease.UID {
				delete(pd.Leases, key)
			} else if prior.Issued {
				prior.Stopped = true
				prior.ExpiresAt = min(prior.ExpiresAt, now.Add(2*time.Minute).UnixMilli())
				pd.Leases[key] = prior
			}
		}
		lease.BorrowedKey, lease.BorrowedGeneration = "", 0
		lease.Issued, lease.Stopped = true, false
		if !alreadyIssued {
			lease.IssuedAt = now.UnixMilli()
			lease.ExpiresAt = now.Add(time.Duration(pd.Settings.LeaseMinutes) * time.Minute).UnixMilli()
		}
		pd.Leases[lease.Key] = lease
		if !alreadyIssued && health.Sequence == a.poolLease.healthSequence && !a.poolLease.skipHealthRefresh {
			h := model.PlaybackPoolHealth{Revision: lease.Revision, UID: lease.UID, State: "ready", CheckedAt: now.UnixMilli(), Sequence: health.Sequence + 1, NextProbe: now.Add(5*time.Minute + poolProbeJitter(lease.AccountID)).UnixMilli()}
			pd.Health[lease.AccountID] = h
			p.recordHealthEvent(&pd, lease.AccountID, health, h)
		}
		if err := a.service.db.SavePlaybackState(ctx, d, pd); err != nil {
			return err
		}
		u.doc, p.doc = d, pd
		a.poolLease.Record = lease
		p.rememberPoolPreference(lease.UserID, lease.UID)
	}
	a.userLease.Record = current
	a.userLease.handoff = nil
	return nil
}

// A link may have been issued before Playing/Progress arrived. Only then can
// an independently admitted successor retire a unique, idle predecessor.
func (s *MemberPlaybackService) confirmPlaybackHandoff(ctx context.Context, e PlaybackEvent) error {
	if s.UserPlayback == nil || e.userGeneration == 0 {
		return nil
	}
	u := s.UserPlayback
	u.mu.Lock()
	policyRevision := u.doc.PolicyRevision
	var current, previous model.UserPlaybackLease
	for _, l := range u.doc.Leases {
		if l.EventKey == e.key && l.Generation == e.userGeneration {
			current = l
		}
	}
	if !current.Issued || current.Device == "" || current.ObservationKey == "" || current.ExpiresAt <= u.now().UnixMilli() || !freshPlaybackEvidence(current.LastPlaybackAt, u.now()) {
		u.mu.Unlock()
		return nil
	}
	matches := 0
	for _, l := range u.doc.Leases {
		if l.Key != current.Key && l.Issued && l.ExpiresAt > u.now().UnixMilli() && l.Device == current.Device && l.UserID == current.UserID && l.ServerID == current.ServerID {
			previous = l
			matches++
		}
	}
	eligible := matches == 1 && previous.PlaybackSource == current.PlaybackSource && u.refs[previous.Generation] == 0 && handoffIdle(previous, u.now())
	u.mu.Unlock()
	if !eligible {
		return nil
	}
	a := &MediaAuthorization{service: s, userLease: &UserPlaybackLease{pool: u, Record: current, handoff: &previous, policyRevision: policyRevision}}
	if current.PlaybackSource == "assigned" {
		if s.Pool == nil {
			return nil
		}
		p := s.Pool
		p.mu.Lock()
		for key, l := range p.doc.Leases {
			if key == l.Key && l.Issued && !l.Stopped && l.ExpiresAt > p.now().UnixMilli() && l.UserID == current.UserID && l.SessionKey == current.EventKey && l.UserGeneration == current.Generation {
				if a.poolLease != nil {
					p.mu.Unlock()
					return nil // Multiple media-source targets: do not guess.
				}
				a.poolLease = &PoolLease{pool: p, Record: l, healthSequence: p.doc.Health[l.AccountID].Sequence}
			}
		}
		p.mu.Unlock()
		if a.poolLease == nil {
			return nil
		}
	}
	err := a.commitHandoff(ctx)
	if errors.Is(err, ErrUserPlaybackStopped) || errors.Is(err, ErrAccountChanged) {
		return nil // A racing stop/replay/activity invalidated this optional handoff.
	}
	return err
}
