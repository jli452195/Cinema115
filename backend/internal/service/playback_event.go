package service

import (
	"context"
	"errors"
	"time"
)

// Captured before the upstream request. Only these generations may be changed
// by its response, even if a stop/replay races with that response.
type PlaybackEvent struct {
	key             string
	userGeneration  uint64
	poolGenerations map[string]uint64
}

func (e PlaybackEvent) Matched() bool { return e.userGeneration != 0 || len(e.poolGenerations) > 0 }

func (s *MemberPlaybackService) CapturePlaybackEvent(server, id, session, client, item string, missingSession bool) PlaybackEvent {
	c := s.emby.snapshot()
	r := s.playbackRecord(c.ServerID, id)
	if c.URL != server || r.UserID == "" {
		return PlaybackEvent{}
	}
	e := PlaybackEvent{key: DirectLinkKey(r.ServerID, r.UserID, session, item), poolGenerations: make(map[string]uint64)}
	fallback := ""
	if missingSession && client != "" {
		fallback = DirectLinkKey("playback-event", r.ServerID, r.UserID, client, item)
	}
	if missingSession && fallback == "" {
		return PlaybackEvent{}
	}
	if s.UserPlayback != nil {
		p := s.UserPlayback
		p.mu.Lock()
		for _, l := range p.doc.Leases {
			if l.ExpiresAt <= p.now().UnixMilli() || l.UserID != r.UserID || l.ServerID != r.ServerID {
				continue
			}
			if !missingSession && l.EventKey == e.key || fallback != "" && l.EventFallback == fallback {
				// With multiple sessions, token + item is only a fallback when unique.
				// An ambiguous event must never stop an arbitrarily selected session.
				if e.userGeneration != 0 {
					p.mu.Unlock()
					return PlaybackEvent{}
				}
				e.key = l.EventKey
				e.userGeneration = l.Generation
			}
		}
		p.mu.Unlock()
	}
	if s.Pool != nil {
		p := s.Pool
		p.mu.Lock()
		// Without PlaySessionId, only a uniquely matched user session
		// is eligible. Never guess which historical pool session was stopped.
		if !missingSession || e.userGeneration != 0 {
			for key, l := range p.doc.Leases {
				if l.UserID == r.UserID && l.SessionKey == e.key && l.ExpiresAt > p.now().UnixMilli() {
					e.poolGenerations[key] = l.Generation
				}
			}
		}
		p.mu.Unlock()
	}
	return e
}

func (s *MemberPlaybackService) ApplyPlaybackEvent(ctx context.Context, e PlaybackEvent, stopped bool) error {
	var userErr, poolErr error
	if s.UserPlayback != nil && e.userGeneration != 0 {
		_, userErr = s.UserPlayback.Event(ctx, e.key, stopped, e.userGeneration)
	}
	// Pool release is independent of the user slot, which may already be gone.
	if s.Pool != nil {
		poolErr = s.Pool.applyCapturedEvent(ctx, e, stopped)
	}
	return errors.Join(userErr, poolErr)
}

func (s *MemberPlaybackService) ReleaseUserPlayback(ctx context.Context, key string, generation uint64) error {
	p := s.UserPlayback
	p.mu.Lock()
	l, ok := p.doc.Leases[key]
	p.mu.Unlock()
	if !ok {
		return nil
	}
	if l.Generation != generation {
		return ErrAccountChanged
	}
	e := PlaybackEvent{key: l.EventKey, poolGenerations: make(map[string]uint64)}
	if s.Pool != nil {
		s.Pool.mu.Lock()
		for k, lease := range s.Pool.doc.Leases {
			if lease.UserID == l.UserID && lease.SessionKey == l.EventKey {
				e.poolGenerations[k] = lease.Generation
			}
		}
		s.Pool.mu.Unlock()
	}
	// First fence further issuance; failure to persist never releases pool slots.
	if err := p.Release(ctx, key, generation); err != nil {
		return err
	}
	if s.Pool != nil {
		return s.Pool.applyCapturedEvent(ctx, e, true)
	}
	return nil
}

func (p *DynamicPool) applyCapturedEvent(ctx context.Context, e PlaybackEvent, stopped bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	d := p.clone()
	now := p.now()
	changed := false
	for key, generation := range e.poolGenerations {
		l, ok := d.Leases[key]
		if !ok || l.Generation != generation || l.ExpiresAt <= now.UnixMilli() {
			continue
		}
		if stopped {
			if !l.Issued {
				delete(d.Leases, key)
				changed = true
				continue
			}
			if l.Stopped {
				continue
			}
			l.Stopped = true
			l.ExpiresAt = min(l.ExpiresAt, now.Add(2*time.Minute).UnixMilli())
		} else {
			if !l.Issued || l.Stopped || key != l.Key {
				continue
			}
			until := now.Add(time.Duration(d.Settings.LeaseMinutes) * time.Minute).UnixMilli()
			if until-l.ExpiresAt < 60000 {
				continue
			}
			l.ExpiresAt = until
		}
		d.Leases[key] = l
		changed = true
	}
	if !changed {
		return nil
	}
	return p.save(ctx, d)
}
