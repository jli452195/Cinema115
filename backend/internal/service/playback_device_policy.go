package service

import (
	"context"
	"errors"
	"time"

	"strm115/backend/internal/model"
)

const (
	DevicePolicyParallel  = "parallel"
	DevicePolicyHeartbeat = "heartbeat_takeover"
)

var (
	ErrPlaybackDeviceConflict = errors.New("此用户的其他设备仍在播放或准备中，请停止原设备或等待播放心跳超时")
	ErrPlaybackDeviceIdentity = errors.New("此播放策略需要可信设备信息，请通过反代退出登录后重新登录")
)

func normalizeDevicePolicy(v model.PlaybackDevicePolicy) (model.PlaybackDevicePolicy, error) {
	if v.Mode == "" {
		v.Mode = DevicePolicyParallel
	}
	if v.SilenceSeconds == 0 {
		v.SilenceSeconds = 60
	}
	if v.Mode != DevicePolicyParallel && v.Mode != DevicePolicyHeartbeat {
		return v, memberError("未知的播放设备策略")
	}
	if v.SilenceSeconds < 30 || v.SilenceSeconds > 180 {
		return v, memberError("播放心跳静默时间须为 30 至 180 秒")
	}
	return v, nil
}

func (p *UserPlayback) DevicePolicy() model.PlaybackDevicePolicy {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.doc.DevicePolicy
}

func (p *UserPlayback) ConfigureDevicePolicy(ctx context.Context, v model.PlaybackDevicePolicy) error {
	v, err := normalizeDevicePolicy(v)
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.doc.DevicePolicy == v {
		return nil
	}
	d := p.clone()
	d.DevicePolicy = v
	d.PolicyRevision++ // Fence preparations admitted under a different strategy.
	return p.save(ctx, d)
}

// Called with UserPlayback.mu held, both before cloud work and at commit.
// Preparations reserve admission without retiring or muting the old device.
func checkDeviceAdmission(d model.UserPlaybackDocument, current model.UserPlaybackLease, now time.Time) error {
	if d.DevicePolicy.Mode != DevicePolicyHeartbeat {
		return nil
	}
	if current.Device == "" {
		return ErrPlaybackDeviceIdentity
	}
	ownIssued := false
	for _, other := range d.Leases {
		if other.ServerID == current.ServerID && other.UserID == current.UserID && other.Device == current.Device && other.Issued && other.ExpiresAt > now.UnixMilli() {
			ownIssued = true
		}
	}
	for _, other := range d.Leases {
		if other.Key == current.Key || other.ServerID != current.ServerID || other.UserID != current.UserID || other.Device == current.Device || other.ExpiresAt <= now.UnixMilli() {
			continue
		}
		if !other.Issued {
			if !ownIssued {
				return ErrPlaybackDeviceConflict
			}
			continue // The incumbent may refresh while a challenger prepares.
		}
		first := other.FirstIssuedAt
		if first == 0 {
			first = other.IssuedAt
		}
		if now.UnixMilli()-max(first, other.LastPlaybackAt) < int64(d.DevicePolicy.SilenceSeconds)*1000 {
			return ErrPlaybackDeviceConflict
		}
	}
	return nil
}

// Preserve a device's initial grace across URL refreshes and prefetch children.
// Only accepted playback evidence can keep extending its activity window.
func deviceFirstIssued(d model.UserPlaybackDocument, current model.UserPlaybackLease, now time.Time) int64 {
	first := current.FirstIssuedAt
	if first == 0 {
		first = now.UnixMilli()
	}
	for _, other := range d.Leases {
		if other.ServerID == current.ServerID && other.UserID == current.UserID && other.Device == current.Device && other.Device != "" && other.Issued && other.ExpiresAt > now.UnixMilli() {
			at := other.FirstIssuedAt
			if at == 0 {
				at = other.IssuedAt
			}
			if at > 0 {
				first = min(first, at)
			}
		}
	}
	return first
}

func retiredDeviceCount(before, after model.UserPlaybackDocument, current model.UserPlaybackLease) int {
	devices := make(map[string]bool)
	for key, old := range before.Leases {
		if _, ok := after.Leases[key]; !ok && old.Device != current.Device && old.UserID == current.UserID && old.ServerID == current.ServerID {
			devices[old.Device] = true
		}
	}
	return len(devices)
}

