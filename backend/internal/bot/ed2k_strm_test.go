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

type ed2kSTRMImporterFunc func(context.Context, model.ED2KSTRMTarget, []string, string) (model.ED2KSTRMImportResult, error)

func (f ed2kSTRMImporterFunc) ImportToTarget(ctx context.Context, target model.ED2KSTRMTarget, links []string, requestID string) (model.ED2KSTRMImportResult, error) {
	return f(ctx, target, links, requestID)
}

func botED2KTarget(id, name, output, botID string) model.ED2KSTRMTarget {
	return model.ED2KSTRMTarget{ID: id, Name: name, OutputDir: output, AssignedBotID: botID}
}

func newED2KSTRMTestBot(t *testing.T) *Telegram {
	t.Helper()
	b := newBatchTestBot(t, nil)
	b.BotID = "bot-a"
	b.ED2KSTRMTargets = []model.ED2KSTRMTarget{botED2KTarget("target-a", "电影", "ed2k/movies", b.BotID)}
	b.ED2KSTRM = ed2kSTRMImporterFunc(func(context.Context, model.ED2KSTRMTarget, []string, string) (model.ED2KSTRMImportResult, error) {
		return model.ED2KSTRMImportResult{}, nil
	})
	return b
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

func TestED2KSTRMCommandHonorsAllowedChatsBotFilterAndPrivateChat(t *testing.T) {
	b := newED2KSTRMTestBot(t)
	botMessage := cloudUpdate(2, 42, "private", "/ed2kstrm "+botED2KLink)
	botMessage.Message.From.IsBot = true
	caption := cloudUpdate(4, 42, "private", "")
	caption.Message.Caption = "/ed2kstrm 导入文件"
	caption.Message.CaptionEntities = []telegramEntity{{Type: "text_link", Offset: 10, Length: 4, URL: botED2KLink}}
	state := telegramState{}
	b.enqueueUpdates(context.Background(), &state, []telegramUpdate{cloudUpdate(1, 999, "private", "/ed2k_strm "+botED2KLink), botMessage, cloudUpdate(3, 42, "supergroup", "/ed2kstrm "+botED2KLink), caption}, time.Now())
	if len(state.Queue) != 1 || state.Queue[0].UpdateID != 4 || state.Queue[0].Items[0].URL != botED2KLink || len(state.Replies) != 1 || !strings.Contains(state.Replies[0].Text, "私聊") {
		t.Fatalf("聊天授权、Bot 过滤或图文入口有误：%+v", state)
	}
}

func TestED2KSTRMCommandRequiresAssignedTargetAndValidLinkCount(t *testing.T) {
	b := newED2KSTRMTestBot(t)
	b.ED2KSTRMTargets = nil
	state := telegramState{}
	b.enqueueUpdates(context.Background(), &state, []telegramUpdate{cloudUpdate(1, 42, "private", "/ed2k_strm "+botED2KLink)}, time.Now())
	if len(state.Queue) != 0 || len(state.Replies) != 1 || !strings.Contains(state.Replies[0].Text, "尚未分配") {
		t.Fatalf("未分配目标的 Bot 得到了全局导入权限：%+v", state)
	}
	b.ED2KSTRMTargets = []model.ED2KSTRMTarget{botED2KTarget("target-a", "电影", "ed2k/movies", "another-bot")}
	state = telegramState{}
	b.enqueueUpdates(context.Background(), &state, []telegramUpdate{cloudUpdate(2, 42, "private", "/ed2k_strm "+botED2KLink)}, time.Now())
	if len(state.Queue) != 0 || len(state.Replies) != 1 {
		t.Fatalf("使用了分配给其他 Bot 的目标：%+v", state)
	}
	b.ED2KSTRMTargets[0].AssignedBotID = b.BotID
	state = telegramState{}
	b.enqueueUpdates(context.Background(), &state, []telegramUpdate{cloudUpdate(3, 42, "private", "/ed2k_strm magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567")}, time.Now())
	if len(state.Queue) != 0 || len(state.ResourceSelections) != 0 || len(state.Replies) != 1 {
		t.Fatalf("其他协议错误进入导入：%+v", state)
	}
	links := make([]string, 0, 51)
	for i := 0; i < 51; i++ {
		links = append(links, fmt.Sprintf("ed2k://|file|movie%d.mkv|123|%032x|/", i, i+1))
	}
	state = telegramState{}
	b.enqueueUpdates(context.Background(), &state, []telegramUpdate{cloudUpdate(4, 42, "private", "/ed2k_strm "+strings.Join(links, "\n"))}, time.Now())
	if len(state.Queue) != 0 || len(state.Replies) != 1 || !strings.Contains(state.Replies[0].Text, "50") {
		t.Fatalf("超量输入未完整拒绝：%+v", state)
	}
}

func TestED2KSTRMQueuedImportAndReplySurviveRestart(t *testing.T) {
	b := newED2KSTRMTestBot(t)
	second := "ed2k://|file|episode.mkv|456|fedcba9876543210fedcba9876543210|/"
	imports := 0
	b.ED2KSTRM = ed2kSTRMImporterFunc(func(_ context.Context, target model.ED2KSTRMTarget, links []string, requestID string) (model.ED2KSTRMImportResult, error) {
		imports++
		persisted := readED2KSTRMBotState(t, b.StatePath)
		if persisted.Batch == nil || persisted.Batch.Mode != "ed2k_strm" || persisted.Batch.Next != 0 || persisted.Batch.ED2KRequestID != requestID {
			t.Error("导入前没有持久保存批次及请求编号")
		}
		if target != persisted.Batch.ED2KSTRMTarget || target != b.ED2KSTRMTargets[0] || !reflect.DeepEqual(links, []string{botED2KLink, second}) || requestID != b.ed2kSTRMRequestID(9, target.ID) {
			t.Errorf("目标快照、链接或请求编号有误：%+v %v %s", target, links, requestID)
		}
		return model.ED2KSTRMImportResult{Resources: []model.ED2KSTRMResource{{Status: "ready"}}, Tasks: []model.ED2KSTRMTask{{Status: "done"}, {Status: "queued"}}}, nil
	})
	state := telegramState{}
	b.enqueueUpdates(context.Background(), &state, []telegramUpdate{cloudUpdate(9, 42, "private", "/ed2kstrm "+botED2KLink+"\n"+second)}, time.Now())
	if err := b.save(state); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/sendMessage") {
			t.Errorf("恢复队列时发生额外请求：%s", r.URL.Path)
		}
		w.WriteHeader(503)
		cancel()
	}))
	b.BaseURL = server.URL
	if err := b.Run(ctx); err != nil {
		t.Fatal(err)
	}
	server.Close()
	state = readED2KSTRMBotState(t, b.StatePath)
	if imports != 1 || state.Batch == nil || state.Batch.Next != 2 || state.Pending == nil || !strings.Contains(state.Pending.Text, "输出任务已接收：2 个") || strings.Contains(state.Pending.Text, "已生成") {
		t.Fatalf("没有保存任务接收回执：imports=%d state=%+v", imports, state)
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

func TestED2KSTRMCanceledImportReusesRequestIDWithoutAnotherExport(t *testing.T) {
	b := newED2KSTRMTestBot(t)
	state := telegramState{}
	b.enqueueUpdates(context.Background(), &state, []telegramUpdate{cloudUpdate(4, 42, "private", "/ed2kstrm "+botED2KLink)}, time.Now())
	if !b.startReadyBatch(&state, time.Now()) || state.Pending != nil {
		t.Fatal("入队操作不应发送开始状态")
	}
	if err := b.save(state); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	accepted := map[string]bool{}
	exports, calls := 0, 0
	b.ED2KSTRM = ed2kSTRMImporterFunc(func(_ context.Context, _ model.ED2KSTRMTarget, _ []string, id string) (model.ED2KSTRMImportResult, error) {
		calls++
		if !accepted[id] {
			exports++
			accepted[id] = true
		}
		if calls == 1 {
			cancel()
			return model.ED2KSTRMImportResult{}, context.Canceled
		}
		return model.ED2KSTRMImportResult{Tasks: []model.ED2KSTRMTask{{}}}, nil
	})
	if err := b.processBatchLink(ctx, &state); !errors.Is(err, context.Canceled) {
		t.Fatalf("未传递取消信号：%v", err)
	}
	if state.Batch.Next != 0 || state.Pending != nil {
		t.Fatalf("取消时错误推进批次：%+v", state)
	}
	state = readED2KSTRMBotState(t, b.StatePath)
	if err := b.processBatchLink(context.Background(), &state); err != nil {
		t.Fatal(err)
	}
	if calls != 2 || exports != 1 || state.Batch.Next != 1 {
		t.Fatalf("重启后没有复用相同请求：calls=%d exports=%d state=%+v", calls, exports, state)
	}
}

func TestED2KSTRMTargetChangesStopQueuedBatch(t *testing.T) {
	for _, change := range []string{"directory", "assignment", "deleted"} {
		t.Run(change, func(t *testing.T) {
			b := newED2KSTRMTestBot(t)
			state := telegramState{}
			b.enqueueUpdates(context.Background(), &state, []telegramUpdate{cloudUpdate(7, 42, "private", "/ed2kstrm "+botED2KLink)}, time.Now())
			state.Batch, state.Queue = state.Queue[0], nil
			calls := 0
			b.ED2KSTRM = ed2kSTRMImporterFunc(func(context.Context, model.ED2KSTRMTarget, []string, string) (model.ED2KSTRMImportResult, error) {
				calls++
				return model.ED2KSTRMImportResult{}, nil
			})
			switch change {
			case "directory":
				b.ED2KSTRMTargets[0].OutputDir = "changed"
			case "assignment":
				b.ED2KSTRMTargets[0].AssignedBotID = "bot-b"
			case "deleted":
				b.ED2KSTRMTargets = nil
			}
			if err := b.processBatchLink(context.Background(), &state); err != nil {
				t.Fatal(err)
			}
			if calls != 0 || state.Pending == nil || !strings.Contains(state.Pending.Text, "未提交") {
				t.Fatalf("排队目标变化后仍提交：calls=%d pending=%+v", calls, state.Pending)
			}
		})
	}
}

func TestED2KSTRMRequestIDScopesBotMessageAndTarget(t *testing.T) {
	b := newED2KSTRMTestBot(t)
	id := b.ed2kSTRMRequestID(7, "target-a")
	b.Token = "123:rotated-secret"
	if b.ed2kSTRMRequestID(7, "target-a") != id {
		t.Fatal("更换同一 Bot 的令牌改变请求身份")
	}
	if b.ed2kSTRMRequestID(8, "target-a") == id || b.ed2kSTRMRequestID(7, "target-b") == id {
		t.Fatal("请求编号没有隔离消息和目标")
	}
	b.Token = "456:rotated-secret"
	if b.ed2kSTRMRequestID(7, "target-a") == id {
		t.Fatal("不同 Bot 共用了请求编号")
	}
}

func TestED2KSTRMImportErrorDoesNotExposeUpstreamDetails(t *testing.T) {
	b := newED2KSTRMTestBot(t)
	b.Logs = &service.GenerationLogs{}
	const detail = "UID=secret; CID=private"
	b.ED2KSTRM = ed2kSTRMImporterFunc(func(context.Context, model.ED2KSTRMTarget, []string, string) (model.ED2KSTRMImportResult, error) {
		return model.ED2KSTRMImportResult{}, errors.New(detail)
	})
	state := telegramState{}
	b.enqueueUpdates(context.Background(), &state, []telegramUpdate{cloudUpdate(4, 42, "private", "/ed2kstrm "+botED2KLink)}, time.Now())
	state.Batch, state.Queue = state.Queue[0], nil
	if err := b.processBatchLink(context.Background(), &state); err != nil {
		t.Fatal(err)
	}
	if state.Pending == nil || strings.Contains(state.Pending.Text, detail) || !strings.Contains(state.Pending.Text, "导入未完成") {
		t.Fatalf("泄露上游错误或没有报告失败：%+v", state.Pending)
	}
	for _, entry := range b.Logs.Snapshot("DEBUG") {
		if strings.Contains(entry.Message, detail) {
			t.Fatal("上游原始错误进入日志")
		}
	}
}
