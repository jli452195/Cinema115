package service

import (
	"context"
	"time"

	"strm115/backend/internal/model"
)

const deviceCandidateTTL = 90 * time.Second

func requestPoolSlot(req PoolRequest, user string) string {
	if req.Download || req.Task || req.Device == "" {
		return ""
	}
	if req.slotKey != "" {
		return req.slotKey
	}
	// Device already contains the authenticated server/user/player scope.
	return DirectLinkKey("pool-slot", user, req.Device)
}

func poolCapacityKey(key string, l model.PlaybackPoolLease) string {
	if l.SlotKey != "" {
		return "device:" + l.SlotKey
	}
	return "lease:" + key
}

func poolSlotTarget(d model.PlaybackPoolDocument, slot, user string, now int64) model.PlaybackPoolLease {
	var result model.PlaybackPoolLease
	if slot == "" {
		return result
	}
	for key, l := range d.Leases {
		if key != l.Key || l.SlotKey != slot || l.UserID != user || l.ExpiresAt <= now {
			continue
		}
		rank := func(v model.PlaybackPoolLease) int {
			if v.Stopped {
				return 0
			}
			if v.Issued {
				return 2
			}
			return 1
		}
		if result.Key == "" || rank(l) > rank(result) || rank(l) == rank(result) && l.Generation > result.Generation {
			result = l
		}
	}
	return result
}

func poolHasSlot(d model.PlaybackPoolDocument, slot, uid string, now int64) bool {
	if slot == "" {
		return false
	}
	for _, l := range d.Leases {
		if l.SlotKey == slot && l.UID == uid && l.ExpiresAt > now {
			return true
		}
	}

	return false
}

