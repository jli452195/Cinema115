package tguser

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gotd/td/tg"
	"golang.org/x/sync/errgroup"
)

type telegraphTestTransport func(*http.Request) (*http.Response, error)

func (f telegraphTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func telegraphFixture(title string, content []any) []byte {
	data, _ := json.Marshal(map[string]any{"ok": true, "result": map[string]any{"title": title, "content": content}})
	return data
}

func telegraphTestNode(tag string, children ...any) map[string]any {
	return map[string]any{"tag": tag, "children": children}
}

func TestChannelTelegraphCandidatesKeepIndirectPosts(t *testing.T) {
	message := &tg.Message{ID: 8, Message: "朝鲜律师 (2023) {tmdbid-206715}", ReplyMarkup: &tg.ReplyInlineMarkup{Rows: []tg.KeyboardButtonRow{{Buttons: []tg.KeyboardButtonClass{
		&tg.KeyboardButtonURL{Text: "ED2K链接", URL: "https://telegra.ph/tmdbid-206715-11-17"},
		&tg.KeyboardButtonURL{Text: "重复", URL: "https://telegra.ph/tmdbid-206715-11-17?track=1"},
		&tg.KeyboardButtonCallback{Text: "不执行回调", Data: []byte("never")},
	}}}}}
	item, ok := channelMessage(message, Channel{ID: "1"}, "朝鲜律师")
	if !ok || len(item.Links) != 0 || len(item.Sources) != 1 || item.Sources[0].Type != "telegraph" {
		t.Fatalf("外链帖子未保留为候选: %+v", item)
	}
	for _, raw := range []string{"https://telegra.ph.evil.test/x", "https://user@telegra.ph/x", "https://telegra.ph:443/x", "https://telegra.ph/a/b", "https://telegra.ph/../x", "https://example.test/redirect?url=https://telegra.ph/x"} {
		if _, ok := channelTelegraphSource(raw); ok {
			t.Fatalf("接受了非受支持页面: %s", raw)
		}
	}
	if _, ok := channelMessage(message, Channel{ID: "1"}, "另一部电影"); ok {
		t.Fatal("无关作品进入外链候选")
	}
}

func TestTelegraphVersionGroupsKeepEnglishFileIdentity(t *testing.T) {
	file := "ed2k://|file|Joseon.Attorney.S01E07.NF.WEB-DL.1080p.H264.AAC.mkv|12345|0123456789ABCDEF0123456789ABCDEF|/"
	other := "ed2k://|file|Joseon.Attorney.S01E08.2160p.HDR.HEVC.mkv|23456|1123456789ABCDEF0123456789ABCDEF|/"
	anchor := telegraphTestNode("a", "文件")
	anchor["attrs"] = map[string]string{"href": file}
	data := telegraphFixture("朝鲜律师 (2023) {tmdbid-206715}", []any{
		telegraphTestNode("h3", "1080p WEB-DL"), telegraphTestNode("p", "字幕：简体中文/未知语言"), telegraphTestNode("p", anchor, "\n", file),
		telegraphTestNode("h3", "2160p HDR"), telegraphTestNode("pre", other),
	})
	result, err := parseTelegraph(data, ChannelSearchTarget{Title: "朝鲜律师", Kind: "tv", TMDBID: "206715"})
	if err != nil || len(result.Groups) != 2 || len(result.Groups[0].Links) != 1 || len(result.Groups[1].Links) != 1 {
		t.Fatalf("分组或文件去重失败: %+v, %v", result, err)
	}
	first := result.Groups[0].Links[0]
	if first.File == nil || first.File.Hash != "0123456789abcdef0123456789abcdef" || first.File.SizeBytes != "12345" || !strings.HasPrefix(first.File.Name, "Joseon.Attorney") {
		t.Fatalf("真实文件身份缺失: %+v", first)
	}
	if first.Metadata == nil || first.Metadata.Episode != "S01E07" || first.Metadata.Season == nil || *first.Metadata.Season != 1 || first.Metadata.EpisodeNumber == nil || *first.Metadata.EpisodeNumber != 7 || first.Metadata.Resolution != "1080p" || first.Metadata.Audio != "AAC" {
		t.Fatalf("结构化规格缺失: %+v", first.Metadata)
	}
	if result.Groups[1].Links[0].Metadata.Resolution != "2160p" || result.Groups[1].Links[0].Metadata.Episode != "S01E08" {
		t.Fatal("不同版本间串用了规格或季集")
	}
	if first.Metadata.Subtitles != "简体中文" || result.Groups[1].Links[0].Metadata.Subtitles != "" {
		t.Fatal("明确字幕没有保留在所属版本组内")
	}
}

func TestTelegraphGroupMetadataOnlyFillsUnambiguousMissingFields(t *testing.T) {
	for _, name := range []string{"Series.S01E03.1080p.H264.mkv", "Series.S01E03.1080p.2160p.H264.HEVC.SDR.HDR.mkv", "Series.mkv"} {
		link := "ed2k://|file|" + name + "|123|0123456789abcdef0123456789abcdef|/"
		data := telegraphFixture("作品 {tmdbid-42}", []any{telegraphTestNode("h3", "Series.S02E09.2160p.HEVC.HDR"), telegraphTestNode("p", link)})
		result, err := parseTelegraph(data, ChannelSearchTarget{Title: "作品", Kind: "tv", TMDBID: "42"})
		if err != nil {
			t.Fatal(err)
		}
		metadata := result.Groups[0].Links[0].Metadata
		switch name {
		case "Series.S01E03.1080p.H264.mkv":
			if metadata.Episode != "S01E03" || metadata.Resolution != "1080p" || metadata.VideoCodec != "H264" || metadata.DynamicRange != "HDR" {
				t.Fatalf("本组声明覆盖了文件明确值或未补齐缺失值: %+v", metadata)
			}
		case "Series.S01E03.1080p.2160p.H264.HEVC.SDR.HDR.mkv":
			if metadata.Resolution != "" || metadata.VideoCodec != "" || metadata.DynamicRange != "" {
				t.Fatalf("文件自身歧义被组声明掩盖: %+v", metadata)
			}
		case "Series.mkv":
			if metadata.Episode != "" || metadata.Season != nil || metadata.EpisodeNumber != nil {
				t.Fatalf("组声明被误当作文件季集: %+v", metadata)
			}
		}
	}
}

func TestChannelED2KFileIdentityRejectsPathsAndBrokenEscapes(t *testing.T) {
	for _, name := range []string{"%GG.mkv", "%00.mkv", "%0A.mkv", "..%2Fvideo.mkv", "dir%5Cvideo.mkv", ".", "..", "  "} {
		if _, err := channelED2KIdentity("ed2k://|file|" + name + "|123|0123456789abcdef0123456789abcdef|/"); err == nil {
			t.Fatalf("损坏或路径式文件名被接受: %q", name)
		}
	}
	file, err := channelED2KIdentity("ed2k://|file|A+B.%E4%B8%AD.mkv|123|0123456789ABCDEF0123456789ABCDEF|/")
	if err != nil || file.Name != "A+B.中.mkv" || file.Hash != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("正常转义或加号被改变: %+v %v", file, err)
	}
}

