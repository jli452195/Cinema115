package bot

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/service"
)

func TestResultEditsStartMessage(t *testing.T) {
	for _, fails := range []bool{false, true} {
		name := "成功覆盖提示"
		if fails {
			name = "失败覆盖提示"
		}
		t.Run(name, func(t *testing.T) {
			generated, sends, edits := 0, 0, 0
			var bot *Telegram
			bot = newBatchTestBot(t, batchGeneratorFunc(func(context.Context, service.GenerateInput) (*service.GenerationResult, error) {
				generated++
				data, err := os.ReadFile(bot.StatePath)
				if err != nil {
					t.Fatal(err)
				}
				var state telegramState
				if err := json.Unmarshal(data, &state); err != nil {
					t.Fatal(err)
				}
				if state.Batch.StatusMessageID != 900 || state.Pending.EditMessageID != 900 {
					t.Fatal("生成前没有持久化提示 ID 和编辑检查点")
				}
				if fails {
					return nil, p115.ErrShareUnderReview
				}
				return &service.GenerationResult{Written: 1}, nil
			}))
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var input struct {
					Offset    int64  `json:"offset"`
					MessageID int64  `json:"message_id"`
					Text      string `json:"text"`
					Reply     *struct {
						ID int64 `json:"message_id"`
					} `json:"reply_parameters"`
				}
				if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
					t.Error(err)
				}
				switch {
				case strings.HasSuffix(r.URL.Path, "/sendMessage"):
					sends++
					if !strings.Contains(input.Text, "开始处理 1 个链接") || input.Reply == nil || input.Reply.ID != 711 {
						t.Error("开始提示或引用错误")
					}
					w.Write([]byte(`{"ok":true,"result":{"message_id":900}}`))
				case strings.HasSuffix(r.URL.Path, "/editMessageText"):
					edits++
					if input.MessageID != 900 || input.Reply != nil {
						t.Error("编辑了原用户消息或错误的提示")
					}
					want := "✅ 【115网盘】"
					if fails {
						want = "❌ 【115网盘】"
					}
					if !strings.HasPrefix(input.Text, want) {
						t.Error("最终结果不正确")
					}
					w.Write([]byte(`{"ok":true,"result":{"message_id":900}}`))
				default:
					if input.Offset == 12 {
						w.Write([]byte(`{"ok":true,"result":[]}`))
						cancel()
						return
					}
					w.Write([]byte(`{"ok":true,"result":[{"update_id":11,"message":{"message_id":711,"chat":{"id":42},"text":"https://115.com/s/test"}}]}`))
				}
			}))
			defer server.Close()
			bot.BaseURL = server.URL
			if err := bot.Run(ctx); err != nil {
				t.Fatal(err)
			}
			if generated != 1 || sends != 1 || edits != 1 {
				t.Fatal("没有在同一条提示上更新，或重复生成", generated, sends, edits)
			}
		})
	}
}

func TestPersistedEditsResumeAndMissingMessageFallback(t *testing.T) {
	for _, response := range []string{"success", "unchanged", "missing"} {
		t.Run(response, func(t *testing.T) {
			bot := newBatchTestBot(t, batchGeneratorFunc(func(context.Context, service.GenerateInput) (*service.GenerationResult, error) {
				t.Error("补发结果重新执行了生成")
				return nil, errors.New("不应执行")
			}))
			state := telegramState{Offset: 11, Batch: &messageBatch{UpdateID: 11, ChatID: 42, MessageID: 711, StatusMessageID: 900, Items: []messageShare{{URL: "https://115.com/s/test"}}, Next: 1}, Pending: &pendingReply{UpdateID: 11, ChatID: 42, MessageID: 711, EditMessageID: 900, Text: "✅ 已保存的结果"}}
			if err := bot.save(state); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			sends, edits := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasSuffix(r.URL.Path, "/editMessageText"):
					edits++
					if response == "unchanged" {
						w.WriteHeader(400)
						w.Write([]byte(`{"ok":false,"description":"Bad Request: message is not modified: specified new message content is exactly the same"}`))
						return
					}
					if response == "missing" {
						w.WriteHeader(400)
						w.Write([]byte(`{"ok":false,"description":"Bad Request: message to edit not found"}`))
						return
					}
					w.Write([]byte(`{"ok":true,"result":{"message_id":900}}`))
				case strings.HasSuffix(r.URL.Path, "/sendMessage"):
					sends++
					w.Write([]byte(`{"ok":true,"result":{"message_id":901}}`))
				default:
					w.Write([]byte(`{"ok":true,"result":[]}`))
					cancel()
				}
			}))
			defer server.Close()
			bot.BaseURL = server.URL
			if err := bot.Run(ctx); err != nil {
				t.Fatal(err)
			}
			wantSends := 0
			if response == "missing" {
				wantSends = 1
			}
			if edits != 1 || sends != wantSends {
				t.Fatal("编辑重试或删除后补发行为错误", edits, sends)
			}
			data, err := os.ReadFile(bot.StatePath)
			if err != nil {
				t.Fatal(err)
			}
			state = telegramState{}
			if err := json.Unmarshal(data, &state); err != nil {
				t.Fatal(err)
			}
			if state.Offset != 12 || state.Pending != nil || state.Batch != nil {
				t.Fatal("更新结果后未推进进度")
			}
		})
	}
}

func TestFailedEditKeepsSavedResult(t *testing.T) {
	bot := newBatchTestBot(t, batchGeneratorFunc(func(context.Context, service.GenerateInput) (*service.GenerationResult, error) {
		t.Fatal("编辑重试不应重新生成")
		return nil, nil
	}))
	state := telegramState{Pending: &pendingReply{UpdateID: 11, ChatID: 42, EditMessageID: 900, Text: "结果"}}
	if err := bot.save(state); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503); cancel() }))
	defer server.Close()
	bot.BaseURL = server.URL
	if err := bot.Run(ctx); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(bot.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	if state.Pending == nil || state.Pending.EditMessageID != 900 || state.Pending.Text != "结果" {
		t.Fatal("网络失败丢失待编辑结果")
	}
}
