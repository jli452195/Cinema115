package service

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type collectedGenerator struct {
	mu    sync.Mutex
	sizes []int
}

func (g *collectedGenerator) Generate(context.Context, GenerateInput) (*GenerationResult, error) {
	return &GenerationResult{Written: 1}, nil
}
func (g *collectedGenerator) GenerateBatch(_ context.Context, inputs []GenerateInput) ([]BatchOutcome, error) {
	g.mu.Lock()
	g.sizes = append(g.sizes, len(inputs))
	g.mu.Unlock()
	out := make([]BatchOutcome, len(inputs))
	for i := range out {
		out[i].Result = &GenerationResult{Written: 1}
	}
	return out, nil
}

func TestWebCollectionMergesAndPreservesRequestIdentity(t *testing.T) {
	generator := &collectedGenerator{}
	tasks, _, input := setupShareTasks(t, generator)
	settings := tasks.Settings.Value()
	settings.ReshareEnabled, settings.TGBatchWaitSeconds = true, 5
	if err := tasks.Settings.Save(settings); err != nil {
		t.Fatal(err)
	}
	input.Mode = ""
	root, cancel := context.WithCancel(context.Background())
	defer cancel()
	runs := NewShareRuns(root, tasks)
	ctx := WithShareCollectionScope(context.Background(), "bot/tv/94664/session")
	firstID, secondID := strings.Repeat("a", 32), strings.Repeat("b", 32)
	first, err := runs.Submit(ctx, firstID, []GenerateInput{input})
	if err != nil {
		t.Fatal(err)
	}
	secondInput := input
	secondInput.URL = "https://115cdn.com/s/episode2"
	second, err := runs.Submit(ctx, secondID, []GenerateInput{secondInput})
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID || len(second.Items) != 2 || len(second.SelectedTaskIDs) != 1 || !first.ReadyAt.Equal(second.ReadyAt) {
		t.Fatalf("未按窗口合并: %+v", second)
	}
	if first.SelectedTaskIDs[0] == second.SelectedTaskIDs[0] {
		t.Fatal("资源任务编号串联")
	}
	repeated, err := runs.Submit(ctx, secondID, []GenerateInput{secondInput})
	if err != nil || repeated.SelectedTaskIDs[0] != second.SelectedTaskIDs[0] || len(repeated.Items) != 2 {
		t.Fatal("重试重复加入批次", err)
	}
	if _, err := runs.Submit(ctx, secondID, []GenerateInput{input}); err == nil {
		t.Fatal("复用请求编号未拒绝内容变化")
	}
	duplicate, err := runs.Submit(ctx, strings.Repeat("c", 32), []GenerateInput{secondInput})
	if err != nil || duplicate.SelectedTaskIDs[0] != second.SelectedTaskIDs[0] {
		t.Fatal("重复资源未正确去重", err)
	}
	lookup, err := runs.Get(secondID)
	if err != nil || lookup.SelectedTaskIDs[0] != second.SelectedTaskIDs[0] {
		t.Fatal("丢失响应后无法按原编号恢复", err)
	}
	runs.mu.Lock()
	runs.runs[first.ID].readyAt = time.Now().Add(-time.Second)
	runs.mu.Unlock()
	runShareWorker(t, runs, cancel)
	if result := awaitRun(t, runs, first.ID); result.Status != "success" {
		t.Fatal(result)
	}
	generator.mu.Lock()
	defer generator.mu.Unlock()
	if len(generator.sizes) != 1 || generator.sizes[0] != 2 {
		t.Fatalf("没有合并执行: %v", generator.sizes)
	}
}

