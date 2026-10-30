package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
)

func TestGenerationLogsBoundedConcurrentAndFiltered(t *testing.T) {
	logs := &GenerationLogs{}
	var wg sync.WaitGroup
	for n := 0; n < 4; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 600; i++ {
				logs.append("task", "网页", "DEBUG", "读取目录")
				_ = logs.Snapshot("INFO")
			}
		}()
	}
	wg.Wait()
	logs.append("task", "网页", "INFO", "完成")
	all := logs.Snapshot("ALL")
	if len(all) != GenerationLogCapacity || all[0].ID != 2401 || all[len(all)-1].ID != 402 {
		t.Fatal("容量或顺序错误")
	}
	for i := 1; i < len(all); i++ {
		if all[i].ID >= all[i-1].ID {
			t.Fatal("顺序错误")
		}
	}
	if got := logs.Snapshot("INFO"); len(got) != 1 || got[0].Message != "完成" {
		t.Fatal("等级筛选错误")
	}
	all[0].Message = "修改副本"
	if logs.Snapshot("INFO")[0].Message != "完成" {
		t.Fatal("快照影响内部数据")
	}
}

func TestLargeGenerationPreservesEarlierDiagnostics(t *testing.T) {
	logs := &GenerationLogs{}
	logs.Append("STRM", "transfer", "网页", "DEBUG", "扫描阶段保留标记")
	files := make([]p115.ShareFile, 0, 2220)
	for i := 0; i < 2220; i++ {
		ext := ".txt"
		if i < 120 {
			ext = ".mkv"
		}
		files = append(files, p115.ShareFile{ID: strconv.Itoa(i + 1), Name: fmt.Sprintf("file-%d%s", i, ext), SHA1: "0123456789ABCDEF0123456789ABCDEF01234567", Size: 2 << 20})
	}
	client := shareListFunc(func(_ context.Context, _ *p115.ShareInfo, _ string, offset int, _ string) (*p115.SharePage, error) {
		return &p115.SharePage{Count: len(files), Offset: offset, Files: files[offset:min(offset+100, len(files))]}, nil
	})
	g := &LoggedGenerator{Logs: logs, Generator: &STRMGenerator{Client: client, Root: t.TempDir(), Cookie: func() string { return "test-cookie" }}}
	result, err := g.Generate(context.Background(), generationOptions(t))
	if err != nil || result.Written != 120 {
		t.Fatal(result, err)
	}
	entries := logs.Snapshot("ALL")
	if len(entries) > 300 || entries[len(entries)-1].Message != "扫描阶段保留标记" {
		t.Fatal("large generation evicted scan diagnostics", len(entries))
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Message, "STRM 生成成功：") && strings.Contains(entry.Message, ".strm") {
			t.Fatal("unbounded file logs", entry)
		}
	}
}

func TestGenerationLogsRepeatedWriteAndSource(t *testing.T) {
	logs := &GenerationLogs{}
	client := shareListFunc(func(context.Context, *p115.ShareInfo, string, int, string) (*p115.SharePage, error) {
		return &p115.SharePage{Count: 2, Files: []p115.ShareFile{{ID: "1", Name: "电影.mkv", SHA1: "0123456789ABCDEF0123456789ABCDEF01234567", Size: 2 << 20}, {ID: "2", Name: "字幕.srt"}}}, nil
	})
	g := &LoggedGenerator{Logs: logs, Generator: &STRMGenerator{Client: client, Root: t.TempDir(), Cookie: func() string { return "private-cookie" }}}
	input := generationOptions(t)
	for i := 0; i < 2; i++ {
		result, err := g.Generate(WithGenerationSource(context.Background(), "Telegram"), input)
		if err != nil || result.Written != 1 {
			t.Fatal("生成失败", err)
		}
	}
	all := logs.Snapshot("ALL")
	var written, filtered, completed bool
	for _, entry := range all {
		if entry.Source != "Telegram" || entry.TaskID == "" {
			t.Fatal("任务来源缺失")
		}
		written = written || entry.Level == "INFO" && strings.Contains(entry.Message, "STRM 写入 1")
		if strings.Contains(entry.Message, "已存在") {
			t.Fatal("不应记录已存在统计")
		}
		filtered = filtered || entry.Level == "DEBUG" && strings.Contains(entry.Message, "按后缀")
		completed = completed || entry.Level == "INFO" && strings.Contains(entry.Message, "总耗时")
		if strings.Contains(entry.Message, "private-cookie") {
			t.Fatal("泄漏凭证")
		}
	}
	if !written || !filtered || !completed {
		t.Fatal("缺少实际生成日志")
	}
	if all[0].TaskID == all[len(all)-1].TaskID {
		t.Fatal("任务编号重复")
	}
}

