package bot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/service"
)

type preparedCloudSubmitter struct {
	prepares int
	version  int
	used     []int
	sizes    []int
	after    func()
}

func (s *preparedCloudSubmitter) PrepareBatch(_ service.CloudDownloadConfig, urls []string) (service.CloudDownloadBatchExecution, error) {
	s.prepares++
	version := s.version
	return cloudBatchFunc(func(_ context.Context, start int) ([]service.CloudDownloadBatchEvent, bool) {
		end := min(start+service.CloudDownloadBatchSize, len(urls))
		s.used = append(s.used, version)
		s.sizes = append(s.sizes, end-start)
		if s.after != nil {
			s.after()
		}
		return acceptedCloudChunk(start, end), false
	}), nil
}

func cloudItemsForTwoChunks() []messageShare {
	items := make([]messageShare, service.CloudDownloadBatchSize+1)
	for i := range items {
		items[i].URL = fmt.Sprintf("magnet:?xt=urn:btih:%040x", i+1)
	}
	return items
}

type preparedShareTransferer struct {
	shareTransferSubmitterStub
	prepares int
	version  int
	used     []int
}

type telegramGenerationTestCredentialKey struct{}

type preparedShareGenerator struct {
	prepares int
	version  int
	used     []int
	inputs   []service.GenerateInput
}

func (g *preparedShareGenerator) PrepareTelegramGeneration(service.AutomationSettings) (func(context.Context) context.Context, error) {
	g.prepares++
	version := g.version
	return func(ctx context.Context) context.Context {
		return context.WithValue(ctx, telegramGenerationTestCredentialKey{}, version)
	}, nil
}

func (g *preparedShareGenerator) Generate(ctx context.Context, input service.GenerateInput) (*service.GenerationResult, error) {
	version, _ := ctx.Value(telegramGenerationTestCredentialKey{}).(int)
	g.used = append(g.used, version)
	g.inputs = append(g.inputs, input)
	return &service.GenerationResult{Written: 1}, nil
}

func (s *preparedShareTransferer) PrepareTransfer(target service.ShareTransferTarget) (service.ShareLinkTransferExecution, error) {
	s.prepares++
	version := s.version
	return func(_ context.Context, _, _ string) (service.ShareLinkTransferReceipt, error) {
		s.used = append(s.used, version)
		return service.ShareLinkTransferReceipt{TargetPath: target.TargetPath, AcceptedItems: 1, ItemCount: 1, BatchCount: 1}, nil
	}, nil
}

func TestCloudBatchPinsOneExecutionForAllLinks(t *testing.T) {
	b := newBatchTestBot(t, nil)
	submit := &preparedCloudSubmitter{version: 1}
	submit.after = func() { submit.version = 2 }
	b.CloudDownloads = submit
	state := telegramState{Batch: &messageBatch{Mode: "cloud", UpdateID: 1, ChatID: 42,
		CloudTarget: service.CloudDownloadConfig{AccountID: "primary", TargetCID: "88", TargetPath: "/下载"},
		Items:       cloudItemsForTwoChunks()}}
	for range 2 {
		if err := b.processBatchLink(context.Background(), &state); err != nil {
			t.Fatal(err)
		}
	}
	if submit.prepares != 1 || !reflect.DeepEqual(submit.used, []int{1, 1}) || !reflect.DeepEqual(submit.sizes, []int{service.CloudDownloadBatchSize, 1}) {
		t.Fatalf("云下载分组或凭据快照错误：%+v", submit)
	}
}