func TestWebCollectionIsolatesScopeSettingsAndExpiredWindow(t *testing.T) {
	tasks, _, input := setupShareTasks(t, &collectedGenerator{})
	settings := tasks.Settings.Value()
	settings.ReshareEnabled, settings.TGBatchWaitSeconds = true, 5
	if err := tasks.Settings.Save(settings); err != nil {
		t.Fatal(err)
	}
	input.Mode = ""
	root, cancel := context.WithCancel(context.Background())
	defer cancel()
	runs := NewShareRuns(root, tasks)
	submit := func(n int, scope string, in GenerateInput) ShareRunView {
		t.Helper()
		in.URL = fmt.Sprintf("https://115cdn.com/s/item%d", n)
		view, err := runs.Submit(WithShareCollectionScope(context.Background(), scope), fmt.Sprintf("%032x", n), []GenerateInput{in})
		if err != nil {
			t.Fatal(err)
		}
		return view
	}
	first := submit(1, "page1", input)
	second := submit(2, "page2", input)
	changed := input
	changed.OutputDir = "another"
	third := submit(3, "page1", changed)
	settings.BotID = "different"
	if err := tasks.Settings.Save(settings); err != nil {
		t.Fatal(err)
	}
	fourth := submit(4, "page1", input)
	settings.BotID = ""
	if err := tasks.Settings.Save(settings); err != nil {
		t.Fatal(err)
	}
	runs.mu.Lock()
	runs.runs[first.ID].readyAt = time.Now().Add(-time.Second)
	runs.mu.Unlock()
	fifth := submit(5, "page1", input)
	seen := map[string]bool{}
	for _, v := range []ShareRunView{first, second, third, fourth, fifth} {
		if seen[v.ID] {
			t.Fatal("不同范围或过期批次被合并")
		}
		seen[v.ID] = true
	}
	input.Mode = "direct"
	direct := submit(6, "page1", input)
	if direct.ReadyAt.After(time.Now()) {
		t.Fatal("普通分享不应等待")
	}
	cancel()
	runShareWorker(t, runs, cancel)
}

func TestShareRunsDefaultAllowsMoreThanTwoConcurrentBatches(t *testing.T) {
	root, cancel := context.WithCancel(context.Background())
	defer cancel()
	var active, peak atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	tasks, _, input := setupShareTasks(t, generateFunc(func(ctx context.Context, _ GenerateInput) (*GenerationResult, error) {
		count := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); count > old; old = peak.Load() {
			if peak.CompareAndSwap(old, count) {
				break
			}
		}
		select {
		case started <- struct{}{}:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		select {
		case <-release:
			return &GenerationResult{Written: 1}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}))
	runs := NewShareRuns(root, tasks)
	var ids []string
	for i := 1; i <= 3; i++ {
		input.URL = fmt.Sprintf("https://115cdn.com/s/concurrent%d", i)
		view, err := runs.Submit(context.Background(), fmt.Sprintf("%032x", i), []GenerateInput{input})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, view.ID)
	}
	runShareWorker(t, runs, cancel)
	for i := 0; i < 3; i++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("后续批次仍等待上一批完成")
		}
	}
	close(release)
	for _, id := range ids {
		if view := awaitRun(t, runs, id); view.Status != "success" {
			t.Fatal(view)
		}
	}
	if peak.Load() != 3 {
		t.Fatalf("并行数=%d", peak.Load())
	}
}

func TestCollectingBatchDoesNotBlockReadyBatchAndCancellation(t *testing.T) {
	root, cancel := context.WithCancel(context.Background())
	defer cancel()
	tasks, _, input := setupShareTasks(t, &collectedGenerator{})
	settings := tasks.Settings.Value()
	settings.ReshareEnabled, settings.TGBatchWaitSeconds, settings.MaxParallelBatches = true, 300, 1
	if err := tasks.Settings.Save(settings); err != nil {
		t.Fatal(err)
	}
	runs := NewShareRuns(root, tasks)
	input.Mode = ""
	waiting, err := runs.Submit(WithShareCollectionScope(context.Background(), "collect"), strings.Repeat("a", 32), []GenerateInput{input})
	if err != nil {
		t.Fatal(err)
	}
	input.Mode, input.URL = "direct", "https://115cdn.com/s/direct"
	ready, err := runs.Submit(context.Background(), strings.Repeat("b", 32), []GenerateInput{input})
	if err != nil {
		t.Fatal(err)
	}
	runShareWorker(t, runs, cancel)
	if view := awaitRun(t, runs, ready.ID); view.Status != "success" {
		t.Fatal(view)
	}
	if view, _ := runs.Get(waiting.ID); view.Status != "queued" {
		t.Fatal("收集窗口未结束就执行")
	}
	if _, err := runs.Cancel(waiting.ID); err != nil {
		t.Fatal(err)
	}
	if view := awaitRun(t, runs, waiting.ID); view.Status != "cancelled" {
		t.Fatal(view)
	}
}