func TestTelegraphRejectsConflictingWorkAndMalformedPages(t *testing.T) {
	target := ChannelSearchTarget{Title: "朝鲜律师", Kind: "tv", TMDBID: "206715"}
	link := "ed2k://|file|English.Name.mkv|123|0123456789abcdef0123456789abcdef|/"
	for _, title := range []string{"朝鲜律师 {tmdbid-123}", "其他电影 (2026)", "其他电影 (2026) {tmdbid-206715}", "朝鲜律师续集 (2023) {tmdbid-206715}"} {
		if _, err := parseTelegraph(telegraphFixture(title, []any{link}), target); err == nil {
			t.Fatalf("作品冲突未拒绝: %s", title)
		}
	}
	anchor := telegraphTestNode("a", "TMDB")
	anchor["attrs"] = map[string]string{"href": "https://www.themoviedb.org/movie/206715"}
	if _, err := parseTelegraph(telegraphFixture("朝鲜律师 {tmdbid-206715}", []any{link, anchor}), target); err == nil {
		t.Fatal("电影与剧集的同号 ID 未区分")
	}
	if _, err := parseTelegraph(telegraphFixture("朝鲜律师 {tmdbid-206715}", []any{"ed2k://|file|bad|1|bad|/"}), target); err == nil {
		t.Fatal("无有效资源页面被报告成功")
	}
	if _, err := parseTelegraph([]byte(`{"ok":false,"error":"PAGE_NOT_FOUND"}`), target); err == nil {
		t.Fatal("官方 API 失败被报告成功")
	}
}

