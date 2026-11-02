package service

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	"strm115/backend/internal/client/clouddrive2"
)

type CD2Event struct {
	Change    *clouddrive2.Change
	Completed []clouddrive2.Change
	Mode      string
	Message   string
	Rescan    bool
	Revision  uint64
}

type cd2EventHub struct {
	ctx         context.Context
	cancel      context.CancelFunc
	done        chan struct{}
	wake        chan struct{}
	subscribers map[chan CD2Event]struct{}
	latest      CD2Event
}

// Watch 共用一个官方事件流，慢订阅者只合并为一次补扫，不阻塞接收。
func (s *CD2Connections) Watch(ctx context.Context, consume func(CD2Event)) error {
	if consume == nil {
		return errors.New("缺少 CD2 事件处理函数")
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrCD2Unavailable
	}
	hub := s.eventHub
	if hub == nil {
		hubCtx, cancel := context.WithCancel(context.Background())
		hub = &cd2EventHub{ctx: hubCtx, cancel: cancel, done: make(chan struct{}), wake: make(chan struct{}, 1), subscribers: make(map[chan CD2Event]struct{})}
		s.eventHub = hub
		go s.runEventHub(hub)
	}
	updates := make(chan CD2Event, 1)
	hub.subscribers[updates] = struct{}{}
	if hub.latest.Mode != "" {
		updates <- hub.latest
	}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(hub.subscribers, updates)
		last := len(hub.subscribers) == 0
		if last {
			hub.cancel()
			if s.eventHub == hub {
				s.eventHub = nil
			}
		}
		s.mu.Unlock()
		if last {
			<-hub.done
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-hub.ctx.Done():
			return nil
		case event := <-updates:
			consume(event)
		}
	}
}

func (s *CD2Connections) publishCD2Event(hub *cd2EventHub, event CD2Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if event.Mode != "" {
		hub.latest = CD2Event{Mode: event.Mode, Message: event.Message, Revision: event.Revision}
	}
	for subscriber := range hub.subscribers {
		select {
		case subscriber <- event:
		default:
			// 队列合并不丢失补扫要求，配置变化与断流也会触发核对。
			var old CD2Event
			select {
			case old = <-subscriber:
			default:
			}
			merged := event
			merged.Rescan = true
			if old.Revision == event.Revision {
				merged.Completed = cd2MergeCompleted(old, event)
			}
			select {
			case subscriber <- merged:
			default:
			}
		}
	}
}

func cd2MergeCompleted(old, next CD2Event) []clouddrive2.Change {
	completed := slices.Clone(old.Completed)
	apply := func(change *clouddrive2.Change) {
		if change == nil {
			return
		}
		completed = slices.DeleteFunc(completed, func(previous clouddrive2.Change) bool {
			return previous.Path == change.Path || change.NewPath != "" && previous.Path == change.NewPath
		})
		if change.Ready {
			completed = append(completed, *change)
		}
	}
	apply(old.Change)
	for _, change := range next.Completed {
		apply(&change)
	}
	if next.Change != nil && !next.Change.Ready {
		apply(next.Change)
	}
	if len(completed) > 128 {
		completed = completed[len(completed)-128:]
	}
	return completed
}

func (s *CD2Connections) runEventHub(hub *cd2EventHub) {
	defer close(hub.done)
	for hub.ctx.Err() == nil {
		lease, err := s.Acquire(hub.ctx, "", 0)
		if err != nil {
			s.publishCD2Event(hub, CD2Event{Mode: "polling", Message: "CD2 事件连接暂不可用，使用定时补查", Rescan: true})
			if !cd2EventRetry(hub) {
				return
			}
			continue
		}
		source, ok := lease.Client.(interface {
			Events(context.Context, func(clouddrive2.Change) error) error
		})
		if !ok {
			_ = lease.Close()
			s.publishCD2Event(hub, CD2Event{Mode: "polling", Message: "当前 CD2 不提供事件订阅，使用定时补查", Rescan: true, Revision: lease.Revision})
			if !cd2EventRetry(hub) {
				return
			}
			continue
		}
		streamCtx, cancel := context.WithCancel(hub.ctx)
		ended := make(chan error, 1)
		var stream sync.WaitGroup
		stream.Add(1)
		go func() {
			defer stream.Done()
			consume := func(change clouddrive2.Change) error {
				s.publishCD2Event(hub, CD2Event{Change: &change, Mode: "realtime", Revision: lease.Revision})
				return streamCtx.Err()
			}
			if readySource, ok := lease.Client.(interface {
				EventsWithReady(context.Context, func(), func(clouddrive2.Change) error) error
			}); ok {
				ended <- readySource.EventsWithReady(streamCtx, func() {
					s.publishCD2Event(hub, CD2Event{Mode: "realtime", Message: "实时监听已启用，定时补查保底", Rescan: true, Revision: lease.Revision})
				}, consume)
			} else {
				ended <- source.Events(streamCtx, consume)
			}
		}()
		select {
		case <-hub.ctx.Done():
		case <-hub.wake:
		case <-ended:
		}
		cancel()
		stream.Wait()
		_ = lease.Close()
		if hub.ctx.Err() != nil {
			return
		}
		s.publishCD2Event(hub, CD2Event{Mode: "polling", Message: "实时监听正在重连，使用定时补查", Rescan: true, Revision: lease.Revision})
		if !cd2EventRetry(hub) {
			return
		}
	}
}

func cd2EventRetry(hub *cd2EventHub) bool {
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	select {
	case <-hub.ctx.Done():
		return false
	case <-hub.wake:
		return true
	case <-timer.C:
		return true
	}
}