func TestShareRunConfiguredConcurrencyAndLateBatch(t *testing.T) {
	root, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	release := make(chan struct{})
	tasks, _, input := setupShareTasks(t, generateFunc(func(ctx context.Context, _ GenerateInput) (*GenerationResult, error) {
		select {
		case started <- struct{}{}:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		select {
		case <-release:
			return &GenerationResult{Written: 1}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}))
	settings := tasks.Settings.Value()
	settings.MaxParallelBatches, settings.ReshareEnabled, settings.TGBatchWaitSeconds = 1, true, 5
	if err := tasks.Settings.Save(settings); err != nil {
		t.Fatal(err)
	}
	input.Mode = ""
	runs := NewShareRuns(root, tasks)
	ctx := WithShareCollectionScope(context.Background(), "page")
	first, err := runs.Submit(ctx, strings.Repeat("a", 32), []GenerateInput{input})
	if err != nil {
		t.Fatal(err)
	}
	runs.mu.Lock()
	runs.runs[first.ID].readyAt = time.Now().Add(-time.Second)
	runs.mu.Unlock()
	runShareWorker(t, runs, cancel)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("第一批未启动")
	}
	input.URL = "https://115cdn.com/s/late"
	second, err := runs.Submit(ctx, strings.Repeat("b", 32), []GenerateInput{input})
	if err != nil || second.ID == first.ID {
		t.Fatal("后到任务追加到执行中的批次", err)
	}
	runs.mu.Lock()
	runs.runs[second.ID].readyAt = time.Now().Add(-time.Second)
	runs.mu.Unlock()
	select {
	case <-started:
		t.Fatal("超过配置的并行数")
	case <-time.After(150 * time.Millisecond):
	}
	settings.MaxParallelBatches = 0
	if err := tasks.Settings.Save(settings); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("放开并行后仍被第一批阻塞")
	}
	if _, err := runs.Cancel(first.ID); err != nil {
		t.Fatal(err)
	}
	if result := awaitRun(t, runs, first.ID); result.Status != "cancelled" {
		t.Fatal(result)
	}
	if view, _ := runs.Get(second.ID); view.Status != "running" {
		t.Fatal("取消第一批误伤第二批")
	}
	close(release)
	if result := awaitRun(t, runs, second.ID); result.Status != "success" {
		t.Fatal(result)
	}
}

func TestWebCollectionDeduplicatesOverlappingLinks(t *testing.T) {
	tasks, _, input := setupShareTasks(t, &collectedGenerator{})
	settings := tasks.Settings.Value()
	settings.ReshareEnabled, settings.TGBatchWaitSeconds = true, 5
	if err := tasks.Settings.Save(settings); err != nil {
		t.Fatal(err)
	}
	input.Mode = ""
	root, cancel := context.WithCancel(context.Background())
	defer cancel()
	runs := NewShareRuns(root, tasks)
	ctx := WithShareCollectionScope(context.Background(), "page")
	one, err := runs.Submit(ctx, strings.Repeat("a", 32), []GenerateInput{input})
	if err != nil {
		t.Fatal(err)
	}
	other := input
	other.URL = "https://115cdn.com/s/other"
	two, err := runs.Submit(ctx, strings.Repeat("b", 32), []GenerateInput{input, other})
	if err != nil || one.ID != two.ID || len(two.Items) != 2 || len(two.SelectedTaskIDs) != 2 {
		t.Fatalf("重叠链接未去重: %+v %v", two, err)
	}
	if two.SelectedTaskIDs[0] != one.SelectedTaskIDs[0] {
		t.Fatal("复用的来源对应关系错误")
	}
	cancel()
	runShareWorker(t, runs, cancel)
}
