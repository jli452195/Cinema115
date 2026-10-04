package tguser

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

// Exercise the real exchange, parser, cache and worker with only the MTProto transport replaced.
type searchTransport struct {
	mu        sync.Mutex
	sends     []string
	messageID int
	polls     int
	sent      bool
	deadline  time.Duration
	reply     func(int, int) []*tg.Message
	sendError error
}

func (f *searchTransport) invoke(ctx context.Context, in bin.Encoder, out bin.Decoder) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	switch request := in.(type) {
	case *tg.UsersGetUsersRequest:
		out.(*tg.UserClassVector).Elems = []tg.UserClass{&tg.User{ID: 1}}
	case *tg.ContactsResolveUsernameRequest:
		*out.(*tg.ContactsResolvedPeer) = tg.ContactsResolvedPeer{Peer: &tg.PeerUser{UserID: 2}, Users: []tg.UserClass{&tg.User{ID: 2, Bot: true}}}
	case *tg.MessagesSendMessageRequest:
		f.sends = append(f.sends, request.Message)
		if f.sendError != nil {
			return f.sendError
		}
		f.messageID += 100
		f.sent, f.polls = true, 0
		if deadline, ok := ctx.Deadline(); ok {
			f.deadline = time.Until(deadline)
		}
		out.(*tg.UpdatesBox).Updates = &tg.UpdateShortSentMessage{ID: f.messageID}
	case *tg.MessagesGetHistoryRequest:
		messages := make([]tg.MessageClass, 0)
		if f.sent {
			f.polls++
			for _, message := range f.reply(f.messageID, f.polls) {
				messages = append(messages, message)
			}
		}
		out.(*tg.MessagesMessagesBox).Messages = &tg.MessagesMessages{Messages: messages}
	default:
		return fmt.Errorf("unexpected mock method %T", request)
	}
	return nil
}

func (f *searchTransport) sentCount() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.sends) }

func transportClient(t *testing.T) (*Client, *searchTransport) {
	t.Helper()
	c := testClient(t)
	c.config.Bot = testBotUsername
	c.searchTiming = searchTiming{time.Second, time.Second, time.Millisecond, 5 * time.Millisecond, 60 * time.Millisecond, 100 * time.Millisecond}
	f := &searchTransport{reply: func(id, poll int) []*tg.Message {
		return []*tg.Message{{ID: id + 1, FromID: &tg.PeerUser{UserID: 2}, Message: fmt.Sprintf("https://115.com/s/test%d", id), ReplyTo: &tg.MessageReplyHeader{ReplyToMsgID: id}}}
	}}
	c.runOperation = func(ctx context.Context, operation func(context.Context, *telegram.Client) error) error {
		client := telegram.NewClient(1, "test", telegram.Options{Middlewares: []telegram.Middleware{telegram.MiddlewareFunc(func(tg.Invoker) telegram.InvokeFunc { return f.invoke })}})
		return operation(ctx, client)
	}
	return c, f
}

func searchEventually(t *testing.T, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !predicate() {
		if time.Now().After(deadline) {
			t.Fatal("condition did not become true")
		}
		time.Sleep(time.Millisecond)
	}
}

func requireOperationError(t *testing.T, err error, code string) *OperationError {
	t.Helper()
	var detail *OperationError
	if !errors.As(err, &detail) || detail.Code != code {
		t.Fatalf("expected %s, got %v", code, err)
	}
	return detail
}

func TestSearchCloseAfterPartialThenReopenAndSwitch(t *testing.T) {
	c, transport := transportClient(t)
	ctx, cancel := context.WithCancel(context.Background())
	_, err := c.SearchWithUpdates(ctx, "first", false, func(result SearchResult) { cancel() })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("waiter not canceled: %v", err)
	}
	// Reopening attaches to the existing operation, including a refresh click.
	result, err := c.Search(context.Background(), "first", true)
	if err != nil || len(result.Links) != 1 || transport.sentCount() != 1 {
		t.Fatalf("reopen: %+v %v", result, err)
	}
	result, err = c.Search(context.Background(), "second", false)
	if err != nil || len(result.Links) != 1 || transport.sentCount() != 2 {
		t.Fatalf("switch: %+v %v", result, err)
	}
	result, err = c.Search(context.Background(), "first", false)
	if err != nil || !result.Cached {
		t.Fatalf("first result not cached: %v", err)
	}
}

