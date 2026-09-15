package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func awaitRun(t *testing.T, runs *ShareRuns, id string) ShareRunView {
	t.Helper()
	timeout := time.NewTimer(3 * time.Second)
	defer timeout.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		view, err := runs.Get(id)
		if err != nil {
			t.Fatal(err)
		}
		if view.Status == "success" || view.Status == "failed" || view.Status == "cancelled" {
			return view
		}
		select {
		case <-timeout.C:
			t.Fatal("后台任务未按期结束")
		case <-tick.C:
		}
	}
}
func runShareWorker(t *testing.T, runs *ShareRuns, cancel context.CancelFunc) {
	t.Helper()
	done := make(chan struct{})
	go func() { defer close(done); runs.Run() }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("后台生成工作器未退出")
		}
	})
}

func TestBackgroundRunSurvivesPageCloseAndPinsWholeSubmission(t *testing.T) {
	root, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := make(chan struct{}, 1)
	release := make(chan struct{})
	var calls, notifications atomic.Int32
	s, db, input := setupShareTasks(t, generateFunc(func(ctx context.Context, in GenerateInput) (*GenerationResult, error) {
		n := calls.Add(1)
		if in.Mode != "reshare" || generationCookie(ctx, nil) != "UID=42_A; SEID=private-cookie" || ctx.Value(automationContextKey{}).(AutomationSettings).AccountID != "original" {
			t.Error("后台任务未固定提交时的模式与账号")
		}
		if n == 1 {
			first <- struct{}{}
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		reportGeneration(ctx, "generating", "测试文件已生成")
		return &GenerationResult{Written: 1}, nil
	}))
	s.Notifier = notificationFunc(func(context.Context, string, GenerateInput, *GenerationResult) error {
		notifications.Add(1)
		return nil
	})
	settings := s.Settings.Value()
	settings.ReshareEnabled = true
	settings.AccountID = "original"
	if err := s.Settings.Save(settings); err != nil {
		t.Fatal(err)
	}
	input.Mode = ""
	runs := NewShareRuns(root, s)
	caller, closePage := context.WithCancel(context.Background())
	view, err := runs.Submit(caller, strings.Repeat("a", 32), []GenerateInput{input, input})
	if err != nil {
		t.Fatal(err)
	}
	if len(taskHistory(t, db)) != 2 {
		t.Fatal("接收成功前未保存全部子任务")
	}
	runShareWorker(t, runs, cancel)
	select {
	case <-first:
	case <-time.After(time.Second):
		t.Fatal("任务未启动")
	}
	closePage()
	// 页面请求取消、配置改动都不能取消已提交的后续链接或换用其他账号。
	s.Cookie = func() string { return "UID=99_A" }
	settings.ReshareEnabled = false
	settings.AccountID = "changed"
	if err := s.Settings.Save(settings); err != nil {
		t.Fatal(err)
	}
	close(release)
	completed := awaitRun(t, runs, view.ID)
	if completed.Status != "success" || calls.Load() != 2 || notifications.Load() != 2 {
		t.Fatal("关页后未完成全部来源或未通知", completed.Status, calls.Load(), notifications.Load())
	}
	repeated, err := runs.Submit(context.Background(), view.ID, []GenerateInput{input, input})
	if err != nil || repeated.Status != "success" || calls.Load() != 2 {
		t.Fatal("重交同一编号重复执行", err)
	}
	input.URL = "https://115.com/s/different"
	if _, err := runs.Submit(context.Background(), view.ID, []GenerateInput{input}); !errors.Is(err, ErrGenerationInput) {
		t.Fatal("编号冲突未拒绝")
	}
	data, err := json.Marshal(completed)
	if err != nil || strings.Contains(string(data), "private-cookie") || strings.Contains(string(data), "cookieHash") {
		t.Fatal("任务轮询暴露凭据")
	}
	completed.Items[0].Result.Count = 99
	again, _ := runs.Get(view.ID)
	if again.Items[0].Result.Count != 1 {
		t.Fatal("轮询结果修改影响内部状态")
	}
}

func TestBackgroundRunExplicitCancelStopsRemainingLinks(t *testing.T) {
	root, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := make(chan struct{}, 1)
	var calls atomic.Int32
	s, db, input := setupShareTasks(t, generateFunc(func(ctx context.Context, _ GenerateInput) (*GenerationResult, error) {
		calls.Add(1)
		first <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	}))
	runs := NewShareRuns(root, s)
	runShareWorker(t, runs, cancel)
	view, err := runs.Submit(context.Background(), strings.Repeat("b", 32), []GenerateInput{input, input})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-first:
	case <-time.After(time.Second):
		t.Fatal("任务未启动")
	}
	if _, err := runs.Cancel(view.ID); err != nil {
		t.Fatal(err)
	}
	completed := awaitRun(t, runs, view.ID)
	if completed.Status != "cancelled" || calls.Load() != 1 {
		t.Fatal("显式取消没有停止剩余链接")
	}
	for _, task := range taskHistory(t, db) {
		if task.Status == "running" || task.Status == "waiting_review" {
			t.Fatal("取消后留下运行任务")
		}
	}
}

func TestBackgroundShutdownAndMissingAccount(t *testing.T) {
	root, cancel := context.WithCancel(context.Background())
	s, db, input := setupShareTasks(t, generateFunc(func(context.Context, GenerateInput) (*GenerationResult, error) {
		t.Error("停机后执行了云端任务")
		return nil, nil
	}))
	runs := NewShareRuns(root, s)
	view, err := runs.Submit(context.Background(), strings.Repeat("c", 32), []GenerateInput{input})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	runs.Run()
	if got, _ := runs.Get(view.ID); got.Status != "cancelled" || taskHistory(t, db)[0].Status == "running" {
		t.Fatal("停机未记录剩余任务取消")
	}
	fresh := NewShareRuns(context.Background(), s)
	s.AccountCookie = func(string) string { return "" }
	if _, err := fresh.Submit(context.Background(), strings.Repeat("d", 32), []GenerateInput{input}); !errors.Is(err, ErrGenerationInput) {
		t.Fatal("未选账号自动回退", err)
	}
}
