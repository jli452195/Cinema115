package bot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf16"

	"strm115/backend/internal/model"
	"strm115/backend/internal/service"
)

const resourceMagnetLink = "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567"

type resourceReadyImporter struct {
	check   func(context.Context) error
	checks  int
	imports int
}

func (f *resourceReadyImporter) CheckImportReady(ctx context.Context) error {
	f.checks++
	if f.check != nil {
		return f.check(ctx)
	}
	return nil
}

func (f *resourceReadyImporter) ImportToTarget(context.Context, model.ED2KSTRMTarget, []string, string) (model.ED2KSTRMImportResult, error) {
	f.imports++
	return model.ED2KSTRMImportResult{Tasks: []model.ED2KSTRMTask{{}}}, nil
}

type resourceRoundTripperFunc func(*http.Request) (*http.Response, error)

func (f resourceRoundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func resourceCloudTarget(b *Telegram, id string) TelegramTransferTarget {
	target := testCloudTarget(id, "云下载-"+id, "/云下载/"+id)
	target.AssignedBotID = b.BotID
	return target
}

func newResourceSelectionTestBot(t *testing.T) (*Telegram, *cloudSubmitterStub) {
	t.Helper()
	b := newED2KSTRMTestBot(t)
	cloud := &cloudSubmitterStub{}
	b.CloudDownloads = cloud
	b.CloudDownloadTargets = []TelegramTransferTarget{resourceCloudTarget(b, "cloud-a")}
	return b, cloud
}

func resourceTestCallback(updateID, userID, promptID int64, choice string) *telegramCallbackQuery {
	callback := &telegramCallbackQuery{ID: "resource-choice", Data: resourceCallbackData(updateID, choice), Message: &telegramCallbackMessage{ID: promptID}}
	callback.From.ID = userID
	callback.Message.Chat.ID, callback.Message.Chat.Type = 42, "private"
	return callback
}

func resourceTestItems(links ...string) []messageShare {
	items := make([]messageShare, 0, len(links))
	for _, link := range links {
		items = append(items, messageShare{URL: link})
	}
	return items
}

type shareResourceGenerator struct {
	inputs []service.GenerateInput
}

func (g *shareResourceGenerator) Generate(_ context.Context, input service.GenerateInput) (*service.GenerationResult, error) {
	g.inputs = append(g.inputs, input)
	return &service.GenerationResult{Written: 1}, nil
}

type shareResourceTransfer struct {
	items   []messageShare
	targets []service.ShareTransferTarget
}

func (f *shareResourceTransfer) Transfer(_ context.Context, target service.ShareTransferTarget, link, password string) (service.ShareLinkTransferReceipt, error) {
	f.items = append(f.items, messageShare{URL: link, Password: password})
	f.targets = append(f.targets, target)
	return service.ShareLinkTransferReceipt{TargetPath: target.TargetPath, AcceptedItems: 1, ItemCount: 1, BatchCount: 1}, nil
}

func newShareResourceTestBot(t *testing.T) (*Telegram, *shareResourceGenerator, *shareResourceTransfer) {
	t.Helper()
	generator := &shareResourceGenerator{}
	b := newBatchTestBot(t, generator)
	transfer := &shareResourceTransfer{}
	b.ShareTransfers = transfer
	b.ShareTransferTargets = []TelegramTransferTarget{testShareTransferTarget("share-movies", "电影", "/电影", b.BotID)}
	return b, generator, transfer
}

func changeShareTestSettings(t *testing.T, b *Telegram, change func(*service.AutomationSettings)) {
	t.Helper()
	settings := b.Settings.Value()
	change(&settings)
	if err := b.Settings.Save(settings); err != nil {
		t.Fatal(err)
	}
}

func enqueueShareResourceSelection(t *testing.T, b *Telegram, now time.Time, text string) telegramState {
	t.Helper()
	state := telegramState{}
	update := cloudUpdate(10, 42, "private", text)
	update.Message.ThreadID = 88
	b.enqueueUpdates(context.Background(), &state, []telegramUpdate{update}, now)
	if len(state.Queue) != 0 || len(state.ResourceSelections) != 1 || len(state.Replies) != 1 || !state.Replies[0].CaptureMessageID || state.Replies[0].UpdateID != 10 {
		t.Fatalf("分享多用途未创建唯一的资源选择：%+v", state)
	}
	state.ResourceSelections[0].PromptMessageID = 500
	return state
}

func enqueueResourceSelection(t *testing.T, b *Telegram, now time.Time) telegramState {
	t.Helper()
	state := telegramState{}
	update := cloudUpdate(10, 42, "private", botED2KLink)
	update.Message.ThreadID = 88
	b.enqueueUpdates(context.Background(), &state, []telegramUpdate{update}, now)
	if len(state.ResourceSelections) != 1 || len(state.Queue) != 0 || len(state.Replies) != 1 {
		t.Fatalf("裸 ED2K 未进入统一入口选择：%+v", state)
	}
	state.ResourceSelections[0].PromptMessageID = 500
	return state
}

func resourceKeyboard(t *testing.T, markup any) [][]map[string]string {
	t.Helper()
	data, err := json.Marshal(markup)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Rows [][]map[string]string `json:"inline_keyboard"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded.Rows
}

func requireResourceKeyboardCleared(t *testing.T, state telegramState, promptID int64) {
	t.Helper()
	for _, reply := range state.Replies {
		if reply.EditMessageID == promptID && reply.ReplyMarkup != nil && len(resourceKeyboard(t, reply.ReplyMarkup)) == 0 {
			return
		}
	}
	t.Fatalf("没有清除原选择消息的按钮：%+v", state.Replies)
}

func TestResourceED2KBareRouteTable(t *testing.T) {
	for _, tc := range []struct {
		name       string
		strmCount  int
		cloudCount int
		wantMode   string
	}{
		{name: "没有入口"},
		{name: "单个STRM目录", strmCount: 1, wantMode: "ed2k_strm"},
		{name: "多个STRM目录", strmCount: 2},
		{name: "单个云下载目录", cloudCount: 1, wantMode: "cloud"},
		{name: "多个云下载目录", cloudCount: 2},
		{name: "双模块单目录", strmCount: 1, cloudCount: 1},
		{name: "双模块多目录", strmCount: 2, cloudCount: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, cloud := newResourceSelectionTestBot(t)
			b.ED2KSTRMTargets, b.CloudDownloadTargets = nil, nil
			for i := 0; i < tc.strmCount; i++ {
				id := fmt.Sprintf("strm-%d", i)
				b.ED2KSTRMTargets = append(b.ED2KSTRMTargets, botED2KTarget(id, "STRM-"+id, "ed2k/"+id, b.BotID))
			}
			for i := 0; i < tc.cloudCount; i++ {
				b.CloudDownloadTargets = append(b.CloudDownloadTargets, resourceCloudTarget(b, fmt.Sprintf("cloud-%d", i)))
			}
			imports, requests := 0, 0
			b.ED2KSTRM = ed2kSTRMImporterFunc(func(context.Context, model.ED2KSTRMTarget, []string, string) (model.ED2KSTRMImportResult, error) {
				imports++
				return model.ED2KSTRMImportResult{}, nil
			})
			b.HTTP = &http.Client{Transport: resourceRoundTripperFunc(func(*http.Request) (*http.Response, error) {
				requests++
				return nil, errors.New("收集阶段禁止网络提交")
			})}
			state := telegramState{}
			now := time.Now()
			b.enqueueUpdates(context.Background(), &state, []telegramUpdate{cloudUpdate(10, 42, "private", botED2KLink)}, now)
			if imports != 0 || cloud.calls != 0 || requests != 0 || state.Offset != 11 {
				t.Fatalf("读取裸链接时发生提前提交或丢失游标：imports=%d cloud=%d requests=%d state=%+v", imports, cloud.calls, requests, state)
			}
			if tc.wantMode != "" {
				if len(state.Queue) != 1 || state.Queue[0].Mode != tc.wantMode || len(state.ResourceSelections) != 0 || len(state.Replies) != 0 || len(state.Queue[0].Items) != 1 || state.Queue[0].Items[0].URL != botED2KLink {
					t.Fatalf("单入口没有直接固定对应批次：%+v", state)
				}
				return
			}
			if len(state.Queue) != 0 || len(state.Replies) != 1 {
				t.Fatalf("多入口或未配置状态没有提示：%+v", state)
			}
			if tc.strmCount+tc.cloudCount == 0 {
				if len(state.ResourceSelections) != 0 {
					t.Fatal("没有候选仍创建选择")
				}
				return
			}
			if len(state.ResourceSelections) != 1 || len(state.ResourceSelections[0].Choices) != tc.strmCount+tc.cloudCount {
				t.Fatalf("多入口没有合并为一张选择：%+v", state)
			}
			selection := state.ResourceSelections[0]
			if selection.UpdateID != 10 || selection.ChatID != 42 || selection.UserID != 7 || selection.MessageID != 110 || !reflect.DeepEqual(selection.Items, resourceTestItems(botED2KLink)) || !selection.Expires.Equal(now.Add(10*time.Minute)) {
				t.Fatalf("选择没有固定原消息和十分钟期限：%+v", selection)
			}
			for i, choice := range selection.Choices {
				if i < tc.strmCount {
					if choice.Kind != "ed2k_strm" || choice.STRMTarget == nil || *choice.STRMTarget != b.ED2KSTRMTargets[i] || choice.CloudTarget != nil {
						t.Fatalf("STRM 候选顺序或快照错误：%+v", choice)
					}
				} else if choice.Kind != "cloud" || choice.CloudTarget == nil || *choice.CloudTarget != b.CloudDownloadTargets[i-tc.strmCount] || choice.STRMTarget != nil {
					t.Fatalf("云下载候选顺序或快照错误：%+v", choice)
				}
			}
			if !state.Replies[0].CaptureMessageID || state.Replies[0].UpdateID != 10 {
				t.Fatal("统一提示没有请求持久保存消息编号")
			}
			buttons := resourceKeyboard(t, state.Replies[0].ReplyMarkup)
			flat := make([]string, 0, len(selection.Choices)+1)
			for _, row := range buttons {
				for _, button := range row {
					flat = append(flat, button["callback_data"])
				}
			}
			want := make([]string, 0, len(selection.Choices)+1)
			for i := range selection.Choices {
				want = append(want, resourceCallbackData(10, fmt.Sprint(i)))
			}
			want = append(want, resourceCallbackData(10, "cancel"))
			if !reflect.DeepEqual(flat, want) {
				t.Fatalf("统一键盘没有按候选索引提供选择及取消：got=%v want=%v", flat, want)
			}
		})
	}
}

func TestResourceED2KBareSelectionCreatesExactlyOneModuleBatch(t *testing.T) {
	for _, tc := range []struct{ choice, mode string }{{"0", "ed2k_strm"}, {"1", "cloud"}} {
		t.Run(tc.mode, func(t *testing.T) {
			b, cloud := newResourceSelectionTestBot(t)
			imports := 0
			b.ED2KSTRM = ed2kSTRMImporterFunc(func(_ context.Context, target model.ED2KSTRMTarget, links []string, requestID string) (model.ED2KSTRMImportResult, error) {
				imports++
				if target != b.ED2KSTRMTargets[0] || !reflect.DeepEqual(links, []string{botED2KLink}) || requestID != b.ed2kSTRMRequestID(10, target.ID) {
					t.Error("选定 STRM 后未使用完整目标与原消息请求身份")
				}
				return model.ED2KSTRMImportResult{Tasks: []model.ED2KSTRMTask{{}}}, nil
			})
			now := time.Now()
			state := enqueueResourceSelection(t, b, now)
			callback := resourceTestCallback(10, 7, 500, tc.choice)
			b.enqueueUpdates(context.Background(), &state, []telegramUpdate{{ID: 11, CallbackQuery: callback}}, now)
			if len(state.ResourceSelections) != 0 || len(state.Queue) != 1 || state.Queue[0].Mode != tc.mode || len(state.CallbackAnswers) != 1 || imports != 0 || cloud.calls != 0 {
				t.Fatalf("选择后重复询问、跨模块投递或提前提交：imports=%d cloud=%d state=%+v", imports, cloud.calls, state)
			}
			batch := state.Queue[0]
			if batch.UpdateID != 10 || batch.ChatID != 42 || batch.MessageID != 110 || batch.ThreadID != 88 {
				t.Fatalf("选择结果丢失原消息范围：%+v", batch)
			}
			requireResourceKeyboardCleared(t, state, 500)
			b.enqueueUpdates(context.Background(), &state, []telegramUpdate{{ID: 12, CallbackQuery: callback}}, now)
			if len(state.Queue) != 1 || len(state.CallbackAnswers) != 2 {
				t.Fatalf("重放按钮产生了额外任务：%+v", state)
			}
			state.Batch, state.Queue = batch, nil
			if err := b.processBatchLink(context.Background(), &state); err != nil {
				t.Fatal(err)
			}
			if (tc.mode == "ed2k_strm" && (imports != 1 || cloud.calls != 0)) || (tc.mode == "cloud" && (imports != 0 || cloud.calls != 1 || cloud.config.TargetPath != b.CloudDownloadTargets[0].TargetPath)) {
				t.Fatalf("选择没有唯一绑定模块：imports=%d cloud=%+v", imports, cloud)
			}
		})
	}
}

func TestResourceED2KBareCancelPersistsAndRejectsReplay(t *testing.T) {
	b, cloud := newResourceSelectionTestBot(t)
	now := time.Now()
	state := enqueueResourceSelection(t, b, now)
	callback := resourceTestCallback(10, 7, 500, "cancel")
	b.enqueueUpdates(context.Background(), &state, []telegramUpdate{{ID: 11, CallbackQuery: callback}}, now)
	if len(state.ResourceSelections) != 0 || len(state.Queue) != 0 || cloud.calls != 0 || len(state.CallbackAnswers) != 1 {
		t.Fatalf("取消创建了任务或未移除选择：%+v", state)
	}
	requireResourceKeyboardCleared(t, state, 500)
	if err := b.save(state); err != nil {
		t.Fatal(err)
	}
	state = readED2KSTRMBotState(t, b.StatePath)
	b.enqueueUpdates(context.Background(), &state, []telegramUpdate{{ID: 12, CallbackQuery: resourceTestCallback(10, 7, 500, "0")}}, now)
	if len(state.Queue) != 0 || len(state.ResourceSelections) != 0 || len(state.CallbackAnswers) != 2 {
		t.Fatalf("取消后重启仍可重新提交：%+v", state)
	}
}

func TestResourceED2KBareCallbackRequiresOriginalSenderChatAndPrompt(t *testing.T) {
	for _, mode := range []string{"sender", "chat", "unauthorized_chat", "prompt", "uncaptured", "group", "missing_message", "invalid_index", "negative_index", "out_of_range"} {
		t.Run(mode, func(t *testing.T) {
			b, _ := newResourceSelectionTestBot(t)
			now := time.Now()
			state := enqueueResourceSelection(t, b, now)
			callback := resourceTestCallback(10, 7, 500, "0")
			switch mode {
			case "sender":
				callback.From.ID = 8
			case "chat":
				b.AllowedChats[43] = true
				callback.Message.Chat.ID = 43
			case "unauthorized_chat":
				delete(b.AllowedChats, 42)
			case "prompt":
				callback.Message.ID = 501
			case "uncaptured":
				state.ResourceSelections[0].PromptMessageID = 0
			case "group":
				callback.Message.Chat.Type = "supergroup"
			case "missing_message":
				callback.Message = nil
			case "invalid_index":
				callback.Data = resourceCallbackData(10, "bad")
			case "negative_index":
				callback.Data = resourceCallbackData(10, "-1")
			case "out_of_range":
				callback.Data = resourceCallbackData(10, "2")
			}
			b.enqueueUpdates(context.Background(), &state, []telegramUpdate{{ID: 11, CallbackQuery: callback}}, now)
			if len(state.Queue) != 0 || len(state.ResourceSelections) != 1 || len(state.CallbackAnswers) != 1 {
				t.Fatalf("伪造、未授权或错误索引的回调通过：%+v", state)
			}
		})
	}
}

func TestResourceED2KBareExpiredChoiceCannotSubmit(t *testing.T) {
	b, cloud := newResourceSelectionTestBot(t)
	now := time.Now()
	state := enqueueResourceSelection(t, b, now)
	b.enqueueUpdates(context.Background(), &state, []telegramUpdate{{ID: 11, CallbackQuery: resourceTestCallback(10, 7, 500, "1")}}, now.Add(10*time.Minute))
	if len(state.Queue) != 0 || len(state.ResourceSelections) != 0 || cloud.calls != 0 || len(state.CallbackAnswers) != 1 {
		t.Fatalf("到期的选择仍能提交：%+v", state)
	}
	requireResourceKeyboardCleared(t, state, 500)
}

func TestResourceED2KBareSelectionRestoresSnapshotsAfterRestart(t *testing.T) {
	b, _ := newResourceSelectionTestBot(t)
	now := time.Now()
	state := enqueueResourceSelection(t, b, now)
	if err := b.save(state); err != nil {
		t.Fatal(err)
	}
	restored := readED2KSTRMBotState(t, b.StatePath)
	if len(restored.ResourceSelections) != 1 || !reflect.DeepEqual(restored.ResourceSelections[0].Choices, state.ResourceSelections[0].Choices) || !reflect.DeepEqual(restored.ResourceSelections[0].Items, resourceTestItems(botED2KLink)) || restored.ResourceSelections[0].PromptMessageID != 500 {
		t.Fatalf("重启丢失选择中的候选快照：%+v", restored)
	}
	b.enqueueUpdates(context.Background(), &restored, []telegramUpdate{{ID: 11, CallbackQuery: resourceTestCallback(10, 7, 500, "1")}}, now)
	if len(restored.Queue) != 1 || restored.Queue[0].Mode != "cloud" || restored.Queue[0].CloudTargetID != "cloud-a" || len(restored.ResourceSelections) != 0 {
		t.Fatalf("重启后没有复用原消息选择：%+v", restored)
	}
}

func TestResourceED2KBareChangedTargetNeverFallsBack(t *testing.T) {
	for _, tc := range []struct{ kind, change string }{
		{"ed2k_strm", "directory"}, {"ed2k_strm", "assignment"}, {"ed2k_strm", "deleted"}, {"ed2k_strm", "module_disabled"},
		{"cloud", "directory"}, {"cloud", "account"}, {"cloud", "assignment"}, {"cloud", "deleted"}, {"cloud", "not_ready"}, {"cloud", "module_disabled"},
	} {
		t.Run(tc.kind+"/"+tc.change, func(t *testing.T) {
			b, cloud := newResourceSelectionTestBot(t)
			now := time.Now()
			state := enqueueResourceSelection(t, b, now)
			index := "0"
			if tc.kind == "ed2k_strm" {
				switch tc.change {
				case "directory":
					b.ED2KSTRMTargets[0].OutputDir = "changed/output"
				case "assignment":
					b.ED2KSTRMTargets[0].AssignedBotID = "other-bot"
				case "deleted":
					b.ED2KSTRMTargets = nil
				case "module_disabled":
					b.ED2KSTRM = nil
				}
			} else {
				index = "1"
				switch tc.change {
				case "directory":
					b.CloudDownloadTargets[0].TargetPath = "/changed"
				case "account":
					b.CloudDownloadTargets[0].AccountID = "new-account"
				case "assignment":
					b.CloudDownloadTargets[0].AssignedBotID = "other-bot"
				case "deleted":
					b.CloudDownloadTargets = nil
				case "not_ready":
					b.CloudDownloadTargets[0].Ready = false
				case "module_disabled":
					b.CloudDownloads = nil
				}
			}
			b.enqueueUpdates(context.Background(), &state, []telegramUpdate{{ID: 11, CallbackQuery: resourceTestCallback(10, 7, 500, index)}}, now)
			if len(state.Queue) != 0 || len(state.ResourceSelections) != 0 || cloud.calls != 0 || len(state.CallbackAnswers) != 1 {
				t.Fatalf("目标变化后投递或改投了其他入口：%+v", state)
			}
			requireResourceKeyboardCleared(t, state, 500)
		})
	}
}

func TestResourceED2KBareQueueFullRetainsSelectionForRetry(t *testing.T) {
	b, _ := newResourceSelectionTestBot(t)
	now := time.Now()
	state := enqueueResourceSelection(t, b, now)
	for i := 0; i < 100; i++ {
		state.Queue = append(state.Queue, &messageBatch{Mode: "cloud", UpdateID: int64(i + 100), ChatID: 42, Items: []messageShare{{URL: resourceMagnetLink}}})
	}
	b.enqueueUpdates(context.Background(), &state, []telegramUpdate{{ID: 11, CallbackQuery: resourceTestCallback(10, 7, 500, "0")}}, now)
	if len(state.Queue) != 100 || len(state.ResourceSelections) != 1 || len(state.CallbackAnswers) != 1 {
		t.Fatalf("队列满时丢失选择或继续追加任务：%+v", state)
	}
	state.Queue = nil
	b.enqueueUpdates(context.Background(), &state, []telegramUpdate{{ID: 12, CallbackQuery: resourceTestCallback(10, 7, 500, "0")}}, now)
	if len(state.Queue) != 1 || state.Queue[0].Mode != "ed2k_strm" || len(state.ResourceSelections) != 0 {
		t.Fatalf("队列恢复后不能重试同一选择：%+v", state)
	}
}

func TestResourceED2KBareHiddenTextAndCaptionLinks(t *testing.T) {
	for _, mode := range []string{"hidden_text", "caption", "hidden_caption"} {
		t.Run(mode, func(t *testing.T) {
			b := newED2KSTRMTestBot(t)
			update := cloudUpdate(10, 42, "private", "文件")
			switch mode {
			case "hidden_text":
				update.Message.Entities = []telegramEntity{{Type: "text_link", Offset: 0, Length: 2, URL: botED2KLink}}
			case "caption":
				update.Message.Text = ""
				update.Message.Caption = "文件\n" + botED2KLink
			case "hidden_caption":
				update.Message.Text = ""
				update.Message.Caption = "文件"
				update.Message.CaptionEntities = []telegramEntity{{Type: "text_link", Offset: 0, Length: 2, URL: botED2KLink}}
			}
			state := telegramState{}
			b.enqueueUpdates(context.Background(), &state, []telegramUpdate{update}, time.Now())
			if len(state.Queue) != 1 || state.Queue[0].Mode != "ed2k_strm" || len(state.Queue[0].Items) != 1 || state.Queue[0].Items[0].URL != botED2KLink || len(state.ResourceSelections) != 0 {
				t.Fatalf("隐藏链接或图文的裸 ED2K 未被统一识别：%+v", state)
			}
		})
	}
}

func TestResourceShareAndCloudMixedMessageIsRejectedBeforeRouting(t *testing.T) {
	const share = "https://115.com/s/sharecode"
	for _, link := range []string{botED2KLink, resourceMagnetLink} {
		for _, command := range []string{"", "/cloud ", "/ed2kstrm "} {
			for _, shareFirst := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/shareFirst=%t", link[:6], command, shareFirst), func(t *testing.T) {
					b, cloud := newResourceSelectionTestBot(t)
					imports, generations := 0, 0
					b.ED2KSTRM = ed2kSTRMImporterFunc(func(context.Context, model.ED2KSTRMTarget, []string, string) (model.ED2KSTRMImportResult, error) {
						imports++
						return model.ED2KSTRMImportResult{}, nil
					})
					b.Generator = batchGeneratorFunc(func(context.Context, service.GenerateInput) (*service.GenerationResult, error) {
						generations++
						return &service.GenerationResult{}, nil
					})
					text := link + "\n" + share
					if shareFirst {
						text = share + "\n" + link
					}
					state := telegramState{}
					b.enqueueUpdates(context.Background(), &state, []telegramUpdate{cloudUpdate(10, 42, "private", command+text)}, time.Now())
					if len(state.Queue) != 0 || len(state.ResourceSelections) != 0 || len(state.Replies) != 1 || cloud.calls != 0 || imports != 0 || generations != 0 {
						t.Fatalf("分享混发绕过整条拒绝：cloud=%d imports=%d generation=%d state=%+v", cloud.calls, imports, generations, state)
					}
				})
			}
		}
	}
}

func TestResourceHiddenShareAndCloudMixedMessageIsRejected(t *testing.T) {
	for _, cloudLink := range []string{botED2KLink, resourceMagnetLink} {
		for _, caption := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/caption=%t", cloudLink[:6], caption), func(t *testing.T) {
				b, cloud := newResourceSelectionTestBot(t)
				text := "分享\n资源"
				entities := []telegramEntity{testLinkEntity(text, "资源", cloudLink), testLinkEntity(text, "分享", "https://115.com/s/sharecode")}
				update := cloudUpdate(10, 42, "private", text)
				update.Message.Entities = entities
				if caption {
					update.Message.Text, update.Message.Caption = "", text
					update.Message.Entities, update.Message.CaptionEntities = nil, entities
				}
				state := telegramState{}
				b.enqueueUpdates(context.Background(), &state, []telegramUpdate{update}, time.Now())
				if len(state.Queue) != 0 || len(state.ResourceSelections) != 0 || len(state.Replies) != 1 || cloud.calls != 0 {
					t.Fatalf("隐藏链接顺序改变后混发未整条拒绝：%+v", state)
				}
			})
		}
	}
}

func TestResourceED2KBareHiddenLinkUsesTargetRatherThanDisplayedProtocol(t *testing.T) {
	const shareLink = "https://115.com/s/sharecode"
	for _, tc := range []struct {
		name, display, target, mode, expectedLink string
		wantSelection                             bool
	}{
		{name: "ED2K显示磁力", display: resourceMagnetLink, target: botED2KLink, wantSelection: true, expectedLink: botED2KLink},
		{name: "外站显示ED2K", display: botED2KLink, target: "https://example.invalid/file"},
		{name: "分享显示ED2K", display: botED2KLink, target: shareLink, mode: "direct", expectedLink: shareLink},
		{name: "磁力显示ED2K", display: botED2KLink, target: resourceMagnetLink, mode: "cloud", expectedLink: resourceMagnetLink},
		{name: "ED2K显示另一文件", display: "ed2k://|file|display.mkv|456|fedcba9876543210fedcba9876543210|/", target: botED2KLink, wantSelection: true, expectedLink: botED2KLink},
	} {
		for _, caption := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/caption=%t", tc.name, caption), func(t *testing.T) {
				b, cloud := newResourceSelectionTestBot(t)
				// 前缀包含补充平面字符，确保隐藏文本范围按 Telegram 的 UTF-16 计数处理。
				update := cloudUpdate(10, 42, "private", "😀 "+tc.display)
				entity := telegramEntity{Type: "text_link", Offset: 3, Length: len(tc.display), URL: tc.target}
				update.Message.Entities = []telegramEntity{entity}
				if caption {
					update.Message.Caption, update.Message.Text = update.Message.Text, ""
					update.Message.CaptionEntities, update.Message.Entities = update.Message.Entities, nil
				}
				state := telegramState{}
				b.enqueueUpdates(context.Background(), &state, []telegramUpdate{update}, time.Now())
				if cloud.calls != 0 {
					t.Fatalf("隐藏链接在选择前提交或进入错误选择：%+v", state)
				}
				if tc.wantSelection {
					if len(state.Queue) != 0 || len(state.ResourceSelections) != 1 || !reflect.DeepEqual(state.ResourceSelections[0].Items, resourceTestItems(tc.expectedLink)) || len(state.Replies) != 1 {
						t.Fatalf("隐藏文字被当作实际资源或混发链接：%+v", state)
					}
				} else if tc.mode != "" {
					if len(state.ResourceSelections) != 0 || len(state.Queue) != 1 || state.Queue[0].Mode != tc.mode || len(state.Queue[0].Items) != 1 || state.Queue[0].Items[0].URL != tc.expectedLink || len(state.Replies) != 0 {
						t.Fatalf("隐藏链接没有按实际目标协议处理：%+v", state)
					}
				} else if len(state.ResourceSelections) != 0 || len(state.Queue) != 0 || len(state.Replies) != 0 {
					t.Fatalf("外站显示文字触发了网盘资源处理：%+v", state)
				}
			})
		}
	}
}

func TestResourceED2KBareRespectsMessageAuthorization(t *testing.T) {
	for _, mode := range []string{"unauthorized_chat", "bot_sender", "group"} {
		t.Run(mode, func(t *testing.T) {
			b, cloud := newResourceSelectionTestBot(t)
			update := cloudUpdate(10, 42, "private", botED2KLink)
			switch mode {
			case "unauthorized_chat":
				update.Message.Chat.ID = 999
			case "bot_sender":
				update.Message.From.IsBot = true
			case "group":
				update.Message.Chat.Type = "supergroup"
			}
			state := telegramState{}
			b.enqueueUpdates(context.Background(), &state, []telegramUpdate{update}, time.Now())
			if len(state.Queue) != 0 || len(state.ResourceSelections) != 0 || cloud.calls != 0 || (mode != "group" && len(state.Replies) != 0) || (mode == "group" && len(state.Replies) != 1) {
				t.Fatalf("裸 ED2K 绕过消息授权或私聊限制：%+v", state)
			}
		})
	}
}

func TestResourceED2KBareReadinessFailureCannotAutoFallback(t *testing.T) {
	for _, withCloud := range []bool{false, true} {
		t.Run(fmt.Sprintf("cloud=%t", withCloud), func(t *testing.T) {
			b, cloud := newResourceSelectionTestBot(t)
			const secretDetail = "Cookie=secret; UID=private"
			ready := &resourceReadyImporter{check: func(ctx context.Context) error {
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > 2*time.Second {
					t.Error("只读可用性检查没有两秒超时")
				}
				return errors.New(secretDetail)
			}}
			b.ED2KSTRM = ready
			if !withCloud {
				b.CloudDownloadTargets = nil
			}
			state := telegramState{}
			b.enqueueUpdates(context.Background(), &state, []telegramUpdate{cloudUpdate(10, 42, "private", botED2KLink)}, time.Now())
			if ready.checks == 0 || ready.imports != 0 || cloud.calls != 0 || len(state.Queue) != 0 || len(state.Replies) != 1 {
				t.Fatalf("不可用的 STRM 自动改投或未做只读检查：ready=%+v cloud=%d state=%+v", ready, cloud.calls, state)
			}
			encoded, err := json.Marshal(state.Replies)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(encoded), secretDetail) {
				t.Fatal("只读可用性检查泄露上游错误")
			}
			if withCloud {
				if len(state.ResourceSelections) != 1 || len(state.ResourceSelections[0].Choices) != 2 || state.ResourceSelections[0].Choices[0].Kind != "ed2k_strm" || state.ResourceSelections[0].Choices[1].Kind != "cloud" {
					t.Fatalf("不可用的 STRM 与可用 cloud 没有保留显式选择：%+v", state)
				}
				state.ResourceSelections[0].PromptMessageID = 500
				b.enqueueUpdates(context.Background(), &state, []telegramUpdate{{ID: 11, CallbackQuery: resourceTestCallback(10, 7, 500, "0")}}, time.Now())
				if len(state.Queue) != 0 || ready.imports != 0 || cloud.calls != 0 || len(state.ResourceSelections) != 0 {
					t.Fatalf("选择不可用的 STRM 后改投或提交：%+v", state)
				}
			} else if len(state.ResourceSelections) != 0 {
				t.Fatalf("唯一候选不可用时仍创建选择：%+v", state)
			}
		})
	}
}

func TestResourceED2KBareExplicitCommandsKeepModuleRouting(t *testing.T) {
	b, _ := newResourceSelectionTestBot(t)
	state := telegramState{}
	b.enqueueUpdates(context.Background(), &state, []telegramUpdate{
		cloudUpdate(10, 42, "private", "/ed2kstrm "+botED2KLink),
		cloudUpdate(11, 42, "private", "/cloud "+botED2KLink+"\n"+resourceMagnetLink),
	}, time.Now())
	if len(state.ResourceSelections) != 0 || len(state.Queue) != 2 || state.Queue[0].Mode != "ed2k_strm" || len(state.Queue[0].Items) != 1 || state.Queue[1].Mode != "cloud" || len(state.Queue[1].Items) != 2 || len(state.Replies) != 0 {
		t.Fatalf("显式命令被统一裸链接路由或混合保护接管：%+v", state)
	}
}

func TestResourceCommandsOnlyNarrowCandidatesBeforeOneSelection(t *testing.T) {
	for _, tc := range []struct{ command, kind string }{
		{"/ed2k_strm", "ed2k_strm"}, {"/ed2kstrm@CinemaBot", "ed2k_strm"}, {"/cloud", "cloud"},
	} {
		t.Run(tc.command, func(t *testing.T) {
			b, cloud := newResourceSelectionTestBot(t)
			b.ED2KSTRMTargets = append(b.ED2KSTRMTargets, botED2KTarget("target-b", "剧集", "ed2k/tv", b.BotID))
			b.CloudDownloadTargets = append(b.CloudDownloadTargets, resourceCloudTarget(b, "cloud-b"))
			state := telegramState{}
			now := time.Now()
			b.enqueueUpdates(context.Background(), &state, []telegramUpdate{cloudUpdate(10, 42, "private", tc.command+" "+botED2KLink)}, now)
			if len(state.Queue) != 0 || len(state.ResourceSelections) != 1 || len(state.ResourceSelections[0].Choices) != 2 || len(state.Replies) != 1 || cloud.calls != 0 {
				t.Fatalf("显式命令没有复用统一选择：%+v", state)
			}
			for _, choice := range state.ResourceSelections[0].Choices {
				if choice.Kind != tc.kind {
					t.Fatalf("显式命令提供了其他用途：%+v", choice)
				}
			}
			state.ResourceSelections[0].PromptMessageID = 500
			b.enqueueUpdates(context.Background(), &state, []telegramUpdate{{ID: 11, CallbackQuery: resourceTestCallback(10, 7, 500, "1")}}, now)
			if len(state.Queue) != 1 || state.Queue[0].Mode != tc.kind || len(state.ResourceSelections) != 0 || cloud.calls != 0 {
				t.Fatalf("命令选择后又询问目录或提前提交：%+v", state)
			}
			if tc.kind == "ed2k_strm" && (state.Queue[0].ED2KSTRMTarget != b.ED2KSTRMTargets[1] || state.Queue[0].ED2KRequestID != b.ed2kSTRMRequestID(10, "target-b")) {
				t.Fatal("显式 STRM 选择未固定原始目标快照与请求编号")
			}
			if tc.kind == "cloud" && state.Queue[0].CloudTargetID != "cloud-b" {
				t.Fatal("显式云下载选择没有固定选定目录")
			}
			requireResourceKeyboardCleared(t, state, 500)
		})
	}
}

func TestResourceMagnetMessagesUseCloudSelectionForEntireBatch(t *testing.T) {
	for _, prefix := range []string{"", "/cloud ", "/cloud redo ", "/重新转存 "} {
		for _, links := range [][]string{{resourceMagnetLink}, {resourceMagnetLink, botED2KLink}, {botED2KLink, resourceMagnetLink}} {
			t.Run(fmt.Sprintf("%s/%s/%d", prefix, links[0][:6], len(links)), func(t *testing.T) {
				b, cloud := newResourceSelectionTestBot(t)
				ready := &resourceReadyImporter{check: func(context.Context) error { return errors.New("STRM 不可用") }}
				b.ED2KSTRM = ready
				b.CloudDownloadTargets = append(b.CloudDownloadTargets, resourceCloudTarget(b, "cloud-b"))
				state := telegramState{}
				now := time.Now()
				b.enqueueUpdates(context.Background(), &state, []telegramUpdate{cloudUpdate(10, 42, "private", prefix+strings.Join(links, "\n"))}, now)
				if len(state.Queue) != 0 || len(state.ResourceSelections) != 1 || len(state.ResourceSelections[0].Choices) != 2 || !reflect.DeepEqual(state.ResourceSelections[0].Items, resourceTestItems(links...)) || len(state.Replies) != 1 || ready.checks != 0 || ready.imports != 0 || cloud.calls != 0 {
					t.Fatalf("磁力消息未整批等待普通转存目录，或检查了 STRM：ready=%+v state=%+v", ready, state)
				}
				for _, choice := range state.ResourceSelections[0].Choices {
					if choice.Kind != "cloud" {
						t.Fatalf("含磁力消息提供了 STRM 候选：%+v", choice)
					}
				}
				state.ResourceSelections[0].PromptMessageID = 500
				b.enqueueUpdates(context.Background(), &state, []telegramUpdate{{ID: 11, CallbackQuery: resourceTestCallback(10, 7, 500, "1")}}, now)
				if len(state.Queue) != 1 || state.Queue[0].Mode != "cloud" || state.Queue[0].CloudTargetID != "cloud-b" || len(state.Queue[0].Items) != len(links) || len(state.ResourceSelections) != 0 || ready.checks != 0 {
					t.Fatalf("选定目录没有应用于磁力与 ED2K 整批：%+v", state)
				}
				for i, item := range state.Queue[0].Items {
					if item.URL != links[i] {
						t.Fatalf("批次链接顺序或内容错误：got=%+v want=%v", state.Queue[0].Items, links)
					}
				}
				state.Batch, state.Queue = state.Queue[0], nil
				if err := b.processBatchLink(context.Background(), &state); err != nil {
					t.Fatal(err)
				}
				if cloud.calls != len(links) || cloud.config.TargetPath != b.CloudDownloadTargets[1].TargetPath || ready.imports != 0 || ready.checks != 0 {
					t.Fatalf("批次执行没有固定普通转存用途和目录：cloud=%+v ready=%+v", cloud, ready)
				}
			})
		}
	}
}

func TestResourceSTRMCommandRejectsMagnetWithoutDroppingLinks(t *testing.T) {
	for _, command := range []string{"/ed2k_strm ", "/ed2kstrm "} {
		for _, links := range [][]string{{resourceMagnetLink}, {resourceMagnetLink, botED2KLink}, {botED2KLink, resourceMagnetLink}} {
			t.Run(fmt.Sprintf("%s/%s/%d", command, links[0][:6], len(links)), func(t *testing.T) {
				b, cloud := newResourceSelectionTestBot(t)
				ready := &resourceReadyImporter{}
				b.ED2KSTRM = ready
				state := telegramState{}
				b.enqueueUpdates(context.Background(), &state, []telegramUpdate{cloudUpdate(10, 42, "private", command+strings.Join(links, "\n"))}, time.Now())
				if len(state.Queue) != 0 || len(state.ResourceSelections) != 0 || len(state.Replies) != 1 || ready.imports != 0 || ready.checks != 0 || cloud.calls != 0 {
					t.Fatalf("STRM 命令丢弃磁力后处理了剩余链接：%+v", state)
				}
			})
		}
	}
}

func TestResourceEmptyCommandsExplainDirectLinks(t *testing.T) {
	for _, command := range []string{"/cloud", "/ed2k_strm", "/ed2kstrm"} {
		t.Run(command, func(t *testing.T) {
			b, cloud := newResourceSelectionTestBot(t)
			state := telegramState{}
			b.enqueueUpdates(context.Background(), &state, []telegramUpdate{cloudUpdate(10, 42, "private", command)}, time.Now())
			if len(state.Queue) != 0 || len(state.ResourceSelections) != 0 || len(state.Replies) != 1 || cloud.calls != 0 || !strings.Contains(state.Replies[0].Text, "直接发送") {
				t.Fatalf("空命令没有提示直接发链接：%+v", state)
			}
		})
	}
}

func TestResourceED2KBareOtherCommandsCannotBypassPurposeSelection(t *testing.T) {
	for _, command := range []string{"/start", "/help@CinemaBot", "/unknown"} {
		t.Run(command, func(t *testing.T) {
			b, cloud := newResourceSelectionTestBot(t)
			state := telegramState{}
			b.enqueueUpdates(context.Background(), &state, []telegramUpdate{cloudUpdate(10, 42, "private", command+"\n"+botED2KLink)}, time.Now())
			if len(state.Queue) != 0 || len(state.ResourceSelections) != 1 || len(state.Replies) != 1 || cloud.calls != 0 {
				t.Fatalf("无明确用途的命令绕过选择并提交云下载：%+v", state)
			}
		})
	}
}

func TestResourceED2KBareAllCandidatesUnavailableCreateNoSelection(t *testing.T) {
	b, cloud := newResourceSelectionTestBot(t)
	ready := &resourceReadyImporter{check: func(context.Context) error { return errors.New("尚未配置主账号") }}
	b.ED2KSTRM = ready
	b.CloudDownloadTargets[0].Ready = false
	state := telegramState{}
	b.enqueueUpdates(context.Background(), &state, []telegramUpdate{cloudUpdate(10, 42, "private", botED2KLink)}, time.Now())
	if len(state.Queue) != 0 || len(state.ResourceSelections) != 0 || len(state.Replies) != 1 || ready.checks != 1 || ready.imports != 0 || cloud.calls != 0 {
		t.Fatalf("所有候选不可用时仍创建选择或提交任务：%+v", state)
	}
}

func TestResourceED2KBareChoiceAppliesToEveryLinkInMessage(t *testing.T) {
	b, cloud := newResourceSelectionTestBot(t)
	second := "ed2k://|file|episode.mkv|456|fedcba9876543210fedcba9876543210|/"
	now := time.Now()
	state := telegramState{}
	b.enqueueUpdates(context.Background(), &state, []telegramUpdate{cloudUpdate(10, 42, "private", botED2KLink+"\n"+second+"\n"+botED2KLink)}, now)
	if len(state.Queue) != 0 || len(state.ResourceSelections) != 1 || !reflect.DeepEqual(state.ResourceSelections[0].Items, resourceTestItems(botED2KLink, second)) || cloud.calls != 0 {
		t.Fatalf("等待选择时丢失或重复了整条消息链接：%+v", state)
	}
	state.ResourceSelections[0].PromptMessageID = 500
	b.enqueueUpdates(context.Background(), &state, []telegramUpdate{{ID: 11, CallbackQuery: resourceTestCallback(10, 7, 500, "1")}}, now)
	if len(state.Queue) != 1 || len(state.Queue[0].Items) != 2 || state.Queue[0].Mode != "cloud" || state.Queue[0].CloudTargetID != "cloud-a" || len(state.ResourceSelections) != 0 {
		t.Fatalf("整条消息没有一次性固定同一云下载目标：%+v", state)
	}
	state.Batch, state.Queue = state.Queue[0], nil
	if err := b.processBatchLink(context.Background(), &state); err != nil {
		t.Fatal(err)
	}
	if cloud.calls != 2 || cloud.config.TargetPath != b.CloudDownloadTargets[0].TargetPath || cloud.url != second {
		t.Fatalf("选择没有应用到每个链接：%+v", cloud)
	}
}

func TestResourceED2KBareReadinessInheritsPollingCancellation(t *testing.T) {
	b, cloud := newResourceSelectionTestBot(t)
	b.CloudDownloadTargets = nil
	ready := &resourceReadyImporter{check: func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}}
	b.ED2KSTRM = ready
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	state := telegramState{}
	started := time.Now()
	b.enqueueUpdates(ctx, &state, []telegramUpdate{cloudUpdate(10, 42, "private", botED2KLink)}, started)
	if time.Since(started) >= time.Second || ready.imports != 0 || cloud.calls != 0 || len(state.Queue) != 0 || len(state.ResourceSelections) != 0 {
		t.Fatalf("停止轮询后可用性检查仍占用超时预算或提交任务：%+v", state)
	}
}

func TestResourceED2KBareLargeUnavailableSelectionFitsTelegramLimits(t *testing.T) {
	for _, count := range []int{99, 100} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			b, cloud := newResourceSelectionTestBot(t)
			b.ED2KSTRM = &resourceReadyImporter{check: func(context.Context) error { return errors.New("主账号未就绪") }}
			b.ED2KSTRMTargets = make([]model.ED2KSTRMTarget, 0, count-1)
			for i := 0; i < count-1; i++ {
				id := fmt.Sprintf("strm-%d", i)
				name := fmt.Sprintf("%02d-", i) + strings.Repeat("目录名称", 19)
				b.ED2KSTRMTargets = append(b.ED2KSTRMTargets, botED2KTarget(id, name, "ed2k/"+id, b.BotID))
			}
			state := telegramState{}
			b.enqueueUpdates(context.Background(), &state, []telegramUpdate{cloudUpdate(10, 42, "private", botED2KLink)}, time.Now())
			if len(state.Queue) != 0 || len(state.Replies) != 1 || cloud.calls != 0 || len(utf16.Encode([]rune(state.Replies[0].Text))) >= 4096 {
				t.Fatalf("长不可用目录提示超出 Telegram 正文限制或提前提交：%+v", state)
			}
			if count == 99 {
				if len(state.ResourceSelections) != 1 || len(state.ResourceSelections[0].Choices) != 99 {
					t.Fatalf("上限内的候选被丢弃：%+v", state)
				}
				buttons := 0
				for _, row := range resourceKeyboard(t, state.Replies[0].ReplyMarkup) {
					for _, button := range row {
						buttons++
						if len(button["callback_data"]) > 64 {
							t.Fatal("回调超过 Telegram 的 64 字节限制")
						}
					}
				}
				if buttons != 100 {
					t.Fatalf("99 个候选及取消没有限制为 100 个按钮：%d", buttons)
				}
			} else if len(state.ResourceSelections) != 0 || state.Replies[0].ReplyMarkup != nil {
				t.Fatalf("超过 99 个候选仍建立了无法安全发送的选择：%+v", state)
			}
		})
	}
}

func runResourceSelectionHTTP(t *testing.T, editStatus int) {
	t.Helper()
	b, cloud := newResourceSelectionTestBot(t)
	var imports, unexpectedCalls, prompts, clears, answers atomic.Int32
	b.ED2KSTRM = ed2kSTRMImporterFunc(func(_ context.Context, target model.ED2KSTRMTarget, links []string, requestID string) (model.ED2KSTRMImportResult, error) {
		imports.Add(1)
		if target != b.ED2KSTRMTargets[0] || !reflect.DeepEqual(links, []string{botED2KLink}) || requestID != b.ed2kSTRMRequestID(20, target.ID) {
			t.Error("完整轮询流程未提交原始选择的快照")
		}
		return model.ED2KSTRMImportResult{Tasks: []model.ED2KSTRMTask{{}}}, nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Offset      int64           `json:"offset"`
			MessageID   int64           `json:"message_id"`
			ReplyMarkup json.RawMessage `json:"reply_markup"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/sendMessage"):
			if len(input.ReplyMarkup) > 0 {
				prompts.Add(1)
			}
			w.Write([]byte(`{"ok":true,"result":{"message_id":700}}`))
		case strings.HasSuffix(r.URL.Path, "/editMessageText"), strings.HasSuffix(r.URL.Path, "/editMessageReplyMarkup"):
			if input.MessageID == 700 && len(input.ReplyMarkup) > 0 {
				var markup struct {
					Rows [][]json.RawMessage `json:"inline_keyboard"`
				}
				if err := json.Unmarshal(input.ReplyMarkup, &markup); err == nil && len(markup.Rows) == 0 {
					clears.Add(1)
				}
			}
			if editStatus != http.StatusOK {
				w.WriteHeader(editStatus)
				w.Write([]byte(`{"ok":false,"description":"message to edit not found"}`))
				return
			}
			w.Write([]byte(`{"ok":true,"result":{"message_id":700}}`))
		case strings.HasSuffix(r.URL.Path, "/answerCallbackQuery"):
			answers.Add(1)
			w.Write([]byte(`{"ok":true,"result":true}`))
		case strings.HasSuffix(r.URL.Path, "/getUpdates"):
			var updates []telegramUpdate
			switch input.Offset {
			case 0:
				updates = []telegramUpdate{cloudUpdate(20, 42, "private", botED2KLink)}
			case 21:
				data, err := os.ReadFile(b.StatePath)
				var state telegramState
				if err != nil || json.Unmarshal(data, &state) != nil || len(state.ResourceSelections) != 1 || state.ResourceSelections[0].PromptMessageID != 700 || len(state.Queue) != 0 || imports.Load() != 0 {
					t.Error("收到回调前没有落盘提示编号，或等待期间已经提交")
				}
				updates = []telegramUpdate{{ID: 21, CallbackQuery: resourceTestCallback(20, 7, 700, "0")}}
			default:
				cancel()
			}
			if updates == nil {
				updates = []telegramUpdate{}
			}
			json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": updates})
		default:
			unexpectedCalls.Add(1)
			t.Errorf("出现意外的网络调用：%s", r.URL.Path)
			w.WriteHeader(400)
		}
	}))
	defer server.Close()
	b.BaseURL = server.URL
	if err := b.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if imports.Load() != 1 || unexpectedCalls.Load() != 0 || cloud.calls != 0 || prompts.Load() != 1 || clears.Load() != 1 || answers.Load() != 1 {
		t.Fatalf("完整统一选择流程出现丢失或重复：imports=%d unexpected=%d cloud=%d prompts=%d clears=%d answers=%d", imports.Load(), unexpectedCalls.Load(), cloud.calls, prompts.Load(), clears.Load(), answers.Load())
	}
	state := readED2KSTRMBotState(t, b.StatePath)
	if len(state.ResourceSelections) != 0 || len(state.Queue) != 0 || state.Batch != nil || state.Pending != nil || len(state.CallbackAnswers) != 0 {
		t.Fatalf("流程结束后仍留有未消费选择或任务：%+v", state)
	}
}

