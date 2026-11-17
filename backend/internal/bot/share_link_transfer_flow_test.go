package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestShareTransferPollingSelectsPurposeAndDirectoryAndCompletesBatch(t *testing.T) {
	generator := &generatorFake{}
	b := newBatchTestBot(t, generator)
	b.ShareTransferTargets = []TelegramTransferTarget{
		testShareTransferTarget("animation", "动画", "/动画", b.BotID),
		testShareTransferTarget("movies", "电影", "/电影", b.BotID),
	}
	transfer := &shareTransferSubmitterStub{}
	b.ShareTransfers = transfer
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	steps := make(chan string, 20)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input map[string]any
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Errorf("decode Telegram request: %v", err)
			cancel()
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]; method {
		case "getUpdates":
			if input["offset"] == float64(0) {
				steps <- "receive"
				fmt.Fprint(w, `{"ok":true,"result":[{"update_id":10,"message":{"message_id":110,"chat":{"id":42,"type":"private"},"from":{"id":7},"text":"https://115.com/s/first 提取码：abcd\nhttps://115.com/s/second 提取码：efgh"}}]}`)
			} else if input["offset"] == float64(11) {
				state := readED2KSTRMBotState(t, b.StatePath)
				if len(state.ResourceSelections) != 1 || state.ResourceSelections[0].PromptMessageID != 500 || len(state.ResourceSelections[0].Choices) != 3 || len(state.ResourceSelections[0].Items) != 2 || state.ResourceSelections[0].Items[0].Password != "abcd" || state.ResourceSelections[0].Items[1].Password != "efgh" || len(state.Queue) != 0 || transfer.calls != 0 || generator.calls.Load() != 0 {
					t.Error("回调之前没有持久保存双用途选择或提取码，或提前提交业务")
				}
				steps <- "choose"
				fmt.Fprint(w, `{"ok":true,"result":[{"update_id":11,"callback_query":{"id":"choose-movies","from":{"id":7},"message":{"message_id":500,"chat":{"id":42,"type":"private"}},"data":"resource:10:2"}}]}`)
			} else {
				t.Errorf("unexpected polling after the selected batch: %+v", input)
				cancel()
				fmt.Fprint(w, `{"ok":true,"result":[]}`)
			}
		case "answerCallbackQuery":
			steps <- "acknowledge"
			fmt.Fprint(w, `{"ok":true,"result":true}`)
		case "sendMessage", "editMessageText":
			text, _ := input["text"].(string)
			switch {
			case method == "editMessageText" && input["message_id"] == float64(500):
				steps <- "clear"
				if input["reply_markup"] == nil {
					t.Error("选择后没有清理统一键盘")
				}
				fmt.Fprint(w, `{"ok":true,"result":{"message_id":500}}`)
			case input["reply_markup"] != nil:
				steps <- "prompt"
				if !strings.Contains(text, "用途") {
					t.Error("双用途提示没有提供一次性的用途与目录选择")
				}
				fmt.Fprint(w, `{"ok":true,"result":{"message_id":500}}`)
			case strings.Contains(text, "开始处理"):
				steps <- "start"
				fmt.Fprint(w, `{"ok":true,"result":{"message_id":501}}`)
			case strings.Contains(text, "115 已接受分享转存请求"):
				if !strings.Contains(text, "/电影") {
					t.Errorf("result did not retain selected directory: %s", text)
				}
				steps <- "result"
				fmt.Fprint(w, `{"ok":true,"result":{"message_id":502}}`)
				if transfer.calls == 2 {
					cancel()
				}
			default:
				t.Errorf("unexpected Telegram reply: %s", text)
				cancel()
				fmt.Fprint(w, `{"ok":true,"result":{"message_id":503}}`)
			}
		default:
			t.Errorf("unexpected Telegram method: %s", method)
			cancel()
			fmt.Fprint(w, `{"ok":false}`)
		}
	}))
	defer server.Close()
	b.HTTP, b.BaseURL = server.Client(), server.URL
	if err := b.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if transfer.calls != 2 || generator.calls.Load() != 0 || transfer.target.ID != "movies" || transfer.target.TargetPath != "/电影" || transfer.url != "https://115.com/s/second" || transfer.password != "efgh" {
		t.Fatalf("selected multi-link batch was not submitted intact: %+v", transfer)
	}
	got := make([]string, 0, len(steps))
	for len(steps) > 0 {
		got = append(got, <-steps)
	}
	want := []string{"receive", "prompt", "choose", "acknowledge", "clear", "start", "result", "result"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("incomplete receive/select/submit/reply flow: got %v want %v", got, want)
	}
}
