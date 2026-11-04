package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 隔离合成数据只用于查询性能验证，不接触真实整理记录、网盘或文件任务。
func mediaLivePerfFixture(tb testing.TB, failed, done, running int) (*Store, []MediaOrganizeLiveTaskRow) {
	tb.Helper()
	s, err := Open(context.Background(), filepath.Join(tb.TempDir(), "isolated-query.db"))
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { _ = s.Close() })
	prefix := "/隔离性能测试/" + strings.Repeat("较长目录名称/", 8)
	row := func(run, file, status string, i int) MediaOrganizeTaskRow {
		name := fmt.Sprintf("测试作品 - S01E%04d - 2160p.WEB-DL.H265.DDP 2.0.mkv", i)
		record, err := json.Marshal(map[string]any{
			"runId": run, "fileId": file, "name": name, "title": "测试作品", "kind": "tv", "mediaType": "video",
			"season": 1, "episode": i, "source": prefix + "来源/" + name, "target": prefix + "目标/" + name,
			"status": status, "message": "隔离合成摘要", "at": "2026-11-03T00:00:00Z", "attachmentCount": 3,
		})
		if err != nil {
			tb.Fatal(err)
		}
		return MediaOrganizeTaskRow{
			FileID: file, Group: status, At: fmt.Sprintf("2026-11-03T00:%02d:%02d.000000000Z", i/60%60, i%60),
			Search: strings.ToLower(name + "\n测试作品\n" + prefix + "来源/" + name + "\n" + prefix + "目标/" + name), Record: string(record),
		}
	}
	saved := MediaOrganizeRunRow{ID: "saved", RuleID: "synthetic-rule", Status: "partial", Record: "{}", Tasks: make([]MediaOrganizeTaskRow, 0, failed)}
	for i := range failed {
		saved.Tasks = append(saved.Tasks, row(saved.ID, fmt.Sprintf("failed-%06d", i), "failed", i))
	}
	if err := s.SaveMediaOrganize(context.Background(), "{}", []MediaOrganizeRunRow{saved}, nil); err != nil {
		tb.Fatal(err)
	}
	runtime := make([]MediaOrganizeLiveTaskRow, 0, done+running)
	for i := range done + running {
		status := "done"
		if i >= done {
			status = "running"
		}
		runtime = append(runtime, MediaOrganizeLiveTaskRow{RunID: "live", MediaOrganizeTaskRow: row("live", fmt.Sprintf("live-%06d", i), status, i)})
	}
	return s, runtime
}

func TestMediaOrganizeLiveQueryScale(t *testing.T) {
	s, runtime := mediaLivePerfFixture(t, 517, 969, 12)
	raw, err := json.Marshal(runtime)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("隔离规模：517 failed + 969 done + 12 running，runtime JSON %.1f KiB", float64(len(raw))/1024)
	start := time.Now()
	result, err := s.QueryMediaOrganizeLive(context.Background(), "", "all", 1, runtime)
	t.Logf("实际 QueryMediaOrganizeLive：%s，total=%d，err=%v", time.Since(start), result.Total, err)
	if err != nil || result.Total != 1498 || len(result.Records) != 20 || result.Counts["failed"] != 517 || result.Counts["done"] != 969 || result.Counts["running"] != 12 {
		t.Fatal("大规模实时摘要查询不能改变筛选计数与分页语义", result.Counts, err)
	}
}

func TestMediaOrganizeLiveQueryScaleFiltersAndReplaces(t *testing.T) {
	s, runtime := mediaLivePerfFixture(t, 500, 988, 12)
	for i := 0; i < 12; i++ {
		row := &runtime[988+i]
		row.RunID, row.FileID = "saved", fmt.Sprintf("failed-%06d", i)
		row.Record = fmt.Sprintf("{\"runId\":\"saved\",\"fileId\":\"%s\",\"status\":\"running\"}", row.FileID)
	}
	runtime[0].Search += "\n唯一%_匹配"
	result, err := s.QueryMediaOrganizeLive(context.Background(), "", "all", 1, runtime)
	if err != nil || result.Total != 1488 || result.Counts["failed"] != 488 || result.Counts["done"] != 988 || result.Counts["running"] != 12 {
		t.Fatal("同一键的运行摘要应覆盖历史失败，大规模合并不能重复计数", result.Counts, err)
	}
	last, err := s.QueryMediaOrganizeLive(context.Background(), "", "failed", 999, runtime)
	if err != nil || last.Total != 488 || last.Page != 25 || len(last.Records) != 8 || last.Counts["done"] != 988 {
		t.Fatal("状态筛选必须先于分页，分组统计仍覆盖整个搜索结果", last, err)
	}
	found, err := s.QueryMediaOrganizeLive(context.Background(), "%_", "all", 1, runtime)
	if err != nil || found.Total != 1 || found.Records[0] != runtime[0].Record {
		t.Fatal("实时摘要的百分号与下划线应按字面搜索", found, err)
	}
	durable, err := s.QueryMediaOrganize(context.Background(), "", "all", 1)
	if err != nil || durable.Total != 500 || durable.Counts["failed"] != 500 || durable.Counts["done"] != 0 || durable.Counts["running"] != 0 {
		t.Fatal("规模查询也不能持久化运行中或成功摘要", durable.Counts, err)
	}
}

func TestMediaOrganizeLiveQueryPreservesTextIdentity(t *testing.T) {
	s, _ := mediaLivePerfFixture(t, 0, 0, 0)
	run := MediaOrganizeRunRow{ID: "text-ids", RuleID: "synthetic-rule", Status: "partial", Record: "{}", Tasks: []MediaOrganizeTaskRow{
		{FileID: "1", Group: "failed", Record: "persisted-one"},
		{FileID: "01", Group: "failed", Record: "persisted-zero-one"},
	}}
	if err := s.SaveMediaOrganize(context.Background(), "{}", []MediaOrganizeRunRow{run}, nil); err != nil {
		t.Fatal(err)
	}
	runtime := []MediaOrganizeLiveTaskRow{{RunID: run.ID, MediaOrganizeTaskRow: MediaOrganizeTaskRow{FileID: "01", Group: "done", Record: "live-zero-one"}}}
	result, err := s.QueryMediaOrganizeLive(context.Background(), "", "all", 1, runtime)
	if err != nil || result.Total != 2 || result.Counts["failed"] != 1 || result.Counts["done"] != 1 {
		t.Fatal("文本键不能把带前导零的文件标识转换为同一个数字", result, err)
	}
	for _, record := range result.Records {
		if record == "persisted-zero-one" {
			t.Fatal("同一文本身份的旧失败未被实时摘要覆盖", result)
		}
	}
}

func BenchmarkMediaOrganizeLiveQuery(b *testing.B) {
	for _, scale := range []struct {
		name                  string
		failed, done, running int
	}{
		{name: "500failed_1000live", failed: 500, done: 988, running: 12},
		{name: "517failed_981live", failed: 517, done: 969, running: 12},
	} {
		b.Run(scale.name, func(b *testing.B) {
			s, runtime := mediaLivePerfFixture(b, scale.failed, scale.done, scale.running)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				result, err := s.QueryMediaOrganizeLive(context.Background(), "", "all", 1, runtime)
				if err != nil || result.Total != scale.failed+scale.done+scale.running {
					b.Fatal(result.Counts, err)
				}
			}
		})
	}
}
