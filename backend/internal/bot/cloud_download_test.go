package bot

import (
	"context"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/service"
)

type cloudSubmitterStub struct {
	config  service.CloudDownloadConfig
	url     string
	calls   int
	removed bool
}

type shareTransferSubmitterStub struct {
	target   service.ShareTransferTarget
	url      string
	password string
	calls    int
}

func (s *shareTransferSubmitterStub) Transfer(_ context.Context, target service.ShareTransferTarget, rawURL, password string) (service.ShareLinkTransferReceipt, error) {
	s.target, s.url, s.password = target, rawURL, password
	s.calls++
	return service.ShareLinkTransferReceipt{TargetPath: target.TargetPath, AcceptedItems: 1, ItemCount: 1, BatchCount: 1}, nil
}

func (s *cloudSubmitterStub) Submit(_ context.Context, config service.CloudDownloadConfig, rawURL string) (service.CloudDownloadReceipt, error) {
	s.config, s.url = config, rawURL
	s.calls++
	return service.CloudDownloadReceipt{AccountName: "家庭账号", TargetPath: "/下载", RemovedOldTask: s.removed}, nil
}

func cloudUpdate(id, chat int64, chatType, text string) telegramUpdate {
	m := &telegramMessage{ID: id + 100, Text: text}
	m.Chat.ID, m.Chat.Type = chat, chatType
	m.From.ID = 7
	return telegramUpdate{ID: id, Message: m}
}

func testCloudTarget(id, name, path string) TelegramTransferTarget {
	return TelegramTransferTarget{ID: id, Name: name, AccountID: "account-2", TargetCID: "88", TargetPath: path, AssignedBotID: "bot-a", Ready: true}
}

func testShareTransferTarget(id, name, path, botID string) TelegramTransferTarget {
	return TelegramTransferTarget{ID: id, Name: name, AccountID: "account-1", TargetCID: "88", TargetPath: path, AssignedBotID: botID, Ready: true}
}

func TestCloudDownloadCollectionRequiresPrivateConfiguredChat(t *testing.T) {
	b := newBatchTestBot(t, nil)
	b.CloudDownloadTargets = []TelegramTransferTarget{testCloudTarget("target-1", "转存 XX", "/下载")}
	b.CloudDownloads = &cloudSubmitterStub{}
	link := "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567"
	state := telegramState{}
	b.enqueueUpdates(context.Background(), &state, []telegramUpdate{cloudUpdate(1, 42, "private", "/cloud "+link)}, time.Now())
	if len(state.Queue) != 1 || state.Queue[0].Mode != "cloud" || len(state.Queue[0].Items) != 1 || state.Queue[0].Items[0].URL != link {
		t.Fatalf("私聊磁力没有进入独立云下载队列：%+v", state)
	}

	state = telegramState{}
	b.enqueueUpdates(context.Background(), &state, []telegramUpdate{cloudUpdate(2, 42, "supergroup", link)}, time.Now())
	if len(state.Queue) != 0 || len(state.Replies) != 1 || !strings.Contains(state.Replies[0].Text, "私聊") {
		t.Fatalf("群聊磁力没有被明确拒绝：%+v", state)
	}

	b.CloudDownloadTargets = nil
	state = telegramState{}
	b.enqueueUpdates(context.Background(), &state, []telegramUpdate{cloudUpdate(3, 42, "private", link)}, time.Now())
	if len(state.Queue) != 0 || len(state.Replies) != 1 || !strings.Contains(state.Replies[0].Text, "尚未分配") {
		t.Fatalf("未配置 Bot 没有提示管理员配置：%+v", state)
	}
}

func TestCloudOnlyReceiverDoesNotProcessShareLinks(t *testing.T) {
	b := newBatchTestBot(t, nil)
	b.ShareEnabled = false
	b.CloudDownloadTargets = []TelegramTransferTarget{testCloudTarget("target-1", "转存 XX", "/下载")}
	b.CloudDownloads = &cloudSubmitterStub{}
	state := telegramState{}
	b.enqueueUpdates(context.Background(), &state, []telegramUpdate{cloudUpdate(1, 42, "private", "https://115.com/s/sharecode")}, time.Now())
	if len(state.Queue) != 0 || len(state.Replies) != 0 {
		t.Fatalf("只绑定云下载的 Bot 错误处理了 STRM 分享：%+v", state)
	}
}

func TestCloudDownloadBatchUsesProfileSnapshotAndDoesNotEchoLink(t *testing.T) {
	b := newBatchTestBot(t, nil)
	stub := &cloudSubmitterStub{}
	b.CloudDownloads = stub
	target := testCloudTarget("target-1", "转存电影", "/电影")
	target.AccountID, target.TargetCID = "fixed-account", "99"
	b.CloudDownloadTargets = []TelegramTransferTarget{target}
	link := "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&dn=private-name"
	state := telegramState{Batch: &messageBatch{Mode: "cloud", UpdateID: 7, ChatID: 42, CloudTargetName: "转存电影", CloudTarget: service.CloudDownloadConfig{AccountID: "fixed-account", TargetCID: "99", TargetPath: "/电影"}, Items: []messageShare{{URL: link}}}}
	if err := b.processBatchLink(context.Background(), &state); err != nil {
		t.Fatal(err)
	}
	if stub.calls != 1 || stub.config.AccountID != "fixed-account" || stub.config.TargetCID != "99" || stub.url != link {
		t.Fatalf("提交未使用 Bot 功能配置快照：%+v", stub)
	}
	if strings.Contains(state.Pending.Text, link) || !strings.Contains(state.Pending.Text, "已提交到 115 云下载") {
		t.Fatalf("回复泄露链接或没有报告提交结果：%s", state.Pending.Text)
	}
}

