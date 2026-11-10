package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
)

func mediaColdFixture(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "cold-history.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	runs := []MediaOrganizeRunRow{
		{ID: "cold", RuleID: "rule", At: "2026-11-03", Record: `{"id":"cold","status":"partial","rule":{"id":"rule","storage":"115","accountId":"primary","sourceId":"original","targetId":"target","source":"/source","target":"/target"},"classification":{"revision":"frozen"},"groups":[{"id":"g1","title":"第一作品","tmdbId":1},{"id":"g2","title":"第二作品","tmdbId":2},{"id":"g3","title":"失败作品","tmdbId":3}],"entries":[{"file":{"id":"01","mediaType":"video","parentId":"p1","sourcePath":"Season/video.mkv","sha1":"ABC"},"group":"g1","relative":"目标/video.mkv","target":"/target/video.mkv","status":"done","result":{"action":"move"},"cd2FileId":"native","cd2Hashes":{"2":"hash"},"cd2Current":{"path":"original"}},{"file":{"id":"sub","mediaType":"subtitle","parentId":"p1"},"videoId":"01","group":"g1","status":"done"},{"file":{"id":"1","mediaType":"video","parentId":"p2","sourcePath":"SeasonExtra/other.mkv"},"group":"g2","relative":"目标/other.mkv","status":"done"},{"file":{"id":"bad","mediaType":"video","parentId":"p3","sourcePath":"Failed/bad.mkv"},"group":"g3","status":"error","p115Move":{"phase":"moving"}},{"file":{"id":"bad-sub","mediaType":"subtitle"},"videoId":"bad","group":"g3","status":"error"}]}`, Tasks: []MediaOrganizeTaskRow{{FileID: "01", Group: "done", Record: "01"}, {FileID: "1", Group: "done", Record: "1"}, {FileID: "bad", Group: "failed", Record: "bad"}}},
		{ID: "other", RuleID: "other-rule", At: "2026-11-02", Record: `{"id":"other","status":"completed","rule":{"id":"other-rule"},"entries":[],"groups":[]}`},
		{ID: "active", RuleID: "rule", At: "2026-11-04", Record: `{"id":"active","status":"queued","rule":{"id":"rule"},"entries":[],"groups":[]}`},
		{ID: "empty", RuleID: "rule", At: "2026-11-01", Record: `{"id":"empty","status":"failed","rule":{"id":"rule"},"entries":[],"groups":[]}`},
	}
	if err := s.SaveMediaOrganize(context.Background(), `{"rules":[]}`, runs, nil); err != nil {
		t.Fatal(err)
	}
	return s
}

func mediaColdIDs(t *testing.T, records []string) []string {
	t.Helper()
	ids := make([]string, 0, len(records))
	for _, raw := range records {
		var value struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal([]byte(raw), &value); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, value.ID)
	}
	return ids
}