func TestGenerationLogErrorLevelsAndProgress(t *testing.T) {
	for _, tc := range []struct {
		err   error
		level string
	}{{context.Canceled, "WARNING"}, {context.DeadlineExceeded, "ERROR"}, {ErrNoMedia, "WARNING"}, {ErrGenerationInput, "ERROR"}, {errors.New("https://115.com/?password=secret Cookie: private"), "ERROR"}} {
		logs := &GenerationLogs{}
		reported := false
		ctx := WithGenerationProgress(context.Background(), func(GenerationProgress) { reported = true })
		g := &LoggedGenerator{Logs: logs, Generator: generateFunc(func(ctx context.Context, _ GenerateInput) (*GenerationResult, error) {
			reportGeneration(ctx, "waiting", "private upstream address and cookie")
			return nil, tc.err
		})}
		_, err := g.Generate(ctx, GenerateInput{})
		if !errors.Is(err, tc.err) || !reported {
			t.Fatal("改变了原有错误或进度回调")
		}
		all := logs.Snapshot("ALL")
		if len(all) != 4 || all[0].Level != tc.level || all[1].Level != "DEBUG" {
			t.Fatal("日志等级不正确", all)
		}
		for _, entry := range all {
			if strings.Contains(entry.Message, "private") || strings.Contains(entry.Message, "secret") {
				t.Fatal("上游凭证进入日志")
			}
		}
	}
}

