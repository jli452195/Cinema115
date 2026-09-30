package service

import (
	"context"
	"fmt"
	"sync"
)

// Target UIDs serialize cloud writes; unrelated UIDs may run concurrently.
// Exclusive maintenance waits for active requests and prevents resurrection.
type playbackScheduler struct {
	mu        sync.Mutex
	changed   chan struct{}
	uids      map[string]bool
	exclusive bool
	writers   int
}

func (s *PlaybackCache) acquirePlayback(ctx context.Context, uid string) (func(), error) {
	p := &s.scheduler
	warm, _ := ctx.Value(playbackPrewarmKey{}).(bool)
	if !warm {
		s.foregroundWaiting.Add(1)
		defer s.foregroundWaiting.Add(-1)
	}
	p.mu.Lock()
	if p.changed == nil {
		p.changed = make(chan struct{})
		p.uids = make(map[string]bool)
	}
	if uid == "" {
		p.writers++
	}
	defer func() {
		if uid == "" {
			p.writers--
			close(p.changed)
			p.changed = make(chan struct{})
		}
		p.mu.Unlock()
	}()
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		available := !p.exclusive && ((uid == "" && len(p.uids) == 0) || (uid != "" && p.writers == 0 && len(p.uids) < 4 && !p.uids[uid]))
		if warm && (!available || s.foregroundWaiting.Load() > 0) {
			return nil, fmt.Errorf("播放正在使用或等待，跳过本次预热")
		}
		if available {
			if uid == "" {
				p.exclusive = true
			} else {
				p.uids[uid] = true
			}
			return func() {
				p.mu.Lock()
				if uid == "" {
					p.exclusive = false
				} else {
					delete(p.uids, uid)
				}
				close(p.changed)
				p.changed = make(chan struct{})
				p.mu.Unlock()
			}, nil
		}
		changed := p.changed
		p.mu.Unlock()
		select {
		case <-ctx.Done():
		case <-changed:
		}
		p.mu.Lock()
	}
}
