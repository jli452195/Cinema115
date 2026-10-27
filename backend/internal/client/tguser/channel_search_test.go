package tguser

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

const channelED2K = "ed2k://|file|movie 1080p.mkv|123|0123456789abcdef0123456789abcdef|/"

func TestChannelMessageDirectLinks(t *testing.T) {
	m := &tg.Message{ID: 9, Date: 1234, Message: "📺 夏日口袋 S01E02\n密码：abcd\n" + channelED2K + "\nhttps://115.com/s/one\nhttps://t.me/example_bot?start=pay\nmagnet:?xt=urn:btih:" + strings.Repeat("a", 40), Entities: []tg.MessageEntityClass{
		&tg.MessageEntityTextURL{Offset: 2, Length: 4, URL: "https://115.com/s/one"},
	}, ReplyMarkup: &tg.ReplyInlineMarkup{Rows: []tg.KeyboardButtonRow{{Buttons: []tg.KeyboardButtonClass{
		&tg.KeyboardButtonURL{Text: "获取链接", URL: "https://115cdn.com/s/two?password=efgh"},
		&tg.KeyboardButtonURL{Text: "bot", URL: "https://t.me/example_bot?start=pay"},
		&tg.KeyboardButtonCallback{Text: "取链", Data: []byte("private")},
	}}}}}
	item, ok := channelMessage(m, Channel{ID: "4358697065", Title: "资源频道", Username: "resource_channel"}, "")
	if !ok || len(item.Links) != 3 || item.ID != "4358697065:9" || item.SourceURL != "https://t.me/resource_channel/9" {
		t.Fatalf("message parse: %+v", item)
	}
	for _, link := range item.Links {
		if strings.Contains(link.URL, "bot") || strings.HasPrefix(link.URL, "magnet:") {
			t.Fatal("indirect link escaped")
		}
	}
	if strings.Contains(item.Links[1].URL, "password=abcd") {
		t.Fatal("separate password guessed for multiple shares")
	}
	m.ReplyMarkup = nil
	m.Entities = nil
	m.Message = "测试标题\n密码：abcd\nhttps://115.com/s/one\n" + channelED2K
	item, ok = channelMessage(m, Channel{ID: "2"}, "")
	if !ok || item.Title != "测试标题" || item.SourceURL != "https://t.me/c/2/9" || !strings.Contains(item.Links[0].URL, "password=abcd") {
		t.Fatalf("single share password: %+v", item)
	}
	for _, raw := range []string{"https://115.com.evil.example/s/x", "https://user@115.com/s/x", "ed2k://|file|a|123|bad|/", "magnet:?xt=urn:btih:" + strings.Repeat("a", 40), "https://t.me/test_bot?start=x"} {
		m.Message = raw
		if _, ok := channelMessage(m, Channel{ID: "2"}, ""); ok {
			t.Fatalf("unsupported link accepted: %s", raw)
		}
	}
	m.Message = "隐藏分享"
	m.Entities = []tg.MessageEntityClass{&tg.MessageEntityTextURL{URL: "https://115cdn.com/s/hidden?password=xy12"}}
	if item, ok = channelMessage(m, Channel{ID: "2"}, ""); !ok || len(item.Links) != 1 {
		t.Fatal("hidden URL missing")
	}
	m.Message = "固定招呼语\n测试影片 2160p 多音轨\nhttps://115.com/s/one"
	if item, ok = channelMessage(m, Channel{ID: "2"}, "测试影片"); !ok || item.Title != "测试影片 2160p 多音轨" {
		t.Fatalf("query title not preferred: %+v", item)
	}
}

func searchFixture(t *testing.T, count int) (*Client, *channelTransport) {
	t.Helper()
	c, f := channelClient(t)
	c.channels = channelSettings{OwnerID: 1, Peers: make([]channelPeer, count)}
	for i := range c.channels.Peers {
		c.channels.Peers[i] = channelPeer{Channel: Channel{ID: fmt.Sprint(i + 1), Title: fmt.Sprintf("频道 %d", i+1)}, AccessHash: int64(100 + i)}
	}
	return c, f
}
func channelMessages(id int64, first, count int) []tg.MessageClass {
	messages := make([]tg.MessageClass, 0, count)
	for n := 0; n < count; n++ {
		messages = append(messages, &tg.Message{ID: first - n, Date: 1000 + first - n, PeerID: &tg.PeerChannel{ChannelID: id}, Message: fmt.Sprintf("测试资源 %d\nhttps://115.com/s/channel%dmessage%d", first-n, id, first-n)})
	}
	return messages
}

