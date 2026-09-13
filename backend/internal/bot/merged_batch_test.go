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

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/service"
)

type mergedTestGenerator struct {
	single batchGeneratorFunc
	merged func(context.Context, []service.GenerateInput) ([]service.BatchOutcome, error)
}

func (g mergedTestGenerator) Generate(ctx context.Context, in service.GenerateInput) (*service.GenerationResult, error) {
	return g.single(ctx, in)
}

func (g mergedTestGenerator) GenerateBatch(ctx context.Context, inputs []service.GenerateInput) ([]service.BatchOutcome, error) {
	return g.merged(ctx, inputs)
}

func TestMergedBatchCheckpointAndRepliesSurviveRestart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var b *Telegram
	calls := 0
	gen := mergedTestGenerator{
		single: func(context.Context, service.GenerateInput) (*service.GenerationResult, error) {
			t.Error("合并任务退回逐条转存")
			return nil, errors.New("不应调用")
		},
		merged: func(_ context.Context, inputs []service.GenerateInput) ([]service.BatchOutcome, error) {
			calls++
			data, err := os.ReadFile(b.StatePath)
			if err != nil {
				t.Fatal(err)
			}
			var checkpoint telegramState
			if err := json.Unmarshal(data, &checkpoint); err != nil {
				t.Fatal(err)
			}
			if checkpoint.Batch.Next != 3 || checkpoint.Pending == nil || len(checkpoint.Replies) != 0 {
				t.Fatal("云端操作之前未保存全部中断回复")
			}
			if len(inputs) != 3 || inputs[0].Mode != "reshare" {
				t.Fatal("未统一提交转存输入")
			}
			cancel()
			return []service.BatchOutcome{
				{Result: &service.GenerationResult{Written: 1, ReshareURL: "https://115.com/s/own", Cleaned: true}},
				{Err: fmt.Errorf("%w；已安排审核重试", p115.ErrShareUnderReview)},
				{Result: &service.GenerationResult{Written: 2, ReshareURL: "https://115.com/s/own", Cleaned: true}},
			}, nil
		},
	}
	b = newBatchTestBot(t, gen)
	state := telegramState{Offset: 4, Batch: &messageBatch{Mode: "reshare", UpdateID: 1, ChatID: 42, MessageID: 101, ThreadID: 7, StatusMessageID: 900, SourceMessageIDs: []int64{101, 102, 103}, Items: []messageShare{{URL: "https://115.com/s/a"}, {URL: "https://115.com/s/review"}, {URL: "https://115.com/s/b"}}}}
	if err := b.save(state); err != nil {
		t.Fatal(err)
	}
	if err := b.Run(ctx); err != nil {
		t.Fatal(err)
	}
	restartCtx, stop := context.WithTimeout(context.Background(), 3*time.Second)
	defer stop()
	var methods []string
	var refs []int64
	var texts []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		if method == "getUpdates" {
			fmt.Fprint(w, `{"ok":true,"result":[]}`)
			stop()
			return
		}
		var in struct {
			Text      string `json:"text"`
			MessageID int64  `json:"message_id"`
			Reply     struct {
				ID int64 `json:"message_id"`
			} `json:"reply_parameters"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			t.Error(err)
		}
		methods = append(methods, method)
		refs = append(refs, in.Reply.ID)
		texts = append(texts, in.Text)
		if method == "editMessageText" && in.MessageID != 900 {
			t.Error("没有覆盖原提示")
		}
		fmt.Fprint(w, `{"ok":true,"result":{"message_id":901}}`)
	}))
	defer server.Close()
	b.BaseURL = server.URL
	if err := b.Run(restartCtx); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || !reflect.DeepEqual(methods, []string{"editMessageText"}) || !reflect.DeepEqual(refs, []int64{0}) {
		t.Fatal("重启重复转存、丢失结果或引用错误", calls, methods, refs)
	}
	if !strings.Contains(texts[0], "已安排审核重试") || strings.Count(texts[0], "https://115.com/s/own") != 1 || !strings.Contains(texts[0], "成功 2 个，失败 1 个") || !strings.Contains(texts[0], "成功 3 个，失败 0 个") {
		t.Fatal("审核和共用分享回复不明确")
	}
}

func TestReadyDirectBatchPrecedesMergedBatch(t *testing.T) {
	b := newBatchTestBot(t, nil)
	now := time.Now()
	state := telegramState{Queue: []*messageBatch{
		{ChatID: 42, Mode: "reshare", ReadyAt: now, Items: []messageShare{{URL: "https://115.com/s/a"}}},
		{ChatID: 42, Mode: "direct", ReadyAt: now, Items: []messageShare{{URL: "https://115.com/s/b"}}},
	}}
	if !b.startReadyBatch(&state, now) || state.Batch.Mode != "direct" {
		t.Fatal("普通分享没有优先直接处理")
	}
}

func TestMergedBatchSendsOnePromptAndEditsOneSummary(t *testing.T) {
	calls := 0
	b := newBatchTestBot(t, mergedTestGenerator{merged: func(_ context.Context, inputs []service.GenerateInput) ([]service.BatchOutcome, error) {
		calls++
		if len(inputs) != 2 {
			t.Error("没有合并两个来源")
		}
		return []service.BatchOutcome{{Result: &service.GenerationResult{Written: 1, ReshareURL: "https://115.com/s/own", Cleaned: true}}, {Result: &service.GenerationResult{Written: 2, ReshareURL: "https://115.com/s/own", Cleaned: true}}}, nil
	}})
	settings := b.Settings.Value()
	settings.ReshareEnabled = true
	settings.TGBatchWaitSeconds = 0
	if err := b.Settings.Save(settings); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var methods []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		var input struct {
			Offset    int64  `json:"offset"`
			MessageID int64  `json:"message_id"`
			Text      string `json:"text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		if method == "getUpdates" {
			if input.Offset == 0 {
				fmt.Fprint(w, `{"ok":true,"result":[{"update_id":1,"message":{"message_id":101,"chat":{"id":42},"text":"https://115.com/s/a https://115.com/s/b"}}]}`)
			} else {
				fmt.Fprint(w, `{"ok":true,"result":[]}`)
				cancel()
			}
			return
		}
		methods = append(methods, method)
		if method == "editMessageText" && (input.MessageID != 900 || !strings.Contains(input.Text, "成功 2 个，失败 0 个") || strings.Count(input.Text, "https://115.com/s/own") != 1) {
			t.Error("没有原地覆盖成一条汇总", input.Text)
		}
		fmt.Fprint(w, `{"ok":true,"result":{"message_id":900}}`)
	}))
	defer server.Close()
	b.BaseURL = server.URL
	if err := b.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || !reflect.DeepEqual(methods, []string{"sendMessage", "editMessageText"}) {
		t.Fatal("批次重复发送了结果", calls, methods)
	}
}
