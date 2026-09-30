package service

import (
	"context"
	"fmt"
	"time"

	"strm115/backend/internal/client/p115"
)

// Memory-only, credential-scoped cooldowns. Errors are never treated as misses.
type playbackSearchBackoff struct {
	until time.Time
	err   error
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
func (s *PlaybackCache) backoffPlaybackSearch(key, scope string, until time.Time, err error) {
	s.runtimeMu.Lock()
	defer s.runtimeMu.Unlock()
	if s.searchBackoff == nil {
		s.searchBackoff = make(map[string]playbackSearchBackoff)
	}
	for k, v := range s.searchBackoff {
		if time.Now().After(v.until) {
			delete(s.searchBackoff, k)
		}
	}
	if len(s.searchBackoff) >= 512 {
		return
	}
	s.searchBackoff[DirectLinkKey(key, scope)] = playbackSearchBackoff{until: until, err: err}
}
func (s *PlaybackCache) waitPlaybackSearch(ctx context.Context, key, scope string) error {
	s.runtimeMu.Lock()
	defer s.runtimeMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	k := DirectLinkKey(key, scope)
	v, ok := s.searchBackoff[k]
	if !ok {
		return nil
	}
	if !time.Now().Before(v.until) {
		delete(s.searchBackoff, k)
		return nil
	}
	if v.err != nil {
		// A cached rejection is not fresh evidence for a recovery query.
		return fmt.Errorf("目标文件查询暂不可用，请稍后重试；未执行秒传：%s", p115.SharePlaybackFailureReason(v.err))
	}
	// If an administrator cleared the pending record, do not retain its miss.
	if e, ok := s.entryByKey(key); !ok || !time.Now().Before(e.RetryNotBefore) {
		delete(s.searchBackoff, k)
		return nil
	}
	return &PlaybackPendingError{RetryAt: v.until}
}
