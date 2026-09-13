package bot

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"strm115/backend/internal/service"
)

type generatorFake struct{ calls atomic.Int32 }

func (g *generatorFake) Generate(_ context.Context, in service.GenerateInput) (*service.GenerationResult, error) {
	g.calls.Add(1)
	return &service.GenerationResult{Written: 1, ReshareURL: "https://115cdn.com/s/own?password=test", Cleaned: true}, nil
}

func TestTelegramMessageParsing(t *testing.T) {
	links := ParseMessages("链接：https://115cdn.com/s/abc\n提取码：fwgl")
	if len(links) != 1 || links[0].URL != "https://115cdn.com/s/abc" || links[0].Password != "fwgl" {
		t.Fatal(links)
	}
	if links := ParseMessages("https://115.com/s/abc https://115.com/s/def"); len(links) != 2 {
		t.Fatal("应接收全部链接")
	}
	if links := ParseMessages("https://115.com.attacker.test/s/abc"); len(links) != 0 {
		t.Fatal("错误的分享域名")
	}
}

func TestTelegramAllowlistAndDurablePendingReply(t *testing.T) {
	for _, resume := range []bool{false, true} {
		t.Run(map[bool]string{false: "正常消息", true: "重启回复"}[resume], func(t *testing.T) {
			dir := t.TempDir()
			store, err := service.NewAutomationStore(filepath.Join(dir, "settings.json"), dir, "http://nas:8115")
			if err != nil {
				t.Fatal(err)
			}
			generator := &generatorFake{}
			bot, err := NewTelegram("123:test", "42", dir, generator, store)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			var replies atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var input map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
					t.Error(err)
				}
				if strings.HasSuffix(r.URL.Path, "sendMessage") || strings.HasSuffix(r.URL.Path, "editMessageText") {
					replies.Add(1)
					if string(input["chat_id"]) != "42" {
						t.Error("向未授权聊天发送消息")
					}
					var reply struct {
						MessageID int64 `json:"message_id"`
						Allow     bool  `json:"allow_sending_without_reply"`
					}
					if strings.HasSuffix(r.URL.Path, "editMessageText") {
						if string(input["message_id"]) != "900" || input["reply_parameters"] != nil || input["message_thread_id"] != nil {
							t.Error("编辑没有复用 Bot 提示消息")
						}
					} else {
						if err := json.Unmarshal(input["reply_parameters"], &reply); err != nil {
							t.Error(err)
						}
						if reply.MessageID != 711 || !reply.Allow || string(input["message_thread_id"]) != "77" {
							t.Error("没有引用当前聊天的原消息或丢失话题")
						}
					}
					w.Write([]byte(`{"ok":true,"result":{"message_id":900}}`))
					return
				}
				if string(input["offset"]) == "12" {
					w.Write([]byte(`{"ok":true,"result":[]}`))
					cancel()
					return
				}
				w.Write([]byte(`{"ok":true,"result":[{"update_id":10,"message":{"chat":{"id":999},"text":"https://115.com/s/ignored"}},{"update_id":11,"message":{"message_id":711,"message_thread_id":77,"forward_origin":{"type":"channel","message_id":99999},"chat":{"id":42},"text":"https://115.com/s/accepted"}}]}`))
			}))
			defer server.Close()
			bot.BaseURL = server.URL
			if resume {
				if err := bot.save(telegramState{Offset: 11, Pending: &pendingReply{UpdateID: 11, ChatID: 42, MessageID: 711, ThreadID: 77, Text: "已保存的处理结果"}}); err != nil {
					t.Fatal(err)
				}
			}
			if err := bot.Run(ctx); err != nil {
				t.Fatal(err)
			}
			want := int32(1)
			wantReplies := int32(2)
			if resume {
				want = 0
				wantReplies = 1
			}
			if generator.calls.Load() != want || replies.Load() != wantReplies {
				t.Fatal("重复转存或回复数量异常", generator.calls.Load(), replies.Load())
			}
			data, err := os.ReadFile(bot.StatePath)
			if err != nil {
				t.Fatal(err)
			}
			var state telegramState
			if err := json.Unmarshal(data, &state); err != nil {
				t.Fatal(err)
			}
			if state.Offset != 12 || state.Pending != nil {
				t.Fatal("消息处理进度未持久化")
			}
		})
	}
}

func TestTelegramRequiresAllowlist(t *testing.T) {
	if _, err := NewTelegram("123:test", "", t.TempDir(), nil, nil); err == nil {
		t.Fatal("配置了 Bot 却没有聊天白名单")
	}
}