func TestTelegraphTitleMatchingKeepsRealPageFormatAndNormalizedNames(t *testing.T) {
	for _, value := range []struct {
		title  string
		target ChannelSearchTarget
	}{
		{"朝鲜律师 (2023) {tmdbid-206715}", ChannelSearchTarget{Title: "朝鲜律师", Kind: "tv", TMDBID: "206715"}},
		{"嫁金钗 (2026) {tmdbid-300146}", ChannelSearchTarget{Title: "嫁金钗", Kind: "tv", TMDBID: "300146"}},
		{"朝鮮律師（2023） {tmdbid-206715}", ChannelSearchTarget{Title: "朝鲜律师", Kind: "tv", TMDBID: "206715"}},
		{"１２３：朝鲜律师 (2023) {tmdbid-206715}", ChannelSearchTarget{Title: "123 朝鲜律师", Kind: "tv", TMDBID: "206715"}},
	} {
		if !telegraphTitleMatches(value.title, value.target) {
			t.Fatalf("真实标题格式或名称规范化未兼容: %s", value.title)
		}
	}
	if telegraphTitleMatches("!!! {tmdbid-206715}", ChannelSearchTarget{Title: "!!!", Kind: "tv", TMDBID: "206715"}) {
		t.Fatal("无有效片名时不能只凭数字确认作品")
	}
}

func TestTelegraphProgressSuffixKeepsExactWorkIdentity(t *testing.T) {
	target := ChannelSearchTarget{Title: "嫁金钗", Kind: "tv", TMDBID: "300146"}
	for _, title := range []string{
		"嫁金钗 全30集",
		"嫁金钗 (2026) 全30集 {tmdbid-300146}",
		"嫁金钗 全30集 (2026) {tmdbid-300146}",
		"嫁金钗 全 7 集 · S01E02-E08",
		"嫁金钗 (2026) 全 7 集 · S01E02-E08 {tmdbid=300146}",
		"嫁金钗 更新至第18集",
		"嫁金钗 更新至18集 {tmdbid-300146}",
	} {
		if !telegraphTitleMatches(title, target) {
			t.Fatalf("明确尾部集数说明不应改变作品身份: %q", title)
		}
	}
	for _, title := range []string{
		"嫁金钗续集 全30集",
		"嫁金钗 全30集续篇",
		"嫁金钗全30集",
		"嫁金钗 全30集 花絮",
		"全30集 嫁金钗",
		"嫁金钗 第30集",
		"嫁金钗 全30集 {tmdbid-42}",
		"嫁金钗 全30集 {tmdbid-300146} {tmdbid-42}",
		"嫁金钗续集 全 7 集 · S01E02-E08",
		"嫁金钗 全 7 集 · S01E02-E08 花絮",
		"嫁金钗 全 7 集 · S01E02-E08 {tmdbid-42}",
		"嫁金钗 全 7 集 · E02-E08",
		"嫁金钗 全 7 集 · S01E02-08",
		"嫁金钗 全 7 集 · S01E02-E",
		"嫁金钗 全 7 集 · S01E02-E08x",
		"嫁金钗 全 7 集 / S01E02-E08",
	} {
		if telegraphTitleMatches(title, target) {
			t.Fatalf("尾部说明适配放宽了片名或 TMDB 校验: %q", title)
		}
	}
	// 真实片名本身可能带集数文字，优先保留原有的精确名称匹配。
	for _, kind := range []string{"tv", "movie"} {
		if !telegraphTitleMatches("作品 全30集", ChannelSearchTarget{Title: "作品 全30集", Kind: kind}) {
			t.Fatalf("尾部适配破坏了原本精确一致的作品名: %s", kind)
		}
	}
}

