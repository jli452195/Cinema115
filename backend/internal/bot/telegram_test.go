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
	link, password, err := ParseMessage("链接：https://115cdn.com/s/abc\n提取码：fwgl")
	if err != nil || link != "https://115cdn.com/s/abc" || password != "fwgl" {
		t.Fatal(link, password, err)
	}
	if _, _, err := ParseMessage("https://115.com/s/abc https://115.com/s/def"); err == nil {
		t.Fatal("应拒绝多链接")
	}
	if link, _, _ := ParseMessage("https://115.com.attacker.test/s/abc"); link != "" {
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
				if strings.HasSuffix(r.URL.Path, "sendMessage") {
					replies.Add(1)
					if string(input["chat_id"]) != "42" {
						t.Error("向未授权聊天发送消息")
					}
					w.Write([]byte(`{"ok":true,"result":{}}`))
					return
				}
				if string(input["offset"]) == "12" {
					w.Write([]byte(`{"ok":true,"result":[]}`))
					cancel()
					return
				}
				w.Write([]byte(`{"ok":true,"result":[{"update_id":10,"message":{"chat":{"id":999},"text":"https://115.com/s/ignored"}},{"update_id":11,"message":{"chat":{"id":42},"text":"https://115.com/s/accepted"}}]}`))
			}))
			defer server.Close()
			bot.BaseURL = server.URL
			if resume {
				if err := bot.save(telegramState{Offset: 11, Pending: &pendingReply{UpdateID: 11, ChatID: 42, Text: "已保存的处理结果"}}); err != nil {
					t.Fatal(err)
				}
			}
			if err := bot.Run(ctx); err != nil {
				t.Fatal(err)
			}
			want := int32(1)
			if resume {
				want = 0
			}
			if generator.calls.Load() != want || replies.Load() != 1 {
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
