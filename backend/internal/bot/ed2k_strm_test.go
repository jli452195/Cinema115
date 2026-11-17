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
	"testing"
	"time"

	"strm115/backend/internal/model"
	"strm115/backend/internal/service"
)

const botED2KLink = "ed2k://|file|movie.mkv|123|0123456789abcdef0123456789abcdef|/"

type ed2kSTRMImporterFunc func(context.Context, []string) ([]model.ED2KSTRMResource, error)

func (f ed2kSTRMImporterFunc) Import(ctx context.Context, links []string) ([]model.ED2KSTRMResource, error) {
	return f(ctx, links)
}

func TestED2KSTRMExplicitCommandKeepsCloudDownloadsSeparate(t *testing.T) {
	b := newBatchTestBot(t, nil)
	imports := 0
	b.ED2KSTRM = ed2kSTRMImporterFunc(func(_ context.Context, _ []string) ([]model.ED2KSTRMResource, error) {
		imports++
		return nil, nil
	})
	b.CloudDownloads = &cloudSubmitterStub{}
	b.CloudDownloadTargets = []TelegramTransferTarget{testCloudTarget("cloud", "下载", "/下载")}
	state := telegramState{}
	command := cloudUpdate(1, 42, "private", "/ed2k_strm@CinemaBot "+botED2KLink+"\nhttps://115.com/s/sharecode")
	command.Message.ThreadID = 77
	b.enqueueUpdates(&state, []telegramUpdate{command, cloudUpdate(2, 42, "private", botED2KLink)}, time.Now())
	if imports != 0 || len(state.Queue) != 2 || len(state.Replies) != 0 {
		t.Fatalf("收集阶段提前执行或重复分流：imports=%d state=%+v", imports, state)
	}
	if batch := state.Queue[0]; batch.Mode != "ed2k_strm" || batch.ThreadID != 77 || len(batch.Items) != 1 || batch.Items[0].URL != botED2KLink {
		t.Fatalf("明确命令没有进入独立队列：%+v", batch)
	}
	if state.Queue[1].Mode != "cloud" || state.Offset != 3 {
		t.Fatalf("普通 ED2K 的云下载行为发生变化：%+v", state)
	}
}

func TestED2KSTRMCommandHonorsAllowedChatsAndBotFilter(t *testing.T) {
	b := newBatchTestBot(t, nil)
	b.ED2KSTRM = ed2kSTRMImporterFunc(func(context.Context, []string) ([]model.ED2KSTRMResource, error) { return nil, nil })
	botMessage := cloudUpdate(2, 42, "private", "/ed2kstrm "+botED2KLink)
	botMessage.Message.From.IsBot = true
	caption := cloudUpdate(3, 42, "supergroup", "")
	caption.Message.Caption = "/ed2kstrm 导入文件"
	caption.Message.CaptionEntities = []telegramEntity{{Type: "text_link", Offset: 10, Length: 4, URL: botED2KLink}}
	state := telegramState{}
	b.enqueueUpdates(&state, []telegramUpdate{cloudUpdate(1, 999, "private", "/ed2k_strm "+botED2KLink), botMessage, caption}, time.Now())
	if len(state.Queue) != 1 || state.Queue[0].UpdateID != 3 || state.Queue[0].Items[0].URL != botED2KLink || len(state.Replies) != 0 {
		t.Fatalf("白名单、Bot 过滤或图文入口有误：%+v", state)
	}
}

func TestED2KSTRMCommandRejectsMissingLinksAndOversizedMessages(t *testing.T) {
	b := newBatchTestBot(t, nil)
	state := telegramState{}
	b.enqueueUpdates(&state, []telegramUpdate{cloudUpdate(1, 42, "private", "/ed2k_strm "+botED2KLink)}, time.Now())
	if len(state.Queue) != 0 || len(state.Replies) != 1 || !strings.Contains(state.Replies[0].Text, "尚未启用") {
		t.Fatalf("模块不可用时未明确拒绝：%+v", state)
	}
	b.ED2KSTRM = ed2kSTRMImporterFunc(func(context.Context, []string) ([]model.ED2KSTRMResource, error) { return nil, nil })
	state = telegramState{}
	b.enqueueUpdates(&state, []telegramUpdate{cloudUpdate(2, 42, "private", "/ed2k_strm magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567")}, time.Now())
	if len(state.Queue) != 0 || len(state.Replies) != 1 || !strings.Contains(state.Replies[0].Text, "ED2K 文件链接") {
		t.Fatalf("其他协议错误进入导入：%+v", state)
	}
	links := make([]string, 0, 51)
	for i := 0; i < 51; i++ {
		links = append(links, fmt.Sprintf("ed2k://|file|movie%d.mkv|123|%032x|/", i, i+1))
	}
	state = telegramState{}
	b.enqueueUpdates(&state, []telegramUpdate{cloudUpdate(3, 42, "private", "/ed2k_strm "+strings.Join(links, "\n"))}, time.Now())
	if len(state.Queue) != 0 || len(state.Replies) != 1 || !strings.Contains(state.Replies[0].Text, "最多导入 50") {
		t.Fatalf("超量输入未完整拒绝：%+v", state)
	}
}

func readED2KSTRMBotState(t *testing.T, path string) telegramState {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var state telegramState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	return state
}

