package service

import (
	"context"
	"fmt"
	"sync"
	"time"

	"strm115/backend/internal/client/p115"
)

// Observe existing I/O only. Logs contain fixed stage names, local job IDs and
// counts/durations, never credentials, upstream response bodies or file URLs.
func (s *AnimeAdminService) observeAnimeStage(ctx context.Context, jobID, stage string) (context.Context, func()) {
	if s.Logs == nil {
		return ctx, func() {}
	}
	started := time.Now()
	var mu sync.Mutex
	var once sync.Once
	var total p115.RequestTiming
	s.Logs.Append("订阅", jobID, "动漫："+stage, "DEBUG", "阶段开始："+stage)
	ctx = p115.WithRequestTiming(ctx, func(t p115.RequestTiming) {
		mu.Lock()
		defer mu.Unlock()
		total.Requests += t.Requests
		total.Writes += t.Writes
		total.Retries += t.Retries
		total.Wait += t.Wait
		total.HTTP += t.HTTP
	})
	return ctx, func() {
		once.Do(func() {
			mu.Lock()
			defer mu.Unlock()
			s.Logs.Append("订阅", jobID, "动漫："+stage, "DEBUG", fmt.Sprintf("阶段结束：%s；耗时 %.3fs；115 请求 %d（读取 %d，写入 %d，重试 %d）；限速等待 %.3fs；HTTP %.3fs", stage, time.Since(started).Seconds(), total.Requests, total.Requests-total.Writes, total.Writes, total.Retries, total.Wait.Seconds(), total.HTTP.Seconds()))
		})
	}
}
