package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/service"
)

func collectionUpdate(id, chat, thread int64, link string) telegramUpdate {
	m := &telegramMessage{ID: id + 100, ThreadID: thread, Text: link}
	m.Chat.ID = chat
	return telegramUpdate{ID: id, Message: m}
}

func TestCollectionWindowIsolationAndDirectBypass(t *testing.T) {
	b := newBatchTestBot(t, nil)
	b.AllowedChats[43] = true
	settings := b.Settings.Value()
	settings.ReshareEnabled = true
	settings.FixedSharePassword = "abcd"
	if err := b.Settings.Save(settings); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	state := telegramState{}
	b.enqueueUpdates(&state, []telegramUpdate{collectionUpdate(1, 42, 7, "https://115.com/s/a")}, now)
	b.enqueueUpdates(&state, []telegramUpdate{
		collectionUpdate(2, 43, 7, "https://115.com/s/b"),
		collectionUpdate(3, 42, 8, "https://115.com/s/c"),
		collectionUpdate(4, 42, 7, "https://115.com/s/d"),
		collectionUpdate(5, 42, 7, "https://115.com/s/e?password=abcd"),
		collectionUpdate(6, 99, 7, "https://115.com/s/denied"),
	}, now.Add(4*time.Second))
	if state.Offset != 7 || len(state.Queue) != 3 || len(state.Queue[0].Items) != 3 || !state.Queue[0].ReadyAt.Equal(now.Add(9*time.Second)) {
		t.Fatal("窗口未顺延、聊天混合或丢失消息", state)
	}
	if state.Queue[0].SourceMessageIDs[1] != 104 {
		t.Fatal("合并丢失原消息引用")
	}
	settings.ReshareEnabled = false
	if err := b.Settings.Save(settings); err != nil {
		t.Fatal(err)
	}
	b.enqueueUpdates(&state, []telegramUpdate{collectionUpdate(7, 42, 7, "https://115.com/s/direct")}, now.Add(4*time.Second))
	if !b.startReadyBatch(&state, now.Add(4*time.Second)) || state.Batch.Mode != "direct" || state.Batch.Items[0].URL != "https://115.com/s/direct" {
		t.Fatal("直接生成被转存等待阻塞")
	}
	state.Batch, state.Pending = nil, nil
	if b.startReadyBatch(&state, now.Add(4*time.Second)) {
		t.Fatal("窗口结束前开始了转存")
	}
	if b.startReadyBatch(&state, now.Add(5*time.Second)) {
		t.Fatal("仍按首条消息截止时间执行")
	}
	if !b.startReadyBatch(&state, now.Add(9*time.Second)) || len(state.Batch.Items) != 3 {
		t.Fatal("窗口结束未统一处理")
	}
}

func TestCollectionZeroWaitAndNormalMode(t *testing.T) {
	for _, reshare := range []bool{false, true} {
		b := newBatchTestBot(t, nil)
		settings := b.Settings.Value()
		settings.ReshareEnabled = reshare
		if reshare {
			settings.TGBatchWaitSeconds = 0
		}
		if err := b.Settings.Save(settings); err != nil {
			t.Fatal(err)
		}
		now := time.Now()
		state := telegramState{}
		b.enqueueUpdates(&state, []telegramUpdate{collectionUpdate(1, 42, 0, "https://115.com/s/a")}, now)
		if !b.startReadyBatch(&state, now) {
			t.Fatal("普通模式或零等待没有立即执行")
		}
	}
}

