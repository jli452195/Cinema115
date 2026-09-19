package mikan

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func cardHTML(id, title, cover string) string {
	return `<a href="/Home/Bangumi/` + id + `"><span data-src="` + cover + `"></span><div class="an-text" title="` + title + `">截断名称</div></a>`
}

func bangumiPage() string {
	return `<p class="bangumi-title">一弦定音！</p><a class="subgroup-name" data-anchor="#19">字幕组甲</a><a class="subgroup-name" data-anchor="#146">字幕组乙</a><div class="subgroup-text" id="19"></div>` +
		testPage(strings.ReplaceAll(testRow(testHash, "第一组资源", "2019/01/01 12:00"), ` class="js-search-results-row"`, "")) +
		`<a class="js-expand-episode" data-bangumiid="1940" data-subtitlegroupid="19" data-take="65">更多</a><div class="subgroup-text" id="146"></div>` +
		testPage(strings.ReplaceAll(testRow(strings.Repeat("a", 40), "第二组资源", "2019/02/01 12:00"), ` class="js-search-results-row"`, ""))
}

func TestParseBangumiCardsOptionalAndSafe(t *testing.T) {
	cards := cardHTML("1940", "一弦定音！", "/images/Bangumi/201904/poster.jpg?width=400&amp;height=400") + cardHTML("1940", "重复", "") + cardHTML("2077", "一弦定音！第2部分", "https://evil.test/cover.jpg") + cardHTML("bad", "无效编号", "")
	result, err := Parse(strings.NewReader(cards + testPage(testRow(testHash, "磁力资源", "2019/01/01 12:00"))))
	if err != nil || len(result.Bangumis) != 2 || len(result.Items) != 1 {
		t.Fatal(result, err)
	}
	if result.Bangumis[0].Title != "一弦定音！" || !strings.HasPrefix(result.Bangumis[0].CoverURL, BaseURL+"/images/Bangumi/") || result.Bangumis[1].CoverURL != "" {
		t.Fatal(result.Bangumis)
	}
	for _, raw := range []string{"//evil.test/images/Bangumi/a.jpg", "javascript:alert(1)", "https://mikanani.me@evil.test/images/Bangumi/a.jpg", "/images/Bangumi/../../secret", "/images/Bangumi/a/../x.jpg"} {
		if coverLink(raw) != "" {
			t.Fatal(raw)
		}
	}
	result, err = Parse(strings.NewReader(testPage("")))
	if err != nil || result.Bangumis == nil || len(result.Bangumis) != 0 {
		t.Fatal(result, err)
	}
}

func TestParseBangumiGroupsAndMore(t *testing.T) {
	result, err := parse(strings.NewReader(bangumiPage()), "1940", "", 0)
	if err != nil || len(result.Items) != 2 || len(result.Groups) != 2 || len(result.More) != 1 {
		t.Fatal(result, err)
	}
	if result.Bangumi.Title != "一弦定音！" || result.Items[0].GroupID != "146" || result.Items[1].GroupID != "19" || result.More[0].Take != 65 {
		t.Fatal(result)
	}
	for _, page := range []string{
		strings.ReplaceAll(bangumiPage(), `id="19"`, `id="invalid"`),
		strings.ReplaceAll(bangumiPage(), `data-bangumiid="1940"`, `data-bangumiid="2077"`),
		strings.ReplaceAll(bangumiPage(), "urn:btih:", "urn:bad:"),
		"<html>Just a moment...</html>",
	} {
		if _, err := parse(strings.NewReader(page), "1940", "", 0); !errors.Is(err, ErrParse) {
			t.Fatal(err)
		}
	}
}

func TestBangumiExpansionAndValidation(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path == "/Home/Bangumi/1940" {
			fmt.Fprint(w, bangumiPage())
			return
		}
		if r.URL.Path != "/Home/ExpandEpisodeTable" || r.URL.Query().Get("bangumiId") != "1940" || r.URL.Query().Get("subtitleGroupId") != "19" {
			t.Error(r.URL)
		}
		var rows strings.Builder
		for i := 0; i < 65; i++ {
			rows.WriteString(strings.ReplaceAll(testRow(fmt.Sprintf("%040x", i), "资源", "2019/01/01 12:00"), ` class="js-search-results-row"`, ""))
		}
		fmt.Fprint(w, testPage(rows.String()))
	}))
	defer server.Close()
	client := New()
	client.BaseURL = server.URL
	result, err := client.Bangumi(context.Background(), "1940", "", 0)
	if err != nil || len(result.Items) != 2 {
		t.Fatal(result, err)
	}
	result, err = client.Bangumi(context.Background(), "1940", "19", 65)
	if err != nil || len(result.Items) != 65 || len(result.More) != 1 || result.More[0].Take != 115 || result.Items[0].GroupID != "19" {
		t.Fatal(result, err)
	}
	result, err = client.Bangumi(context.Background(), "1940", "19", 115)
	if err != nil || len(result.More) != 0 {
		t.Fatal(result, err)
	}
	for _, input := range []struct {
		id, group string
		take      int
	}{{"https://evil.test", "", 0}, {"../1940", "", 0}, {"1940", "19&bad=1", 65}, {"1940", "", 65}, {"1940", "19", 0}, {"1940", "19", 10001}} {
		if _, err := client.Bangumi(context.Background(), input.id, input.group, input.take); !errors.Is(err, ErrInput) {
			t.Fatal(input, err)
		}
	}
	if calls != 3 {
		t.Fatal(calls)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.Bangumi(ctx, "1940", "", 0); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

// 实站检查只读取公开番剧页及展开列表，不下载种子，也不调用 115。
func TestLiveBangumi(t *testing.T) {
	if os.Getenv("MIKAN_LIVE") != "1" {
		t.Skip("未启用公开站点检查")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	client := New()
	for _, query := range []string{"一弦定音！", "女生万岁"} {
		result, err := client.Search(ctx, query, "")
		if err != nil || len(result.Items) == 0 {
			t.Fatal(query, err)
		}
		if query == "一弦定音！" && len(result.Bangumis) != 2 {
			t.Fatal(result.Bangumis)
		}
		t.Logf("%s：%d 个番剧入口，%d 条资源", query, len(result.Bangumis), len(result.Items))
	}
	result, err := client.Bangumi(ctx, "1940", "", 0)
	if err != nil || len(result.Items) == 0 || len(result.More) == 0 {
		t.Fatal(result, err)
	}
	t.Logf("番剧 1940：%d 条首屏资源，%d 个字幕组，%d 个待展开字幕组", len(result.Items), len(result.Groups), len(result.More))
	more := result.More[0]
	for i := 0; i < 3; i++ {
		expanded, err := client.Bangumi(ctx, "1940", more.GroupID, more.Take)
		if err != nil || len(expanded.Items) == 0 {
			t.Fatal(err)
		}
		t.Logf("字幕组 %s：take=%d，取得 %d 条，还有更多=%t", more.GroupID, more.Take, len(expanded.Items), len(expanded.More) > 0)
		if len(expanded.More) == 0 {
			return
		}
		more = expanded.More[0]
	}
	t.Fatal("测试番剧未能完成加载")
}
