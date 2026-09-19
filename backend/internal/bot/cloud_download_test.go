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
	redos   int
	removed bool
}

func (s *cloudSubmitterStub) Redo(_ context.Context, config service.CloudDownloadConfig, rawURL string) (service.CloudDownloadReceipt, error) {
	s.config, s.url = config, rawURL
	s.redos++
	return service.CloudDownloadReceipt{AccountName: "家庭账号", TargetPath: "/下载", RemovedOldTask: s.removed}, nil
}

func (s *cloudSubmitterStub) Submit(_ context.Context, config service.CloudDownloadConfig, rawURL string) (service.CloudDownloadReceipt, error) {
	s.config, s.url = config, rawURL
	s.calls++
	return service.CloudDownloadReceipt{AccountName: "家庭账号", TargetPath: "/下载", RemovedOldTask: s.removed}, nil
}

func cloudUpdate(id, chat int64, chatType, text string) telegramUpdate {
	m := &telegramMessage{ID: id + 100, Text: text}
	m.Chat.ID, m.Chat.Type = chat, chatType
	return telegramUpdate{ID: id, Message: m}
}

func TestCloudDownloadCollectionRequiresPrivateConfiguredChat(t *testing.T) {
	b := newBatchTestBot(t, nil)
	b.CloudDownloadConfig = BotCloudDownloadConfig{Enabled: true, AccountID: "account-2", TargetCID: "88", TargetPath: "/下载"}
	b.CloudDownloads = &cloudSubmitterStub{}
	link := "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567"
	state := telegramState{}
	b.enqueueUpdates(&state, []telegramUpdate{cloudUpdate(1, 42, "private", "/cloud "+link)}, time.Now())
	if len(state.Queue) != 1 || state.Queue[0].Mode != "cloud" || !state.Queue[0].CloudRedo || len(state.Queue[0].Items) != 1 || state.Queue[0].Items[0].URL != link {
		t.Fatalf("私聊磁力没有进入独立云下载队列：%+v", state)
	}

	state = telegramState{}
	b.enqueueUpdates(&state, []telegramUpdate{cloudUpdate(2, 42, "supergroup", link)}, time.Now())
	if len(state.Queue) != 0 || len(state.Replies) != 1 || !strings.Contains(state.Replies[0].Text, "只允许") {
		t.Fatalf("群聊磁力没有被明确拒绝：%+v", state)
	}

	b.CloudDownloadConfig.Enabled = false
	state = telegramState{}
	b.enqueueUpdates(&state, []telegramUpdate{cloudUpdate(3, 42, "private", link)}, time.Now())
	if len(state.Queue) != 0 || len(state.Replies) != 1 || !strings.Contains(state.Replies[0].Text, "尚未配置") {
		t.Fatalf("未配置 Bot 没有提示管理员配置：%+v", state)
	}
}

func TestCloudOnlyReceiverDoesNotProcessShareLinks(t *testing.T) {
	b := newBatchTestBot(t, nil)
	b.ShareEnabled = false
	b.CloudDownloadConfig = BotCloudDownloadConfig{Enabled: true, AccountID: "account-2", TargetCID: "88", TargetPath: "/下载"}
	b.CloudDownloads = &cloudSubmitterStub{}
	state := telegramState{}
	b.enqueueUpdates(&state, []telegramUpdate{cloudUpdate(1, 42, "private", "https://115.com/s/sharecode")}, time.Now())
	if len(state.Queue) != 0 || len(state.Replies) != 0 {
		t.Fatalf("只绑定云下载的 Bot 错误处理了 STRM 分享：%+v", state)
	}
}

func TestCloudDownloadBatchUsesProfileSnapshotAndDoesNotEchoLink(t *testing.T) {
	b := newBatchTestBot(t, nil)
	stub := &cloudSubmitterStub{}
	b.CloudDownloads = stub
	b.CloudDownloadConfig = BotCloudDownloadConfig{Enabled: true, AccountID: "fixed-account", TargetCID: "99", TargetPath: "/电影"}
	link := "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&dn=private-name"
	state := telegramState{Batch: &messageBatch{Mode: "cloud", UpdateID: 7, ChatID: 42, Items: []messageShare{{URL: link}}}}
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

func TestCloudDownloadRedoCommandAndOneBatchReply(t *testing.T) {
	b := newBatchTestBot(t, nil)
	stub := &cloudSubmitterStub{removed: true}
	b.CloudDownloads = stub
	b.CloudDownloadConfig = BotCloudDownloadConfig{Enabled: true, AccountID: "fixed-account", TargetCID: "99", TargetPath: "/电影"}
	first := "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567"
	second := "ed2k://|file|movie.mkv|123|0123456789abcdef0123456789abcdef|/"
	state := telegramState{}
	b.enqueueUpdates(&state, []telegramUpdate{cloudUpdate(7, 42, "private", "/cloud redo "+first+"\n"+second)}, time.Now())
	if len(state.Queue) != 1 || len(state.Queue[0].Items) != 2 || !state.Queue[0].CloudRedo {
		t.Fatalf("redo 命令没有应用到整条消息：%+v", state.Queue)
	}
	if !b.startReadyBatch(&state, time.Now()) || state.Pending != nil {
		t.Fatal("云下载批次错误发送了开始消息")
	}
	if err := b.processBatchLink(context.Background(), &state); err != nil {
		t.Fatal(err)
	}
	if stub.redos != 0 || stub.calls != 2 || state.Pending == nil || !strings.Contains(state.Pending.Text, "已重新提交：2 个") {
		t.Fatalf("没有生成单条 redo 汇总：stub=%+v pending=%+v", stub, state.Pending)
	}
}

func TestCloudDownloadLegacyBatchAlsoUsesDefaultResubmit(t *testing.T) {
	b := newBatchTestBot(t, nil)
	stub := &cloudSubmitterStub{removed: true}
	b.CloudDownloads = stub
	b.CloudDownloadConfig = BotCloudDownloadConfig{Enabled: true, AccountID: "fixed-account", TargetCID: "99", TargetPath: "/电影"}
	state := telegramState{Batch: &messageBatch{Mode: "cloud", UpdateID: 7, ChatID: 42, Items: []messageShare{{URL: "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567"}}}}
	if err := b.processBatchLink(context.Background(), &state); err != nil {
		t.Fatal(err)
	}
	if stub.calls != 1 || stub.redos != 0 || !strings.Contains(state.Pending.Text, "已重新提交到 115 云下载") || !strings.Contains(state.Pending.Text, "网盘文件已保留") {
		t.Fatal("旧批次未使用统一入口")
	}
}
