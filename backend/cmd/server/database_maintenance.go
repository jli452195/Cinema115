package main

import (
	"context"
	"time"

	"strm115/backend/internal/storage"
)

// 维护随服务退出；只记录等待计数和耗时，不记录 SQL 参数或业务文档。
func runDatabaseMaintenance(ctx context.Context, database *storage.Store, logf func(string, ...any)) {
	optimize := func() {
		maintenanceCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		started := time.Now()
		if err := database.Optimize(maintenanceCtx); err != nil {
			if ctx.Err() == nil {
				logf("SQLite 查询统计维护失败：%v", err)
			}
			return
		}
		logf("SQLite 查询统计维护完成，耗时 %s", time.Since(started).Round(time.Millisecond))
	}
	previous := database.Stats()
	optimize()
	statsTicker := time.NewTicker(5 * time.Minute)
	defer statsTicker.Stop()
	optimizeTicker := time.NewTicker(24 * time.Hour)
	defer optimizeTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-optimizeTicker.C:
			optimize()
		case <-statsTicker.C:
			current := database.Stats()
			if count := current.WaitCount - previous.WaitCount; count > 0 {
				logf("SQLite 连接池：过去 5 分钟等待 %d 次，累计等待 %s，当前占用 %d/%d", count, (current.WaitDuration - previous.WaitDuration).Round(time.Millisecond), current.InUse, current.MaxOpenConnections)
			}
			previous = current
		}
	}
}