func TestResourceED2KBareRunCapturesPromptBeforePersistedCallback(t *testing.T) {
	runResourceSelectionHTTP(t, http.StatusOK)
}

func TestResourceED2KBareOptionalKeyboardEditFailureDoesNotBlockImport(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusForbidden, http.StatusInternalServerError} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			runResourceSelectionHTTP(t, status)
		})
	}
}

func TestResourceShareSelectsPurposeOnceAndPreservesPasswordsAfterRestart(t *testing.T) {
	for _, tc := range []struct{ choice, mode string }{{"0", "direct"}, {"1", "share_transfer"}} {
		t.Run(tc.mode, func(t *testing.T) {
			b, generator, transfer := newShareResourceTestBot(t)
			now := time.Now()
			text := "https://115.com/s/first 提取码：aaaa\nhttps://115.com/s/first 提取码：bbbb\nhttps://115.com/s/second?password=cccc"
			want := []messageShare{{URL: "https://115.com/s/first", Password: "aaaa"}, {URL: "https://115.com/s/first", Password: "bbbb"}, {URL: "https://115.com/s/second?password=cccc"}}
			state := enqueueShareResourceSelection(t, b, now, text)
			selection := state.ResourceSelections[0]
			if len(selection.Choices) != 2 || selection.Choices[0].Kind != "share_strm" || selection.Choices[0].ShareSettings == nil || *selection.Choices[0].ShareSettings != *shareTestSnapshot(t, b) || selection.Choices[1].Kind != "share_transfer" || selection.Choices[1].ShareTarget == nil || *selection.Choices[1].ShareTarget != b.ShareTransferTargets[0] || !reflect.DeepEqual(selection.Items, want) {
				t.Fatalf("分享候选没有同时固定用途、目录和各自提取码：%+v", selection)
			}
			if len(generator.inputs) != 0 || len(transfer.items) != 0 {
				t.Fatal("等待选择时提前调用了生成或转存")
			}
			if err := b.save(state); err != nil {
				t.Fatal(err)
			}
			state = readED2KSTRMBotState(t, b.StatePath)
			if len(state.ResourceSelections) != 1 || !reflect.DeepEqual(state.ResourceSelections[0].Items, want) || !reflect.DeepEqual(state.ResourceSelections[0].Choices, selection.Choices) || state.ResourceSelections[0].PromptMessageID != 500 {
				t.Fatalf("重启丢失分享提取码或目标快照：%+v", state)
			}
			callback := resourceTestCallback(10, 7, 500, tc.choice)
			b.enqueueUpdates(context.Background(), &state, []telegramUpdate{{ID: 11, CallbackQuery: callback}}, now)
			if len(state.Queue) != 1 || state.Queue[0].Mode != tc.mode || !reflect.DeepEqual(state.Queue[0].Items, want) || state.Queue[0].ThreadID != 88 || len(state.ResourceSelections) != 0 || len(generator.inputs) != 0 || len(transfer.items) != 0 {
				t.Fatalf("选择没有为整条分享消息固定唯一批次：%+v", state)
			}
			requireResourceKeyboardCleared(t, state, 500)
			if tc.mode == "direct" && (state.Queue[0].ShareSettings == nil || *state.Queue[0].ShareSettings != *shareTestSnapshot(t, b)) {
				t.Fatal("分享 STRM 批次丢失生成配置快照")
			}
			b.enqueueUpdates(context.Background(), &state, []telegramUpdate{{ID: 12, CallbackQuery: callback}}, now)
			if len(state.Queue) != 1 || len(state.ResourceSelections) != 0 {
				t.Fatal("分享选择重放生成了第二个批次")
			}
			state.Batch, state.Queue = state.Queue[0], nil
			for state.Batch.Next < len(state.Batch.Items) {
				state.Pending = nil
				if err := b.processBatchLink(context.Background(), &state); err != nil {
					t.Fatal(err)
				}
			}
			if tc.mode == "direct" {
				if len(generator.inputs) != len(want) || len(transfer.items) != 0 {
					t.Fatalf("STRM 选择没有唯一提交到生成器：generator=%v transfer=%v", generator.inputs, transfer.items)
				}
				for i, input := range generator.inputs {
					if input.URL != want[i].URL || input.Password != want[i].Password || input.OutputDir != state.Batch.ShareSettings.Generation.OutputDir || input.Mode != "direct" {
						t.Fatalf("生成时提取码串用或配置变化：%+v", input)
					}
				}
			} else if len(generator.inputs) != 0 || !reflect.DeepEqual(transfer.items, want) || len(transfer.targets) != len(want) || transfer.targets[0].TargetPath != "/电影" || transfer.targets[2] != transfer.targets[0] {
				t.Fatalf("转存选择没有完整复用同一目标及提取码：generator=%v transfer=%+v", generator.inputs, transfer)
			}
		})
	}
}

