package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"strm115/backend/internal/client/mikan"
)

type mikanReaderFunc func(context.Context, string, string) (mikan.Result, error)

func (f mikanReaderFunc) Search(ctx context.Context, q, g string) (mikan.Result, error) {
	return f(ctx, q, g)
}

func TestMikanFallbackCacheRefreshAndGroups(t *testing.T) {
	var mu sync.Mutex
	calls := make([]string, 0, 8)
	reader := mikanReaderFunc(func(ctx context.Context, q, g string) (mikan.Result, error) {
		mu.Lock()
		calls = append(calls, q+":"+g)
		mu.Unlock()
		result := mikan.Result{Items: []mikan.Item{}, Groups: []mikan.Group{{ID: "615", Name: "字幕组"}}}
		if q == "原名" {
			result.Items = append(result.Items, mikan.Item{ID: "hash", Title: "资源"})
		}
		return result, nil
	})
	logs := &GenerationLogs{}
	s := NewMikanService(reader, logs)
	input := MikanInput{Query: "中文标题", Fallback: "原名"}
	first, err := s.Search(context.Background(), input)
	if err != nil || len(first.Items) != 1 || len(first.Attempts) != 2 || first.Query != "原名" || first.Cached {
		t.Fatal(first, err)
	}
	if first.ExpiresAt.Sub(first.FetchedAt) != 10*time.Minute {
		t.Fatal("非空缓存时间错误")
	}
	first.Items[0].Title = "用户修改"
	second, err := s.Search(context.Background(), input)
	if err != nil || !second.Cached || second.Items[0].Title != "资源" || len(calls) != 2 {
		t.Fatal(second, err, calls)
	}
	input.Refresh = true
	if _, err = s.Search(context.Background(), input); err != nil || len(calls) != 4 {
		t.Fatal(err, calls)
	}
	input.Refresh = false
	input.Group = "615"
	filtered, err := s.Search(context.Background(), input)
	if err != nil || len(filtered.Attempts) != 1 || len(filtered.Items) != 0 || len(calls) != 5 {
		t.Fatal(filtered, err, calls)
	}
	if filtered.ExpiresAt.Sub(filtered.FetchedAt) != time.Minute {
		t.Fatal("空缓存时间错误")
	}
	if len(logs.Snapshot("ERROR", "蜜柑")) != 0 {
		t.Fatal("正常空结果被记成错误")
	}
	found := false
	for _, e := range logs.Snapshot("DEBUG", "蜜柑") {
		found = found || strings.Contains(e.Message, "缓存或同一")
	}
	if !found {
		t.Fatal("缺少缓存日志")
	}
}

func TestMikanErrorsNotCachedAndExpired(t *testing.T) {
	calls := 0
	s := NewMikanService(mikanReaderFunc(func(context.Context, string, string) (mikan.Result, error) {
		calls++
		return mikan.Result{}, mikan.ErrParse
	}), &GenerationLogs{})
	for i := 0; i < 2; i++ {
		if _, err := s.Search(context.Background(), MikanInput{Query: "测试", Fallback: "原名"}); !errors.Is(err, mikan.ErrParse) {
			t.Fatal(err)
		}
	}
	if calls != 2 || len(s.cache) != 0 {
		t.Fatal("异常缓存或错误触发兜底")
	}
	s.Client = mikanReaderFunc(func(context.Context, string, string) (mikan.Result, error) {
		calls++
		return mikan.Result{Items: []mikan.Item{}, Groups: []mikan.Group{}}, nil
	})
	s.cache["测试\x00"] = mikanCache{expires: time.Now().Add(-time.Second)}
	result, err := s.Search(context.Background(), MikanInput{Query: "测试"})
	if err != nil || result.Cached || calls != 3 {
		t.Fatal(result, err, calls)
	}
}

func TestMikanConcurrentMergeAndWaiterCancellation(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	s := NewMikanService(mikanReaderFunc(func(ctx context.Context, q, g string) (mikan.Result, error) {
		close(started)
		select {
		case <-release:
			return mikan.Result{Items: []mikan.Item{}, Groups: []mikan.Group{}}, nil
		case <-ctx.Done():
			return mikan.Result{}, ctx.Err()
		}
	}), nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if _, err := s.Search(ctx, MikanInput{Query: "测试"}); err != nil {
			t.Error(err)
		}
	}()
	<-started
	waitCtx, stop := context.WithCancel(ctx)
	stop()
	if _, err := s.Search(waitCtx, MikanInput{Query: "测试"}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.Search(ctx, MikanInput{Query: "测试"}); err != nil {
				t.Error(err)
			}
		}()
	}
	close(release)
	wg.Wait()
}
