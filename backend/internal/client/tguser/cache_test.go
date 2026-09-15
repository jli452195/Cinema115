package tguser

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"
)

func TestSearchCacheReuseAndRefresh(t *testing.T) {
	if searchCacheTTL != 24*time.Hour {
		t.Fatalf("搜索缓存时长 = %s，期望 24h", searchCacheTTL)
	}
	c := testClient(t)
	now := time.Now()
	c.cacheResult("search:女生万岁", SearchResult{Text: "没有找到资源", Empty: true}, now)
	got, err := c.Search(context.Background(), " 女生万岁 ", false)
	if err != nil || !got.Cached || !got.Empty {
		t.Fatalf("无结果缓存没有复用：%+v %v", got, err)
	}
	// 未配置网络凭据，强制刷新必须走真实查询路径而非返回缓存。
	if _, err := c.Search(context.Background(), "女生万岁", true); !errors.Is(err, ErrConfig) {
		t.Fatalf("刷新没有绕过缓存：%v", err)
	}
	if _, ok := c.cachedResult("search:女生万岁", now.Add(searchCacheTTL)); ok {
		t.Fatal("过期缓存仍被返回")
	}
	c.cacheResult("search:权限", SearchResult{Text: "请先完成认证"}, now)
	if _, ok := c.cachedResult("search:权限", now); ok {
		t.Fatal("权限提示不应缓存")
	}
}

func TestCacheActionsAndIsolation(t *testing.T) {
	c := testClient(t)
	now := time.Now()
	action, err := c.addAction(storedAction{direct: "https://115.com/s/one?password=abcd"}, "版本一", "resource", "1", "1G")
	if err != nil {
		t.Fatal(err)
	}
	if remaining := time.Until(c.results[action.ID].expires); remaining < searchCacheTTL-time.Second || remaining > searchCacheTTL {
		t.Fatalf("资源操作标识有效期 = %s，期望约 24h", remaining)
	}
	quota := 10
	result := SearchResult{Items: []Action{action}, Links: []string{"https://115.com/s/one?password=abcd"}, Remaining: &quota}
	c.cacheResult("search:电影", result, now)
	result.Items[0].Title = "污染"
	*result.Remaining = 0
	got, ok := c.cachedResult("search:电影", now)
	if !ok || got.Items[0].Title != "版本一" || *got.Remaining != 10 {
		t.Fatal("缓存被调用方修改")
	}
	got.Links[0] = "污染"
	if again, _ := c.cachedResult("search:电影", now); again.Links[0] == "污染" {
		t.Fatal("读取缓存没有隔离副本")
	}
	delete(c.results, action.ID)
	if _, ok := c.cachedResult("search:电影", now); ok {
		t.Fatal("返回了无法点选的操作标识")
	}
	for i := 0; i <= searchCacheLimit; i++ {
		c.cacheResult(strconv.Itoa(i), SearchResult{Empty: true}, now.Add(time.Duration(i)*time.Millisecond))
	}
	if len(c.searchCache) != searchCacheLimit {
		t.Fatal("缓存容量没有上限")
	}
	if _, ok := c.searchCache["0"]; ok {
		t.Fatal("没有淘汰最旧结果")
	}
	if err := c.Disconnect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(c.searchCache) != 0 || len(c.results) != 0 {
		t.Fatal("退出后仍保留账号缓存")
	}
}