func TestTelegraphProgressSuffixDoesNotInferTVForMovieTarget(t *testing.T) {
	target := ChannelSearchTarget{Title: "嫁金钗", Kind: "movie", TMDBID: "300146"}
	for _, title := range []string{"嫁金钗 全30集", "嫁金钗 全 7 集 · S01E02-E08", "嫁金钗 更新至第18集"} {
		// 页面没有 TMDB 外链时，也不能把剧集进度后缀用于同名电影。
		if _, err := parseTelegraph(telegraphFixture(title, []any{telegraphTestNode("p", channelED2K)}), target); err == nil {
			t.Fatalf("剧集进度后缀被用于电影条目: %s", title)
		}
	}
}

func TestTelegraphRealCompleteSeriesTitleStillChecksTMDBLinks(t *testing.T) {
	target := ChannelSearchTarget{Title: "嫁金钗", Kind: "tv", TMDBID: "300146"}
	file := "ed2k://|file|嫁金钗(2026)(tmdbid=300146).S01E06.1080p.mkv|12345|0123456789abcdef0123456789abcdef|/"
	for _, title := range []string{"嫁金钗 全30集", "嫁金钗 全 7 集 · S01E02-E08"} {
		for _, value := range []struct {
			href  string
			valid bool
		}{
			{"https://www.themoviedb.org/tv/300146", true},
			{"https://www.themoviedb.org/movie/300146", false},
			{"https://www.themoviedb.org/tv/42", false},
		} {
			anchor := telegraphTestNode("a", "TMDB")
			anchor["attrs"] = map[string]string{"href": value.href}
			result, err := parseTelegraph(telegraphFixture(title, []any{anchor, telegraphTestNode("p", file)}), target)
			if (err == nil) != value.valid {
				t.Fatalf("全集标题导致 TMDB 类型或 ID 校验变化: %s, %s, %v", title, value.href, err)
			}
			if value.valid && (len(result.Groups) != 1 || len(result.Groups[0].Links) != 1 || result.Groups[0].Links[0].Metadata.Episode != "S01E06") {
				t.Fatalf("真实全集标题未提取到第六集: %s, %+v", title, result)
			}
		}
	}
}

func TestTelegraphHeadingLinksStillValidateMediaKindAndID(t *testing.T) {
	target := ChannelSearchTarget{Title: "朝鲜律师", Kind: "tv", TMDBID: "206715"}
	for _, value := range []struct {
		href  string
		valid bool
	}{
		{"https://www.themoviedb.org/movie/206715", false},
		{"https://www.themoviedb.org/tv/42", false},
		{"https://www.themoviedb.org/TV/206715", true},
	} {
		anchor := telegraphTestNode("a", "版本")
		anchor["attrs"] = map[string]string{"href": value.href}
		data := telegraphFixture("朝鲜律师 (2023) {tmdbid-206715}", []any{telegraphTestNode("h3", telegraphTestNode("strong", anchor)), telegraphTestNode("p", channelED2K)})
		_, err := parseTelegraph(data, target)
		if (err == nil) != value.valid {
			t.Fatalf("标题内 TMDB 链接校验不符合预期: %s, %v", value.href, err)
		}
	}
}

func telegraphResolverFixture(t *testing.T) (*Client, ChannelResource) {
	t.Helper()
	c := testClient(t)
	if err := c.sessionStore.Save("session"); err != nil {
		t.Fatal(err)
	}
	c.authorized, c.userID = true, 1
	c.channels = channelSettings{OwnerID: 1, Peers: []channelPeer{{Channel: Channel{ID: "1"}}}}
	source, _ := channelTelegraphSource("https://telegra.ph/tmdbid-206715-11-17")
	item := ChannelResource{ID: "1:8", Sources: []ChannelSource{source}}
	c.channelSearches = map[string]*channelSearch{"search": {ownerID: 1, fingerprint: channelFingerprint(c.channels), expires: time.Now().Add(time.Minute), target: ChannelSearchTarget{Title: "朝鲜律师", Kind: "tv", TMDBID: "206715"}, result: ChannelSearchResult{Items: []ChannelResource{item}}}}
	return c, item
}

