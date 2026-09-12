package bot

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/service"
)

type batchGeneratorFunc func(context.Context, service.GenerateInput) (*service.GenerationResult, error)

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

func TestBatchContinuesAfterFailureAndProcessesLaterMessages(t *testing.T) {
	var generated []string
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
		if strings.HasSuffix(r.URL.Path, "sendMessage") {
			if in.ChatID != 42 {
				t.Error("回复了未授权聊天")
			}
			replies = append(replies, in.Text)
			w.Write([]byte(`{"ok":true,"result":{}}`))
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
	if len(replies) != 5 || !strings.Contains(replies[0], "已接收 3") || !strings.Contains(replies[2], "【2/3】") || !strings.Contains(replies[2], "示例失败") || !strings.Contains(replies[3], "新增 8") {
		t.Fatal("逐条结果不完整", replies)
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
				if strings.HasSuffix(r.URL.Path, "sendMessage") {
					replies++
					w.Write([]byte(`{"ok":true,"result":{}}`))
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
	if state.Batch.Next != 1 || !strings.Contains(state.Pending.Text, "新增 1") {
		t.Fatal("处理结果未保存")
	}
}
