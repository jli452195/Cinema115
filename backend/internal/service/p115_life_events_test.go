package service

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
)

type lifeCursorFake struct {
	mu     sync.Mutex
	values map[string]string
	fail   bool
}

func (f *lifeCursorFake) LoadDriveSTRMCursor(_ context.Context, id string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.values[id], nil
}
func (f *lifeCursorFake) SaveDriveSTRMCursor(_ context.Context, id, cursor string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return errors.New("模拟磁盘失败")
	}
	f.values[id] = cursor
	return nil
}

func lifeHubFixture(t *testing.T) (*P115LifeEvents, *P115LifeSubscription, *P115LifeSubscription, *lifeCursorFake) {
	t.Helper()
	store := &lifeCursorFake{values: map[string]string{"main": "100"}}
	h := NewP115LifeEvents(store)
	a, err := h.Subscribe("fast")
	if err != nil {
		t.Fatal(err)
	}
	b, err := h.Subscribe("slow")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.account(context.Background(), "main", "42"); err != nil {
		t.Fatal(err)
	}
	return h, a, b, store
}

func publishLifeEvent(h *P115LifeEvents, id int) {
	h.publish(P115LifeEventBatch{AccountID: "main", UID: "42", Events: []p115.LifeEvent{{ID: strconv.Itoa(id), Type: 2, ParentID: "20"}}}, strconv.Itoa(id))
}

func TestLifeEventHubIndependentReadersAndSafeCheckpoint(t *testing.T) {
	h, a, b, store := lifeHubFixture(t)
	ctx := context.Background()
	publishLifeEvent(h, 101)
	first, _ := a.Read()
	first.Events[0].ParentID = "forged"
	first, _ = a.Read()
	if first.Events[0].ParentID != "20" {
		t.Fatal("消费者修改了共享事件")
	}
	if err := a.Acknowledge(ctx, first); err != nil {
		t.Fatal(err)
	}
	if cursor, _ := store.LoadDriveSTRMCursor(ctx, "main"); cursor != "100" {
		t.Fatal("慢消费者未交接就推进了恢复进度")
	}
	if head, err := h.account(ctx, "main", "42"); err != nil || head != "101" {
		t.Fatal("采集进度受慢消费者阻塞", head, err)
	}
	publishLifeEvent(h, 102)
	next, _ := a.Read()
	if next.Events[0].ID != "102" {
		t.Fatal("快消费者重读旧事件")
	}
	if err := a.Acknowledge(ctx, next); err != nil {
		t.Fatal(err)
	}
	old, _ := b.Read()
	if old.Events[0].ID != "101" {
		t.Fatal("慢消费者丢事件")
	}
	if err := b.Acknowledge(ctx, old); err != nil {
		t.Fatal(err)
	}
	if cursor, _ := store.LoadDriveSTRMCursor(ctx, "main"); cursor != "101" {
		t.Fatal("共同进度错误", cursor)
	}
	last, _ := b.Read()
	if err := b.Acknowledge(ctx, last); err != nil {
		t.Fatal(err)
	}
	if cursor, _ := store.LoadDriveSTRMCursor(ctx, "main"); cursor != "102" || len(h.batches) != 0 || h.events != 0 {
		t.Fatal("已完成缓存未清理", cursor)
	}
}

func TestLifeEventHubRetriesCheckpointWithoutAnotherEvent(t *testing.T) {
	h, a, b, store := lifeHubFixture(t)
	ctx := context.Background()
	publishLifeEvent(h, 101)
	batch, _ := a.Read()
	if err := a.Acknowledge(ctx, batch); err != nil {
		t.Fatal(err)
	}
	store.fail = true
	batch, _ = b.Read()
	if err := b.Acknowledge(ctx, batch); err == nil {
		t.Fatal("提交失败被隐藏")
	}
	store.fail = false
	if err := h.checkpoint(ctx); err != nil {
		t.Fatal(err)
	}
	if cursor, _ := store.LoadDriveSTRMCursor(ctx, "main"); cursor != "101" {
		t.Fatal("没有新事件时未重试保存")
	}
}