func TestTelegraphResolverRetriesCachesAndRequiresSavedCandidate(t *testing.T) {
	c, item := telegraphResolverFixture(t)
	target := ChannelSearchTarget{Kind: "tv", TMDBID: "206715"}
	var calls atomic.Int32
	data := telegraphFixture("朝鲜律师 {tmdbid-206715}", []any{channelED2K})
	c.telegraphHTTP = &http.Client{Transport: telegraphTestTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Scheme != "https" || r.URL.Host != "api.telegra.ph" || r.URL.Path != "/getPage/tmdbid-206715-11-17" || r.URL.Query().Get("return_content") != "true" {
			t.Errorf("没有使用固定官方页面接口: %s", r.URL)
		}
		if calls.Add(1) == 1 {
			return nil, errors.New("临时网络故障")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(data)))}, nil
	})}
	if _, err := c.ResolveChannelSource(context.Background(), "forged", item.Sources[0].ID, target); err == nil || calls.Load() != 0 {
		t.Fatal("伪造候选触发了网络读取")
	}
	if _, err := c.ResolveChannelSource(context.Background(), item.ID, item.Sources[0].ID, target); err == nil {
		t.Fatal("网络故障被隐藏")
	}
	result, err := c.ResolveChannelSource(context.Background(), item.ID, item.Sources[0].ID, target)
	if err != nil || result.Cached || len(result.Groups) != 1 || calls.Load() != 2 {
		t.Fatalf("失败后不能重试: %+v %v", result, err)
	}
	result, err = c.ResolveChannelSource(context.Background(), item.ID, item.Sources[0].ID, target)
	if err != nil || !result.Cached || calls.Load() != 2 {
		t.Fatalf("重复读取未使用缓存: %+v %v", result, err)
	}
	c.channelSearches["search"].expires = time.Now().Add(-time.Second)
	if _, err := c.ResolveChannelSource(context.Background(), item.ID, item.Sources[0].ID, target); err == nil || calls.Load() != 2 {
		t.Fatal("过期搜索仍可利用解析缓存")
	}
	c.channelSearches["search"].expires = time.Now().Add(time.Minute)
	c.userID = 2
	if _, err := c.ResolveChannelSource(context.Background(), item.ID, item.Sources[0].ID, target); err == nil || calls.Load() != 2 {
		t.Fatal("更换账号仍可利用原账号候选")
	}
}

func TestTelegraphResolverRejectsConfigurationChangedDuringFetch(t *testing.T) {
	c, item := telegraphResolverFixture(t)
	target := ChannelSearchTarget{Kind: "tv", TMDBID: "206715"}
	started, proceed := make(chan struct{}), make(chan struct{})
	data := telegraphFixture("朝鲜律师 {tmdbid-206715}", []any{channelED2K})
	c.telegraphHTTP = &http.Client{Transport: telegraphTestTransport(func(r *http.Request) (*http.Response, error) {
		close(started)
		select {
		case <-r.Context().Done():
			return nil, r.Context().Err()
		case <-proceed:
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(data)))}, nil
		}
	})}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var group errgroup.Group
	group.Go(func() error {
		_, err := c.ResolveChannelSource(ctx, item.ID, item.Sources[0].ID, target)
		var operation *OperationError
		if !errors.As(err, &operation) || operation.Code != "channel_search_expired" {
			return errors.New("配置改变后的解析结果未失效")
		}
		return nil
	})
	select {
	case <-started:
		c.mu.Lock()
		c.channels.Peers = nil
		c.mu.Unlock()
		close(proceed)
	case <-ctx.Done():
		t.Fatal("读取未开始")
	}
	if err := group.Wait(); err != nil {
		t.Fatal(err)
	}
}

