package service

import (
	"context"
	"strings"
	"time"
)

type poolPreference struct {
	UID   string
	Until int64
}
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

// Pool lock must be held. Recent preference lives 15 minutes, bounded to 1024
// hashed users. It never changes existing sessions or overrides lower load.
func (p *DynamicPool) poolPreferences(ctx context.Context, user string, now int64) func(string) int {
	for key, pref := range p.recent {
		if pref.Until <= now {
			delete(p.recent, key)
		}
	}
	pref := p.recent[DirectLinkKey("pool-preference", user)]
	copies, _ := ctx.Value(poolCopyPreferenceKey{}).(map[string]bool)
	return func(uid string) int {
		score := 0
		if copies[uid] {
			score += 2
		}
		if pref.UID == uid {
			score++
		}
		return score
	}
}

func (p *DynamicPool) rememberPoolPreference(user, uid string) {
	now := p.now()
	for key, pref := range p.recent {
		if pref.Until <= now.UnixMilli() {
			delete(p.recent, key)
		}
	}
	key := DirectLinkKey("pool-preference", user)
	if _, ok := p.recent[key]; !ok && len(p.recent) >= 1024 {
		oldest := ""
		var until int64
		for k, pref := range p.recent {
			if oldest == "" || pref.Until < until {
				oldest, until = k, pref.Until
			}
		}
		delete(p.recent, oldest)
	}
	p.recent[key] = poolPreference{UID: uid, Until: now.Add(15 * time.Minute).UnixMilli()}
}