func TestCollectionSlidingWindowHasNoMaximumAndDuplicatesDoNotDelay(t *testing.T) {
	b := newBatchTestBot(t, nil)
	settings := b.Settings.Value()
	settings.ReshareEnabled, settings.TGBatchWaitSeconds = true, 5
	if err := b.Settings.Save(settings); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	state := telegramState{}
	for i := int64(0); i < 12; i++ {
		at := now.Add(time.Duration(i) * 4 * time.Second)
		b.enqueueUpdates(&state, []telegramUpdate{collectionUpdate(i+1, 42, 7, fmt.Sprintf("https://115.com/s/item%d", i))}, at)
		if len(state.Queue) != 1 || !state.Queue[0].ReadyAt.Equal(at.Add(5*time.Second)) {
			t.Fatal("new resources did not extend same batch beyond 30 seconds")
		}
	}
	deadline := state.Queue[0].ReadyAt // 49 seconds after first input.
	b.enqueueUpdates(&state, []telegramUpdate{collectionUpdate(13, 42, 7, "https://115.com/s/item0")}, deadline.Add(-time.Second))
	if !state.Queue[0].ReadyAt.Equal(deadline) {
		t.Fatal("duplicate link extended deadline")
	}
	b.enqueueUpdates(&state, []telegramUpdate{collectionUpdate(13, 42, 7, "https://115.com/s/item0")}, deadline.Add(-time.Millisecond))
	if len(state.Queue[0].Items) != 13 {
		t.Fatal("update retry duplicated resources")
	}
	if err := b.save(state); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(b.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	var restored telegramState
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	if !restored.Queue[0].ReadyAt.Equal(deadline) {
		t.Fatal("extended deadline did not persist")
	}
	b.enqueueUpdates(&restored, []telegramUpdate{collectionUpdate(14, 42, 7, "https://115.com/s/next")}, deadline)
	if len(restored.Queue) != 2 || !restored.Queue[1].ReadyAt.Equal(deadline.Add(5*time.Second)) {
		t.Fatal("expired window accepted new resource")
	}
}

func TestCollectionPollingCollectsAndPreservesReplies(t *testing.T) {
	var generated []string
	started := time.Now()
	b := newBatchTestBot(t, batchGeneratorFunc(func(_ context.Context, in service.GenerateInput) (*service.GenerationResult, error) {
		if time.Since(started) < time.Second {
			t.Error("尚未等满窗口就开始生成")
		}
		if in.Mode != "reshare" {
			t.Error("入队模式没有保留")
		}
		generated = append(generated, in.URL)
		return &service.GenerationResult{Written: 1}, nil
	}))
	settings := b.Settings.Value()
	settings.ReshareEnabled, settings.TGBatchWaitSeconds = true, 1
	if err := b.Settings.Save(settings); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	var methods []string
	var references []int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Offset    int64  `json:"offset"`
			Text      string `json:"text"`
			MessageID int64  `json:"message_id"`
			Reply     struct {
				MessageID int64 `json:"message_id"`
			} `json:"reply_parameters"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			t.Error(err)
		}
		if !strings.HasSuffix(r.URL.Path, "/getUpdates") {
			methods = append(methods, r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:])
			references = append(references, in.Reply.MessageID)
			if len(methods) == 1 && !strings.Contains(in.Text, "开始处理 2 个链接") {
				t.Error("连续消息未合成批次")
			}
			if len(methods) == 2 && in.MessageID != 900 {
				t.Error("首条结果没有覆盖开始提示")
			}
			fmt.Fprint(w, `{"ok":true,"result":{"message_id":900}}`)
			return
		}
		var updates []telegramUpdate
		switch in.Offset {
		case 0:
			updates = []telegramUpdate{collectionUpdate(1, 42, 7, "https://115.com/s/a")}
		case 2:
			updates = []telegramUpdate{collectionUpdate(2, 42, 7, "https://115.com/s/b")}
		default:
			if len(methods) == 3 {
				cancel()
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": updates})
	}))
	defer server.Close()
	b.BaseURL = server.URL
	if err := b.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(generated, []string{"https://115.com/s/a", "https://115.com/s/b"}) || !reflect.DeepEqual(methods, []string{"sendMessage", "editMessageText", "sendMessage"}) || !reflect.DeepEqual(references, []int64{101, 0, 102}) {
		t.Fatal("消息丢失、回复覆盖或引用错误", generated, methods, references)
	}
}

func TestCollectionSurvivesRestartWithoutRewindingOffset(t *testing.T) {
	var generated []string
	b := newBatchTestBot(t, batchGeneratorFunc(func(_ context.Context, in service.GenerateInput) (*service.GenerationResult, error) {
		generated = append(generated, in.URL)
		return &service.GenerationResult{Written: 1}, nil
	}))
	state := telegramState{}
	settings := b.Settings.Value()
	settings.ReshareEnabled = true
	if err := b.Settings.Save(settings); err != nil {
		t.Fatal(err)
	}
	b.enqueueUpdates(&state, []telegramUpdate{
		collectionUpdate(1, 42, 7, "https://115.com/s/a"),
		collectionUpdate(2, 42, 8, "https://115.com/s/b"),
	}, time.Now().Add(-time.Minute))
	if err := b.save(state); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/getUpdates") {
			var in struct {
				Offset int64 `json:"offset"`
			}
			json.NewDecoder(r.Body).Decode(&in)
			if in.Offset != 3 {
				t.Error("处理队列后接收游标倒退")
			}
			fmt.Fprint(w, `{"ok":true,"result":[]}`)
			cancel()
			return
		}
		fmt.Fprint(w, `{"ok":true,"result":{"message_id":900}}`)
	}))
	defer server.Close()
	b.BaseURL = server.URL
	if err := b.Run(ctx); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(b.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	var saved telegramState
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if len(generated) != 2 || len(saved.Queue) != 0 || saved.Batch != nil || saved.Offset != 3 {
		t.Fatal("重启队列未恢复或未正确完成")
	}
}

func TestCollectionCancellationKeepsWaitingDeadline(t *testing.T) {
	b := newBatchTestBot(t, batchGeneratorFunc(func(context.Context, service.GenerateInput) (*service.GenerationResult, error) {
		t.Error("等待取消时不应生成")
		return nil, nil
	}))
	settings := b.Settings.Value()
	settings.ReshareEnabled = true
	if err := b.Settings.Save(settings); err != nil {
		t.Fatal(err)
	}
	state := telegramState{}
	b.enqueueUpdates(&state, []telegramUpdate{collectionUpdate(1, 42, 7, "https://115.com/s/a")}, time.Now())
	deadline := state.Queue[0].ReadyAt
	if err := b.save(state); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/getUpdates") {
			t.Error("等待结束前发送了开始提示")
		}
		cancel()
		fmt.Fprint(w, `{"ok":true,"result":[]}`)
	}))
	defer server.Close()
	b.BaseURL = server.URL
	if err := b.Run(ctx); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(b.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	var saved telegramState
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Offset != 2 || len(saved.Queue) != 1 || !saved.Queue[0].ReadyAt.Equal(deadline) || saved.Batch != nil {
		t.Fatal("停止期间丢失队列或重置等待时间")
	}
}
