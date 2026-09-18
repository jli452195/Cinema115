package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	"strm115/backend/internal/client/gying"
)

const DefaultGyingBaseURL = "https://www.xn--wcv59z.com"

const (
	gyingSearchCacheTTL      = 10 * time.Minute
	gyingEmptySearchCacheTTL = 2 * time.Minute
	gyingSearchCacheLimit    = 128
)

type GyingConfigInput struct {
	BaseURL  string `json:"baseUrl"`
	Username string `json:"username"`
	Password string `json:"password"`
}

type GyingResourceType struct {
	Type  string `json:"type"`
	Label string `json:"label"`
}

type GyingView struct {
	BaseURL       string              `json:"baseUrl"`
	Username      string              `json:"username"`
	Configured    bool                `json:"configured"`
	HasSession    bool                `json:"hasSession"`
	ResourceOrder []string            `json:"resourceOrder"`
	ResourceTypes []GyingResourceType `json:"resourceTypes"`
}

var defaultGyingResourceTypes = []GyingResourceType{
	{Type: "magnet", Label: "磁力"},
	{Type: "aliyun", Label: "阿里云盘"},
	{Type: "quark", Label: "夸克"},
	{Type: "baidu", Label: "百度"},
	{Type: "xunlei", Label: "迅雷"},
	{Type: "mobile", Label: "移动云盘"},
	{Type: "115", Label: "115"},
	{Type: "123", Label: "123"},
	{Type: "guangya", Label: "光鸭网盘"},
	{Type: "uc", Label: "UC"},
	{Type: "tianyi", Label: "天翼"},
	{Type: "other", Label: "其他网盘"},
}

type GyingSearchInput struct {
	CacheKey      string
	OriginalTitle string
	IMDBID        string
	Refresh       bool
}

type GyingSearchResult struct {
	gying.SearchResponse
	Cached         bool       `json:"cached"`
	CacheExpiresAt *time.Time `json:"cacheExpiresAt,omitempty"`
}

type gyingSearchCacheEntry struct {
	result  gying.SearchResponse
	expires time.Time
}

type gyingDocument struct {
	Version       int                   `json:"version"`
	BaseURL       string                `json:"baseUrl"`
	Username      string                `json:"username"`
	Password      string                `json:"password"`
	Cookies       []gying.SessionCookie `json:"cookies"`
	ResourceOrder []string              `json:"resourceOrder,omitempty"`
}

type GyingService struct {
	mu          sync.RWMutex
	opMu        sync.Mutex
	store       *SessionStore
	doc         gyingDocument
	client      *gying.Client
	searchCache map[string]gyingSearchCacheEntry
}

func NewGyingService(store *SessionStore) (*GyingService, error) {
	s := &GyingService{
		store:       store,
		doc:         gyingDocument{Version: 1, Cookies: []gying.SessionCookie{}, ResourceOrder: defaultGyingResourceOrder()},
		searchCache: make(map[string]gyingSearchCacheEntry, gyingSearchCacheLimit),
	}
	if strings.TrimSpace(store.Value()) == "" {
		return s, nil
	}
	if err := json.Unmarshal([]byte(store.Value()), &s.doc); err != nil || s.doc.Version != 1 {
		return nil, fmt.Errorf("观影配置无法解析")
	}
	s.doc.ResourceOrder = normalizeGyingResourceOrder(s.doc.ResourceOrder)
	if s.doc.BaseURL == "" && s.doc.Username == "" && s.doc.Password == "" {
		return s, nil
	}
	client, err := gying.New(s.doc.BaseURL, nil)
	if err != nil {
		return nil, fmt.Errorf("恢复观影配置: %w", err)
	}
	if err := client.RestoreSession(s.doc.Cookies); err != nil {
		return nil, fmt.Errorf("恢复观影会话: %w", err)
	}
	s.client = client
	return s, nil
}

func (s *GyingService) View() GyingView {
	s.mu.RLock()
	defer s.mu.RUnlock()
	resourceTypes := make([]GyingResourceType, len(defaultGyingResourceTypes))
	copy(resourceTypes, defaultGyingResourceTypes)
	return GyingView{
		BaseURL: s.doc.BaseURL, Username: s.doc.Username,
		Configured: s.client != nil && s.doc.Password != "", HasSession: len(s.doc.Cookies) > 0,
		ResourceOrder: append([]string(nil), s.doc.ResourceOrder...), ResourceTypes: resourceTypes,
	}
}

func defaultGyingResourceOrder() []string {
	result := make([]string, len(defaultGyingResourceTypes))
	for index, resourceType := range defaultGyingResourceTypes {
		result[index] = resourceType.Type
	}
	return result
}