func TestLifeEventHubOverflowRepairsOnlyLaggingConsumer(t *testing.T) {
	h, a, b, store := lifeHubFixture(t)
	h.maxBatch = 1
	ctx := context.Background()
	publishLifeEvent(h, 101)
	stale, _ := b.Read()
	first, _ := a.Read()
	if err := a.Acknowledge(ctx, first); err != nil {
		t.Fatal(err)
	}
	publishLifeEvent(h, 102)
	fast, _ := a.Read()
	slow, _ := b.Read()
	if fast.Repair || !slow.Repair {
		t.Fatal("溢出不应让已追上的消费者补扫", fast.Repair, slow.Repair)
	}
	if err := b.Acknowledge(ctx, stale); !errors.Is(err, ErrLifeEventExpired) {
		t.Fatal("旧确认清除了补扫", err)
	}
	if err := a.Acknowledge(ctx, fast); err != nil {
		t.Fatal(err)
	}
	if cursor, _ := store.LoadDriveSTRMCursor(ctx, "main"); cursor != "100" {
		t.Fatal("补扫交接前推进了进度")
	}
	if err := b.Acknowledge(ctx, slow); err != nil {
		t.Fatal(err)
	}
	if cursor, _ := store.LoadDriveSTRMCursor(ctx, "main"); cursor != "102" {
		t.Fatal("补扫交接后未推进")
	}
}

func TestLifeEventHubAccountChangeRejectsOldBatch(t *testing.T) {
	h, a, _, store := lifeHubFixture(t)
	ctx := context.Background()
	publishLifeEvent(h, 101)
	old, _ := a.Read()
	if _, err := h.account(ctx, "replacement", "43"); err != nil {
		t.Fatal(err)
	}
	if err := a.Acknowledge(ctx, old); !errors.Is(err, ErrLifeEventExpired) {
		t.Fatal("换号后接受了旧事件", err)
	}
	if _, ok := a.Read(); ok {
		t.Fatal("旧事件转移到新账号")
	}
	if cursor, _ := store.LoadDriveSTRMCursor(ctx, "replacement"); cursor != "" {
		t.Fatal("旧进度转移到新账号")
	}
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := a.Wait(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal("订阅等待忽略取消", err)
	}
}

func waitLifeCondition(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for !condition() {
		select {
		case <-deadline.C:
			t.Fatal("等待生活事件状态超时")
		case <-tick.C:
		}
	}
}

func TestDriveLifeRefreshCoalescesAndCallerCancellationKeepsSharedRead(t *testing.T) {
	s, _ := driveSTRMEventFixture(t)
	settings, _ := s.Settings(context.Background())
	settings.EventIntervalSeconds = 0
	if _, err := s.SaveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	r := NewDriveSTRMEvents(s, driveSTRMLifeFake(func(ctx context.Context, _, _ int, _ string) (p115.LifeEventPage, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		select {
		case <-ctx.Done():
			return p115.LifeEventPage{}, ctx.Err()
		case <-release:
		}
		return p115.LifeEventPage{Count: 1, Events: []p115.LifeEvent{{ID: "101", Type: 2, ParentID: "20"}}}, nil
	}))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); r.run(ctx, time.Hour) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("生活事件协程未退出")
		}
	}()
	waitLifeCondition(t, func() bool { r.mu.RLock(); defer r.mu.RUnlock(); return r.running != nil })
	caller, cancelCaller := context.WithCancel(ctx)
	first := make(chan error, 1)
	go func() { first <- r.Check(caller) }()
	<-started
	if err := r.RequestRefresh(ctx); err != nil {
		t.Fatal(err)
	}
	cancelCaller()
	if err := <-first; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	close(release)
	waitLifeCondition(t, func() bool { return !r.State().LastCheckedAt.IsZero() })
	if err := consumeCachedLifeEvents(ctx, r); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("并发刷新重复请求了115", calls.Load())
	}
}