func TestGenerationInputDetailsStayDebugAndStartRemainsInfo(t *testing.T) {
	root := t.TempDir()
	logs := &GenerationLogs{}
	g := &LoggedGenerator{Root: root, Logs: logs, Generator: generateFunc(func(context.Context, GenerateInput) (*GenerationResult, error) {
		return &GenerationResult{Written: 8}, nil
	})}
	input := generationOptions(t)
	input.URL = "https://private-user:private-pass@115.com/s/demo?password=old-password&token=private-token#private-fragment"
	input.Password = "new1"
	input.OutputDir = "待整理"
	input.Recursive = true
	if _, err := g.Generate(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	messages := make(map[string]string)
	for _, entry := range logs.Snapshot("ALL") {
		messages[entry.Message] = entry.Level
		if strings.Contains(entry.Message, "private-") || strings.Contains(entry.Message, "old-password") {
			t.Fatal("记录了无关凭证或未使用的提取码")
		}
	}
	for expected, level := range map[string]string{
		"遍历模式：包含所有子目录":                                  "DEBUG",
		"115链接：https://115cdn.com/s/demo?password=new1": "DEBUG",
		"开始处理任务：demo":                                   "INFO",
		"使用配置的目录：待整理（" + filepath.ToSlash(filepath.Join(root, "待整理")) + "）": "DEBUG",
	} {
		if messages[expected] != level {
			t.Fatalf("任务信息等级错误：%s level=%s", expected, messages[expected])
		}
	}
}

func TestReshareStagesInGenerationLogs(t *testing.T) {
	w, input := setupWorkflow(t, &transferFake{})
	logs := &GenerationLogs{}
	g := &LoggedGenerator{Generator: w, Logs: logs}
	if _, err := g.Generate(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	all := logs.Snapshot("ALL")
	var permanent, cleaned bool
	for _, entry := range all {
		wantLevel := "DEBUG"
		if strings.HasPrefix(entry.Message, "开始处理任务：") || strings.HasPrefix(entry.Message, "分享者与所选账号") || strings.HasPrefix(entry.Message, "正在核对分享者 user ID") || strings.Contains(entry.Message, "总耗时") {
			wantLevel = "INFO"
		}
		if entry.Level != wantLevel {
			t.Fatal("模式提示或处理阶段的等级错误", entry)
		}
		permanent = permanent || strings.Contains(entry.Message, "设置并确认分享长期有效")
		cleaned = cleaned || strings.Contains(entry.Message, "临时目录已移入回收站")
	}
	if !permanent || !cleaned || !strings.Contains(all[0].Message, "转存分享生成 STRM 完成！") {
		t.Fatal("转存阶段日志缺失")
	}
}

func TestGenerationInfoKeepsOneCompletionSummary(t *testing.T) {
	for _, tc := range []struct {
		name          string
		count, failAt int
	}{
		{"不足十个", 7, 0},
		{"整十不重复完成", 20, 0},
		{"非整十完成", 25, 0},
		{"中途失败不报告完成", 25, 13},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := make([]p115.ShareFile, 0, tc.count+1)
			for i := 1; i <= tc.count; i++ {
				if i == tc.failAt {
					files = append(files, p115.ShareFile{ID: "blocked-directory", Name: "blocked", Directory: true})
				} else {
					files = append(files, p115.ShareFile{ID: strconv.Itoa(i), Name: fmt.Sprintf("video-%d.mkv", i), SHA1: "0123456789ABCDEF0123456789ABCDEF01234567", Size: 2 << 20})
				}
			}
			files = append(files, p115.ShareFile{ID: "subtitle", Name: "字幕.srt"})
			client := shareListFunc(func(_ context.Context, _ *p115.ShareInfo, cid string, _ int, _ string) (*p115.SharePage, error) {
				if cid == "blocked-directory" {
					return &p115.SharePage{Count: 1, Files: []p115.ShareFile{{ID: strconv.Itoa(tc.failAt), Name: fmt.Sprintf("video-%d.mkv", tc.failAt), SHA1: "0123456789ABCDEF0123456789ABCDEF01234567", Size: 2 << 20}}}, nil
				}
				return &p115.SharePage{Count: len(files), Files: files}, nil
			})
			root := t.TempDir()
			input := generationOptions(t)
			if tc.failAt > 0 {
				// 用普通文件占据子目录，模拟无法通过数字后缀规避的父路径冲突。
				if err := os.MkdirAll(filepath.Join(root, input.OutputDir), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, input.OutputDir, "blocked"), []byte("占位"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			logs := &GenerationLogs{}
			generator := &LoggedGenerator{Logs: logs, Generator: &STRMGenerator{Root: root, Client: client, Cookie: func() string { return "test-cookie" }}}
			result, err := generator.Generate(context.Background(), input)
			if (err != nil) != (tc.failAt > 0) {
				t.Fatal("生成结果不符合预期", err)
			}
			wantWritten := tc.count
			if tc.failAt > 0 {
				wantWritten = tc.failAt - 1
			}
			if result == nil || result.Written != wantWritten {
				t.Fatal("写入数量错误")
			}
			completed := 0
			for _, entry := range logs.Snapshot("INFO") {
				if strings.HasPrefix(entry.Message, "STRM 生成成功：") {
					t.Fatal("job emitted file-count milestones", entry)
				}
				if strings.Contains(entry.Message, "总耗时") {
					completed++
					if !strings.Contains(entry.Message, fmt.Sprintf("STRM 写入 %d，失败 0", wantWritten)) {
						t.Fatal("final summary lost write statistics", entry)
					}
				}
			}
			wantCompleted := 1
			if tc.failAt > 0 {
				wantCompleted = 0
			}
			if completed != wantCompleted {
				t.Fatalf("incorrect completion count=%d want=%d", completed, wantCompleted)
			}
			var pageLogged, summaryLogged bool
			for _, entry := range logs.Snapshot("DEBUG") {
				pageLogged = pageLogged || entry.Message == fmt.Sprintf("读取分享目录第 1 页，本页 %d 项", len(files))
				if tc.failAt == 0 {
					summaryLogged = summaryLogged || strings.HasPrefix(entry.Message, fmt.Sprintf("STRM 生成完成！成功：%d，失败：0，耗时 ", wantWritten))
				} else {
					summaryLogged = summaryLogged || strings.HasPrefix(entry.Message, fmt.Sprintf("STRM 生成中断！成功：%d，失败：1，未处理：%d，耗时 ", wantWritten, tc.count-wantWritten-1))
				}
			}
			if !pageLogged || !summaryLogged {
				t.Fatal("DEBUG 分页或内部统计缺失")
			}
			detailed := 0
			for _, entry := range logs.Snapshot("DEBUG") {
				if strings.Contains(entry.Message, "STRM 生成成功：") && strings.Contains(entry.Message, ".strm") {
					detailed++
				}
			}
			if detailed != wantWritten {
				t.Fatal("DEBUG 缺少逐文件记录")
			}
			if tc.failAt > 0 && len(logs.Snapshot("ERROR")) != 1 {
				t.Fatal("写入失败没有保留 ERROR 日志")
			}
		})
	}
}

func TestSTRMWriteProgressThrottlesByElapsedTime(t *testing.T) {
	logs := &GenerationLogs{}
	ctx := context.WithValue(context.Background(), generationLogKey{}, generationLogger{store: logs, task: "time-progress", source: "网页"})
	start := time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)
	progress := strmWriteProgress{last: start}
	progress.report(ctx, start.Add(time.Second), 10, 50)
	progress.report(ctx, start.Add(14*time.Second), 19, 50)
	if len(logs.Snapshot("INFO")) != 0 {
		t.Fatal("progress logged before the interval elapsed")
	}
	progress.report(ctx, start.Add(15*time.Second), 20, 50)
	progress.report(ctx, start.Add(29*time.Second), 39, 50)
	progress.report(ctx, start.Add(30*time.Second), 40, 50)
	progress.report(ctx, start.Add(31*time.Second), 50, 50)
	entries := logs.Snapshot("INFO")
	if len(entries) != 2 || entries[0].Message != "STRM 生成进度：40/50" || entries[1].Message != "STRM 生成进度：20/50" {
		t.Fatalf("interval progress missing, duplicated, or forced at completion: %+v", entries)
	}
}
