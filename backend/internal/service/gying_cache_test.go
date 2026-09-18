package service

import (
	"strconv"
	"testing"
	"time"

	"strm115/backend/internal/client/gying"
)

func TestGyingSearchCacheTTLRefreshAndIsolation(t *testing.T) {
	if gyingSearchCacheTTL != 10*time.Minute || gyingEmptySearchCacheTTL != 2*time.Minute {
		t.Fatalf("观影缓存时效不正确: success=%s empty=%s", gyingSearchCacheTTL, gyingEmptySearchCacheTTL)
	}
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	service := &GyingService{searchCache: make(map[string]gyingSearchCacheEntry, gyingSearchCacheLimit)}
	result := gying.SearchResponse{
		Query: "Original Name",
		Items: []gying.SearchItem{{
			ID:    "one",
			Title: "版本一",
			Links: []gying.ResourceLink{{Type: "quark", URL: "https://example.com/one"}},
		}},
	}
	fresh := service.cacheSearchLocked("tv:42", result, now)
	if fresh.Cached || fresh.CacheExpiresAt == nil || !fresh.CacheExpiresAt.Equal(now.Add(10*time.Minute)) {
		t.Fatalf("新搜索的缓存元数据不正确: %+v", fresh)
	}
	if fresh.Items == nil || fresh.Attempts == nil {
		t.Fatal("搜索响应数组不应编码为 null")
	}
	result.Items[0].Title = "污染"
	fresh.Items[0].Links[0].URL = "污染"
	cached, ok := service.cachedSearchLocked("tv:42", now.Add(time.Minute))
	if !ok || !cached.Cached || cached.Items[0].Title != "版本一" || cached.Items[0].Links[0].URL != "https://example.com/one" {
		t.Fatalf("缓存未命中或被调用方污染: %+v", cached)
	}
	cached.Items[0].Title = "再次污染"
	again, _ := service.cachedSearchLocked("tv:42", now.Add(2*time.Minute))
	if again.Items[0].Title != "版本一" {
		t.Fatal("缓存读取结果没有隔离副本")
	}
	if _, ok := service.cachedSearchLocked("tv:42", now.Add(10*time.Minute)); ok {
		t.Fatal("到期缓存仍被返回")
	}

	empty := service.cacheSearchLocked("movie:7", gying.SearchResponse{Items: []gying.SearchItem{}}, now)
	if empty.CacheExpiresAt == nil || !empty.CacheExpiresAt.Equal(now.Add(2*time.Minute)) {
		t.Fatalf("空结果缓存时效不正确: %+v", empty.CacheExpiresAt)
	}
	if empty.Items == nil {
		t.Fatal("空结果 items 不应编码为 null")
	}
}

func TestGyingSearchCacheLimitAndClear(t *testing.T) {
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	service := &GyingService{searchCache: make(map[string]gyingSearchCacheEntry, gyingSearchCacheLimit)}
	for index := 0; index <= gyingSearchCacheLimit; index++ {
		service.cacheSearchLocked(strconv.Itoa(index), gying.SearchResponse{
			Items: []gying.SearchItem{{ID: strconv.Itoa(index)}},
		}, now.Add(time.Duration(index)*time.Millisecond))
	}
	if len(service.searchCache) != gyingSearchCacheLimit {
		t.Fatalf("缓存数量=%d，期望=%d", len(service.searchCache), gyingSearchCacheLimit)
	}
	if _, ok := service.searchCache["0"]; ok {
		t.Fatal("达到容量限制后没有淘汰最早结果")
	}
	service.clearSearchCacheLocked()
	if len(service.searchCache) != 0 {
		t.Fatal("配置变更后缓存没有清空")
	}
}

func TestGyingPartialSearchIsNotCached(t *testing.T) {
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	service := &GyingService{searchCache: make(map[string]gyingSearchCacheEntry, gyingSearchCacheLimit)}
	result := service.cacheSearchLocked("tv:42", gying.SearchResponse{
		Items:         []gying.SearchItem{},
		Partial:       true,
		FailedDetails: 1,
		Attempts:      []gying.SearchAttempt{{Method: "title", Query: "测试", Total: 2, Failed: 1}},
	}, now)
	if result.CacheExpiresAt != nil || len(service.searchCache) != 0 || !result.Partial {
		t.Fatalf("不完整结果不应缓存: %+v cache=%d", result, len(service.searchCache))
	}
}

func TestGyingCacheIsolatesMagnetMetadata(t *testing.T) {
	seeds := 0
	result := gying.SearchResponse{Items: []gying.SearchItem{{ID: "movie", Links: []gying.ResourceLink{{Type: "magnet", SubType: "i4", SubTypeLabel: "原盘", Seeds: &seeds}}}}}
	now := time.Now()
	svc := &GyingService{searchCache: make(map[string]gyingSearchCacheEntry)}
	fresh := svc.cacheSearchLocked("movie:test", result, now)
	seeds = 10
	*fresh.Items[0].Links[0].Seeds = 20
	fresh.Items[0].Links[0].SubTypeLabel = "changed"
	cached, ok := svc.cachedSearchLocked("movie:test", now)
	if !ok || *cached.Items[0].Links[0].Seeds != 0 || cached.Items[0].Links[0].SubTypeLabel != "原盘" {
		t.Fatal("cached metadata was mutated by the caller")
	}
	*cached.Items[0].Links[0].Seeds = 30
	again, _ := svc.cachedSearchLocked("movie:test", now)
	if *again.Items[0].Links[0].Seeds != 0 {
		t.Fatal("cache reads share a seed pointer")
	}
}
