package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestMediaOrganizeFailedRefsSearchAndOrder(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "records.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rows := []MediaOrganizeRunRow{
		{ID: "first", RuleID: "rule", Record: `{}`, Tasks: []MediaOrganizeTaskRow{
			{FileID: "b", Group: "failed", At: "2026-11-12T02:00:00Z", Search: "work literal_%_"},
			{FileID: "a", Group: "failed", At: "2026-11-12T02:00:00Z", Search: "work literal_%_"},
			{FileID: "done", Group: "done", At: "2026-11-12T03:00:00Z", Search: "work literal_%_"},
		}},
		{ID: "second", RuleID: "rule", Record: `{}`, Tasks: []MediaOrganizeTaskRow{
			{FileID: "later", Group: "failed", At: "2026-11-12T03:00:00Z", Search: "other"},
		}},
	}
	if err := s.SaveMediaOrganize(ctx, `{}`, rows, nil); err != nil {
		t.Fatal(err)
	}
	wanted := []MediaOrganizeTaskRef{{RunID: "first", FileID: "a"}, {RunID: "first", FileID: "b"}}
	for _, query := range []string{" WORK ", "%", "_"} {
		got, err := s.QueryMediaOrganizeFailedRefs(ctx, query)
		if err != nil || !reflect.DeepEqual(got, wanted) {
			t.Fatal("失败任务应沿用字面搜索和稳定排序", query, got, err)
		}
	}
	got, err := s.QueryMediaOrganizeFailedRefs(ctx, "")
	if err != nil || len(got) != 3 || got[0].FileID != "later" {
		t.Fatal("全部失败查询不能分页或包含成功", got, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.QueryMediaOrganizeFailedRefs(canceled, ""); !errors.Is(err, context.Canceled) {
		t.Fatal("失败任务查询应传递取消", err)
	}
}

func TestMediaOrganizeResolvedCandidatesProjectOnlyIndexedOverlap(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "records.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	entry := func(id string) map[string]any {
		return map[string]any{"file": map[string]any{"id": id, "mediaType": "video", "size": 123}, "modified": 456,
			"status": "done", "result": map[string]any{"status": "done", "at": "2026-11-12T03:00:00Z"}}
	}
	entries := make([]map[string]any, 0, 2002)
	entries = append(entries, entry("a"), entry("b"))
	for i := range 2000 {
		entries = append(entries, entry(fmt.Sprintf("unrelated-large-entry-%d", i)))
	}
	raw, err := json.Marshal(map[string]any{"id": "success", "createdAt": "2026-11-12T02:00:00Z", "status": "completed",
		"rule":    map[string]any{"id": "rule", "storage": "local", "source": "/source", "target": "/target"},
		"entries": entries, "groups": []map[string]any{{"id": "unrelated-group"}}})
	if err != nil {
		t.Fatal(err)
	}
	rows := []MediaOrganizeRunRow{
		{ID: "failed-a", RuleID: "rule", Record: `{}`, Tasks: []MediaOrganizeTaskRow{{FileID: "a", Group: "failed"}}},
		{ID: "failed-b", RuleID: "rule", Record: `{}`, Tasks: []MediaOrganizeTaskRow{{FileID: "b", Group: "failed"}}},
		{ID: "unmatched", RuleID: "rule", Record: `{}`, Tasks: []MediaOrganizeTaskRow{{FileID: "missing", Group: "failed"}}},
		{ID: "other-rule", RuleID: "other", Record: `{}`, Tasks: []MediaOrganizeTaskRow{{FileID: "a", Group: "failed"}}},
		{ID: "success", RuleID: "rule", Record: string(raw), Tasks: []MediaOrganizeTaskRow{{FileID: "a", Group: "done"}, {FileID: "b", Group: "done"}}},
		{ID: "unrelated-success", RuleID: "rule", Record: `not-json`, Tasks: []MediaOrganizeTaskRow{{FileID: "unrelated", Group: "done"}}},
	}
	if err := s.SaveMediaOrganize(ctx, `{}`, rows, nil); err != nil {
		t.Fatal(err)
	}
	for _, scope := range [][]string{nil, {"failed-a"}, {"unmatched"}, {}} {
		got, err := s.LoadMediaOrganizeResolvedCandidates(ctx, scope)
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		if scope == nil {
			count = 2
		} else if len(scope) == 1 && scope[0] == "failed-a" {
			count = 1
		}
		if len(got) != count {
			t.Fatal("协调候选应限定失败批次及同规则交集", scope, got)
		}
		for _, candidate := range got {
			var projected struct {
				ID, CreatedAt string
				Rule          map[string]any
				Entries       []map[string]any
				Groups        []map[string]any
			}
			if err := json.Unmarshal([]byte(candidate.Completed), &projected); err != nil {
				t.Fatal(err)
			}
			if projected.ID != "success" || projected.CreatedAt == "" || projected.Rule["source"] != "/source" || len(projected.Entries) != 1 || len(projected.Groups) != 0 || strings.Contains(candidate.Completed, "unrelated-large-entry") {
				t.Fatal("成功投影只应保留交集条目及原始冻结信息", candidate)
			}
			file := projected.Entries[0]["file"].(map[string]any)
			if candidate.RunID != "failed-"+file["id"].(string) || projected.Entries[0]["modified"] != float64(456) || projected.Entries[0]["result"].(map[string]any)["status"] != "done" {
				t.Fatal("投影应保留对应失败批次及成功身份和完成证据", candidate)
			}
		}
	}
	combined := MediaOrganizeRunRow{ID: "failed-both", RuleID: "rule", Record: `{}`, Tasks: []MediaOrganizeTaskRow{{FileID: "a", Group: "failed"}, {FileID: "b", Group: "failed"}}}
	if err := s.SaveMediaOrganize(ctx, `{}`, []MediaOrganizeRunRow{combined}, nil); err != nil {
		t.Fatal(err)
	}
	combinedCandidates, err := s.LoadMediaOrganizeResolvedCandidates(ctx, []string{combined.ID})
	if err != nil || len(combinedCandidates) != 1 || strings.Count(combinedCandidates[0].Completed, `"modified":456`) != 2 {
		t.Fatal("同一失败与成功批次的多文件交集应合并投影，不能重复候选", combinedCandidates, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.LoadMediaOrganizeResolvedCandidates(canceled, nil); !errors.Is(err, context.Canceled) {
		t.Fatal("协调候选查询应传递取消", err)
	}
}
