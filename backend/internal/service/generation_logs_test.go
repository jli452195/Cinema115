package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

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

func TestGenerationLogsRepeatedWriteAndSource(t *testing.T) {
	logs := &GenerationLogs{}
	client := shareListFunc(func(context.Context, *p115.ShareInfo, string, int, string) (*p115.SharePage, error) {
		return &p115.SharePage{Count: 2, Files: []p115.ShareFile{{ID: "1", Name: "电影.mkv", Size: 2 << 20}, {ID: "2", Name: "字幕.srt"}}}, nil
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
		written = written || entry.Level == "INFO" && strings.Contains(entry.Message, "STRM 生成成功")
		if strings.Contains(entry.Message, "已存在") {
			t.Fatal("不应记录已存在统计")
		}
		filtered = filtered || entry.Level == "DEBUG" && strings.Contains(entry.Message, "按后缀")
		completed = completed || strings.Contains(entry.Message, "任务完成")
		if strings.Contains(entry.Message, "password=") || strings.Contains(entry.Message, "private-cookie") {
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
		if len(all) != 3 || all[0].Level != tc.level || all[1].Level != "DEBUG" {
			t.Fatal("日志等级不正确", all)
		}
		for _, entry := range all {
			if strings.Contains(entry.Message, "private") || strings.Contains(entry.Message, "secret") {
				t.Fatal("上游凭证进入日志")
			}
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
		permanent = permanent || strings.Contains(entry.Message, "设置并确认分享长期有效")
		cleaned = cleaned || strings.Contains(entry.Message, "临时目录已删除")
	}
	if !permanent || !cleaned || !strings.Contains(all[0].Message, "任务完成") {
		t.Fatal("转存阶段日志缺失")
	}
}
