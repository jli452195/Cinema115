package service

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestGyingResourceOrderValidationAndForwardCompatibility(t *testing.T) {
	order, err := validateGyingResourceOrder([]string{"115", "magnet"})
	if err != nil {
		t.Fatal(err)
	}
	if len(order) != len(defaultGyingResourceTypes) || order[0] != "115" || order[1] != "magnet" {
		t.Fatalf("缺少的资源类型没有按默认顺序补齐: %v", order)
	}
	if _, err := validateGyingResourceOrder([]string{"115", "115"}); err == nil {
		t.Fatal("重复资源类型没有被拒绝")
	}
	if _, err := validateGyingResourceOrder([]string{"unknown"}); err == nil {
		t.Fatal("未知资源类型没有被拒绝")
	}

	normalized := normalizeGyingResourceOrder([]string{"removed", "123", "123", "115"})
	if normalized[0] != "123" || normalized[1] != "115" || len(normalized) != len(defaultGyingResourceTypes) {
		t.Fatalf("旧配置没有被安全归一化: %v", normalized)
	}
}

func TestGyingResourceOrderPersistsWithoutClearingSearchCache(t *testing.T) {
	store, err := NewSessionStore(filepath.Join(t.TempDir(), "gying-config.enc"), "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	searcher, err := NewGyingService(store)
	if err != nil {
		t.Fatal(err)
	}
	searcher.searchCache["tv:1"] = gyingSearchCacheEntry{}
	view, err := searcher.UpdateResourceOrder([]string{"115", "guangya", "magnet"})
	if err != nil {
		t.Fatal(err)
	}
	if len(searcher.searchCache) != 1 {
		t.Fatal("展示排序变化不应清除搜索缓存")
	}
	if view.ResourceOrder[0] != "115" || view.ResourceOrder[1] != "guangya" {
		t.Fatalf("资源排序响应错误: %v", view.ResourceOrder)
	}
	if err := searcher.Clear(); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewGyingService(store)
	if err != nil {
		t.Fatal(err)
	}
	if restarted.View().Configured || !reflect.DeepEqual(restarted.View().ResourceOrder, view.ResourceOrder) {
		t.Fatalf("清除登录后资源排序没有保留: before=%v after=%v", view.ResourceOrder, restarted.View().ResourceOrder)
	}
}