// Lock order user -> pool. Network work has already completed with pinned
// credentials. A failed transaction leaves both lifecycles unchanged.
func (a *MediaAuthorization) commitDevicePlayback(ctx context.Context) error {
	u, p := a.userLease.pool, a.poolLease.pool
	u.mu.Lock()
	defer u.mu.Unlock()
	p.mu.Lock()
	defer p.mu.Unlock()
	ud, pd := u.clone(), p.clone()
	now := u.now()
	current, ok := ud.Leases[a.userLease.Record.Key]
	if !ok || current.Generation != a.userLease.Record.Generation || current.ExpiresAt <= now.UnixMilli() {
		return ErrUserPlaybackStopped
	}
	if err := a.userLease.applyDevicePolicy(&ud, &pd, current, now); err != nil {
		return err
	}
	wasIssued := current.Issued
	lease, ok := pd.Leases[a.poolLease.Record.Key]
	if !ok || lease.Generation != a.poolLease.Record.Generation || lease.Stopped || lease.ExpiresAt <= now.UnixMilli() || pd.Modes[lease.AccountID] == "disabled" {
		return ErrAccountChanged
	}
	for key, other := range pd.Leases {
		if key != lease.Key && other.SlotKey == lease.SlotKey && other.Issued && !other.Stopped && other.ExpiresAt > now.UnixMilli() && other.Generation > lease.Generation && other.UID != lease.UID {
			return ErrAccountChanged
		}
	}
	h := pd.Health[lease.AccountID]
	if h.Sequence != a.poolLease.healthSequence && h.State != "ready" && h.Revision == lease.Revision {
		return ErrAccountChanged
	}
	transfer := ud.DevicePolicy.Mode == DevicePolicyHeartbeat
	// Keep every confirmed session independent. For clients without playback
	// events, preserve one anchor plus the latest candidate, never an unbounded
	// sequence of URL-only records. Do not retire in-flight preparations.
	var anchor model.UserPlaybackLease
	confirmed := false
	for _, other := range ud.Leases {
		if other.Key == current.Key || other.Device != current.Device || other.ServerID != current.ServerID || other.UserID != current.UserID || other.PlaybackSource != current.PlaybackSource || !other.Issued || other.ExpiresAt <= now.UnixMilli() {
			continue
		}
		if other.LastPlaybackAt != 0 {
			confirmed = true
		}
		if anchor.Key == "" || other.Generation < anchor.Generation {
			anchor = other
		}
	}
	for key, other := range ud.Leases {
		if wasIssued || key == current.Key || !other.Issued || other.LastPlaybackAt != 0 || other.Device != current.Device || other.ServerID != current.ServerID || other.UserID != current.UserID || other.PlaybackSource != current.PlaybackSource || u.refs[other.Generation] != 0 || !confirmed && key == anchor.Key {
			continue
		}
		delete(ud.Leases, key)
		ud.Stopped[other.EventKey] = now.Add(5 * time.Minute).UnixMilli()
		if other.EventFallback != "" {
			ud.Stopped["fallback:"+other.EventFallback] = now.Add(5 * time.Minute).UnixMilli()
		}
		for pk, child := range pd.Leases {
			if child.SlotKey == lease.SlotKey && child.SessionKey == other.EventKey && child.UserGeneration == other.Generation {
				if child.UID == lease.UID || transfer {
					delete(pd.Leases, pk)
				} else {
					child.Stopped = true
					child.ExpiresAt = min(child.ExpiresAt, now.Add(2*time.Minute).UnixMilli())
					pd.Leases[pk] = child
				}
			}
		}
	}
	unconfirmed := 0
	for key, other := range ud.Leases {
		if key != current.Key && other.Issued && other.LastPlaybackAt == 0 && other.ExpiresAt > now.UnixMilli() && other.Device == current.Device && other.ServerID == current.ServerID && other.UserID == current.UserID && other.PlaybackSource == current.PlaybackSource {
			unconfirmed++
		}
	}
	if !wasIssued && current.LastPlaybackAt == 0 && (confirmed && unconfirmed > 0 || !confirmed && unconfirmed > 1) {
		return ErrPoolBusy // An in-flight candidate cannot be evicted safely.
	}
	// A stopped child on the same UID no longer needs a second record when a
	// new live child owns the slot. Old requests remain fenced by generation.
	for key, other := range pd.Leases {
		if key == lease.Key || other.SlotKey != lease.SlotKey {
			continue
		}
		if other.UID == lease.UID && other.Stopped {
			delete(pd.Leases, key)
		} else if other.UID != lease.UID && other.Issued && !other.Stopped {
			if transfer {
				delete(pd.Leases, key)
				continue
			}
			other.Stopped = true
			other.ExpiresAt = min(other.ExpiresAt, now.Add(2*time.Minute).UnixMilli())
			pd.Leases[key] = other
		}
	}
	current.Issued, current.IssuedAt, current.UpdatedAt = true, now.UnixMilli(), now.UnixMilli()
	current.FirstIssuedAt = deviceFirstIssued(ud, current, now)
	if ud.Stopped["fallback:"+current.EventFallback] > now.UnixMilli() {
		current.EventFallback = ""
	}
	current.ExpiresAt = now.Add(userPlaybackIdleTTL).UnixMilli()
	if current.LastPlaybackAt == 0 && (current.CandidateUntil != 0 || !wasIssued && (confirmed || anchor.Key != "")) {
		if current.CandidateUntil == 0 {
			current.CandidateUntil = now.Add(deviceCandidateTTL).UnixMilli()
		}
		current.ExpiresAt = current.CandidateUntil
		if current.ExpiresAt <= now.UnixMilli() {
			return ErrUserPlaybackStopped
		}
	} else {
		current.CandidateUntil = 0
	}
	ud.Leases[current.Key] = current
	lease.Issued, lease.IssuedAt, lease.BorrowedKey, lease.BorrowedGeneration = true, now.UnixMilli(), "", 0
	lease.ExpiresAt = min(current.ExpiresAt, now.Add(time.Duration(pd.Settings.LeaseMinutes)*time.Minute).UnixMilli())
	pd.Leases[lease.Key] = lease
	if transfer {
		releaseTransferredResiduals(&pd, ud, current.UserID, lease.Key)
		// A borrowed predecessor may expire or disappear during network work.
		// Count the final document after retirement, with borrowing cleared.
		if poolUIDOccupancy(pd, lease.UID, now.UnixMilli()).total > pd.Settings.MaxSessions {
			return ErrPoolBusy
		}
	}
	if h.Sequence == a.poolLease.healthSequence {
		health := model.PlaybackPoolHealth{Revision: lease.Revision, UID: lease.UID, State: "ready", CheckedAt: now.UnixMilli(), Sequence: h.Sequence + 1, NextProbe: now.Add(5*time.Minute + poolProbeJitter(lease.AccountID)).UnixMilli()}
		pd.Health[lease.AccountID] = health
		p.recordHealthEvent(&pd, lease.AccountID, h, health)
	}
	if err := a.service.db.SavePlaybackState(ctx, ud, pd); err != nil {
		return err
	}
	a.retiredDevices = retiredDeviceCount(u.doc, ud, current)
	u.doc, p.doc = ud, pd
	a.userLease.Record, a.poolLease.Record = current, lease
	a.userLease.handoff = nil
	p.rememberPoolPreference(lease.UserID, lease.UID)
	return nil
}
