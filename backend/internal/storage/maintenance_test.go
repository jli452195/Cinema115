package storage

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"strm115/backend/internal/model"
)

func TestSQLiteOptimizePreservesDataAndCancellation(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	items := make([]model.EmbyItem, 200)
	for i := range items {
		items[i] = model.EmbyItem{ID: fmt.Sprint(i), Type: "Episode", SeriesID: fmt.Sprint(i / 20), ParentID: fmt.Sprint(i / 10), LibraryID: "library"}
	}
	if err := s.EmbyStage(ctx, "scope", "gen", items); err != nil {
		t.Fatal(err)
	}
	if err := s.Optimize(ctx); err != nil {
		t.Fatal(err)
	}
	var stat string
	if err := s.db.QueryRowContext(ctx, "SELECT stat FROM sqlite_stat1 WHERE idx='emby_parent'").Scan(&stat); err != nil || stat == "" {
		t.Fatal("维护未生成索引统计", stat, err)
	}
	var count, foreignKeys int
	if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM emby_items").Scan(&count); err != nil || count != len(items) {
		t.Fatal("统计维护改变了业务数据", count, err)
	}
	if err := s.db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil || foreignKeys != 1 {
		t.Fatal("统计维护改变了外键约束", foreignKeys, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := s.Optimize(canceled); !errors.Is(err, context.Canceled) {
		t.Fatal("统计维护未响应取消", err)
	}
	if stats := s.Stats(); stats.MaxOpenConnections != 1 || stats.InUse != 0 {
		t.Fatal("维护泄漏连接或改变了连接池", stats)
	}
}