func TestCloudDownloadCommandUsesSafeResubmitAndOneBatchReply(t *testing.T) {
	b := newBatchTestBot(t, nil)
	stub := &cloudSubmitterStub{removed: true}
	b.CloudDownloads = stub
	b.CloudDownloadTargets = []TelegramTransferTarget{testCloudTarget("target-1", "转存电影", "/电影")}
	first := "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567"
	second := "ed2k://|file|movie.mkv|123|0123456789abcdef0123456789abcdef|/"
	state := telegramState{}
	b.enqueueUpdates(context.Background(), &state, []telegramUpdate{cloudUpdate(7, 42, "private", "/cloud redo "+first+"\n"+second)}, time.Now())
	if len(state.Queue) != 1 || len(state.Queue[0].Items) != 2 {
		t.Fatalf("redo 命令没有应用到整条消息：%+v", state.Queue)
	}
	if !b.startReadyBatch(&state, time.Now()) || state.Pending != nil {
		t.Fatal("云下载批次错误发送了开始消息")
	}
	if err := b.processBatchLink(context.Background(), &state); err != nil {
		t.Fatal(err)
	}
	if stub.calls != 2 || state.Pending == nil || !strings.Contains(state.Pending.Text, "已重新提交：2 个") {
		t.Fatalf("没有生成单条 redo 汇总：stub=%+v pending=%+v", stub, state.Pending)
	}
}

func TestCloudDownloadLegacyBatchAlsoUsesDefaultResubmit(t *testing.T) {
	b := newBatchTestBot(t, nil)
	stub := &cloudSubmitterStub{removed: true}
	b.CloudDownloads = stub
	b.CloudDownloadTargets = []TelegramTransferTarget{testCloudTarget("target-1", "转存电影", "/电影")}
	state := telegramState{Batch: &messageBatch{Mode: "cloud", UpdateID: 7, ChatID: 42, Items: []messageShare{{URL: "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567"}}}}
	if err := b.processBatchLink(context.Background(), &state); err != nil {
		t.Fatal(err)
	}
	if stub.calls != 1 || !strings.Contains(state.Pending.Text, "已重新提交到 115 云下载") || !strings.Contains(state.Pending.Text, "网盘文件已保留") {
		t.Fatal("旧批次未使用统一入口")
	}
}

func TestShareLinkTransferUsesItsOwnQueuedTelegramModule(t *testing.T) {
	b := newBatchTestBot(t, nil)
	b.ShareEnabled = false
	transfer := &shareTransferSubmitterStub{}
	b.ShareTransfers = transfer
	b.ShareTransferTargets = []TelegramTransferTarget{testShareTransferTarget("target-1", "动画", "/动画", b.BotID)}
	link := "https://115.com/s/sharecode?password=abcd"
	state := telegramState{}
	b.enqueueUpdates(context.Background(), &state, []telegramUpdate{cloudUpdate(30, 42, "private", "提取码：abcd\n"+link)}, time.Now())
	if len(state.Queue) != 1 || state.Queue[0].Mode != "share_transfer" || len(state.Queue[0].Items) != 1 || state.Queue[0].ShareTarget.TargetPath != "/动画" {
		t.Fatalf("115 share link did not enter the dedicated transfer queue: %+v", state.Queue)
	}
	state.Batch = state.Queue[0]
	state.Queue = nil
	if err := b.processBatchLink(context.Background(), &state); err != nil {
		t.Fatal(err)
	}
	if transfer.calls != 1 || transfer.url != link || transfer.password != "abcd" || transfer.target.TargetPath != "/动画" || state.Pending == nil || !strings.Contains(state.Pending.Text, "/动画") || strings.Contains(state.Pending.Text, link) {
		t.Fatalf("share transfer did not preserve the extracted code or protect the link: transfer=%+v pending=%+v", transfer, state.Pending)
	}
}

func TestShareLinkTransferRejectsGroupMessages(t *testing.T) {
	b := newBatchTestBot(t, nil)
	b.ShareEnabled = false
	b.ShareTransferTargets = []TelegramTransferTarget{testShareTransferTarget("target-1", "动画", "/动画", b.BotID)}
	b.ShareTransfers = &shareTransferSubmitterStub{}
	state := telegramState{}
	b.enqueueUpdates(context.Background(), &state, []telegramUpdate{cloudUpdate(31, 42, "supergroup", "https://115.com/s/share-code")}, time.Now())
	if len(state.Queue) != 0 || len(state.Replies) != 1 || !strings.Contains(state.Replies[0].Text, "私聊") {
		t.Fatalf("share transfer should reject group messages: %+v", state)
	}
}
