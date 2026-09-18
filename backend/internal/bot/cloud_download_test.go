package bot

import (
	"context"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/service"
)

type cloudSubmitterStub struct {
	config service.CloudDownloadConfig
	url    string
	calls  int
}

func (s *cloudSubmitterStub) Submit(_ context.Context, config service.CloudDownloadConfig, rawURL string) (service.CloudDownloadReceipt, error) {
	s.config, s.url = config, rawURL
	s.calls++
	return service.CloudDownloadReceipt{AccountName: "家庭账号", TargetPath: "/下载"}, nil
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
	if len(state.Queue) != 1 || state.Queue[0].Mode != "cloud" || len(state.Queue[0].Items) != 1 || state.Queue[0].Items[0].URL != link {
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
	if strings.Contains(state.Pending.Text, link) || !strings.Contains(state.Pending.Text, "115 已接受任务") {
		t.Fatalf("回复泄露链接或没有区分提交与完成：%s", state.Pending.Text)
	}
}