func TestResourceShareUnavailableCandidateDoesNotChooseOtherPurpose(t *testing.T) {
	for _, unavailable := range []string{"generator", "unprepared", "settings", "account", "share_transfer"} {
		t.Run(unavailable, func(t *testing.T) {
			b, generator, transfer := newShareResourceTestBot(t)
			index := "0"
			switch unavailable {
			case "generator":
				b.Generator = nil
			case "unprepared":
				b.Generator = generator
			case "settings":
				b.Settings = nil
			case "account":
				b.Settings.ValidateAccount = func(string) error { return errors.New("主账号凭据失效") }
			case "share_transfer":
				index = "1"
				b.ShareTransferTargets[0].Ready = false
			}
			now := time.Now()
			state := enqueueShareResourceSelection(t, b, now, "https://115.com/s/first")
			if len(state.ResourceSelections[0].Choices) != 2 || len(generator.inputs) != 0 || len(transfer.items) != 0 {
				t.Fatalf("失效用途导致自动改投另一个用途：%+v", state)
			}
			b.enqueueUpdates(context.Background(), &state, []telegramUpdate{{ID: 11, CallbackQuery: resourceTestCallback(10, 7, 500, index)}}, now)
			if len(state.Queue) != 0 || len(state.ResourceSelections) != 0 || len(generator.inputs) != 0 || len(transfer.items) != 0 || len(state.CallbackAnswers) != 1 {
				t.Fatalf("选择不可用分享用途后发生提交或改投：%+v", state)
			}
			requireResourceKeyboardCleared(t, state, 500)
		})
	}
}

