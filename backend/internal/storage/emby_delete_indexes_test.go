package storage

import (
	"context"
	"strings"
	"testing"

	"strm115/backend/internal/model"
)

func TestEmbyDeletionIndexesMigrationAndPlans(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	// 模拟现有版本升级，确认补齐索引不会影响已有范围和快照。
	if _, err := s.db.ExecContext(ctx, `DROP INDEX emby_parent; DROP INDEX emby_library; INSERT INTO emby_snapshots(scope,generation) VALUES('scope','gen'); PRAGMA user_version=69;`); err != nil {
		t.Fatal(err)
	}
	if err := s.initialize(ctx); err != nil {
		t.Fatal(err)
	}
	var version, snapshots int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil || version != schemaVersion {
		t.Fatal("迁移版本错误", version, err)
	}
	if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM emby_snapshots WHERE scope='scope' AND generation='gen'").Scan(&snapshots); err != nil || snapshots != 1 {
		t.Fatal("迁移改变了已有快照", snapshots, err)
	}
	rows, err := s.db.QueryContext(ctx, "EXPLAIN QUERY PLAN "+embyDeleteItemsSQL, "scope", "gen", "item")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan strings.Builder
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan.WriteString(detail + "\n")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for _, index := range []string{"emby_series", "emby_parent", "emby_library"} {
		if !strings.Contains(plan.String(), index) {
			t.Errorf("删除查询未使用 %s：\n%s", index, plan.String())
		}
	}
	if strings.Contains(plan.String(), "SCAN emby_items") || strings.Contains(plan.String(), "USING PRIMARY KEY (snapshot_id=?)") {
		t.Errorf("删除查询仍扫描整份快照：\n%s", plan.String())
	}
}

func TestEmbyDeletionDeduplicatesAndKeepsOtherSnapshots(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	for _, target := range []struct{ scope, generation string }{{"scope", "gen"}, {"scope", "other-gen"}, {"other-scope", "gen"}} {
		items := []model.EmbyItem{
			{ID: "target", Type: "Series", SeriesID: "target", ParentID: "target", LibraryID: "target"},
			{ID: "child", Type: "Episode", SeriesID: "target", ParentID: "target", LibraryID: "target"},
			{ID: "unrelated", Type: "Movie", LibraryID: "other-library"},
		}
		if err := s.EmbyStage(ctx, target.scope, target.generation, items); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := s.db.QueryContext(ctx, embyDeleteItemsSQL, "scope", "gen", "target")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	deleted := make(map[string]bool, 2)
	for rows.Next() {
		var kind, id, seriesID string
		if err := rows.Scan(&kind, &id, &seriesID); err != nil {
			t.Fatal(err)
		}
		if deleted[id] {
			t.Fatal("多分支匹配重复返回条目", id)
		}
		deleted[id] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if len(deleted) != 2 || !deleted["target"] || !deleted["child"] {
		t.Fatal("删除范围错误", deleted)
	}
	var remaining int
	if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM emby_items").Scan(&remaining); err != nil || remaining != 7 {
		t.Fatal("影响了其他快照、范围或无关条目", remaining, err)
	}
}
