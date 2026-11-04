package service

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"strm115/backend/internal/client/p115"
)

var ErrLifeEventExpired = errors.New("生活事件批次已合并，请重新读取")

type lifeEventCursorStore interface {
	LoadDriveSTRMCursor(context.Context, string) (string, error)
	SaveDriveSTRMCursor(context.Context, string, string) error
}

// P115LifeEventBatch 只在内存共享，不包含凭据；Repair 要求消费者核对自身监控来源。
type P115LifeEventBatch struct {
	AccountID        string
	UID              string
	Events           []p115.LifeEvent
	KnownDirectories []string
	Repair           bool
	sequence         uint64
	epoch            uint64
	cursor           string
}

type LifeEventConsumerState struct {
	PendingBatches int       `json:"pendingBatches"`
	LastError      string    `json:"lastError,omitempty"`
	LastConsumedAt time.Time `json:"lastConsumedAt,omitempty"`
}

// P115LifeEvents 使用单份有界内存缓存和各订阅者独立进度，复用已有共同安全游标。
// 订阅者必须先安全交接到现有任务队列，再确认批次；慢消费者不阻塞采集。
type P115LifeEvents struct {
	mu        sync.Mutex
	commitMu  sync.Mutex
	store     lifeEventCursorStore
	accountID string
	uid       string
	head      string
	committed string
	epoch     uint64
	sequence  uint64
	batches   []P115LifeEventBatch
	consumers map[string]*P115LifeSubscription
	events    int
	maxEvents int
	maxBatch  int
	parents   map[string]*lifeEventParentResult
}

type P115LifeSubscription struct {
	hub            *P115LifeEvents
	name           string
	wake           chan struct{}
	ack            uint64
	state          LifeEventConsumerState
	consumeMu      sync.Mutex
	work           *lifeEventWork
	repairSequence uint64
}

func NewP115LifeEvents(store lifeEventCursorStore) *P115LifeEvents {
	return &P115LifeEvents{store: store, consumers: make(map[string]*P115LifeSubscription), maxEvents: 20000, maxBatch: 256}
}

// Subscribe 在采集启动前注册；同步 Read 和异步 Wait 共用同一订阅进度，不额外请求网盘。
func (h *P115LifeEvents) Subscribe(name string) (*P115LifeSubscription, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if name == "" || h.accountID != "" {
		return nil, fmt.Errorf("生活事件订阅必须在采集启动前注册")
	}
	if _, exists := h.consumers[name]; exists {
		return nil, fmt.Errorf("生活事件订阅名称已存在")
	}
	return h.addSubscription(name), nil
}

func (h *P115LifeEvents) addSubscription(name string) *P115LifeSubscription {
	s := &P115LifeSubscription{hub: h, name: name, wake: make(chan struct{}, 1), ack: h.sequence}
	h.consumers[name] = s
	return s
}

