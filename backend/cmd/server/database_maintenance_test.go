package main

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"strm115/backend/internal/storage"
)

func TestDatabaseMaintenanceStopsOnCancellation(t *testing.T) {
	database, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "maintenance.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	reported := false
	runDatabaseMaintenance(ctx, database, func(string, ...any) {
		reported = true
		cancel()
	})
	if !reported || !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatal("维护任务未在取消后及时退出", reported, ctx.Err())
	}
	if stats := database.Stats(); stats.InUse != 0 {
		t.Fatal("维护退出后连接仍被占用", stats)
	}
}