func TestChannelSearchPaginationRetryCacheAndScope(t *testing.T) {
	c, f := searchFixture(t, 2)
	var mu sync.Mutex
	calls := make(map[int64][]int)
	f.search = func(ctx context.Context, r *tg.MessagesSearchRequest) (tg.MessagesMessagesClass, error) {
		p, ok := r.Peer.(*tg.InputPeerChannel)
		if !ok || p.ChannelID < 1 || p.ChannelID > 2 || p.AccessHash != 99+p.ChannelID || r.Q != "完整标题长于十八个汉字也不能截断的电影标题" || r.Limit != 20 {
			t.Fatalf("wrong search scope %+v", r)
		}
		mu.Lock()
		calls[p.ChannelID] = append(calls[p.ChannelID], r.OffsetID)
		attempt := len(calls[p.ChannelID])
		mu.Unlock()
		if p.ChannelID == 2 && attempt == 1 {
			return nil, errors.New("private transport details")
		}
		if p.ChannelID == 1 && r.OffsetID == 0 {
			messages := channelMessages(1, 40, 20)
			// Even filtered messages must advance this channel's raw offset.
			messages[19].(*tg.Message).Message = "not a direct resource"
			return &tg.MessagesChannelMessages{Count: 40, Messages: messages}, nil
		}
		messages := channelMessages(p.ChannelID, 20, 1)
		// Search responses must not expose a different chat's message.
		messages = append(messages, channelMessages(999, 19, 1)...)
		return &tg.MessagesChannelMessages{Count: 40, Messages: messages}, nil
	}
	title := "完整标题长于十八个汉字也不能截断的电影标题"
	first, err := c.SearchChannels(context.Background(), title, "", false)
	if err != nil || len(first.Items) != 19 || !first.HasMore || !first.Partial || first.Cursor == "" || strings.Contains(first.Warnings[0].Error, "private") {
		t.Fatalf("first %+v %v", first, err)
	}
	cached, err := c.SearchChannels(context.Background(), title, "", false)
	if err != nil || !cached.Cached || len(calls[1]) != 1 || len(calls[2]) != 1 {
		t.Fatal("reopen sent a duplicate Telegram request", err)
	}
	second, err := c.SearchChannels(context.Background(), title, first.Cursor, false)
	if err != nil || len(second.Items) != 21 || second.HasMore || second.Partial {
		t.Fatalf("second %+v %v", second, err)
	}
	if calls[1][1] != 21 || calls[2][1] != 0 {
		t.Fatalf("failed source advanced its offset: %+v", calls)
	}
	for _, item := range second.Items {
		if item.Channel.ID == "999" {
			t.Fatal("wrong peer result leaked")
		}
	}
	retry, err := c.SearchChannels(context.Background(), title, first.Cursor, false)
	if err != nil || !retry.Cached || len(retry.Items) != 21 || len(calls[1]) != 2 {
		t.Fatal("lost-response retry skipped a page", err)
	}
	if _, err := c.SearchChannels(context.Background(), "other title", first.Cursor, false); err == nil {
		t.Fatal("cursor reused across title")
	}
	if _, err := c.SearchChannels(context.Background(), title, "forged", false); err == nil {
		t.Fatal("forged cursor accepted")
	}
	if _, err := c.SearchChannels(context.Background(), title, "", true); err != nil || len(calls[1]) != 3 {
		t.Fatal("refresh reused cache", err)
	}
	c.mu.Lock()
	c.channels.Peers = c.channels.Peers[:1]
	c.mu.Unlock()
	if _, err := c.SearchChannels(context.Background(), title, first.Cursor, false); err == nil {
		t.Fatal("cursor survived source change")
	}
}

func TestChannelSearchBoundedParallelismAndCancellation(t *testing.T) {
	c, f := searchFixture(t, 10)
	var active, maxActive, calls atomic.Int32
	f.search = func(ctx context.Context, r *tg.MessagesSearchRequest) (tg.MessagesMessagesClass, error) {
		n := active.Add(1)
		defer active.Add(-1)
		calls.Add(1)
		for old := maxActive.Load(); n > old && !maxActive.CompareAndSwap(old, n); old = maxActive.Load() {
		}
		select {
		case <-time.After(10 * time.Millisecond):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return &tg.MessagesMessages{}, nil
	}
	first, err := c.SearchChannels(context.Background(), "test", "", false)
	if err != nil || calls.Load() != 6 || maxActive.Load() > 3 || maxActive.Load() < 2 || !first.HasMore {
		t.Fatalf("parallelism %d calls %d: %v", maxActive.Load(), calls.Load(), err)
	}
	second, err := c.SearchChannels(context.Background(), "test", first.Cursor, false)
	if err != nil || calls.Load() != 10 || second.HasMore {
		t.Fatal("later configured channels not reached", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	var once sync.Once
	f.search = func(ctx context.Context, r *tg.MessagesSearchRequest) (tg.MessagesMessagesClass, error) {
		once.Do(func() { close(started) })
		<-ctx.Done()
		return nil, ctx.Err()
	}
	finished := make(chan error, 1)
	go func() { _, err := c.SearchChannels(ctx, "cancel", "", false); finished <- err }()
	<-started
	cancel()
	if err := <-finished; !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation lost", err)
	}
	if err := c.lock(); err != nil {
		t.Fatal("connection gate stuck", err)
	}
	c.unlock()
}

func TestChannelSearchFloodWaitAndAccountChange(t *testing.T) {
	c, f := searchFixture(t, 1)
	calls := 0
	f.search = func(context.Context, *tg.MessagesSearchRequest) (tg.MessagesMessagesClass, error) {
		calls++
		return nil, &tgerr.Error{Type: "FLOOD_WAIT", Argument: 30}
	}
	result, err := c.SearchChannels(context.Background(), "test", "", false)
	if err != nil || result.RetryAt == nil || !result.Partial || !result.HasMore {
		t.Fatal("flood wait lost", err)
	}
	if _, err := c.SearchChannels(context.Background(), "test", result.Cursor, false); err == nil || calls != 1 {
		t.Fatal("flood wait immediately retried", err)
	}
	if _, err := c.SearchChannels(context.Background(), "test", "", false); err != nil || calls != 1 {
		t.Fatal("cached resources unavailable during cooldown", err)
	}
	f.userID = 2
	if err := c.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(c.channelSearches) != 0 || !c.channelRetryAt.IsZero() {
		t.Fatal("account switch retained channel cache")
	}
	if _, err := c.SearchChannels(context.Background(), "test", result.Cursor, false); err == nil {
		t.Fatal("old account cursor accepted")
	}
}