func TestDriveLifeSlowConsumerDoesNotBlockCollectionOrSTRM(t *testing.T) {
	s, db := driveSTRMEventFixture(t)
	settings, _ := s.Settings(context.Background())
	settings.EventIntervalSeconds = 0
	if _, err := s.SaveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	r := NewDriveSTRMEvents(s, driveSTRMLifeFake(func(context.Context, int, int, string) (p115.LifeEventPage, error) {
		id := 100 + int(calls.Add(1))
		return p115.LifeEventPage{Count: 1, Events: []p115.LifeEvent{{ID: strconv.Itoa(id), Type: 2, ParentID: "20"}}}, nil
	}))
	r.organize.consumeMu.Lock()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); r.run(ctx, time.Hour) }()
	defer func() {
		r.organize.consumeMu.Unlock()
		if err := consumeCachedLifeEvents(ctx, r); err != nil {
			t.Error(err)
		}
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("慢消费者退出失败")
		}
	}()
	waitLifeCondition(t, func() bool { r.mu.RLock(); defer r.mu.RUnlock(); return r.running != nil })
	if err := r.Check(ctx); err != nil {
		t.Fatal(err)
	}
	waitLifeCondition(t, func() bool { value, err := db.GetDriveSTRM(ctx, "mapping"); return err == nil && value.Queued })
	if err := r.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatal("慢消费者阻塞了采集", calls.Load())
	}
	if cursor, _ := db.LoadDriveSTRMCursor(ctx, "main"); cursor != "" {
		t.Fatal("慢消费者未交接却推进了安全游标")
	}
}

func TestDriveLifeAdditionalConsumerNeedsNoSTRMSettingsOrMapping(t *testing.T) {
	s, db := driveSTRMEventFixture(t)
	ctx := context.Background()
	if err := db.DeleteDriveSTRM(ctx, "mapping"); err != nil {
		t.Fatal(err)
	}
	s.Store = driveSTRMUnsavedSettings{s.Store}
	var requests int
	r := NewDriveSTRMEvents(s, driveSTRMLifeFake(func(context.Context, int, int, string) (p115.LifeEventPage, error) {
		requests++
		return p115.LifeEventPage{Count: 1, Events: []p115.LifeEvent{{ID: "101", Type: 8}}}, nil
	}))
	sub, err := r.Hub.Subscribe("other-feature")
	if err != nil {
		t.Fatal(err)
	}
	if err := checkAndConsumeLifeEvents(ctx, r); err != nil {
		t.Fatal(err)
	}
	batch, ok := sub.Read()
	if !ok || !batch.Repair || requests != 1 {
		t.Fatal("独立订阅受STRM配置限制", ok, requests)
	}
	if cursor, _ := db.LoadDriveSTRMCursor(ctx, "main"); cursor != "" {
		t.Fatal("外部消费者未交接就保存进度")
	}
	if err := sub.Acknowledge(ctx, batch); err != nil {
		t.Fatal(err)
	}
}

type lifeParentFake struct {
	calls   atomic.Int32
	started chan struct{}
	release chan struct{}
}

func (f *lifeParentFake) LifeEventParent(ctx context.Context, _ string, _ string) (string, error) {
	if f.calls.Add(1) == 1 {
		close(f.started)
	}
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-f.release:
		return "20", nil
	}
}