func TestED2KSTRMQueuedImportAndReplySurviveRestart(t *testing.T) {
	b := newBatchTestBot(t, nil)
	second := "ed2k://|file|episode.mkv|456|fedcba9876543210fedcba9876543210|/"
	imports := 0
	b.ED2KSTRM = ed2kSTRMImporterFunc(func(_ context.Context, links []string) ([]model.ED2KSTRMResource, error) {
		imports++
		persisted := readED2KSTRMBotState(t, b.StatePath)
		if persisted.Batch == nil || persisted.Batch.Mode != "ed2k_strm" || persisted.Batch.Next != 0 {
			t.Error("导入前没有持久保存活动批次")
		}
		if !reflect.DeepEqual(links, []string{botED2KLink, second}) {
			t.Errorf("链接不完整：%v", links)
		}
		return []model.ED2KSTRMResource{{Hash: "0123456789abcdef0123456789abcdef", Size: 123, Status: "ready"}, {Hash: "fedcba9876543210fedcba9876543210", Size: 456, Status: "queued"}}, nil
	})
	state := telegramState{}
	b.enqueueUpdates(&state, []telegramUpdate{cloudUpdate(9, 42, "private", "/ed2kstrm "+botED2KLink+"\n"+second)}, time.Now())
	if err := b.save(state); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/sendMessage") {
			t.Errorf("重启处理队列时发生额外请求：%s", r.URL.Path)
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		cancel()
	}))
	b.BaseURL = server.URL
	if err := b.Run(ctx); err != nil {
		t.Fatal(err)
	}
	server.Close()
	state = readED2KSTRMBotState(t, b.StatePath)
	if imports != 1 || state.Batch == nil || state.Batch.Next != 2 || state.Pending == nil || !strings.Contains(state.Pending.Text, "已生成：1 个；后台处理：1 个") {
		t.Fatalf("结果未持久保存：imports=%d state=%+v", imports, state)
	}

	ctx, cancelRestart := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelRestart()
	replies := 0
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/sendMessage") {
			replies++
			w.Write([]byte(`{"ok":true,"result":{"message_id":100}}`))
			return
		}
		w.Write([]byte(`{"ok":true,"result":[]}`))
		cancelRestart()
	}))
	defer server.Close()
	b.BaseURL = server.URL
	if err := b.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if imports != 1 || replies != 1 {
		t.Fatalf("补发回复重复导入或丢失汇总：imports=%d replies=%d", imports, replies)
	}
}

func TestED2KSTRMCanceledImportKeepsRecoverableBatch(t *testing.T) {
	b := newBatchTestBot(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	b.ED2KSTRM = ed2kSTRMImporterFunc(func(context.Context, []string) ([]model.ED2KSTRMResource, error) {
		cancel()
		return nil, context.Canceled
	})
	state := telegramState{Batch: &messageBatch{Mode: "ed2k_strm", UpdateID: 4, ChatID: 42, Items: []messageShare{{URL: botED2KLink}}}}
	if err := b.save(state); err != nil {
		t.Fatal(err)
	}
	if err := b.processBatchLink(ctx, &state); !errors.Is(err, context.Canceled) {
		t.Fatalf("未向上传递取消信号：%v", err)
	}
	if state.Batch.Next != 0 || state.Pending != nil {
		t.Fatalf("取消时错误推进批次：%+v", state)
	}
	persisted := readED2KSTRMBotState(t, b.StatePath)
	if persisted.Batch == nil || persisted.Batch.Next != 0 || persisted.Pending != nil {
		t.Fatalf("取消后无法重试：%+v", persisted)
	}
}

func TestED2KSTRMServiceInjectionDoesNotEnablePolling(t *testing.T) {
	h := &Hub{dataDir: t.TempDir(), ED2KSTRM: &service.ED2KSTRMService{}}
	profile := BotProfile{ID: "notifications-only", Token: "123:test", AllowedChatIDs: "42"}
	m := h.newManager(profile, false)
	if m.config.Enabled || pollsMessages(profile, "", false, false) {
		t.Fatal("只注入 ED2K STRM 服务就开启了额外 getUpdates 消费者")
	}
	worker, err := m.newWorker(m.config)
	if err != nil {
		t.Fatal(err)
	}
	if worker.ED2KSTRM == nil {
		t.Fatal("没有为已有接收 Bot 注入导入服务")
	}
}

func TestED2KSTRMImportErrorDoesNotExposeUpstreamDetails(t *testing.T) {
	b := newBatchTestBot(t, nil)
	b.Logs = &service.GenerationLogs{}
	const detail = "UID=secret; CID=private"
	b.ED2KSTRM = ed2kSTRMImporterFunc(func(context.Context, []string) ([]model.ED2KSTRMResource, error) {
		return nil, errors.New(detail)
	})
	state := telegramState{Batch: &messageBatch{Mode: "ed2k_strm", UpdateID: 4, ChatID: 42, Items: []messageShare{{URL: botED2KLink}}}}
	if err := b.processBatchLink(context.Background(), &state); err != nil {
		t.Fatal(err)
	}
	if state.Pending == nil || strings.Contains(state.Pending.Text, detail) || !strings.Contains(state.Pending.Text, "导入未完成") {
		t.Fatalf("上游原始错误泄露或未报告失败：%+v", state.Pending)
	}
	for _, entry := range b.Logs.Snapshot("DEBUG") {
		if strings.Contains(entry.Message, detail) {
			t.Fatal("上游原始错误进入日志")
		}
	}
}