func TestSearchDetachedCompletionCachesWithoutViewer(t *testing.T) {
	c, transport := transportClient(t)
	ctx, cancel := context.WithCancel(context.Background())
	_, _ = c.SearchWithUpdates(ctx, "first", false, func(SearchResult) { cancel() })
	searchEventually(t, func() bool { c.mu.Lock(); defer c.mu.Unlock(); return len(c.searches) == 0 })
	result, err := c.Search(context.Background(), "first", false)
	if err != nil || !result.Cached || transport.sentCount() != 1 {
		t.Fatalf("detached completion: %+v %v", result, err)
	}
}

func TestSearchConcurrentViewersShareOneSend(t *testing.T) {
	c, transport := transportClient(t)
	if err := c.lock(); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	failures := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := c.Search(context.Background(), "same", true)
			if err != nil {
				failures <- err
			} else if len(result.Links) != 1 {
				failures <- errors.New("missing shared result")
			}
		}()
	}
	searchEventually(t, func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		job := c.searches["search:same"]
		return job != nil && job.subscribers == 16
	})
	c.unlock()
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	if transport.sentCount() != 1 {
		t.Fatalf("duplicate sends: %d", transport.sentCount())
	}
}

func TestSentMessageIDMatchesRandomID(t *testing.T) {
	updates := &tg.Updates{Updates: []tg.UpdateClass{&tg.UpdateMessageID{ID: 70, RandomID: 3}, &tg.UpdateMessageID{ID: 80, RandomID: 4}}}
	if sentMessageID(updates, 4) != 80 || sentMessageID(updates, 5) != 0 {
		t.Fatal("sent message correlation failed")
	}
}

func TestSearchFIFOAndCancelQueuedWithoutSend(t *testing.T) {
	c, transport := transportClient(t)
	if err := c.lock(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		select {
		case <-c.gate:
		default:
		}
	})
	var wg sync.WaitGroup
	launch := func(title string, ctx context.Context) {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = c.Search(ctx, title, false) }()
	}
	firstCtx, cancelFirst := context.WithCancel(context.Background())
	defer cancelFirst()
	launch("cancel", firstCtx)
	searchEventually(t, func() bool { c.mu.Lock(); defer c.mu.Unlock(); return len(c.queue) == 1 })
	launch("second", context.Background())
	searchEventually(t, func() bool { c.mu.Lock(); defer c.mu.Unlock(); return len(c.queue) == 2 })
	launch("third", context.Background())
	searchEventually(t, func() bool { c.mu.Lock(); defer c.mu.Unlock(); return len(c.queue) == 3 })
	cancelFirst()
	searchEventually(t, func() bool { c.mu.Lock(); defer c.mu.Unlock(); return len(c.queue) == 2 })
	c.unlock()
	wg.Wait()
	transport.mu.Lock()
	defer transport.mu.Unlock()
	if fmt.Sprint(transport.sends) != "[second third]" {
		t.Fatalf("send order: %v", transport.sends)
	}
}

func TestSearchCacheReadableDuringBusyAndCooldown(t *testing.T) {
	c, _ := transportClient(t)
	c.cacheResult("search:cached", SearchResult{Empty: true}, time.Now())
	if err := c.lock(); err != nil {
		t.Fatal(err)
	}
	defer c.unlock()
	c.mu.Lock()
	c.cooldown = time.Now().Add(time.Minute)
	c.mu.Unlock()
	r, err := c.Search(context.Background(), "cached", false)
	if err != nil || !r.Cached {
		t.Fatalf("cache blocked: %v", err)
	}
	_, err = c.Search(context.Background(), "new", false)
	detail := requireOperationError(t, err, "cooldown")
	if detail.RetryAt == nil {
		t.Fatal("missing retry deadline")
	}
}