func TestMediaOrganizeColdStartupAndSelectors(t *testing.T) {
	s := mediaColdFixture(t)
	ctx := context.Background()
	loaded, err := s.LoadMediaOrganize(ctx)
	if err != nil || !loaded.Initialized || !reflect.DeepEqual(mediaColdIDs(t, loaded.Runs), []string{"active"}) {
		t.Fatal("启动只能读取活动工作，停止历史必须留在数据库", loaded, err)
	}
	for _, test := range []struct {
		ids          []string
		rule, status string
		want         []string
	}{
		{ids: []string{"other", "cold"}, want: []string{"cold", "other"}},
		{ids: []string{"missing"}, want: []string{}},
		{rule: "rule", status: "failed", want: []string{"cold"}},
		{rule: "rule", status: "done", want: []string{"cold"}},
		{rule: "rule", status: "empty", want: []string{"active", "empty"}},
		{status: "pending", want: []string{"cold"}},
	} {
		records, err := s.LoadMediaOrganizeRuns(ctx, test.ids, test.rule, test.status)
		if err != nil || !reflect.DeepEqual(mediaColdIDs(t, records), test.want) {
			t.Fatal(test, records, err)
		}
	}
	for _, test := range []struct{ ids, want []string }{
		{ids: []string{"bad"}, want: []string{"cold"}},
		{ids: []string{"01"}, want: []string{}},
		{ids: nil, want: []string{}},
	} {
		records, err := s.LoadMediaOrganizeFailures(ctx, "rule", test.ids)
		if err != nil || !reflect.DeepEqual(mediaColdIDs(t, records), test.want) {
			t.Fatal(test, records, err)
		}
	}
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM media_organize_runs`).Scan(&count); err != nil || count != 4 {
		t.Fatal("只读加载改变了历史", count, err)
	}
}

func TestMediaOrganizeColdTaskAndReferenceProjection(t *testing.T) {
	s := mediaColdFixture(t)
	ctx := context.Background()
	raw, err := s.LoadMediaOrganizeTask(ctx, "cold", "01")
	if err != nil {
		t.Fatal(err)
	}
	var task struct {
		Rule           map[string]any   `json:"rule"`
		Classification map[string]any   `json:"classification"`
		Entries        []map[string]any `json:"entries"`
		Groups         []map[string]any `json:"groups"`
	}
	if err := json.Unmarshal([]byte(raw), &task); err != nil {
		t.Fatal(err)
	}
	if len(task.Entries) != 2 || len(task.Groups) != 1 || task.Groups[0]["id"] != "g1" || task.Rule["accountId"] != "primary" || task.Classification["revision"] != "frozen" || task.Entries[0]["cd2FileId"] != "native" {
		t.Fatal("详情必须保留冻结身份，仅返回当前视频与附件", raw)
	}
	for _, fileID := range []string{"missing", "sub"} {
		if _, err := s.LoadMediaOrganizeTask(ctx, "cold", fileID); !errors.Is(err, sql.ErrNoRows) {
			t.Fatal("附件或缺失文件不能冒充视频任务", fileID, err)
		}
	}
	for _, test := range []struct {
		parents, scopes []string
		recursive       bool
		want            string
	}{
		{parents: []string{"p1"}, want: "01"},
		{scopes: []string{"Season"}, want: "01"},
		{parents: []string{"p2"}, want: "1"},
	} {
		records, err := s.LoadMediaOrganizeReferences(ctx, "rule", test.parents, test.scopes, test.recursive)
		if err != nil || len(records) != 1 {
			t.Fatal(test, records, err)
		}
		if err := json.Unmarshal([]byte(records[0]), &task); err != nil {
			t.Fatal(err)
		}
		if len(task.Entries) != 1 || len(task.Groups) != 1 || task.Entries[0]["file"].(map[string]any)["id"] != test.want {
			t.Fatal("关联范围不能扩大到前缀相近目录或其他视频", test, records)
		}
	}
	records, err := s.LoadMediaOrganizeReferences(ctx, "rule", nil, nil, true)
	if err != nil || len(records) != 1 {
		t.Fatal(records, err)
	}
	if err := json.Unmarshal([]byte(records[0]), &task); err != nil || len(task.Entries) != 2 || len(task.Groups) != 2 {
		t.Fatal("递归关联也只能读取成功视频", records, err)
	}
	records, err = s.LoadMediaOrganizeReferences(ctx, "rule", nil, []string{"."}, false)
	if err != nil || len(records) != 1 {
		t.Fatal("根目录公共附件应关联子目录中的成功视频", records, err)
	}
	if err := json.Unmarshal([]byte(records[0]), &task); err != nil || len(task.Entries) != 2 || len(task.Groups) != 2 {
		t.Fatal("点号根范围与空根范围必须具有相同语义", records, err)
	}
	records, err = s.LoadMediaOrganizeReferences(ctx, "rule", nil, nil, false)
	if err != nil || len(records) != 0 {
		t.Fatal("没有范围的非递归关联应为空", records, err)
	}
}

func TestMediaOrganizeColdQueriesRespectCancellation(t *testing.T) {
	s := mediaColdFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.LoadMediaOrganizeRuns(ctx, []string{"cold"}, "", ""); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := s.LoadMediaOrganizeTask(ctx, "cold", "01"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := s.LoadMediaOrganizeReferences(ctx, "rule", []string{"p1"}, nil, false); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := s.LoadMediaOrganizeFailures(ctx, "rule", []string{"bad"}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