func TestShareSTRMBatchPinsCredentialsAndSettingsBetweenReplies(t *testing.T) {
	generator := &preparedShareGenerator{version: 1}
	b := newBatchTestBot(t, generator)
	state := telegramState{}
	b.enqueueUpdates(context.Background(), &state, []telegramUpdate{cloudUpdate(1, 42, "private", "https://115.com/s/first 提取码：aaaa\nhttps://115.com/s/second 提取码：bbbb")}, time.Now())
	if len(state.Queue) != 1 || state.Queue[0].ShareSettings == nil {
		t.Fatalf("分享 STRM 没有固定批次设置：%+v", state)
	}
	state.Batch, state.Queue = state.Queue[0], nil
	original := state.Batch.ShareSettings.Generation.OutputDir
	if err := b.processBatchLink(context.Background(), &state); err != nil {
		t.Fatal(err)
	}
	generator.version = 2
	changeShareTestSettings(t, b, func(settings *service.AutomationSettings) {
		settings.Generation.OutputDir = "changed-output"
	})
	state.Pending = nil
	if err := b.processBatchLink(context.Background(), &state); err != nil {
		t.Fatal(err)
	}
	if generator.prepares != 1 || !reflect.DeepEqual(generator.used, []int{1, 1}) || len(generator.inputs) != 2 || generator.inputs[0].OutputDir != original || generator.inputs[1].OutputDir != original || generator.inputs[0].Password != "aaaa" || generator.inputs[1].Password != "bbbb" {
		t.Fatalf("已启动分享批次在两次回复间重新加载凭据或设置：%+v", generator)
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	var restored telegramState
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.Batch.generationContext != nil {
		t.Fatal("凭据装饰器进入了持久任务")
	}
}

func TestShareTransferBatchPinsExecutionBetweenReplies(t *testing.T) {
	b := newBatchTestBot(t, nil)
	transfer := &preparedShareTransferer{version: 1}
	b.ShareTransfers = transfer
	b.ShareTransferTargets = []TelegramTransferTarget{testShareTransferTarget("target", "电影", "/电影", b.BotID)}
	state := telegramState{Batch: &messageBatch{Mode: "share_transfer", UpdateID: 1, ChatID: 42,
		ShareTarget: service.ShareTransferTarget{ID: "target", Name: "电影", AccountID: "account-1", TargetCID: "88", TargetPath: "/电影", AssignedBotID: b.BotID},
		Items:       []messageShare{{URL: "https://115.com/s/first"}, {URL: "https://115.com/s/second"}}}}
	if err := b.processBatchLink(context.Background(), &state); err != nil {
		t.Fatal(err)
	}
	transfer.version = 2
	state.Pending = nil
	if err := b.processBatchLink(context.Background(), &state); err != nil {
		t.Fatal(err)
	}
	if transfer.prepares != 1 || transfer.calls != 0 || !reflect.DeepEqual(transfer.used, []int{1, 1}) {
		t.Fatalf("sharing reloaded credentials between item replies: prepares=%d fallback=%d versions=%v", transfer.prepares, transfer.calls, transfer.used)
	}
}

func TestShareTransferRestartStopsUnstartedItems(t *testing.T) {
	b := newBatchTestBot(t, nil)
	transfer := &preparedShareTransferer{version: 1}
	b.ShareTransfers = transfer
	b.ShareTransferTargets = []TelegramTransferTarget{testShareTransferTarget("target", "电影", "/电影", b.BotID)}
	state := telegramState{Batch: &messageBatch{Mode: "share_transfer", UpdateID: 1, ChatID: 42,
		ShareTarget: service.ShareTransferTarget{ID: "target", Name: "电影", AccountID: "account-1", TargetCID: "88", TargetPath: "/电影", AssignedBotID: b.BotID},
		Items:       []messageShare{{URL: "https://115.com/s/first"}, {URL: "https://115.com/s/second"}}}}
	if err := b.processBatchLink(context.Background(), &state); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	var restored telegramState
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.Batch.shareExecution != nil {
		t.Fatal("runtime execution must not be serialized")
	}
	transfer.version = 2
	restored.Pending = nil
	if err := b.processBatchLink(context.Background(), &restored); err != nil {
		t.Fatal(err)
	}
	if len(transfer.used) != 1 || transfer.prepares != 1 || restored.Batch.Next != 2 || !strings.Contains(restored.Pending.Text, "1 个尚未开始") || !strings.Contains(restored.Pending.Text, "快照已丢失") {
		t.Fatalf("resumed transfer continued using new credentials: versions=%v pending=%+v", transfer.used, restored.Pending)
	}
}

func TestCloudBatchCancellationKeepsRemainingItemsUnstarted(t *testing.T) {
	b := newBatchTestBot(t, nil)
	submit := &preparedCloudSubmitter{version: 1}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	submit.after = cancel
	b.CloudDownloads = submit
	state := telegramState{Batch: &messageBatch{Mode: "cloud", UpdateID: 1, ChatID: 42,
		Items: cloudItemsForTwoChunks()}}
	if err := b.processBatchLink(ctx, &state); !errors.Is(err, context.Canceled) {
		t.Fatalf("want canceled context, got %v", err)
	}
	if state.Batch.Next != service.CloudDownloadBatchSize || len(submit.used) != 1 {
		t.Fatalf("shutdown consumed unstarted links: next=%d versions=%v", state.Batch.Next, submit.used)
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	var restored telegramState
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	restored.Pending = nil
	submit.version = 2
	if err := b.processBatchLink(context.Background(), &restored); err != nil {
		t.Fatal(err)
	}
	if restored.Batch.Next != len(state.Batch.Items) || len(submit.used) != 1 || !strings.Contains(restored.Pending.Text, "快照已失效") ||
		!strings.Contains(restored.Pending.Text, "已提交 100 条") || !strings.Contains(restored.Pending.Text, "尚未开始 1 条") {
		t.Fatalf("resumed cloud batch reread credentials: next=%d versions=%v pending=%+v", restored.Batch.Next, submit.used, restored.Pending)
	}
}

func TestCloudDownloadUnavailableDirectoryDoesNotChooseAnotherDirectory(t *testing.T) {
	b := newBatchTestBot(t, nil)
	b.CloudDownloads = &cloudSubmitterStub{}
	ready := testCloudTarget("ready", "电影", "/电影")
	unavailable := testCloudTarget("unavailable", "旧目录", "/旧目录")
	unavailable.Ready = false
	b.CloudDownloadTargets = []TelegramTransferTarget{unavailable, ready}
	state := telegramState{}
	b.enqueueUpdates(context.Background(), &state, []telegramUpdate{cloudUpdate(1, 42, "private", "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567")}, time.Now())
	if len(state.Queue) != 0 || len(state.ResourceSelections) != 1 || len(state.ResourceSelections[0].Choices) != 2 || len(state.Replies) != 1 {
		t.Fatalf("失效目录被自动替换为另一个可用目录：%+v", state)
	}
	state.ResourceSelections[0].PromptMessageID = 500
	b.enqueueUpdates(context.Background(), &state, []telegramUpdate{{ID: 2, CallbackQuery: resourceTestCallback(1, 7, 500, "1")}}, time.Now())
	if len(state.Queue) != 1 || state.Queue[0].CloudTargetID != ready.ID || len(state.ResourceSelections) != 0 {
		t.Fatalf("未按显式选择固定可用目录：%+v", state)
	}
}

func TestCloudDownloadQueuedBatchStopsWhenDestinationChanges(t *testing.T) {
	b := newBatchTestBot(t, nil)
	submit := &cloudSubmitterStub{}
	b.CloudDownloads = submit
	b.CloudDownloadTargets = []TelegramTransferTarget{testCloudTarget("movies", "电影", "/电影")}
	state := telegramState{}
	b.enqueueUpdates(context.Background(), &state, []telegramUpdate{cloudUpdate(1, 42, "private", "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567")}, time.Now())
	b.CloudDownloadTargets[0].TargetPath = "/新电影"
	if !b.startReadyBatch(&state, time.Now()) {
		t.Fatal("queued cloud message did not become ready")
	}
	if err := b.processBatchLink(context.Background(), &state); err != nil {
		t.Fatal(err)
	}
	if submit.calls != 0 || state.Batch.Next != 1 || !strings.Contains(state.Pending.Text, "转存目标已变化") {
		t.Fatalf("unstarted batch used an outdated destination: calls=%d pending=%+v", submit.calls, state.Pending)
	}
}
