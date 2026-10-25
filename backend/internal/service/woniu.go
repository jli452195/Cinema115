package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"strm115/backend/internal/client/woniu"
)

type WoniuConfigInput struct {
	Username string `json:"username"`
	Password string `json:"password"`
}
type WoniuView struct {
	BaseURL       string   `json:"baseUrl"`
	Username      string   `json:"username"`
	Configured    bool     `json:"configured"`
	HasSession    bool     `json:"hasSession"`
	ResourceOrder []string `json:"resourceOrder"`
}
type WoniuSearchInput struct {
	CacheKey, Title, OriginalTitle, Method string
	Page                                   int
	Refresh                                bool
}
type WoniuSearchResult struct {
	woniu.SearchResponse
	Cached         bool       `json:"cached"`
	CacheExpiresAt *time.Time `json:"cacheExpiresAt,omitempty"`
}
type woniuDocument struct {
	Version  int                   `json:"version"`
	Username string                `json:"username"`
	Password string                `json:"password"`
	Cookies  []woniu.SessionCookie `json:"cookies"`
}
type woniuCacheEntry struct {
	result  woniu.SearchResponse
	expires time.Time
}
type WoniuService struct {
	Logs      *GenerationLogs
	mu        sync.RWMutex
	gate      chan struct{}
	store     *SessionStore
	doc       woniuDocument
	client    *woniu.Client
	newClient func() (*woniu.Client, error)
	cache     map[string]woniuCacheEntry // protected by gate, not mu
}

func NewWoniuService(store *SessionStore) (*WoniuService, error) {
	s := &WoniuService{store: store, gate: make(chan struct{}, 1), doc: woniuDocument{Version: 1}, cache: make(map[string]woniuCacheEntry, 128)}
	s.newClient = func() (*woniu.Client, error) { return woniu.New("", nil) }
	if store.Value() == "" {
		return s, nil
	}
	if err := json.Unmarshal([]byte(store.Value()), &s.doc); err != nil || s.doc.Version != 1 {
		return nil, errors.New("蜗牛配置无法解析")
	}
	if s.doc.Username == "" || s.doc.Password == "" {
		return s, nil
	}
	client, err := s.newClient()
	if err != nil {
		return nil, fmt.Errorf("创建蜗牛客户端: %w", err)
	}
	if err := client.RestoreSession(s.doc.Cookies); err != nil {
		return nil, fmt.Errorf("恢复蜗牛会话: %w", err)
	}
	s.client = client
	return s, nil
}

func (s *WoniuService) View() WoniuView {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return WoniuView{BaseURL: woniu.BaseURL, Username: s.doc.Username, Configured: s.client != nil && s.doc.Password != "", HasSession: len(s.doc.Cookies) > 0,
		ResourceOrder: []string{"115", "baidu", "quark", "aliyun", "uc", "xunlei", "123", "tianyi", "mobile", "guangya", "magnet", "other"}}
}

func (s *WoniuService) acquire(ctx context.Context) (func(), error) {
	select {
	case s.gate <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if ctx.Err() != nil {
		<-s.gate
		return nil, ctx.Err()
	}
	return func() { <-s.gate }, nil
}

func (s *WoniuService) save(doc woniuDocument, client *woniu.Client) error {
	raw, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("编码蜗牛配置: %w", err)
	}
	if err := s.store.Save(string(raw)); err != nil {
		return fmt.Errorf("保存蜗牛配置: %w", err)
	}
	s.mu.Lock()
	s.doc, s.client = doc, client
	s.mu.Unlock()
	return nil
}

func (s *WoniuService) Configure(ctx context.Context, input WoniuConfigInput) (view WoniuView, opErr error) {
	finish := s.operation("登录并保存蜗牛配置")
	defer func() { finish(opErr) }()
	release, err := s.acquire(ctx)
	if err != nil {
		return WoniuView{}, err
	}
	defer release()
	input.Username = strings.TrimSpace(input.Username)
	if input.Username == "" || len([]rune(input.Username)) > 100 || len(input.Password) > 512 || strings.ContainsAny(input.Username, "\r\n\x00") {
		return WoniuView{}, woniu.ErrInput
	}
	if input.Password == "" && input.Username == s.doc.Username {
		input.Password = s.doc.Password
	}
	if input.Password == "" {
		return WoniuView{}, woniu.ErrInput
	}
	client, err := s.newClient()
	if err != nil {
		return WoniuView{}, err
	}
	if err := client.Login(ctx, input.Username, input.Password); err != nil {
		return WoniuView{}, err
	}
	doc := woniuDocument{Version: 1, Username: input.Username, Password: input.Password, Cookies: client.SnapshotSession()}
	if err := s.save(doc, client); err != nil {
		return WoniuView{}, err
	}
	clear(s.cache)
	return s.View(), nil
}

