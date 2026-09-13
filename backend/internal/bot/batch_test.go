package bot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/service"
)

type batchGeneratorFunc func(context.Context, service.GenerateInput) (*service.GenerationResult, error)

func TestBatchReviewStopsOnlyCurrentLink(t *testing.T) {
	calls := 0
	logs := &service.GenerationLogs{}
	b := newBatchTestBot(t, &service.LoggedGenerator{Logs: logs, Generator: batchGeneratorFunc(func(_ context.Context, in service.GenerateInput) (*service.GenerationResult, error) {
		calls++
		if strings.HasSuffix(in.URL, "/review") {
			return nil, fmt.Errorf("检查分享状态: %w", p115.ErrShareUnderReview)
		}
		return &service.GenerationResult{Written: 8}, nil
	})})
	state := telegramState{Batch: &messageBatch{UpdateID: 1, ChatID: 42, Items: []messageShare{{URL: "https://115.com/s/review"}, {URL: "https://115.com/s/normal"}}}}
	if err := b.processBatchLink(context.Background(), &state); err != nil {
		t.Fatal(err)
	}
	if state.Batch.Next != 1 || !strings.Contains(state.Pending.Text, "❌ 【115网盘】") || !strings.Contains(state.Pending.Text, "审核") {
		t.Fatal("审核结果不明确")
	}
	state.Pending = nil
	if err := b.processBatchLink(context.Background(), &state); err != nil {
		t.Fatal(err)
	}
	if calls != 2 || state.Batch.Next != 2 || !strings.Contains(state.Pending.Text, "【2/2】") || !strings.Contains(state.Pending.Text, "成功 8 个") {
		t.Fatal("审核影响了后续链接")
	}
	created, progress := 0, 0
	for _, entry := range logs.Snapshot("INFO") {
		if entry.Source != "Telegram" {
			t.Fatal("批次日志缺少来源")
		}
		if entry.Message == "创建新的消息处理批次，2 个链接" {
			created++
		}
		if entry.Message == "消息批次 1：正在处理 1/2" || entry.Message == "消息批次 1：正在处理 2/2" {
			progress++
		}
	}
	if created != 1 || progress != 2 {
		t.Fatal("批次数量或序号日志错误")
	}
}

func (f batchGeneratorFunc) Generate(ctx context.Context, in service.GenerateInput) (*service.GenerationResult, error) {
	return f(ctx, in)
}

func newBatchTestBot(t *testing.T, generator ShareGenerator) *Telegram {
	t.Helper()
	dir := t.TempDir()
	store, err := service.NewAutomationStore(filepath.Join(dir, "settings.json"), dir, "http://nas:8115")
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewTelegram("123:test", "42", dir, generator, store)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestPollingReadsTextAndCaptionHiddenLinks(t *testing.T) {
	var generated []string
	b := newBatchTestBot(t, batchGeneratorFunc(func(_ context.Context, in service.GenerateInput) (*service.GenerationResult, error) {
		share, err := p115.ParseShareURL(in.URL)
		if err != nil {
			t.Fatal(err)
		}
		generated = append(generated, share.Code+":"+share.Password)
		return &service.GenerationResult{Written: 1}, nil
	}))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	replies := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/sendMessage") || strings.HasSuffix(r.URL.Path, "/editMessageText") {
			replies++
			w.Write([]byte(`{"ok":true,"result":{"message_id":900}}`))
			return
		}
		var input struct {
			Offset int64 `json:"offset"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		if input.Offset == 105 {
			w.Write([]byte(`{"ok":true,"result":[]}`))
			cancel()
			return
		}
		w.Write([]byte(`{"ok":true,"result":[
		{"update_id":101,"message":{"chat":{"id":99},"text":"获取链接","entities":[{"type":"text_link","offset":0,"length":4,"url":"https://115.com/s/denied"}]}},
		{"update_id":102,"message":{"chat":{"id":42},"from":{"is_bot":true},"text":"获取链接","entities":[{"type":"text_link","offset":0,"length":4,"url":"https://115.com/s/bot"}]}},
		{"update_id":103,"message":{"chat":{"id":42},"text":"获取链接","entities":[{"type":"text_link","offset":0,"length":4,"url":"https://115.com/s/text?password=abcd"}]}},
		{"update_id":104,"message":{"chat":{"id":42},"caption":"🎬 综艺\n🔗 获取链接","caption_entities":[{"type":"text_link","offset":9,"length":4,"url":"https://115cdn.com/s/swsa6rn3h9l?password=ayss"}]}}
		]}`))
	}))
	defer server.Close()
	b.BaseURL = server.URL
	if err := b.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(generated, []string{"text:abcd", "swsa6rn3h9l:ayss"}) || replies != 4 {
		t.Fatal("隐藏链接没有进入处理流程，或绕过聊天限制", generated, replies)
	}
}

func TestBatchContinuesAfterFailureAndProcessesLaterMessages(t *testing.T) {
	var generated []string
	sends, edits := 0, 0
	b := newBatchTestBot(t, batchGeneratorFunc(func(_ context.Context, in service.GenerateInput) (*service.GenerationResult, error) {
		generated = append(generated, in.URL)
		if strings.HasSuffix(in.URL, "/bad") {
			return nil, errors.New("示例失败")
		}
		return &service.GenerationResult{Written: 8}, nil
	}))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var replies []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Offset int64  `json:"offset"`
			Text   string `json:"text"`
			ChatID int64  `json:"chat_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			t.Error(err)
		}
		if strings.HasSuffix(r.URL.Path, "sendMessage") || strings.HasSuffix(r.URL.Path, "editMessageText") {
			if strings.HasSuffix(r.URL.Path, "sendMessage") {
				sends++
			} else {
				edits++
			}
			if in.ChatID != 42 {
				t.Error("回复了未授权聊天")
			}
			replies = append(replies, in.Text)
			w.Write([]byte(`{"ok":true,"result":{"message_id":900}}`))
			return
		}
		if in.Offset == 13 {
			w.Write([]byte(`{"ok":true,"result":[]}`))
			cancel()
			return
		}
		w.Write([]byte(`{"ok":true,"result":[{"update_id":11,"message":{"chat":{"id":42},"text":"https://115.com/s/a https://115.com/s/bad https://115.com/s/c https://115.com/s/a"}},{"update_id":12,"message":{"chat":{"id":42},"caption":"https://115.com/s/a"}}]}`))
	}))
	defer server.Close()
	b.BaseURL = server.URL
	if err := b.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(generated, []string{"https://115.com/s/a", "https://115.com/s/bad", "https://115.com/s/c", "https://115.com/s/a"}) {
		t.Fatal("漏处理、乱序或跨消息错误去重", generated)
	}
	if len(replies) != 6 || !strings.Contains(replies[0], "开始处理 3 个链接") || !strings.Contains(replies[2], "【2/3】") || !strings.Contains(replies[2], "示例失败") || !strings.Contains(replies[3], "成功 8 个") {
		t.Fatal("逐条结果不完整", replies)
	}
	if sends != 4 || edits != 2 {
		t.Fatal("每条输入消息的开始提示应由首条结果覆盖，后续结果独立保留", sends, edits)
	}
	data, err := os.ReadFile(b.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	var state telegramState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	if state.Offset != 13 || state.Batch != nil || state.Pending != nil {
		t.Fatal("批次未正确完成")
	}
}

