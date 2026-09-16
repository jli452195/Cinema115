package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
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
	var wrongBot atomic.Bool
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
	s.Notifier = notificationFunc(func(_ context.Context, botID, _ string, _ GenerateInput, _ *GenerationResult, _ error) error {
		if botID != "test-bot" {
			wrongBot.Store(true)
		}
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
	settings = s.Settings.Value()
	settings.TelegramEnabled, settings.BotID = true, "test-bot"
	s.Settings.value = settings
	s.Settings.ValidateBot = func(string, bool) error { return nil }
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
	settings.BotID = "changed-bot"
	if err := s.Settings.Save(settings); err != nil {
		t.Fatal(err)
	}
	close(release)
	completed := awaitRun(t, runs, view.ID)
	if completed.Status != "success" || calls.Load() != 2 || notifications.Load() != 2 || wrongBot.Load() {
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

func TestBackgroundRunDeduplicatesEquivalentActiveSnapshot(t *testing.T) {
	root, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, db, input := setupShareTasks(t, generateFunc(func(context.Context, GenerateInput) (*GenerationResult, error) {
		return &GenerationResult{Written: 1, Files: []GeneratedSTRM{{Path: "movie.strm"}}}, nil
	}))
	input.VideoExtensions = "mkv,mp4"
	runs := NewShareRuns(root, s)
	firstID := strings.Repeat("e", 32)
	first, err := runs.Submit(WithGenerationSource(context.Background(), "网页"), firstID, []GenerateInput{input})
	if err != nil {
		t.Fatal(err)
	}
	equivalent := input
	equivalent.URL = "https://115cdn.com/s/demo"
	equivalent.Password = "a+b&c"
	equivalent.CID = "0"
	equivalent.Mode = "direct"
	equivalent.VideoExtensions = ".MP4，mkv"
	duplicate, err := runs.Submit(WithGenerationSource(context.Background(), "订阅"), strings.Repeat("f", 32), []GenerateInput{equivalent})
	if err != nil {
		t.Fatal(err)
	}
	if duplicate.ID != first.ID || len(taskHistory(t, db)) != 1 {
		t.Fatal("等价的活动分享快照没有合并", duplicate.ID, first.ID)
	}
	runShareWorker(t, runs, cancel)
	if completed := awaitRun(t, runs, first.ID); completed.Status != "success" {
		t.Fatal("合并后的任务未完成", completed.Status)
	}
	again, err := runs.Submit(context.Background(), strings.Repeat("1", 32), []GenerateInput{equivalent})
	if err != nil {
		t.Fatal(err)
	}
	if again.ID == first.ID || len(taskHistory(t, db)) != 2 {
		t.Fatal("任务结束后仍被长期去重")
	}
}

func TestGenerateBatchWaitsForExistingEquivalentRun(t *testing.T) {
	root, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	var calls atomic.Int32
	s, _, input := setupShareTasks(t, generateFunc(func(ctx context.Context, _ GenerateInput) (*GenerationResult, error) {
		calls.Add(1)
		started <- struct{}{}
		select {
		case <-release:
			return &GenerationResult{Written: 1, Files: []GeneratedSTRM{{Path: "movie.strm"}}}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}))
	runs := NewShareRuns(root, s)
	runShareWorker(t, runs, cancel)
	first := make(chan []BatchOutcome, 1)
	second := make(chan []BatchOutcome, 1)
	go func() {
		outcomes, _ := runs.GenerateBatch(WithGenerationSource(context.Background(), "网页"), []GenerateInput{input})
		first <- outcomes
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("首个任务未启动")
	}
	go func() {
		outcomes, _ := runs.GenerateBatch(WithGenerationSource(context.Background(), "订阅"), []GenerateInput{input})
		second <- outcomes
	}()
	close(release)
	for _, completed := range []<-chan []BatchOutcome{first, second} {
		select {
		case outcomes := <-completed:
			if len(outcomes) != 1 || outcomes[0].Err != nil || outcomes[0].Result == nil || outcomes[0].Result.Written != 1 {
				t.Fatal("调用方未收到合并任务结果", outcomes)
			}
		case <-time.After(time.Second):
			t.Fatal("调用方等待了不存在的任务编号")
		}
	}
	if calls.Load() != 1 {
		t.Fatal("等价任务重复执行", calls.Load())
	}
}

func TestSuccessfulSnapshotCacheSurvivesNewQueueAndInvalidatesWhenMoved(t *testing.T) {
	var s *ShareTasks
	var calls atomic.Int32
	s, db, input := setupShareTasks(t, generateFunc(func(ctx context.Context, in GenerateInput) (*GenerationResult, error) {
		calls.Add(1)
		directory, err := ResolveOutputDirectory(s.Settings.root, in.OutputDir)
		if err != nil {
			return nil, err
		}
		if err := ensureSTRMDirectory(directory); err != nil {
			return nil, err
		}
		file := GeneratedSTRM{Path: "movie.strm", Content: "http://nas/api/?share_code=demo&id=1\n"}
		if err := writeSTRM(ctx, filepath.Join(directory, file.Path), file.Content); err != nil {
			return nil, err
		}
		return &GenerationResult{Mode: "direct", Directory: directory, Written: 1, Files: []GeneratedSTRM{file}}, nil
	}))
	settings := s.Settings.Value()
	settings.AccountID = "cache-account"
	if err := s.Settings.Save(settings); err != nil {
		t.Fatal(err)
	}

	rootOne, cancelOne := context.WithCancel(context.Background())
	runs := NewShareRuns(rootOne, s)
	doneOne := make(chan struct{})
	go func() { defer close(doneOne); runs.Run() }()
	first, err := runs.Submit(context.Background(), strings.Repeat("2", 32), []GenerateInput{input})
	if err != nil {
		t.Fatal(err)
	}
	first = awaitRun(t, runs, first.ID)
	cancelOne()
	<-doneOne
	if first.Status != "success" || calls.Load() != 1 || len(taskHistory(t, db)) != 1 {
		t.Fatal("首个快照任务未成功")
	}
	cacheKey, err := shareRunFingerprint(settings, "", []GenerateInput{input})
	if err != nil {
		t.Fatal(err)
	}
	record, found, err := db.LoadShareRunCache(context.Background(), cacheKey)
	if err != nil || !found || strings.Contains(record, "private-cookie") || strings.Contains(record, cookieHash("UID=42_A; SEID=private-cookie")) {
		t.Fatal("成功快照未保存或包含账号凭据", found, err)
	}
	otherAccount := settings
	otherAccount.AccountID = "other-account"
	otherKey, err := shareRunFingerprint(otherAccount, "", []GenerateInput{input})
	if err != nil || otherKey == cacheKey {
		t.Fatal("不同账号错误共享成功快照", err)
	}

	rootTwo, cancelTwo := context.WithCancel(context.Background())
	defer cancelTwo()
	fresh := NewShareRuns(rootTwo, s)
	cached, err := fresh.Submit(WithGenerationSource(context.Background(), "订阅"), strings.Repeat("3", 32), []GenerateInput{input})
	if err != nil {
		t.Fatal(err)
	}
	if cached.Status != "success" || cached.Items[0].Result == nil || !cached.Items[0].Result.Cached || calls.Load() != 1 || len(taskHistory(t, db)) != 1 {
		t.Fatal("重启后的完整快照没有直接复用", cached)
	}

	destination := filepath.Join(s.Settings.root, input.OutputDir, "movie.strm")
	if err := os.Rename(destination, filepath.Join(s.Settings.root, "organized.strm")); err != nil {
		t.Fatal(err)
	}
	regenerated, err := fresh.Submit(context.Background(), strings.Repeat("4", 32), []GenerateInput{input})
	if err != nil {
		t.Fatal(err)
	}
	if regenerated.Status != "queued" || len(taskHistory(t, db)) != 2 {
		t.Fatal("本地文件移动后仍错误复用缓存", regenerated)
	}
	runShareWorker(t, fresh, cancelTwo)
	if completed := awaitRun(t, fresh, regenerated.ID); completed.Status != "success" || calls.Load() != 2 {
		t.Fatal("失效缓存没有重新生成", completed.Status, calls.Load())
	}
}