func TestTelegraphResolverKeepsExplicitMediaContextAcrossOverlappingSearches(t *testing.T) {
	c, item := telegraphResolverFixture(t)
	newer := *c.channelSearches["search"]
	newer.expires = newer.expires.Add(time.Minute)
	newer.target = ChannelSearchTarget{Title: "其他电影", Kind: "movie", TMDBID: "206715"}
	c.channelSearches["other"] = &newer
	var calls atomic.Int32
	data := telegraphFixture("朝鲜律师 (2023) {tmdbid-206715}", []any{channelED2K})
	c.telegraphHTTP = &http.Client{Transport: telegraphTestTransport(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(data)))}, nil
	})}
	for _, target := range []ChannelSearchTarget{{Kind: "tv", TMDBID: "42"}, {Kind: "movie", TMDBID: "42"}} {
		if _, err := c.ResolveChannelSource(context.Background(), item.ID, item.Sources[0].ID, target); err == nil || calls.Load() != 0 {
			t.Fatal("未保存该作品的候选却触发了页面读取")
		}
	}
	// 同消息出现在更新的另一作品搜索中，旧弹窗仍必须使用自己的服务端片名。
	target := ChannelSearchTarget{Kind: "tv", TMDBID: "206715", Title: "不可信客户端片名"}
	result, err := c.ResolveChannelSource(context.Background(), item.ID, item.Sources[0].ID, target)
	if err != nil || result.Cached || len(result.Groups) != 1 || calls.Load() != 1 {
		t.Fatalf("旧作品上下文被更新的搜索覆盖: %+v %v", result, err)
	}
	// 即使客户端传入能匹配页面的片名，也不能替代另一作品已保存的真实片名。
	if _, err := c.ResolveChannelSource(context.Background(), item.ID, item.Sources[0].ID, ChannelSearchTarget{Kind: "movie", TMDBID: "206715", Title: "朝鲜律师"}); err == nil || calls.Load() != 2 {
		t.Fatal("其他作品复用了错误上下文或前一作品的解析缓存")
	}
	result, err = c.ResolveChannelSource(context.Background(), item.ID, item.Sources[0].ID, target)
	if err != nil || !result.Cached || calls.Load() != 2 {
		t.Fatal("同一作品没有独立复用自己的解析缓存")
	}
}

func TestTelegraphResolverRequiresValidMediaKey(t *testing.T) {
	c, item := telegraphResolverFixture(t)
	for _, target := range []ChannelSearchTarget{{}, {Kind: "person", TMDBID: "206715"}, {Kind: "tv"}, {Kind: "tv", TMDBID: "0"}, {Kind: "tv", TMDBID: "bad"}} {
		if _, err := c.ResolveChannelSource(context.Background(), item.ID, item.Sources[0].ID, target); !errors.Is(err, ErrInput) {
			t.Fatalf("作品键未被强制校验: %+v, %v", target, err)
		}
	}
}

func TestChannelStructuredMetadataPreservesUnknownSeasonAndRanges(t *testing.T) {
	for _, value := range []struct {
		text   string
		number int
	}{{"作品 第13集", 13}, {"作品 S01E01-02", -1}} {
		links := []ChannelLink{{Type: "115", URL: "https://115.com/s/x"}}
		metadata := channelMetadata(value.text, "", links)
		if metadata == nil || metadata.Season != nil || value.number < 0 && metadata.EpisodeNumber != nil || value.number >= 0 && (metadata.EpisodeNumber == nil || *metadata.EpisodeNumber != value.number) {
			t.Fatalf("未确认的季或集被推断: %+v", metadata)
		}
	}
}

func TestChannelEpisodeYearFieldDoesNotConsumeEpisodeNumber(t *testing.T) {
	for _, filename := range []string{"Series.S01E01.2026.1080p.mkv", "Series.S01E01.2026.2160p.NF.WEB-DL.mkv"} {
		links := []ChannelLink{{Type: "ed2k", URL: "ed2k://|file|" + filename + "|123|0123456789abcdef0123456789abcdef|/"}}
		metadata := channelMetadata("", "", links)
		if metadata == nil || metadata.Episode != "S01E01" || metadata.Season == nil || *metadata.Season != 1 || metadata.EpisodeNumber == nil || *metadata.EpisodeNumber != 1 {
			t.Fatalf("年份吞掉了明确季集: %+v", metadata)
		}
	}
}