// Mutates cloned documents only; publication must follow a successful transaction.
func (l *UserPlaybackLease) applyDevicePolicy(ud *model.UserPlaybackDocument, pd *model.PlaybackPoolDocument, current model.UserPlaybackLease, now time.Time) error {
	if l.policyRevision != ud.PolicyRevision {
		return ErrUserPlaybackStopped
	}
	if err := checkDeviceAdmission(*ud, current, now); err != nil {
		return err
	}
	if ud.DevicePolicy.Mode != DevicePolicyHeartbeat {
		return nil
	}
	ownIssued := false
	for _, other := range ud.Leases {
		if other.ServerID == current.ServerID && other.UserID == current.UserID && other.Device == current.Device && other.Issued && other.ExpiresAt > now.UnixMilli() {
			ownIssued = true
		}
	}
	for key, other := range ud.Leases {
		if key == current.Key || other.ServerID != current.ServerID || other.UserID != current.UserID || other.Device == current.Device || !other.Issued && ownIssued {
			continue
		}
		delete(ud.Leases, key)
		ud.Stopped[other.EventKey] = now.Add(24 * time.Hour).UnixMilli()
		if other.EventFallback != "" {
			ud.Stopped["fallback:"+other.EventFallback] = now.Add(5 * time.Minute).UnixMilli()
		}
		if pd == nil {
			continue
		}
		for pk, child := range pd.Leases {
			if child.UserID != other.UserID || child.SessionKey != other.EventKey || child.UserGeneration != other.Generation {
				continue
			}
			delete(pd.Leases, pk)
		}
	}
	return nil
}

// Non-pool playback can replace an old pool playback after a source change.
// Release its old pool capacity and retire the user in the same transaction.
func (a *MediaAuthorization) commitNonPoolDevicePolicy(ctx context.Context) error {
	u := a.userLease.pool
	u.mu.Lock()
	defer u.mu.Unlock()
	p := a.service.Pool
	if p == nil {
		return ErrUserPlaybackBusy
	}
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
	releaseTransferredResiduals(&pd, ud, current.UserID, "")
	current.FirstIssuedAt = deviceFirstIssued(ud, current, now)
	current.Issued = true
	current.IssuedAt, current.UpdatedAt = now.UnixMilli(), now.UnixMilli()
	current.ExpiresAt = now.Add(userPlaybackIdleTTL).UnixMilli()
	current.Target = a.playbackTarget()
	current.Target.IssuedAt = now.UnixMilli()
	ud.Leases[current.Key] = current
	if err := a.service.db.SavePlaybackState(ctx, ud, pd); err != nil {
		return err
	}
	a.retiredDevices = retiredDeviceCount(u.doc, ud, current)
	u.doc, p.doc = ud, pd
	a.userLease.Record = current
	a.userLease.handoff = nil
	return nil
}

// Only authenticated playback slots are transferable. Download/task leases have
// no device slot and must never be borrowed or retired by this policy. Business
// user IDs are globally unique identities already bound to one Emby server.
func poolTransferCandidate(d model.PlaybackPoolDocument, user, slot string, accounts []P115Account, excluded map[string]bool, now int64) model.PlaybackPoolLease {
	var best model.PlaybackPoolLease
	for key, old := range d.Leases {
		if key != old.Key || old.UserID != user || old.SlotKey == "" || old.SlotKey == slot || old.UserGeneration == 0 || !old.Issued || old.ExpiresAt <= now || excluded[old.UID] {
			continue
		}
		available := false
		for _, a := range accounts {
			h := d.Health[a.ID]
			if eligiblePoolAccount(a) && a.ID == old.AccountID && a.UID == old.UID && a.Revision == old.Revision && a.PlaybackParentCID == old.DirectoryID && d.Modes[a.ID] != "disabled" && d.Modes[a.ID] != "draining" && h.State == "ready" && h.Revision == a.Revision && h.Until <= now {
				available = true
				break
			}
		}
		if !available {
			continue
		}
		borrowed := false
		for _, pending := range d.Leases {
			if !pending.Issued && pending.ExpiresAt > now && pending.BorrowedKey == key && pending.BorrowedGeneration == old.Generation {
				borrowed = true
				break
			}
		}
		if borrowed {
			continue
		}
		if best.Key == "" || best.Stopped && !old.Stopped || best.Stopped == old.Stopped && old.Generation > best.Generation {
			best = old
		}
	}
	return best
}

// Run only on the cloned pool at successful heartbeat-policy commit. Stopped
// playback records are accounting history, not revocable CDN connections.
func releaseTransferredResiduals(d *model.PlaybackPoolDocument, ud model.UserPlaybackDocument, user, keep string) {
	live := make(map[string]uint64, len(ud.Leases))
	for _, l := range ud.Leases {
		if l.UserID == user {
			live[l.EventKey] = l.Generation
		}
	}
	for key, old := range d.Leases {
		if key == keep || old.UserID != user || old.SlotKey == "" || old.UserGeneration == 0 {
			continue
		}
		if old.Stopped || key != old.Key || old.Issued && live[old.SessionKey] != old.UserGeneration {
			delete(d.Leases, key)
		}
	}
}