func TestLifeEventParentLookupSharedAndInvalidated(t *testing.T) {
	h, a, _, _ := lifeHubFixture(t)
	publishLifeEvent(h, 101)
	batch, _ := a.Read()
	reader := &lifeParentFake{started: make(chan struct{}), release: make(chan struct{})}
	account := P115Account{ID: "main", Cookie: "UID=42_A"}
	ctx := context.Background()
	first := make(chan error, 1)
	go func() { _, err := h.resolveParent(ctx, batch, "501", account, reader); first <- err }()
	<-reader.started
	second := make(chan error, 1)
	go func() { _, err := h.resolveParent(ctx, batch, "501", account, reader); second <- err }()
	close(reader.release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if err := <-second; err != nil {
		t.Fatal(err)
	}
	if reader.calls.Load() != 1 {
		t.Fatal("两个消费者重复核验同一文件", reader.calls.Load())
	}
	h.publish(P115LifeEventBatch{AccountID: "main", UID: "42", Events: []p115.LifeEvent{{ID: "102", Type: 6, FileID: "501", ParentID: "30"}}}, "102")
	if _, err := h.resolveParent(ctx, batch, "501", account, reader); err != nil {
		t.Fatal(err)
	}
	if reader.calls.Load() != 2 {
		t.Fatal("移动事件未失效旧父目录")
	}
}

func TestLifeEventParentLookupInvalidationWhileInFlight(t *testing.T) {
	h, a, _, _ := lifeHubFixture(t)
	publishLifeEvent(h, 101)
	batch, _ := a.Read()
	reader := &lifeParentFake{started: make(chan struct{}), release: make(chan struct{})}
	account := P115Account{ID: "main", Cookie: "UID=42_A"}
	finished := make(chan error, 1)
	go func() {
		_, err := h.resolveParent(context.Background(), batch, "501", account, reader)
		finished <- err
	}()
	<-reader.started
	h.publish(P115LifeEventBatch{AccountID: "main", UID: "42", Events: []p115.LifeEvent{{ID: "102", Type: 6, FileID: "501"}}}, "102")
	close(reader.release)
	if err := <-finished; !errors.Is(err, ErrLifeEventExpired) {
		t.Fatal("在途旧查询没有失效", err)
	}
	if _, err := h.resolveParent(context.Background(), batch, "501", account, reader); err != nil {
		t.Fatal(err)
	}
	if reader.calls.Load() != 2 {
		t.Fatal("旧查询污染了新缓存", reader.calls.Load())
	}
}

func TestLifeEventParentLookupCanceledOwnerDoesNotCancelWaiter(t *testing.T) {
	h, a, _, _ := lifeHubFixture(t)
	publishLifeEvent(h, 101)
	batch, _ := a.Read()
	reader := &lifeParentFake{started: make(chan struct{}), release: make(chan struct{})}
	account := P115Account{ID: "main", Cookie: "UID=42_A"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	owner := make(chan error, 1)
	go func() { _, err := h.resolveParent(ctx, batch, "501", account, reader); owner <- err }()
	<-reader.started
	waiter := make(chan error, 1)
	go func() {
		_, err := h.resolveParent(context.Background(), batch, "501", account, reader)
		waiter <- err
	}()
	cancel()
	if err := <-owner; !errors.Is(err, context.Canceled) {
		t.Fatal("首个查询没有响应取消", err)
	}
	close(reader.release)
	if err := <-waiter; err != nil {
		t.Fatal("首个查询取消影响其他消费者", err)
	}
	if reader.calls.Load() != 2 {
		t.Fatal("查询失败没有重新核验", reader.calls.Load())
	}
}

type driveLifeParentClient struct {
	driveSTRMLifeFake
	calls atomic.Int32
}

func (f *driveLifeParentClient) LifeEventParent(context.Context, string, string) (string, error) {
	f.calls.Add(1)
	return "20", nil
}

func TestDriveLifeMissingParentUsesExactLookupInsteadOfRootRepair(t *testing.T) {
	s, db := driveSTRMEventFixture(t)
	ctx := context.Background()
	if err := db.SaveDriveSTRMCursor(ctx, "main", "100"); err != nil {
		t.Fatal(err)
	}
	client := &driveLifeParentClient{driveSTRMLifeFake: func(context.Context, int, int, string) (p115.LifeEventPage, error) {
		return p115.LifeEventPage{Count: 2, Events: []p115.LifeEvent{{ID: "101", Type: 2, FileID: "501"}, {ID: "100", Type: 8}}}, nil
	}}
	r := NewDriveSTRMEvents(s, client)
	if err := checkAndConsumeLifeEvents(ctx, r); err != nil {
		t.Fatal(err)
	}
	value, err := db.GetDriveSTRM(ctx, "mapping")
	if err != nil {
		t.Fatal(err)
	}
	if len(value.PendingDirectories) != 1 || value.PendingDirectories[0] != "20" || client.calls.Load() != 1 {
		t.Fatal("缺父目录导致了全来源补扫", value.PendingDirectories, client.calls.Load())
	}
}

type driveLifeParentChainClient struct {
	driveSTRMLifeFake
	parents map[string]string
	calls   []string
}

func (f *driveLifeParentChainClient) LifeEventParent(_ context.Context, id, _ string) (string, error) {
	f.calls = append(f.calls, id)
	parent, ok := f.parents[id]
	if !ok {
		return "", errors.New("没有配置精确身份")
	}
	return parent, nil
}

func TestDriveLifeMissingParentResolvesMovedAndUnindexedScopes(t *testing.T) {
	for _, tc := range []struct {
		name    string
		event   p115.LifeEvent
		indexed bool
		parents map[string]string
		want    []string
		calls   []string
	}{
		{name: "移动保留新旧父目录", event: p115.LifeEvent{Type: 6, FileID: "501"}, indexed: true, parents: map[string]string{"501": "30"}, want: []string{"20", "30"}, calls: []string{"501"}},
		{name: "带父目录的移动保留旧目录", event: p115.LifeEvent{Type: 6, FileID: "501", ParentID: "30"}, indexed: true, want: []string{"20", "30"}},
		{name: "新建多层目录定位已知祖先", event: p115.LifeEvent{Type: 2, FileID: "501"}, parents: map[string]string{"501": "701", "701": "702", "702": "20"}, want: []string{"20"}, calls: []string{"501", "701", "702"}},
		{name: "范围外目录到根停止", event: p115.LifeEvent{Type: 2, FileID: "501"}, parents: map[string]string{"501": "701", "701": "0"}, calls: []string{"501", "701"}},
		{name: "循环身份触发补扫", event: p115.LifeEvent{Type: 2, FileID: "501"}, parents: map[string]string{"501": "701", "701": "501"}, want: []string{"10"}, calls: []string{"501", "701"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, db := driveSTRMEventFixture(t)
			ctx := context.Background()
			if err := db.SaveDriveSTRMCursor(ctx, "main", "100"); err != nil {
				t.Fatal(err)
			}
			if err := db.SaveDriveSTRMFile(ctx, model.DriveSTRMFile{MappingID: "mapping", FileID: "30", ParentCID: "10", Path: "Season 02", Directory: true}); err != nil {
				t.Fatal(err)
			}
			if tc.indexed {
				if err := db.SaveDriveSTRMFile(ctx, model.DriveSTRMFile{MappingID: "mapping", FileID: "501", ParentCID: "20", Path: "Season 01/Movie.mkv"}); err != nil {
					t.Fatal(err)
				}
			}
			event := tc.event
			event.ID = "101"
			client := &driveLifeParentChainClient{parents: tc.parents, driveSTRMLifeFake: func(context.Context, int, int, string) (p115.LifeEventPage, error) {
				return p115.LifeEventPage{Count: 2, Events: []p115.LifeEvent{event, {ID: "100", Type: 8}}}, nil
			}}
			r := NewDriveSTRMEvents(s, client)
			if err := checkAndConsumeLifeEvents(ctx, r); err != nil {
				t.Fatal(err)
			}
			value, err := db.GetDriveSTRM(ctx, "mapping")
			if err != nil {
				t.Fatal(err)
			}
			sort.Strings(value.PendingDirectories)
			if !reflect.DeepEqual(value.PendingDirectories, tc.want) || !reflect.DeepEqual(client.calls, tc.calls) {
				t.Fatal("扫描范围或查询次数错误", value.PendingDirectories, client.calls)
			}
		})
	}
}

func TestDriveLifeMissingParentResolvesEachNestedMapping(t *testing.T) {
	s, db := driveSTRMEventFixture(t)
	ctx := context.Background()
	if err := db.SaveDriveSTRMCursor(ctx, "main", "100"); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveDriveSTRM(ctx, model.DriveSTRM{ID: "nested", AccountID: "main", SourceCID: "20", Enabled: true, UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveDriveSTRMFile(ctx, model.DriveSTRMFile{MappingID: "mapping", FileID: "30", ParentCID: "20", Path: "Season 01/Part", Directory: true}); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveDriveSTRMFile(ctx, model.DriveSTRMFile{MappingID: "mapping", FileID: "501", ParentCID: "30", Path: "Season 01/Part/Movie.mkv"}); err != nil {
		t.Fatal(err)
	}
	client := &driveLifeParentChainClient{parents: map[string]string{"501": "30", "30": "20"}, driveSTRMLifeFake: func(context.Context, int, int, string) (p115.LifeEventPage, error) {
		return p115.LifeEventPage{Count: 2, Events: []p115.LifeEvent{{ID: "101", Type: 2, FileID: "501"}, {ID: "100", Type: 8}}}, nil
	}}
	r := NewDriveSTRMEvents(s, client)
	if err := checkAndConsumeLifeEvents(ctx, r); err != nil {
		t.Fatal(err)
	}
	nested, err := db.GetDriveSTRM(ctx, "nested")
	if err != nil {
		t.Fatal(err)
	}
	if !nested.Queued || !reflect.DeepEqual(nested.PendingDirectories, []string{"20"}) || !reflect.DeepEqual(client.calls, []string{"501", "30"}) {
		t.Fatal("嵌套映射借用了其他映射的索引进度", nested, client.calls)
	}
}