func (s *WoniuService) Clear(ctx context.Context) error {
	release, err := s.acquire(ctx)
	if err != nil {
		return err
	}
	defer release()
	if err := s.store.Save(""); err != nil {
		return fmt.Errorf("清除蜗牛配置: %w", err)
	}
	s.mu.Lock()
	s.doc = woniuDocument{Version: 1}
	s.client = nil
	s.mu.Unlock()
	clear(s.cache)
	return nil
}

func cloneWoniu(result woniu.SearchResponse) woniu.SearchResponse {
	result.Items = append([]woniu.SearchItem{}, result.Items...)
	for i := range result.Items {
		result.Items[i].Links = append([]woniu.ResourceLink{}, result.Items[i].Links...)
	}
	result.Attempts = append([]woniu.SearchAttempt{}, result.Attempts...)
	return result
}

func (s *WoniuService) Search(ctx context.Context, input WoniuSearchInput) (result WoniuSearchResult, opErr error) {
	finish := s.operation("搜索影视资源")
	defer func() { finish(opErr) }()
	release, err := s.acquire(ctx)
	if err != nil {
		return result, err
	}
	defer release()
	if s.client == nil {
		return result, woniu.ErrNotConfigured
	}
	if input.Page == 0 {
		input.Page = 1
	}
	if input.Page < 1 || input.Page > woniu.MaxPages || input.Method != "" && input.Method != "title" && input.Method != "original" || input.Page > 1 && input.Method == "" {
		return result, woniu.ErrInput
	}
	query, method := strings.TrimSpace(input.Title), "title"
	if input.Method == "original" || query == "" {
		query, method = strings.TrimSpace(input.OriginalTitle), "original"
	}
	if query == "" {
		return result, woniu.ErrInput
	}
	prefix := input.CacheKey + "\x00"
	key := prefix + strconv.Itoa(input.Page) + "\x00" + method
	if !input.Refresh {
		if entry, ok := s.cache[key]; ok && time.Now().Before(entry.expires) {
			expires := entry.expires
			return WoniuSearchResult{SearchResponse: cloneWoniu(entry.result), Cached: true, CacheExpiresAt: &expires}, nil
		}
	}
	// The client and credential snapshot are fixed for the entire request.
	relogged := false
	search := func(query, method string) (woniu.SearchResponse, error) {
		response, err := s.client.Search(ctx, query, method, input.Page)
		if errors.Is(err, woniu.ErrSession) && !relogged {
			relogged = true
			// One explicit session failure permits one login; limits and challenge
			// pages do not trigger automatic password submissions.
			if loginErr := s.client.Login(ctx, s.doc.Username, s.doc.Password); loginErr != nil {
				return response, loginErr
			}
			return s.client.Search(ctx, query, method, input.Page)
		}
		return response, err
	}
	response, err := search(query, method)
	if err != nil {
		return result, err
	}
	original := strings.TrimSpace(input.OriginalTitle)
	if input.Page == 1 && method == "title" && len(response.Items) == 0 && original != "" && original != query {
		firstAttempt := response.Attempts[0]
		response, err = search(original, "original")
		if err != nil {
			return result, err
		}
		response.Attempts = append([]woniu.SearchAttempt{firstAttempt}, response.Attempts...)
	}
	doc := s.doc
	doc.Cookies = s.client.SnapshotSession()
	if err := s.save(doc, s.client); err != nil {
		return result, err
	}
	if input.Refresh && input.Page == 1 {
		for cacheKey := range s.cache {
			if strings.HasPrefix(cacheKey, prefix) {
				delete(s.cache, cacheKey)
			}
		}
	}
	result.SearchResponse = response
	if response.Partial {
		return result, nil
	}
	ttl := 10 * time.Minute
	usable := false
	for _, item := range response.Items {
		if len(item.Links) > 0 {
			usable = true
			break
		}
	}
	if !usable {
		ttl = 2 * time.Minute
	}
	expires := time.Now().Add(ttl)
	for cacheKey, entry := range s.cache {
		if time.Now().After(entry.expires) {
			delete(s.cache, cacheKey)
		}
	}
	if len(s.cache) >= 128 {
		for cacheKey := range s.cache {
			delete(s.cache, cacheKey)
			break
		}
	}
	s.cache[key] = woniuCacheEntry{result: cloneWoniu(response), expires: expires}
	result.CacheExpiresAt = &expires
	return result, nil
}

func (s *WoniuService) operation(action string) func(error) {
	if s.Logs == nil {
		return func(error) {}
	}
	id := s.Logs.NewTaskID("WONIU")
	start := time.Now()
	s.Logs.Append("搜索", id, "蜗牛", "INFO", "开始"+action)
	return func(err error) {
		level, message := "INFO", action+"完成"
		if err != nil {
			_, message = woniu.PublicError(err)
			level = "ERROR"
			if errors.Is(err, context.Canceled) {
				level = "WARNING"
			}
		}
		s.Logs.Append("搜索", id, "蜗牛", level, fmt.Sprintf("%s，耗时 %.2f 秒", message, time.Since(start).Seconds()))
	}
}