func normalizeGyingResourceOrder(input []string) []string {
	known := make(map[string]struct{}, len(defaultGyingResourceTypes))
	for _, resourceType := range defaultGyingResourceTypes {
		known[resourceType.Type] = struct{}{}
	}
	seen := make(map[string]struct{}, len(defaultGyingResourceTypes))
	result := make([]string, 0, len(defaultGyingResourceTypes))
	for _, resourceType := range input {
		if _, ok := known[resourceType]; !ok {
			continue
		}
		if _, ok := seen[resourceType]; ok {
			continue
		}
		seen[resourceType] = struct{}{}
		result = append(result, resourceType)
	}
	for _, resourceType := range defaultGyingResourceTypes {
		if _, ok := seen[resourceType.Type]; !ok {
			result = append(result, resourceType.Type)
		}
	}
	return result
}

func validateGyingResourceOrder(input []string) ([]string, error) {
	known := make(map[string]struct{}, len(defaultGyingResourceTypes))
	for _, resourceType := range defaultGyingResourceTypes {
		known[resourceType.Type] = struct{}{}
	}
	seen := make(map[string]struct{}, len(input))
	for _, resourceType := range input {
		if _, ok := known[resourceType]; !ok {
			return nil, fmt.Errorf("%w: 未知的资源类型 %q", gying.ErrInput, resourceType)
		}
		if _, ok := seen[resourceType]; ok {
			return nil, fmt.Errorf("%w: 资源类型 %q 重复", gying.ErrInput, resourceType)
		}
		seen[resourceType] = struct{}{}
	}
	return normalizeGyingResourceOrder(input), nil
}