func TestBatchRestartSkipsStartedItemAndResumesRemaining(t *testing.T) {
	for _, allowed := range []bool{true, false} {
		t.Run(map[bool]string{true: "继续剩余链接", false: "白名单移除"}[allowed], func(t *testing.T) {
			var generated []string
			b := newBatchTestBot(t, batchGeneratorFunc(func(_ context.Context, in service.GenerateInput) (*service.GenerationResult, error) {
				generated = append(generated, in.URL)
				return &service.GenerationResult{Written: 1}, nil
			}))
			if !allowed {
				delete(b.AllowedChats, 42)
			}
			state := telegramState{Offset: 11, Batch: &messageBatch{UpdateID: 11, ChatID: 42, Items: []messageShare{{URL: "https://115.com/s/already-started"}, {URL: "https://115.com/s/remaining"}}, Next: 1}, Pending: &pendingReply{UpdateID: 11, ChatID: 42, Text: "前一个链接已开始，不自动重做"}}
			if err := b.save(state); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			replies := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "sendMessage") || strings.HasSuffix(r.URL.Path, "editMessageText") {
					replies++
					w.Write([]byte(`{"ok":true,"result":{"message_id":900}}`))
					return
				}
				var in struct{ Offset int64 }
				json.NewDecoder(r.Body).Decode(&in)
				if in.Offset != 12 {
					t.Error("批次结束前推进了 offset")
				}
				w.Write([]byte(`{"ok":true,"result":[]}`))
				cancel()
			}))
			defer server.Close()
			b.BaseURL = server.URL
			if err := b.Run(ctx); err != nil {
				t.Fatal(err)
			}
			if allowed {
				if !reflect.DeepEqual(generated, []string{"https://115.com/s/remaining"}) || replies != 2 {
					t.Fatal("重复执行或丢失剩余链接", generated, replies)
				}
			} else if len(generated) != 0 || replies != 0 {
				t.Fatal("白名单移除后继续了批次")
			}
		})
	}
}

func TestBatchCheckpointPrecedesGeneration(t *testing.T) {
	var b *Telegram
	b = newBatchTestBot(t, batchGeneratorFunc(func(_ context.Context, _ service.GenerateInput) (*service.GenerationResult, error) {
		data, err := os.ReadFile(b.StatePath)
		if err != nil {
			t.Fatal(err)
		}
		var disk telegramState
		if err := json.Unmarshal(data, &disk); err != nil {
			t.Fatal(err)
		}
		if disk.Batch.Next != 1 || disk.Pending == nil || !strings.Contains(disk.Pending.Text, "中断") {
			t.Fatal("云端变更前未持久化检查点")
		}
		return &service.GenerationResult{Written: 1}, nil
	}))
	state := telegramState{Batch: &messageBatch{UpdateID: 1, ChatID: 42, Items: []messageShare{{URL: "https://115.com/s/a"}, {URL: "https://115.com/s/b"}}}}
	if err := b.processBatchLink(context.Background(), &state); err != nil {
		t.Fatal(err)
	}
	if state.Batch.Next != 1 || !strings.Contains(state.Pending.Text, "成功 1 个") {
		t.Fatal("处理结果未保存")
	}
}
