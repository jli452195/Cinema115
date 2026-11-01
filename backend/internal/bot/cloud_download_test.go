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
	m.From.ID = 7
	return telegramUpdate{ID: id, Message: m}
}

func testCloudTarget(id, name, path string) TelegramCloudTarget {
	return TelegramCloudTarget{ID: id, Name: name, AccountID: "account-2", TargetCID: "88", TargetPath: path, Ready: true}
}

func TestCloudDownloadCollectionRequiresPrivateConfiguredChat(t *testing.T) {
	b := newBatchTestBot(t, nil)
	b.CloudDownloadTargets = []TelegramCloudTarget{testCloudTarget("target-1", "转存 XX", "/下载")}
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

	b.CloudDownloadTargets = nil
	state = telegramState{}
	b.enqueueUpdates(&state, []telegramUpdate{cloudUpdate(3, 42, "private", link)}, time.Now())
	if len(state.Queue) != 0 || len(state.Replies) != 1 || !strings.Contains(state.Replies[0].Text, "尚未分配") {
		t.Fatalf("未配置 Bot 没有提示管理员配置：%+v", state)
	}
}

func TestCloudOnlyReceiverDoesNotProcessShareLinks(t *testing.T) {
	b := newBatchTestBot(t, nil)
	b.ShareEnabled = false
	b.CloudDownloadTargets = []TelegramCloudTarget{testCloudTarget("target-1", "转存 XX", "/下载")}
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
	target := testCloudTarget("target-1", "转存电影", "/电影")
	target.AccountID, target.TargetCID = "fixed-account", "99"
	b.CloudDownloadTargets = []TelegramCloudTarget{target}
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

func TestCloudDownloadRedoCommandAndOneBatchReply(t *testing.T) {
	b := newBatchTestBot(t, nil)
	stub := &cloudSubmitterStub{removed: true}
	b.CloudDownloads = stub
	b.CloudDownloadTargets = []TelegramCloudTarget{testCloudTarget("target-1", "转存电影", "/电影")}
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

func TestCloudDownloadMultipleTargetsSelectOnceForWholeMessage(t *testing.T) {
	b := newBatchTestBot(t, nil)
	stub := &cloudSubmitterStub{}
	b.CloudDownloads = stub
	b.CloudDownloadTargets = []TelegramCloudTarget{
		testCloudTarget("target-1", "转存 XX", "/XX"),
		testCloudTarget("target-2", "转存 XC", "/XC"),
	}
	first := "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567"
	second := "ed2k://|file|movie.mkv|123|0123456789abcdef0123456789abcdef|/"
	state := telegramState{}
	b.enqueueUpdates(&state, []telegramUpdate{cloudUpdate(11, 42, "private", first+"\n"+second)}, time.Now())
	if len(state.Queue) != 0 || len(state.CloudSelections) != 1 || len(state.Replies) != 1 {
		t.Fatalf("multiple targets should wait for a Telegram selection: %+v", state)
	}
	selection := state.CloudSelections[0]
	selection.PromptMessageID = 500
	if len(selection.Items) != 2 || state.Replies[0].ReplyMarkup == nil {
		t.Fatalf("selection prompt did not retain the message batch and inline buttons: %+v", state.Replies[0])
	}
	callback := &telegramCallbackQuery{ID: "callback-1", Data: cloudTargetCallbackData(11, "target-2")}
	callback.From.ID = 7
	callback.Message = &telegramCallbackMessage{ID: 500}
	callback.Message.Chat.ID, callback.Message.Chat.Type = 42, "private"
	b.enqueueUpdates(&state, []telegramUpdate{{ID: 12, CallbackQuery: callback}}, time.Now())
	if len(state.Queue) != 1 || state.Queue[0].CloudTargetID != "target-2" || state.Queue[0].CloudTargetName != "转存 XC" || len(state.Queue[0].Items) != 2 {
		t.Fatalf("selection did not create one batch for all links and chosen target: %+v", state.Queue)
	}
	if len(state.CloudSelections) != 0 || len(state.CallbackAnswers) != 1 || !strings.Contains(state.CallbackAnswers[0].Text, "转存 XC") {
		t.Fatalf("selection was not consumed or acknowledged: %+v", state)
	}
	state.Batch = state.Queue[0]
	state.Queue = nil
	if err := b.processBatchLink(context.Background(), &state); err != nil {
		t.Fatal(err)
	}
	if stub.calls != 2 || stub.config.TargetPath != "/XC" || !strings.Contains(state.Pending.Text, "转存 XC") {
		t.Fatalf("chosen destination was not fixed for the whole batch: calls=%d config=%+v reply=%s", stub.calls, stub.config, state.Pending.Text)
	}
}

func TestCloudDownloadSelectionIsBoundToPromptSenderAndCurrentTarget(t *testing.T) {
	b := newBatchTestBot(t, nil)
	b.CloudDownloads = &cloudSubmitterStub{}
	b.CloudDownloadTargets = []TelegramCloudTarget{testCloudTarget("target-1", "转存 XX", "/XX"), testCloudTarget("target-2", "转存 XC", "/XC")}
	state := telegramState{}
	link := "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567"
	b.enqueueUpdates(&state, []telegramUpdate{cloudUpdate(21, 42, "private", link)}, time.Now())
	state.CloudSelections[0].PromptMessageID = 600
	callback := func(updateID int64, userID int64, promptID int64) telegramUpdate {
		query := &telegramCallbackQuery{ID: "callback", Data: cloudTargetCallbackData(21, "target-1")}
		query.From.ID = userID
		query.Message = &telegramCallbackMessage{ID: promptID}
		query.Message.Chat.ID, query.Message.Chat.Type = 42, "private"
		return telegramUpdate{ID: updateID, CallbackQuery: query}
	}
	b.enqueueUpdates(&state, []telegramUpdate{callback(22, 8, 600)}, time.Now())
	if len(state.Queue) != 0 || len(state.CloudSelections) != 1 {
		t.Fatalf("another Telegram user consumed the selection: %+v", state)
	}
	b.CloudDownloadTargets[0].TargetPath = "/changed"
	b.enqueueUpdates(&state, []telegramUpdate{callback(23, 7, 600)}, time.Now())
	if len(state.Queue) != 0 || len(state.CloudSelections) != 0 || !strings.Contains(state.CallbackAnswers[len(state.CallbackAnswers)-1].Text, "已变化") {
		t.Fatalf("stale target configuration was used: %+v", state)
	}
}

func TestCloudDownloadLegacyBatchAlsoUsesDefaultResubmit(t *testing.T) {
	b := newBatchTestBot(t, nil)
	stub := &cloudSubmitterStub{removed: true}
	b.CloudDownloads = stub
	b.CloudDownloadTargets = []TelegramCloudTarget{testCloudTarget("target-1", "转存电影", "/电影")}
	state := telegramState{Batch: &messageBatch{Mode: "cloud", UpdateID: 7, ChatID: 42, Items: []messageShare{{URL: "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567"}}}}
	if err := b.processBatchLink(context.Background(), &state); err != nil {
		t.Fatal(err)
	}
	if stub.calls != 1 || stub.redos != 0 || !strings.Contains(state.Pending.Text, "已重新提交到 115 云下载") || !strings.Contains(state.Pending.Text, "网盘文件已保留") {
		t.Fatal("旧批次未使用统一入口")
	}
}
