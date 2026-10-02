package service

import (
	"context"
	"errors"
	"hash/fnv"
	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
	"time"
)

func (s *MemberPlaybackService) configurePoolRequests() {
	c := s.Pool.Settings()
	s.PoolRequests.Configure(time.Duration(c.UIDIntervalMS)*time.Millisecond, time.Duration(c.GlobalIntervalMS)*time.Millisecond)
	s.PoolRequests.CoolUntil(s.Pool.CoolingUntil())
}
func (s *MemberPlaybackService) ConfigurePool(ctx context.Context, c model.PlaybackPoolSettings) error {
	if c.NotificationBotID != "" && (s.ValidatePoolNotifier == nil) {
		return memberError("通知服务未配置")
	}
	if c.NotificationBotID != "" {
		if err := s.ValidatePoolNotifier(c.NotificationBotID); err != nil {
			return err
		}
	}
	if err := s.Pool.Configure(ctx, c); err != nil {
		return err
	}
	s.configurePoolRequests()
	return nil
}
func (s *MemberPlaybackService) CheckPoolAccount(ctx context.Context, id string) error {
	a, err := s.accounts.Get(id)
	if err != nil || !eligiblePoolAccount(a) {
		return memberError("小号未完成登录或目录配置")
	}
	return s.probePoolAccount(ctx, a)
}
func (s *MemberPlaybackService) probePoolAccount(ctx context.Context, a P115Account) error {
	sequence, release, err := s.Pool.beginProbe(a, false)
	if err != nil {
		return err
	}
	defer release()
	return s.executePoolProbe(ctx, a, sequence)
}

func (s *MemberPlaybackService) executePoolProbe(ctx context.Context, a P115Account, sequence uint64) error {
	if s.Pool.CoolingUntil().After(time.Now()) {
		return memberError("共享池正在限流冷却，请稍后检查")
	}
	select {
	case s.driveHealthGate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	default:
		return ErrPoolBusy
	}
	defer func() { <-s.driveHealthGate }()
	pctx, cancel := context.WithTimeout(p115.WithBackgroundBudget(p115.WithRequestBudget(ctx, &s.PoolRequests)), 8*time.Second)
	err := s.checkMediaDrive(pctx, a)
	cancel()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if persistErr := s.PersistPoolCooling(); persistErr != nil {
		return persistErr
	}
	current, e := s.accounts.Get(a.ID)
	if e != nil || current.Revision != a.Revision {
		return ErrAccountChanged
	}
	if errors.Is(err, p115.ErrRateQueueFull) || errors.Is(err, context.Canceled) {
		return err
	}
	// The safe status is presented in AccountOptions, not raw upstream errors.
	return s.Pool.reportChecked(ctx, a, err, &sequence)
}

func poolProbeJitter(id string) time.Duration {
	h := fnv.New32a()
	_, _ = h.Write([]byte(id))
	return time.Duration(h.Sum32()%15000) * time.Millisecond
}

// No probe queue: one per UID, at most one global background probe/minute.
// Manual calls have a 15-second server-side debounce, including failed calls.
func (p *DynamicPool) beginProbe(a P115Account, background bool) (uint64, func(), error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now()
	if p.doc.GlobalUntil > now.UnixMilli() {
		return 0, nil, ErrPoolCooling
	}
	for uid, until := range p.probes {
		if !until.After(now) {
			delete(p.probes, uid)
		}
	}
	if p.probes[a.UID].After(now) || len(p.probes) >= 128 {
		return 0, nil, ErrPoolBusy
	}
	h := p.doc.Health[a.ID]
	if h.Revision == a.Revision && h.State == "throttled" && h.Until > now.UnixMilli() {
		return 0, nil, ErrPoolCooling
	}
	if background && (now.Sub(p.lastBackgroundProbe) < time.Minute || (len(p.busy) > 0 || p.waiting > 0) && now.Sub(p.lastBackgroundProbe) < 5*time.Minute) {
		return 0, nil, ErrPoolBusy
	}
	if background {
		p.lastBackgroundProbe = now
	}
	p.probes[a.UID] = now.Add(30 * time.Second)
	return h.Sequence, func() { p.mu.Lock(); p.probes[a.UID] = p.now().Add(15 * time.Second); p.mu.Unlock() }, nil
}
func (s *MemberPlaybackService) refreshPoolHealth(ctx context.Context) {
	if s.Pool == nil {
		return
	}
	defer s.flushPoolNotifications(ctx)
	accounts := s.poolAccounts()
	s.Pool.mu.Lock()
	d := s.Pool.clone()
	live := map[string]bool{}
	for _, v := range s.accounts.ManagedList() {
		live[v.ID] = true
	}
	changed := false
	for id := range d.Health {
		if !live[id] {
			delete(d.Health, id)
			changed = true
		}
	}
	for id := range d.Modes {
		if !live[id] {
			delete(d.Modes, id)
			changed = true
		}
	}
	for k, l := range d.Leases {
		if l.ExpiresAt <= s.Pool.now().UnixMilli() {
			delete(d.Leases, k)
			changed = true
		}
	}
	var saveErr error
	if changed {
		saveErr = s.Pool.save(ctx, d)
	}
	var candidate *P115Account
	for _, a := range accounts {
		h := d.Health[a.ID]
		if d.Modes[a.ID] == "disabled" || h.Revision == a.Revision && (h.Until > s.Pool.now().UnixMilli() || h.NextProbe > s.Pool.now().UnixMilli()) {
			continue
		}
		if candidate == nil || h.CheckedAt < d.Health[candidate.ID].CheckedAt {
			v := a
			candidate = &v
		}
	}
	s.Pool.mu.Unlock()
	if saveErr != nil {
		return
	}
	// One background probe per minute; foreground has its own real-time check.
	if candidate != nil {
		if s.Cache != nil && s.Cache.foregroundWaiting.Load() > 0 {
			s.Pool.mu.Lock()
			yield := s.Pool.now().Sub(s.Pool.lastBackgroundProbe) < 5*time.Minute
			s.Pool.mu.Unlock()
			if yield {
				return
			}
		}
		sequence, release, err := s.Pool.beginProbe(*candidate, true)
		if err != nil {
			return
		}
		defer release()
		_ = s.executePoolProbe(ctx, *candidate, sequence)
	}
}