func TestSearchQueueTimeoutAndCapacity(t *testing.T) {
	c, transport := transportClient(t)
	c.searchTiming.queue = 100 * time.Millisecond
	if err := c.lock(); err != nil {
		t.Fatal(err)
	}
	defer c.unlock()
	var wg sync.WaitGroup
	for i := 0; i < searchQueueLimit; i++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); _, _ = c.Search(context.Background(), fmt.Sprint(i), false) }(i)
	}
	searchEventually(t, func() bool { c.mu.Lock(); defer c.mu.Unlock(); return len(c.searches) == searchQueueLimit })
	_, err := c.Search(context.Background(), "overflow", false)
	requireOperationError(t, err, "queue_full")
	wg.Wait()
	if transport.sentCount() != 0 {
		t.Fatal("queued requests were sent")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.cooldown.IsZero() {
		t.Fatal("queue timeout created cooldown")
	}
}

func TestSearchOperationDeadlineAndCooldownRecovery(t *testing.T) {
	c, transport := transportClient(t)
	c.searchTiming.operation = 40 * time.Millisecond
	transport.reply = func(int, int) []*tg.Message { return nil }
	_, err := c.Search(context.Background(), "no reply", false)
	detail := requireOperationError(t, err, "reply_timeout")
	_, err = c.Search(context.Background(), "next", false)
	requireOperationError(t, err, "cooldown")
	if transport.sentCount() != 1 {
		t.Fatal("sent during quarantine")
	}
	transport.mu.Lock()
	if transport.deadline <= 0 || transport.deadline > 40*time.Millisecond {
		t.Fatal("operation has no own deadline")
	}
	transport.reply = func(id, poll int) []*tg.Message { return []*tg.Message{{ID: id + 1, Message: "没有找到资源"}} }
	transport.mu.Unlock()
	time.Sleep(time.Until(*detail.RetryAt) + time.Millisecond)
	r, err := c.Search(context.Background(), "next", false)
	if err != nil || !r.Empty || transport.sentCount() != 2 {
		t.Fatalf("recovery: %+v %v", r, err)
	}
}

func TestSearchUnrecognizedAndPartialResults(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(fmt.Sprint(partial), func(t *testing.T) {
			c, transport := transportClient(t)
			c.searchTiming.operation = 35 * time.Millisecond
			transport.reply = func(id, poll int) []*tg.Message {
				text := "Bot 已回复，但格式无法识别"
				if partial {
					text = fmt.Sprintf("https://115.com/s/part%d", poll)
				}
				return []*tg.Message{{ID: id + 1, Message: text}}
			}
			r, err := c.Search(context.Background(), "first", false)
			if partial {
				requireOperationError(t, err, "partial_result")
				cached, ok := c.cachedResult("search:first", time.Now())
				if !ok || !r.Partial || !cached.Partial || cached.AllLoaded || len(cached.Links) != 1 {
					t.Fatalf("partial cache: %+v", cached)
				}
			} else {
				requireOperationError(t, err, "unrecognized_reply")
			}
		})
	}
}

func TestSearchRPCRejectionVersusAmbiguousSendFailure(t *testing.T) {
	for _, rejected := range []bool{true, false} {
		t.Run(fmt.Sprint(rejected), func(t *testing.T) {
			c, transport := transportClient(t)
			transport.sendError = errors.New("transport disconnected")
			if rejected {
				transport.sendError = &tgerr.Error{Code: 400, Type: "PEER_ID_INVALID"}
			}
			_, err := c.Search(context.Background(), "first", false)
			if err == nil {
				t.Fatal("missing error")
			}
			c.mu.Lock()
			cooling := time.Now().Before(c.cooldown)
			c.mu.Unlock()
			if cooling == rejected {
				t.Fatalf("wrong cooldown for rejected=%v", rejected)
			}
		})
	}
}