func (h *P115LifeEvents) account(ctx context.Context, id, uid string) (string, error) {
	h.commitMu.Lock()
	defer h.commitMu.Unlock()
	h.mu.Lock()
	if h.accountID == id && h.uid == uid {
		head := h.head
		h.mu.Unlock()
		return head, nil
	}
	h.mu.Unlock()
	cursor, err := h.store.LoadDriveSTRMCursor(ctx, id)
	if err != nil {
		return "", err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.accountID, h.uid, h.head, h.committed = id, uid, cursor, cursor
	h.epoch++
	h.batches, h.events = nil, 0
	h.parents = nil
	for _, s := range h.consumers {
		s.ack, s.state, s.repairSequence = h.sequence, LifeEventConsumerState{}, 0
	}
	return cursor, nil
}

func (h *P115LifeEvents) publish(batch P115LifeEventBatch, cursor string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if batch.AccountID != h.accountID || batch.UID != h.uid {
		return
	}
	if cursor == h.head && !batch.Repair && len(batch.Events) == 0 {
		return
	}
	for _, event := range batch.Events {
		if event.ChangesDirectory() {
			delete(h.parents, event.FileID)
		}
	}
	previous := h.sequence
	h.sequence++
	batch.sequence, batch.epoch, batch.cursor = h.sequence, h.epoch, cursor
	if len(h.batches) >= h.maxBatch || h.events+len(batch.Events) > h.maxEvents {
		// 缓存溢出用一次来源补扫覆盖缺口；旧批次的确认不能删除新的补扫任务。
		h.epoch++
		batch.epoch = h.epoch
		if len(batch.Events) > h.maxEvents {
			batch.Events = nil
			batch.Repair = true
		}
		h.batches, h.events = nil, 0
		for _, s := range h.consumers {
			if s.ack < previous {
				s.repairSequence = h.sequence
			}
			s.ack = h.sequence - 1
		}
	}
	h.head = cursor
	h.batches = append(h.batches, batch)
	h.events += len(batch.Events)
	for _, s := range h.consumers {
		select {
		case s.wake <- struct{}{}:
		default:
		}
	}
}

func (s *P115LifeSubscription) Read() (P115LifeEventBatch, bool) {
	return s.read(true)
}

func (s *P115LifeSubscription) read(copyEvents bool) (P115LifeEventBatch, bool) {
	s.hub.mu.Lock()
	defer s.hub.mu.Unlock()
	for _, batch := range s.hub.batches {
		if batch.sequence > s.ack {
			batch.Repair = batch.Repair || s.repairSequence == batch.sequence
			if copyEvents {
				batch.Events = slices.Clone(batch.Events)
				batch.KnownDirectories = slices.Clone(batch.KnownDirectories)
			}
			return batch, true
		}
	}
	return P115LifeEventBatch{}, false
}

func (s *P115LifeSubscription) Wait(ctx context.Context) (P115LifeEventBatch, error) {
	for {
		if err := ctx.Err(); err != nil {
			return P115LifeEventBatch{}, err
		}
		if batch, ok := s.Read(); ok {
			return batch, nil
		}
		select {
		case <-ctx.Done():
			return P115LifeEventBatch{}, ctx.Err()
		case <-s.wake:
		}
	}
}

func (s *P115LifeSubscription) Acknowledge(ctx context.Context, batch P115LifeEventBatch) error {
	h := s.hub
	h.mu.Lock()
	valid := batch.epoch == h.epoch && batch.AccountID == h.accountID && batch.UID == h.uid
	if valid {
		found := false
		for _, pending := range h.batches {
			if pending.sequence > s.ack {
				found = pending.sequence == batch.sequence
				break
			}
		}
		valid = found
	}
	if !valid {
		h.mu.Unlock()
		return ErrLifeEventExpired
	}
	s.ack = batch.sequence
	s.state.LastError, s.state.LastConsumedAt = "", time.Now().UTC()
	h.mu.Unlock()
	return h.checkpoint(ctx)
}

// checkpoint 只推进各消费者已确认的共同边界，失败保留内存批次供后续重试提交。
func (h *P115LifeEvents) checkpoint(ctx context.Context) error {
	h.commitMu.Lock()
	defer h.commitMu.Unlock()
	h.mu.Lock()
	minimum := h.sequence
	for _, s := range h.consumers {
		minimum = min(minimum, s.ack)
	}
	var ready *P115LifeEventBatch
	for i := range h.batches {
		if h.batches[i].sequence <= minimum {
			copy := h.batches[i]
			ready = &copy
		}
	}
	if ready == nil {
		h.clearFinishedErrorsLocked()
		h.mu.Unlock()
		return nil
	}
	batch := *ready
	changed := batch.cursor != h.committed
	h.mu.Unlock()
	if changed {
		if err := h.store.SaveDriveSTRMCursor(ctx, batch.AccountID, batch.cursor); err != nil {
			return fmt.Errorf("保存生活事件共同进度: %w", err)
		}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if batch.epoch != h.epoch {
		return nil
	}
	h.committed = batch.cursor
	cut := 0
	for cut < len(h.batches) && h.batches[cut].sequence <= batch.sequence {
		h.events -= len(h.batches[cut].Events)
		h.batches[cut].Events = nil
		cut++
	}
	h.batches = slices.Delete(h.batches, 0, cut)
	h.clearFinishedErrorsLocked()
	return nil
}

func (h *P115LifeEvents) clearFinishedErrorsLocked() {
	for _, s := range h.consumers {
		pending := false
		for _, batch := range h.batches {
			if batch.sequence > s.ack {
				pending = true
				break
			}
		}
		if !pending {
			s.state.LastError = ""
		}
	}
}

func (s *P115LifeSubscription) failed(err error) {
	s.hub.mu.Lock()
	defer s.hub.mu.Unlock()
	s.state.LastError = err.Error()
}

func (h *P115LifeEvents) states() map[string]LifeEventConsumerState {
	h.mu.Lock()
	defer h.mu.Unlock()
	states := make(map[string]LifeEventConsumerState, len(h.consumers))
	for name, s := range h.consumers {
		value := s.state
		for _, batch := range h.batches {
			if batch.sequence > s.ack {
				value.PendingBatches++
			}
		}
		states[name] = value
	}
	return states
}