func TestResourceShareChangedSnapshotStopsCallbackAndExecution(t *testing.T) {
	for _, tc := range []struct{ kind, change string }{
		{"share_strm", "output"}, {"share_strm", "account"}, {"share_strm", "generation"}, {"share_strm", "reshare"}, {"share_strm", "assignment"}, {"share_strm", "disabled"}, {"share_strm", "unassigned"},
		{"share_transfer", "directory"}, {"share_transfer", "account"}, {"share_transfer", "assignment"}, {"share_transfer", "deleted"}, {"share_transfer", "not_ready"},
	} {
		for _, phase := range []string{"callback", "execution"} {
			t.Run(tc.kind+"/"+tc.change+"/"+phase, func(t *testing.T) {
				b, generator, transfer := newShareResourceTestBot(t)
				now := time.Now()
				state := enqueueShareResourceSelection(t, b, now, "https://115.com/s/first 提取码：aaaa")
				choice := "0"
				if tc.kind == "share_transfer" {
					choice = "1"
				}
				if phase == "execution" {
					b.enqueueUpdates(context.Background(), &state, []telegramUpdate{{ID: 11, CallbackQuery: resourceTestCallback(10, 7, 500, choice)}}, now)
					if len(state.Queue) != 1 {
						t.Fatalf("准备执行配置变化测试时未固定批次：%+v", state)
					}
					state.Batch, state.Queue = state.Queue[0], nil
				}
				if tc.kind == "share_strm" {
					if tc.change == "unassigned" {
						b.ShareEnabled = false
					} else {
						changeShareTestSettings(t, b, func(settings *service.AutomationSettings) {
							switch tc.change {
							case "output":
								settings.Generation.OutputDir = "changed-output"
							case "account":
								settings.AccountID = "new-primary"
							case "generation":
								settings.Generation.MinSizeMB++
							case "reshare":
								settings.ReshareEnabled = true
							case "assignment":
								settings.BotID = "other-bot"
							case "disabled":
								settings.TelegramEnabled = false
							}
						})
					}
				} else {
					switch tc.change {
					case "directory":
						b.ShareTransferTargets[0].TargetPath = "/changed"
					case "account":
						b.ShareTransferTargets[0].AccountID = "new-primary"
					case "assignment":
						b.ShareTransferTargets[0].AssignedBotID = "other-bot"
					case "deleted":
						b.ShareTransferTargets = nil
					case "not_ready":
						b.ShareTransferTargets[0].Ready = false
					}
				}
				if phase == "callback" {
					b.enqueueUpdates(context.Background(), &state, []telegramUpdate{{ID: 11, CallbackQuery: resourceTestCallback(10, 7, 500, choice)}}, now)
					if len(state.Queue) != 0 || len(state.ResourceSelections) != 0 || len(state.CallbackAnswers) != 1 {
						t.Fatalf("分享目标变化后仍然确认选择：%+v", state)
					}
					requireResourceKeyboardCleared(t, state, 500)
				} else {
					if err := b.processBatchLink(context.Background(), &state); err != nil {
						t.Fatal(err)
					}
					if state.Pending == nil || state.Batch.Next != len(state.Batch.Items) {
						t.Fatalf("执行时没有停止配置已变化的分享批次：%+v", state)
					}
				}
				if len(generator.inputs) != 0 || len(transfer.items) != 0 {
					t.Fatalf("分享目标变化后提交或改投了其他用途：generator=%v transfer=%v", generator.inputs, transfer.items)
				}
			})
		}
	}
}

func TestResourceShareDualPurposeInGroupRequiresPrivateSelection(t *testing.T) {
	b, generator, transfer := newShareResourceTestBot(t)
	state := telegramState{}
	b.enqueueUpdates(context.Background(), &state, []telegramUpdate{cloudUpdate(10, 42, "supergroup", "https://115.com/s/first")}, time.Now())
	if len(state.Queue) != 0 || len(state.ResourceSelections) != 0 || len(state.Replies) != 1 || !strings.Contains(state.Replies[0].Text, "私聊") || len(generator.inputs) != 0 || len(transfer.items) != 0 {
		t.Fatalf("群内双用途分享执行了任务或未引导私聊：%+v", state)
	}
}
