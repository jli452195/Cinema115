package service

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"strm115/backend/internal/client/assrt"
)

type AssrtView struct {
	Configured bool `json:"configured"`
}
type AssrtSearch struct {
	Configured bool         `json:"configured"`
	Items      []assrt.Item `json:"items"`
	HasMore    bool         `json:"hasMore"`
}
type assrtCache struct {
	result  AssrtSearch
	expires time.Time
}

// Only provider requests are serialized; configuration reads and the other
// subtitle library remain independent. No polling or background workers.
type AssrtService struct {
	mu        sync.RWMutex
	gate      chan struct{}
	store     *SessionStore
	client    *assrt.Client
	newClient func(string) *assrt.Client
	cache     map[string]assrtCache // protected by gate
	requests  []time.Time           // rolling, shared across token changes (same NAS IP)
}

func NewAssrtService(store *SessionStore) (*AssrtService, error) {
	s := &AssrtService{store: store, gate: make(chan struct{}, 1), newClient: assrt.New, cache: make(map[string]assrtCache)}
	if token := store.Value(); token != "" {
		if !assrt.ValidToken(token) {
			return nil, fmt.Errorf("字幕配置中的射手网 Token 格式无效")
		}
		s.client = s.newClient(token)
	}
	return s, nil
}

func (s *AssrtService) View() AssrtView {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return AssrtView{Configured: s.client != nil}
}
func (s *AssrtService) acquire(ctx context.Context) (func(), error) {
	select {
	case s.gate <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		<-s.gate
		return nil, err
	}
	return func() { <-s.gate }, nil
}
func (s *AssrtService) allow() error {
	now := time.Now()
	first := 0
	for first < len(s.requests) && now.Sub(s.requests[first]) >= time.Minute {
		first++
	}
	s.requests = s.requests[first:]
	if len(s.requests) >= 20 {
		return assrt.ErrRateLimited
	}
	s.requests = append(s.requests, now)
	return nil
}
func (s *AssrtService) Configure(ctx context.Context, token string) error {
	token = strings.TrimSpace(token)
	if !assrt.ValidToken(token) {
		return assrt.ErrToken
	}
	release, err := s.acquire(ctx)
	if err != nil {
		return err
	}
	defer release()
	if err := s.allow(); err != nil {
		return err
	}
	client := s.newClient(token)
	if err := client.Quota(ctx); err != nil {
		return err
	}
	if err := s.store.Save(token); err != nil {
		return fmt.Errorf("保存射手网配置: %w", err)
	}
	s.mu.Lock()
	s.client = client
	s.mu.Unlock()
	clear(s.cache)
	return nil
}
func (s *AssrtService) Clear(ctx context.Context) error {
	release, err := s.acquire(ctx)
	if err != nil {
		return err
	}
	defer release()
	if err := s.store.Save(""); err != nil {
		return fmt.Errorf("清除射手网配置: %w", err)
	}
	s.mu.Lock()
	s.client = nil
	s.mu.Unlock()
	clear(s.cache)
	return nil
}
func (s *AssrtService) Search(ctx context.Context, query string, position int) (AssrtSearch, error) {
	release, err := s.acquire(ctx)
	if err != nil {
		return AssrtSearch{}, err
	}
	defer release()
	if s.client == nil {
		return AssrtSearch{Items: []assrt.Item{}}, nil
	}
	query = strings.TrimSpace(query)
	key := query + "\x00" + strconv.Itoa(position)
	if entry, ok := s.cache[key]; ok && time.Now().Before(entry.expires) {
		return entry.result, nil
	}
	if err := s.allow(); err != nil {
		return AssrtSearch{}, err
	}
	items, err := s.client.Search(ctx, query, position)
	if err != nil {
		return AssrtSearch{}, err
	}
	result := AssrtSearch{Configured: true, Items: items, HasMore: len(items) == assrt.PageSize && position+len(items) <= 1500}
	for key, entry := range s.cache {
		if time.Now().After(entry.expires) {
			delete(s.cache, key)
		}
	}
	if len(s.cache) >= 128 {
		clear(s.cache)
	}
	s.cache[key] = assrtCache{result: result, expires: time.Now().Add(15 * time.Minute)}
	return result, nil
}
func (s *AssrtService) Download(ctx context.Context, id int64) (assrt.Download, error) {
	release, err := s.acquire(ctx)
	if err != nil {
		return assrt.Download{}, err
	}
	defer release()
	if s.client == nil {
		return assrt.Download{}, assrt.ErrNotConfigured
	}
	if err := s.allow(); err != nil {
		return assrt.Download{}, err
	}
	return s.client.Download(ctx, id)
}
