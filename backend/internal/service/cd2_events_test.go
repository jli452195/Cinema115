package service

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"strm115/backend/internal/client/clouddrive2"
)

type cd2EventTestClient struct {
	cd2TestClient
	streams atomic.Int32
	active  atomic.Int32
	changes chan clouddrive2.Change
}

func (c *cd2EventTestClient) Events(ctx context.Context, consume func(clouddrive2.Change) error) error {
	c.streams.Add(1)
	c.active.Add(1)
	defer c.active.Add(-1)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case change := <-c.changes:
			if err := consume(change); err != nil {
				return err
			}
		}
	}
}

func cd2Eventually(t *testing.T, predicate func() bool) {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for !predicate() {
		select {
		case <-timer.C:
			t.Fatal("等待 CD2 事件状态超时")
		case <-ticker.C:
		}
	}
}

func TestCD2WatchSharesStreamAndCancelsLastSubscriber(t *testing.T) {
	s, err := NewCD2Connections(&failingAccountStore{})
	if err != nil {
		t.Fatal(err)
	}
	client := &cd2EventTestClient{changes: make(chan clouddrive2.Change, 1)}
	s.newClient = func(clouddrive2.Config) (cd2Client, error) { return client, nil }
	cd2Save(t, s, "test-token")
	defer s.Close()
	firstCtx, firstCancel := context.WithCancel(context.Background())
	secondCtx, secondCancel := context.WithCancel(context.Background())
	defer firstCancel()
	defer secondCancel()
	var workers sync.WaitGroup
	firstDone, secondDone := make(chan struct{}), make(chan struct{})
	firstReceived, secondReceived := make(chan struct{}, 1), make(chan struct{}, 1)
	workers.Add(2)
	go func() {
		defer workers.Done()
		defer close(firstDone)
		_ = s.Watch(firstCtx, func(event CD2Event) {
			if event.Change != nil {
				firstReceived <- struct{}{}
			}
		})
	}()
	go func() {
		defer workers.Done()
		defer close(secondDone)
		_ = s.Watch(secondCtx, func(event CD2Event) {
			if event.Change != nil {
				secondReceived <- struct{}{}
			}
		})
	}()
	defer func() { firstCancel(); secondCancel(); workers.Wait() }()
	cd2Eventually(t, func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.eventHub != nil && len(s.eventHub.subscribers) == 2 && client.active.Load() == 1
	})
	client.changes <- clouddrive2.Change{Type: "create", Path: "/cloud/new.mkv"}
	for _, received := range []chan struct{}{firstReceived, secondReceived} {
		select {
		case <-received:
		case <-time.After(time.Second):
			t.Fatal("共享事件没有分发给所有订阅者")
		}
	}
	firstCancel()
	select {
	case <-firstDone:
	case <-time.After(time.Second):
		t.Fatal("首个订阅取消没有及时退出")
	}
	if client.active.Load() != 1 || client.streams.Load() != 1 {
		t.Fatal("仍有订阅时不应关闭或重复创建共享事件流")
	}
	secondCancel()
	select {
	case <-secondDone:
	case <-time.After(time.Second):
		t.Fatal("最后一个订阅取消后没有回收事件流")
	}
	if client.active.Load() != 0 {
		t.Fatal("事件流协程未回收")
	}
	s.mu.Lock()
	hub := s.eventHub
	s.mu.Unlock()
	if hub != nil {
		t.Fatal("没有订阅者时应释放共享 Hub")
	}
}

func TestCD2CloseStopsSharedEventStream(t *testing.T) {
	s, err := NewCD2Connections(&failingAccountStore{})
	if err != nil {
		t.Fatal(err)
	}
	client := &cd2EventTestClient{changes: make(chan clouddrive2.Change, 1)}
	s.newClient = func(clouddrive2.Config) (cd2Client, error) { return client, nil }
	cd2Save(t, s, "test-token")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); _ = s.Watch(ctx, func(CD2Event) {}) }()
	cd2Eventually(t, func() bool { return client.active.Load() == 1 })
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("连接服务关闭后订阅没有退出")
	}
	if client.active.Load() != 0 || client.closed != 1 {
		t.Fatal("关闭应等待事件流回收并释放原生连接", client.active.Load(), client.closed)
	}
}

func TestCD2SlowSubscriberRetainsCompletionAndHonorsLaterModification(t *testing.T) {
	s := &CD2Connections{}
	updates := make(chan CD2Event, 1)
	hub := &cd2EventHub{subscribers: map[chan CD2Event]struct{}{updates: {}}}
	s.publishCD2Event(hub, CD2Event{Revision: 1, Change: &clouddrive2.Change{Type: "upload_finished", Path: "/cloud/ready.mkv", Ready: true}})
	s.publishCD2Event(hub, CD2Event{Revision: 1, Change: &clouddrive2.Change{Type: "create", Path: "/cloud/other.mkv"}})
	merged := <-updates
	if !merged.Rescan || len(merged.Completed) != 1 || merged.Completed[0].Path != "/cloud/ready.mkv" {
		t.Fatal("慢订阅者合并事件不能丢失上传完成证据", merged)
	}
	updates <- merged
	s.publishCD2Event(hub, CD2Event{Revision: 1, Change: &clouddrive2.Change{Type: "modify", Path: "/cloud/ready.mkv"}})
	s.publishCD2Event(hub, CD2Event{Revision: 1, Change: &clouddrive2.Change{Type: "create", Path: "/cloud/final.mkv"}})
	if merged = <-updates; len(merged.Completed) != 0 {
		t.Fatal("被修改的路径不能再次复用已合并的旧完成证据", merged)
	}
}
