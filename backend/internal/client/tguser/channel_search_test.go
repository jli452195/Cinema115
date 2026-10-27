package tguser

import (
	"context"
	"errors"
	"fmt"
	"slices"
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

func TestChannelMetadataUsesEpisodeFileFactsAndSize(t *testing.T) {
	message := &tg.Message{ID: 10, Date: 1234, Message: "✅少爷：请您检阅\n🎬 影视：小城良方\n📺 季集：S01E13\n📦 质量：2160p SDR H265 DDP\n💾 大小：1.3GB\ned2k://|file|小城良方(2026).S01E13.WEB-DL.2160p.SDR.H265.DDP.mkv|1395864371|0123456789abcdef0123456789abcdef|/"}
	item, ok := channelMessage(message, Channel{ID: "2"}, "小城良方")
	if !ok || item.Title != "小城良方" || item.Metadata == nil {
		t.Fatalf("metadata item: %+v", item)
	}
	if item.Metadata.Episode != "S01E13" || item.Metadata.Size != "1.3 GiB" {
		t.Fatalf("metadata numbering or size: %+v", item.Metadata)
	}
	joined := strings.Join(item.Metadata.Specs, " ")
	for _, value := range []string{"2160p", "SDR", "WEB-DL", "HEVC", "DDP"} {
		if !strings.Contains(joined, value) {
			t.Fatalf("missing %q in %q", value, joined)
		}
	}
	if len(item.Links) != 1 || item.Links[0].Metadata != nil {
		t.Fatalf("single link metadata was not lifted: %+v", item.Links)
	}

	multiple := &tg.Message{ID: 11, Date: 1234, Message: "小城良方 S01E13\n质量：1080p\nhttps://115.com/s/one\ned2k://|file|小城良方.S01E14.2160p.mkv|123|0123456789abcdef0123456789abcdef|/"}
	item, ok = channelMessage(multiple, Channel{ID: "2"}, "小城良方")
	if !ok || item.Metadata != nil || len(item.Links) != 2 {
		t.Fatalf("multiple link metadata scope: %+v", item)
	}
	if item.Links[0].Metadata == nil || item.Links[0].Metadata.Episode != "S01E13" || item.Links[1].Metadata == nil || item.Links[1].Metadata.Episode != "S01E14" {
		t.Fatalf("file metadata missing: %+v", item.Links)
	}
	message.Message = "小城良方\n季集：S01E13\n质量：2160p HDR\ned2k://|file|小城良方.S01E01.S01E02.1080p.2160p.SDR.HDR.mkv|123|0123456789abcdef0123456789abcdef|/"
	item, ok = channelMessage(message, Channel{ID: "2"}, "小城良方")
	if !ok || item.Metadata == nil || item.Metadata.Episode != "" || slices.Contains(item.Metadata.Specs, "2160p") || slices.Contains(item.Metadata.Specs, "HDR") {
		t.Fatalf("ambiguous filename filled from post: %+v", item.Metadata)
	}
}

func TestChannelMetadataPostFormatsAndAmbiguity(t *testing.T) {
	for _, tc := range []struct {
		name, text, episode, size string
		specs                     []string
	}{
		{"share labelled fields", "影视：小城良方\n季集：S01E13\n质量：2160p DOVI H.265 EAC3\n大小：3.9GB", "S01E13", "3.9 GB", []string{"2160p", "DV", "HEVC", "DDP"}},
		{"share short quality line", "国产剧: 小城良方 (2026) S01E13\n热更 2160p WEB-DL HDR H265 DDP.5.1\n体 积: 2.39G", "S01E13", "2.39 GB", []string{"2160p", "HDR", "WEB-DL", "HEVC", "DDP 5.1"}},
		{"share fields without colon", "影视：小城良方\n季集 S01E13\n质量 2160p HDR H265 DDP\n体积 3.9GB", "S01E13", "3.9 GB", []string{"2160p", "HDR", "HEVC", "DDP"}},
		{"movie unlabelled quality", "外语电影: 末世行者 (2026)\n1080p WEB-DL H264 DDP.5.1 英语 中字\n体 积: 7.29G", "", "7.29 GB", []string{"1080p", "WEB-DL", "H264", "DDP 5.1", "英语 中字"}},
		{"unknown range word", "影视：小城良方\n季集：S01E16\n版本：2160p EDR WEB-DL H.265 DDP 5.1\n体积：1.01 GB", "S01E16", "1.01 GB", []string{"2160p", "EDR", "DDP 5.1"}},
		{"preserve episode range", "小城良方 S01E01-02\n质量：1080p", "S01E01-02", "", []string{"1080p"}},
		{"preserve cross season range", "小城良方 S01E01-S02E03", "S01E01-S02E03", "", nil},
		{"do not invent season", "小城良方 第13集\n质量：1080p", "第13集", "", []string{"1080p"}},
		{"conflicting post episodes", "小城良方 S01E13\n季集：S02E01\n质量：1080p", "", "", []string{"1080p"}},
		{"ignore synopsis and adverts", "英语老师 (2026)\n简介：第13集里提到 HDR 和 2160p。\n推广：2160p HDR HEVC", "", "", nil},
		{"preserve explicit subtitle", "小城良方\n质量：2160p\n字幕：简繁字幕 内封", "", "", []string{"2160p", "内封"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			message := &tg.Message{ID: 12, Date: 1234, Message: tc.text + "\nhttps://115.com/s/test"}
			item, ok := channelMessage(message, Channel{ID: "2"}, "")
			if !ok {
				t.Fatal("post was discarded")
			}
			meta := item.Metadata
			if meta == nil {
				meta = &ChannelMetadata{}
			}
			if meta.Episode != tc.episode || meta.Size != tc.size {
				t.Fatalf("metadata: %+v", meta)
			}
			for _, spec := range tc.specs {
				if !slices.Contains(meta.Specs, spec) {
					t.Fatalf("missing %q in %+v", spec, meta)
				}
			}
			if tc.name == "ignore synopsis and adverts" && len(meta.Specs) != 0 {
				t.Fatalf("non-resource prose parsed as specs: %+v", meta)
			}
		})
	}
}

func TestChannelSearchFiltersUnrelatedMessagesWithoutBreakingOffsets(t *testing.T) {
	c, transport := searchFixture(t, 1)
	transport.search = func(_ context.Context, request *tg.MessagesSearchRequest) (tg.MessagesMessagesClass, error) {
		messages := channelMessages(1, 40, 20) // All valid links, but unrelated titles.
		if request.OffsetID == 0 {
			return &tg.MessagesChannelMessages{Count: 21, Messages: messages}, nil
		}
		if request.OffsetID != 21 {
			t.Fatalf("filtered page did not advance raw offset: %d", request.OffsetID)
		}
		messages = channelMessages(1, 20, 1)
		messages[0].(*tg.Message).Message = "小城 · 良方 S01E13\nhttps://115.com/s/matched"
		return &tg.MessagesChannelMessages{Count: 21, Messages: messages}, nil
	}
	first, err := c.SearchChannels(context.Background(), "小城良方", "", false)
	if err != nil || len(first.Items) != 0 || !first.HasMore {
		t.Fatalf("unrelated page: %+v, %v", first, err)
	}
	next, err := c.SearchChannels(context.Background(), "小城良方", first.Cursor, false)
	if err != nil || len(next.Items) != 1 || next.HasMore {
		t.Fatalf("matching page: %+v, %v", next, err)
	}
	for _, text := range []string{"别的电影\nhttps://115.com/s/小城良方", "其它电影\nhttps://example.org/小城良方\nhttps://115.com/s/one"} {
		if _, ok := channelMessage(&tg.Message{ID: 1, Message: text}, Channel{ID: "2"}, "小城良方"); ok {
			t.Fatal("URL text accepted as matching movie title")
		}
	}
	encoded := "ed2k://|file|%E5%B0%8F%E5%9F%8E%E8%89%AF%E6%96%B9.S01E13.1080p.mkv|123|0123456789abcdef0123456789abcdef|/"
	if _, ok := channelMessage(&tg.Message{ID: 1, Message: "文件\n" + encoded}, Channel{ID: "2"}, "小城良方"); !ok {
		t.Fatal("encoded ED2K filename match lost")
	}
}

func BenchmarkChannelMessageMetadata(b *testing.B) {
	m := &tg.Message{ID: 10, Date: 1234, Message: "影视：小城良方\n季集：S01E13\n质量：2160p SDR H265 DDP\n大小：1.3GB\ned2k://|file|小城良方(2026).S01E13.WEB-DL.2160p.SDR.H265.DDP.mkv|1431809029|0123456789abcdef0123456789abcdef|/"}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		channelMessage(m, Channel{ID: "2"}, "小城良方")
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
			for _, raw := range messages {
				raw.(*tg.Message).Message = "完整标题长于十八个汉字也不能截断的电影标题\n" + raw.(*tg.Message).Message
			}
			// Even filtered messages must advance this channel's raw offset.
			messages[19].(*tg.Message).Message = "not a direct resource"
			return &tg.MessagesChannelMessages{Count: 40, Messages: messages}, nil
		}
		messages := channelMessages(p.ChannelID, 20, 1)
		messages[0].(*tg.Message).Message = "完整标题长于十八个汉字也不能截断的电影标题\n" + messages[0].(*tg.Message).Message
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
