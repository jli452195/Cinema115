package service

import (
	"context"
	"time"

	"strm115/backend/internal/client/emby"
)

func (s *MemberPlaybackService) reconcilePoolSessions(ctx context.Context) {
	if s.Pool == nil || s.emby == nil {
		return
	}
	p := s.Pool
	p.mu.Lock()
	now := p.now()
	if now.Sub(p.lastReconcile) < time.Minute {
		p.mu.Unlock()
		return
	}
	p.lastReconcile = now
	hasLease := false
	for k, l := range p.doc.Leases {
		if k == l.Key && l.Issued && !l.Stopped && l.ObservationKey != "" && l.ExpiresAt > now.UnixMilli() {
			hasLease = true
			break
		}
	}
	p.mu.Unlock()
	if s.UserPlayback != nil {
		if err := s.UserPlayback.Reconcile(ctx, nil); err != nil {
			return
		}
		hasLease = hasLease || len(s.UserPlayback.List()) > 0
	}
	if !hasLease {
		return
	}
	cfg := s.emby.snapshot()
	if cfg.ServerID == "" || cfg.APIKey == "" {
		return
	}
	client, ok := s.NewEmby(cfg.URL, cfg.APIKey).(interface {
		PlaybackSessions(context.Context) ([]emby.PlaybackSession, error)
	})
	if !ok {
		return
	}
	readCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	sessions, err := client.PlaybackSessions(readCtx)
	cancel()
	if err != nil {
		return
	}
	current := s.emby.snapshot()
	if current.ServerID != cfg.ServerID || current.URL != cfg.URL || current.APIKey != cfg.APIKey {
		return
	}
	keys := make(map[string]bool, len(sessions))
	for _, session := range sessions {
		play := session.PlaySessionID
		if play == "" {
			play = session.PlayState.PlaySessionID
		}
		if session.PlaySessionID != "" && session.PlayState.PlaySessionID != "" && session.PlaySessionID != session.PlayState.PlaySessionID {
			continue
		}
		if play == "" || len(play) > 256 || session.UserID == "" || session.NowPlayingItem.ID == "" || session.LastActivityDate.Before(now.Add(-90*time.Second)) || session.LastActivityDate.After(now.Add(time.Minute)) {
			continue
		}
		keys[DirectLinkKey("pool-observation", cfg.ServerID, session.UserID, play, session.NowPlayingItem.ID)] = true
	}
	_ = p.reconcileObserved(ctx, keys)
	if s.UserPlayback != nil {
		_ = s.UserPlayback.Reconcile(ctx, keys)
	}
}

// Only exact-match evidence renews a current lease. Missing fields or sessions,
// offline Emby, and stopped/residual/expired leases retain conservative TTL.
func (p *DynamicPool) reconcileObserved(ctx context.Context, keys map[string]bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	d := p.clone()
	now := p.now()
	changed := false
	for k, l := range d.Leases {
		if k != l.Key || !l.Issued || l.Stopped || l.ExpiresAt <= now.UnixMilli() || l.ObservationKey == "" || !keys[l.ObservationKey] {
			continue
		}
		until := now.Add(time.Duration(d.Settings.LeaseMinutes) * time.Minute).UnixMilli()
		if until-l.ExpiresAt < int64(time.Minute/time.Millisecond) {
			continue
		}
		l.ExpiresAt = until
		d.Leases[k] = l
		changed = true
	}
	if !changed {
		return nil
	}
	return p.save(ctx, d)
}
