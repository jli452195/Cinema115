package bot

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/service"
)

type preparedCloudSubmitter struct {
	cloudSubmitterStub
	prepares int
	version  int
	used     []int
	after    func()
}

func (s *preparedCloudSubmitter) PrepareSubmission(config service.CloudDownloadConfig) (service.CloudDownloadSubmission, error) {
	s.prepares++
	version := s.version
	return func(_ context.Context, _ string) (service.CloudDownloadReceipt, error) {
		s.used = append(s.used, version)
		if s.after != nil {
			s.after()
		}
		return service.CloudDownloadReceipt{TargetPath: config.TargetPath}, nil
	}, nil
}

type preparedShareTransferer struct {
	shareTransferSubmitterStub
	prepares int
	version  int
	used     []int
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
		Items:       []messageShare{{URL: "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567"}, {URL: "ed2k://|file|movie.mkv|123|0123456789abcdef0123456789abcdef|/"}}}}
	if err := b.processBatchLink(context.Background(), &state); err != nil {
		t.Fatal(err)
	}
	if submit.prepares != 1 || submit.calls != 0 || !reflect.DeepEqual(submit.used, []int{1, 1}) {
		t.Fatalf("credentials changed within one Telegram batch: prepares=%d fallback=%d versions=%v", submit.prepares, submit.calls, submit.used)
	}
}

func TestShareTransferBatchPinsExecutionBetweenReplies(t *testing.T) {
	b := newBatchTestBot(t, nil)
	transfer := &preparedShareTransferer{version: 1}
	b.ShareTransfers = transfer
	state := telegramState{Batch: &messageBatch{Mode: "share_transfer", UpdateID: 1, ChatID: 42,
		ShareTarget: service.ShareTransferTarget{ID: "target", AccountID: "primary", TargetCID: "88", TargetPath: "/电影"},
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
	state := telegramState{Batch: &messageBatch{Mode: "share_transfer", UpdateID: 1, ChatID: 42,
		Items: []messageShare{{URL: "https://115.com/s/first"}, {URL: "https://115.com/s/second"}}}}
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
		Items: []messageShare{{URL: "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567"}, {URL: "ed2k://|file|movie.mkv|123|0123456789abcdef0123456789abcdef|/"}}}}
	if err := b.processBatchLink(ctx, &state); !errors.Is(err, context.Canceled) {
		t.Fatalf("want canceled context, got %v", err)
	}
	if state.Batch.Next != 1 || len(submit.used) != 1 {
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
	if restored.Batch.Next != 2 || len(submit.used) != 1 || !strings.Contains(restored.Pending.Text, "快照已丢失") {
		t.Fatalf("resumed cloud batch reread credentials: next=%d versions=%v pending=%+v", restored.Batch.Next, submit.used, restored.Pending)
	}
}

func TestCloudDownloadUsesOnlyReadyDirectoryWithoutPrompt(t *testing.T) {
	b := newBatchTestBot(t, nil)
	b.CloudDownloads = &cloudSubmitterStub{}
	ready := testCloudTarget("ready", "电影", "/电影")
	unavailable := testCloudTarget("unavailable", "旧目录", "/旧目录")
	unavailable.Ready = false
	b.CloudDownloadTargets = []TelegramTransferTarget{unavailable, ready}
	state := telegramState{}
	b.enqueueUpdates(&state, []telegramUpdate{cloudUpdate(1, 42, "private", "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567")}, time.Now())
	if len(state.Queue) != 1 || state.Queue[0].CloudTargetID != ready.ID || len(state.CloudSelections) != 0 || len(state.Replies) != 0 {
		t.Fatalf("only ready destination should be selected automatically: %+v", state)
	}
}

func TestCloudDownloadQueuedBatchStopsWhenDestinationChanges(t *testing.T) {
	b := newBatchTestBot(t, nil)
	submit := &cloudSubmitterStub{}
	b.CloudDownloads = submit
	b.CloudDownloadTargets = []TelegramTransferTarget{testCloudTarget("movies", "电影", "/电影")}
	state := telegramState{}
	b.enqueueUpdates(&state, []telegramUpdate{cloudUpdate(1, 42, "private", "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567")}, time.Now())
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