func TestSearchRejectsExplicitOldReply(t *testing.T) {
	c, transport := transportClient(t)
	transport.reply = func(id, poll int) []*tg.Message {
		return []*tg.Message{
			{ID: id + 1, Message: "https://115.com/s/wrong", ReplyTo: &tg.MessageReplyHeader{ReplyToMsgID: 77}},
			{ID: id + 2, Message: "https://115.com/s/right", ReplyTo: &tg.MessageReplyHeader{ReplyToMsgID: id}},
		}
	}
	r, err := c.Search(context.Background(), "first", false)
	if err != nil || len(r.Links) != 1 || r.Links[0] != "https://115cdn.com/s/right" {
		t.Fatalf("wrong reply mixed: %+v %v", r, err)
	}
}

func TestSearchCloseCancelsAndJoinsWorker(t *testing.T) {
	c, transport := transportClient(t)
	transport.reply = func(int, int) []*tg.Message { return nil }
	done := make(chan struct{})
	go func() { defer close(done); _, _ = c.Search(context.Background(), "first", false) }()
	searchEventually(t, func() bool { return transport.sentCount() == 1 })
	c.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("waiter leaked")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.workerRunning || len(c.searches) != 0 || len(c.gate) != 0 {
		t.Fatal("worker not drained")
	}
}

func TestSearchReplyAssociationForCallbackEdits(t *testing.T) {
	action := storedAction{message: 20, data: []byte("next")}
	if !belongsToExchange(&tg.Message{ID: 20, ReplyTo: &tg.MessageReplyHeader{ReplyToMsgID: 10}}, action, 0, 30, nil) {
		t.Fatal("selected page edit rejected")
	}
	if belongsToExchange(&tg.Message{ID: 19}, action, 0, 30, nil) {
		t.Fatal("unrelated edit accepted")
	}
	if belongsToExchange(&tg.Message{ID: 31, ReplyTo: &tg.MessageReplyHeader{ReplyToMsgID: 10}}, action, 0, 30, nil) {
		t.Fatal("unrelated reply accepted")
	}
}

// Canary uses real HTTP disconnects and production polling/quiet intervals, with no external Bot traffic.
func TestSearchHTTPDisconnectCanary(t *testing.T) {
	c, transport := transportClient(t)
	c.searchTiming = defaultSearchTiming
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		result, err := c.SearchWithProgress(r.Context(), r.URL.Query().Get("title"), false, func(SearchResult) {
			fmt.Fprint(w, "event:partial\n\n")
			flusher.Flush()
		}, func(progress SearchProgress) {
			fmt.Fprintf(w, "event:%s\n\n", progress.Stage)
			flusher.Flush()
		})
		if err != nil {
			return
		}
		fmt.Fprintf(w, "event:complete\ndata:%d\n\n", len(result.Links))
		flusher.Flush()
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"?title=first", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	scanner := bufio.NewScanner(response.Body)
	partial := false
	for scanner.Scan() {
		if scanner.Text() == "event:partial" {
			partial = true
			break
		}
	}
	cancel()
	response.Body.Close()
	if !partial {
		t.Fatal("no first reply before disconnect")
	}
	second, err := server.Client().Get(server.URL + "?title=second")
	if err != nil {
		t.Fatal(err)
	}
	defer second.Body.Close()
	scanner = bufio.NewScanner(second.Body)
	queued, complete := false, false
	for scanner.Scan() {
		queued = queued || scanner.Text() == "event:queued"
		complete = complete || scanner.Text() == "event:complete"
		if strings.Contains(scanner.Text(), "error") {
			t.Fatalf("unexpected event: %s", scanner.Text())
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if !queued || !complete {
		t.Fatalf("queue=%v complete=%v", queued, complete)
	}
	first, err := c.Search(context.Background(), "first", false)
	if err != nil || !first.Cached || transport.sentCount() != 2 {
		t.Fatalf("canary did not preserve detached result: %+v %v", first, err)
	}
}