func validateGyingConfig(input GyingConfigInput) (GyingConfigInput, error) {
	input.BaseURL = strings.TrimRight(strings.TrimSpace(input.BaseURL), "/")
	input.Username = strings.TrimSpace(input.Username)
	if input.BaseURL == "" {
		input.BaseURL = DefaultGyingBaseURL
	}
	if input.Username == "" || len([]rune(input.Username)) > 100 || len(input.Password) > 512 || strings.ContainsAny(input.Username, "\r\n\x00") {
		return GyingConfigInput{}, gying.ErrInput
	}
	u, err := url.Parse(input.BaseURL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Port() != "" || u.Path != "" && u.Path != "/" || u.RawQuery != "" || u.Fragment != "" {
		return GyingConfigInput{}, fmt.Errorf("%w: 站点地址必须是无路径和端口的 HTTPS 域名", gying.ErrInput)
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if host == "localhost" || strings.HasSuffix(host, ".local") || net.ParseIP(host) != nil {
		return GyingConfigInput{}, fmt.Errorf("%w: 站点地址不能使用本机、内网名称或 IP", gying.ErrInput)
	}
	return input, nil
}

// Configure validates credentials against the remote site before replacing
// the encrypted configuration. A blank password retains the saved password.
func (s *GyingService) Configure(ctx context.Context, input GyingConfigInput) (GyingView, error) {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	s.mu.RLock()
	current := s.doc
	s.mu.RUnlock()
	if input.Password == "" && input.BaseURL == current.BaseURL && strings.TrimSpace(input.Username) == current.Username {
		input.Password = current.Password
	}
	input, err := validateGyingConfig(input)
	if err != nil {
		return GyingView{}, err
	}
	if input.Password == "" {
		return GyingView{}, fmt.Errorf("%w: 请输入密码", gying.ErrInput)
	}
	client, err := gying.New(input.BaseURL, nil)
	if err != nil {
		return GyingView{}, err
	}
	if err := client.Login(ctx, input.Username, input.Password); err != nil {
		return GyingView{}, err
	}
	doc := gyingDocument{
		Version: 1, BaseURL: input.BaseURL, Username: input.Username, Password: input.Password,
		Cookies: client.SnapshotSession(), ResourceOrder: normalizeGyingResourceOrder(current.ResourceOrder),
	}
	if err := s.save(doc); err != nil {
		return GyingView{}, err
	}
	s.mu.Lock()
	s.doc, s.client = doc, client
	s.mu.Unlock()
	s.clearSearchCacheLocked()
	return s.View(), nil
}

func (s *GyingService) save(doc gyingDocument) error {
	raw, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("编码观影配置: %w", err)
	}
	if err := s.store.Save(string(raw)); err != nil {
		return fmt.Errorf("保存观影配置: %w", err)
	}
	return nil
}

func (s *GyingService) Clear() error {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	s.mu.RLock()
	resourceOrder := append([]string(nil), s.doc.ResourceOrder...)
	s.mu.RUnlock()
	doc := gyingDocument{Version: 1, Cookies: []gying.SessionCookie{}, ResourceOrder: normalizeGyingResourceOrder(resourceOrder)}
	if err := s.save(doc); err != nil {
		return err
	}
	s.mu.Lock()
	s.doc = doc
	s.client = nil
	s.mu.Unlock()
	s.clearSearchCacheLocked()
	return nil
}

func (s *GyingService) UpdateResourceOrder(input []string) (GyingView, error) {
	resourceOrder, err := validateGyingResourceOrder(input)
	if err != nil {
		return GyingView{}, err
	}
	s.opMu.Lock()
	defer s.opMu.Unlock()
	s.mu.RLock()
	doc := s.doc
	s.mu.RUnlock()
	doc.ResourceOrder = resourceOrder
	if err := s.save(doc); err != nil {
		return GyingView{}, err
	}
	s.mu.Lock()
	s.doc = doc
	s.mu.Unlock()
	return s.View(), nil
}

func (s *GyingService) Search(ctx context.Context, input GyingSearchInput) (GyingSearchResult, error) {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	s.mu.RLock()
	client, doc := s.client, s.doc
	s.mu.RUnlock()
	if client == nil || doc.Password == "" {
		return GyingSearchResult{}, gying.ErrNotConfigured
	}
	now := time.Now()
	if !input.Refresh {
		if result, ok := s.cachedSearchLocked(input.CacheKey, now); ok {
			return result, nil
		}
	}
	result, err := client.SearchByTitle(ctx, input.OriginalTitle, input.IMDBID)
	if errors.Is(err, gying.ErrForbidden) {
		if loginErr := client.Login(ctx, doc.Username, doc.Password); loginErr != nil {
			return GyingSearchResult{}, fmt.Errorf("观影会话失效且重新登录失败: %w", loginErr)
		}
		result, err = client.SearchByTitle(ctx, input.OriginalTitle, input.IMDBID)
	}
	if err != nil {
		return GyingSearchResult{}, err
	}
	doc.Cookies = client.SnapshotSession()
	if err := s.save(doc); err != nil {
		return GyingSearchResult{}, err
	}
	s.mu.Lock()
	s.doc = doc
	s.mu.Unlock()
	return s.cacheSearchLocked(input.CacheKey, result, time.Now()), nil
}

func cloneGyingSearchResponse(result gying.SearchResponse) gying.SearchResponse {
	items := make([]gying.SearchItem, len(result.Items))
	copy(items, result.Items)
	result.Items = items
	attempts := make([]gying.SearchAttempt, len(result.Attempts))
	copy(attempts, result.Attempts)
	result.Attempts = attempts
	for index := range result.Items {
		links := make([]gying.ResourceLink, len(result.Items[index].Links))
		copy(links, result.Items[index].Links)
		result.Items[index].Links = links
	}
	return result
}

// 调用方持有 opMu；配置、搜索和清理共用该锁，缓存不会跨账号配置复用。
func (s *GyingService) cachedSearchLocked(key string, now time.Time) (GyingSearchResult, bool) {
	entry, ok := s.searchCache[key]
	if !ok {
		return GyingSearchResult{}, false
	}
	if !now.Before(entry.expires) {
		delete(s.searchCache, key)
		return GyingSearchResult{}, false
	}
	expires := entry.expires
	return GyingSearchResult{
		SearchResponse: cloneGyingSearchResponse(entry.result),
		Cached:         true,
		CacheExpiresAt: &expires,
	}, true
}

// 调用方持有 opMu。空结果使用更短时效，降低上游临时异常带来的影响。
func (s *GyingService) cacheSearchLocked(key string, result gying.SearchResponse, now time.Time) GyingSearchResult {
	if result.Partial {
		return GyingSearchResult{SearchResponse: cloneGyingSearchResponse(result)}
	}
	ttl := gyingSearchCacheTTL
	if len(result.Items) == 0 {
		ttl = gyingEmptySearchCacheTTL
	}
	expires := now.Add(ttl)
	if s.searchCache == nil {
		s.searchCache = make(map[string]gyingSearchCacheEntry, gyingSearchCacheLimit)
	}
	for cachedKey, entry := range s.searchCache {
		if !now.Before(entry.expires) {
			delete(s.searchCache, cachedKey)
		}
	}
	if _, exists := s.searchCache[key]; !exists && len(s.searchCache) >= gyingSearchCacheLimit {
		var oldestKey string
		var earliestExpiry time.Time
		for cachedKey, entry := range s.searchCache {
			if earliestExpiry.IsZero() || entry.expires.Before(earliestExpiry) {
				oldestKey, earliestExpiry = cachedKey, entry.expires
			}
		}
		delete(s.searchCache, oldestKey)
	}
	cloned := cloneGyingSearchResponse(result)
	s.searchCache[key] = gyingSearchCacheEntry{result: cloned, expires: expires}
	return GyingSearchResult{
		SearchResponse: cloneGyingSearchResponse(cloned),
		CacheExpiresAt: &expires,
	}
}

// 调用方持有 opMu。
func (s *GyingService) clearSearchCacheLocked() {
	s.searchCache = make(map[string]gyingSearchCacheEntry, gyingSearchCacheLimit)
}
