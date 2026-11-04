package storage

import (
	"context"
	"path/filepath"
	"testing"
)

func TestMediaCleanupSummarySurvivesCompactIndexRebuild(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "cleanup.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	run := MediaOrganizeRunRow{ID: "run", RuleID: "rule", Status: "completed", At: "2026-11-04", Record: "compact", Cleanup: `{"runId":"run","files":20}`}
	if err := s.SaveMediaOrganize(ctx, "rules", []MediaOrganizeRunRow{run}, nil); err != nil {
		t.Fatal(err)
	}
	run.Cleanup = ""
	if err := s.SaveMediaOrganize(ctx, "rules", []MediaOrganizeRunRow{run}, nil); err != nil {
		t.Fatal(err)
	}
	query, err := s.QueryMediaOrganize(ctx, "", "all", 1)
	if err != nil || len(query.Cleanup) != 1 || query.Cleanup[0] != `{"runId":"run","files":20}` {
		t.Fatal("重建精简索引不能清空已保存的清理摘要", query, err)
	}
	run.Cleanup = `{"runId":"run","files":21}`
	if err := s.SaveMediaOrganize(ctx, "rules", []MediaOrganizeRunRow{run}, nil); err != nil {
		t.Fatal(err)
	}
	query, err = s.QueryMediaOrganize(ctx, "", "all", 1)
	if err != nil || len(query.Cleanup) != 1 || query.Cleanup[0] != run.Cleanup {
		t.Fatal("新的清理结果应替换旧摘要", query, err)
	}
	if err := s.SaveMediaOrganize(ctx, "rules", nil, []string{run.ID}); err != nil {
		t.Fatal(err)
	}
	query, err = s.QueryMediaOrganize(ctx, "", "all", 1)
	if err != nil || len(query.Cleanup) != 0 {
		t.Fatal("删除运行记录应同时移除其摘要", query, err)
	}
}
