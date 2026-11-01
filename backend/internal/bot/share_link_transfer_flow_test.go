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

func TestShareTransferPollingSelectsDirectoryAndCompletesBatch(t *testing.T) {
	b := newBatchTestBot(t, nil)
	b.ShareEnabled = false
	b.ShareTransferTargets = []TelegramTransferTarget{
		testShareTransferTarget("animation", "动画", "/动画", "bot-1"),
		testShareTransferTarget("movies", "电影", "/电影", "bot-1"),
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
				steps <- "choose"
				fmt.Fprint(w, `{"ok":true,"result":[{"update_id":11,"callback_query":{"id":"choose-movies","from":{"id":7},"message":{"message_id":500,"chat":{"id":42,"type":"private"}},"data":"share:10:movies"}}]}`)
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
			case strings.Contains(text, "请选择接收目录"):
				steps <- "prompt"
				if input["reply_markup"] == nil {
					t.Error("directory prompt has no inline keyboard")
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
	if transfer.calls != 2 || transfer.target.ID != "movies" || transfer.target.TargetPath != "/电影" || transfer.url != "https://115.com/s/second" || transfer.password != "efgh" {
		t.Fatalf("selected multi-link batch was not submitted intact: %+v", transfer)
	}
	got := make([]string, 0, len(steps))
	for len(steps) > 0 {
		got = append(got, <-steps)
	}
	want := []string{"receive", "prompt", "choose", "acknowledge", "start", "result", "result"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("incomplete receive/select/submit/reply flow: got %v want %v", got, want)
	}
}
