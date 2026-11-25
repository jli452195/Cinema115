package bot

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestTelegramTXTResumesReadAndQueuesCompleteED2KList(t *testing.T) {
	ctx := context.Background()
	b := newED2KSTRMTestBot(t)
	const count = 245
	var text strings.Builder
	for i := 1; i <= count; i++ {
		fmt.Fprintf(&text, "ed2k://|file|Film-%d.mkv|123|%032x|/\r\n", i, i)
	}
	var reads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/getFile"):
			if reads.Add(1) == 1 {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			fmt.Fprint(w, `{"ok":true,"result":{"file_path":"documents/list.txt"}}`)
		case strings.HasSuffix(r.URL.Path, "/documents/list.txt"):
			fmt.Fprint(w, text.String())
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	b.BaseURL, b.HTTP = server.URL, server.Client()
	update := cloudUpdate(7, 42, "private", "")
	update.Message.Caption = "/ed2kstrm"
	update.Message.Document = &telegramTextDocument{FileID: "file-id", FileName: "链接.txt"}
	state := telegramState{}
	now := time.Now()
	b.enqueueUpdates(ctx, &state, []telegramUpdate{update}, now)
	if len(state.TextDocuments) != 1 || len(state.Queue) != 0 || state.Offset != 8 {
		t.Fatalf("TXT 附件和游标应一起进入持久队列：%+v", state)
	}
	if !b.processReadyTextDocument(ctx, &state, now) || state.TextDocuments[0].Attempts != 1 || len(state.Replies) != 0 {
		t.Fatalf("瞬时下载失败应安排重试：%+v", state)
	}
	if err := b.save(state); err != nil {
		t.Fatal(err)
	}
	state = readED2KSTRMBotState(t, b.StatePath)
	b.enqueueUpdates(ctx, &state, []telegramUpdate{update}, now)
	if len(state.TextDocuments) != 1 || !b.processReadyTextDocument(ctx, &state, state.TextDocuments[0].ReadyAt) {
		t.Fatalf("重放不应重复登记同一份附件：%+v", state)
	}
	if len(state.TextDocuments) != 0 || len(state.Queue) != 1 || state.Queue[0].Mode != "ed2k_strm" || !state.Queue[0].TextDocument || len(state.Queue[0].Items) != count {
		t.Fatalf("完整 TXT 清单未按原消息入队：待下载 %d，任务 %d", len(state.TextDocuments), len(state.Queue))
	}
}

func TestTelegramTXTParsesPasswordsAndRejectsPartialInput(t *testing.T) {
	message := &telegramMessage{Caption: "提取码：zz99"}
	resources := parseTelegramResources(message, "https://115.com/s/first\nhttps://115.com/s/second 提取码：abcd")
	if len(resources.Shares) != 2 || resources.Shares[0].Password != "" || resources.Shares[1].Password != "abcd" {
		t.Fatalf("附件提取码匹配错误：%+v", resources.Shares)
	}
	b := newED2KSTRMTestBot(t)
	update := cloudUpdate(7, 42, "private", "")
	for _, tc := range []struct{ text, hint string }{
		{botED2KLink + "\ned2k://|file|truncated.mkv", "第 2 行"},
		{botED2KLink + "\nhttps://115.com/s/mixed", "请分开发送"},
	} {
		state := telegramState{}
		b.enqueueTextDocumentLinks(context.Background(), &state, update, tc.text, time.Now())
		if len(state.Queue) != 0 || len(state.ResourceSelections) != 0 || len(state.Replies) != 1 || !strings.Contains(state.Replies[0].Text, tc.hint) {
			t.Fatalf("不合法清单未整份拒绝：%+v", state)
		}
	}
}
