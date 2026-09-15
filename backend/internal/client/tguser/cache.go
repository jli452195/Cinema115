package tguser

import "time"

const searchCacheTTL = 24 * time.Hour
const searchCacheLimit = 64

type cachedSearch struct {
	result  SearchResult
	expires time.Time
}

// 调用方持有 mu；登录身份变化时同时作废缓存与资源操作标识。
func (c *Client) clearSearchLocked() {
	c.searchCache = make(map[string]cachedSearch)
	c.results = make(map[string]storedAction)
}

// 缓存独立副本，避免调用方修改链接或配额指针后污染下次响应。
func cloneResult(result SearchResult) SearchResult {
	result.Items = append([]Action{}, result.Items...)
	result.Links = append([]string{}, result.Links...)
	if result.Remaining != nil {
		value := *result.Remaining
		result.Remaining = &value
	}
	if result.Page != nil {
		value := *result.Page
		result.Page = &value
	}
	if result.ExpiresAt != nil {
		value := *result.ExpiresAt
		result.ExpiresAt = &value
	}
	return result
}

func (c *Client) cachedResult(key string, now time.Time) (SearchResult, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.searchCache[key]
	if !ok {
		return SearchResult{}, false
	}
	valid := now.Before(entry.expires)
	// 操作标识可能因容量限制提前淘汰，不能返回已无法点选的资源列表。
	for _, item := range entry.result.Items {
		action, exists := c.results[item.ID]
		valid = valid && exists && now.Before(action.expires)
	}
	if !valid {
		delete(c.searchCache, key)
		return SearchResult{}, false
	}
	result := cloneResult(entry.result)
	result.Cached = true
	return result, true
}

func (c *Client) cacheResult(key string, result SearchResult, now time.Time) SearchResult {
	c.mu.Lock()
	defer c.mu.Unlock()
	// 权限、配额和其他提示不作为资源结果缓存；明确的“未找到”也缓存。
	if !result.Empty && len(result.Items) == 0 && len(result.Links) == 0 {
		delete(c.searchCache, key)
		return result
	}
	expires := now.Add(searchCacheTTL)
	for _, item := range result.Items {
		action, ok := c.results[item.ID]
		if !ok || !now.Before(action.expires) {
			return result
		}
		if action.expires.Before(expires) {
			expires = action.expires
		}
	}
	if c.searchCache == nil {
		c.searchCache = make(map[string]cachedSearch, searchCacheLimit)
	}
	for id, entry := range c.searchCache {
		if !now.Before(entry.expires) {
			delete(c.searchCache, id)
		}
	}
	if _, exists := c.searchCache[key]; !exists && len(c.searchCache) >= searchCacheLimit {
		var oldest string
		var earliest time.Time
		for id, entry := range c.searchCache {
			if earliest.IsZero() || entry.expires.Before(earliest) {
				oldest, earliest = id, entry.expires
			}
		}
		delete(c.searchCache, oldest)
	}
	result.Cached = false
	result.ExpiresAt = &expires
	c.searchCache[key] = cachedSearch{result: cloneResult(result), expires: expires}
	return result
}
