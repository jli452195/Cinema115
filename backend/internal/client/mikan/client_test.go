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

const testHash = "d3587d3e2b82b2a87bdb79634c9d046420ba19de"

func testRow(hash, title, date string) string {
	return fmt.Sprintf(`<tr class="js-search-results-row"><td><input data-magnet="magnet:?xt=urn:btih:%s&amp;tr=https%%3A%%2F%%2Ftracker.test" /></td><td><a class="magnet-link-wrap" href="/Home/Episode/%s">%s</a><a class="js-magnet" data-clipboard-text="magnet:?xt=urn:btih:%s">复制</a></td><td>1.7GB</td><td>%s</td><td><a href="/Download/20150129/%s.torrent">下载</a></td></tr>`, hash, hash, title, hash, date, hash)
}
func testPage(rows string) string {
	return `<div class="episode-table"><table><tbody>` + rows + `</tbody></table></div>`
}

func TestParseSearch(t *testing.T) {
	hash2 := "9df6051f95a1818cf21a76f0753c897f72adfcc4"
	page := `<a class="subgroup-longname" data-subgroupid="615">字幕&amp;组</a><a class="subgroup-longname" data-subgroupid="615">重复</a>` + testPage(testRow(testHash, "&#x5973;生万岁 [1920x1080]", "2015/01/29 14:25")+testRow(hash2, "[2160p] 新资源", "2026/01/01 12:00")+testRow(testHash, "重复", "2015/01/29 14:25")) + `<div class="m-search-item">移动端重复资源</div>`
	result, err := Parse(strings.NewReader(page))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 2 || result.Items[0].ID != hash2 || result.Items[1].Title != "女生万岁 [1920x1080]" {
		t.Fatalf("解析结果错误 %+v", result)
	}
	if result.Items[0].Resolution != "2160P" || result.Items[1].Resolution != "1080P" {
		t.Fatal(result.Items)
	}
	if len(result.Groups) != 1 || result.Groups[0].Name != "字幕&组" {
		t.Fatal(result.Groups)
	}
	if result.Items[0].URL != BaseURL+"/Home/Episode/"+hash2 || !strings.HasPrefix(result.Items[0].Magnet, "magnet:") {
		t.Fatal(result.Items[0])
	}
}
func TestParseAllRowsAndEmpty(t *testing.T) {
	var rows strings.Builder
	for i := 0; i < 646; i++ {
		rows.WriteString(testRow(fmt.Sprintf("%040x", i), "资源 1080P", "2026/09/17 21:31"))
	}
	result, err := Parse(strings.NewReader(testPage(rows.String())))
	if err != nil || len(result.Items) != 646 {
		t.Fatal(len(result.Items), err)
	}
	result, err = Parse(strings.NewReader(testPage("")))
	if err != nil || result.Items == nil || len(result.Items) != 0 {
		t.Fatal(result, err)
	}
	emptyPage := `<input id="header-search" name="searchstr" value="空结果"><div class="no-subscribe-bangumi hidden-sm hidden-xs">"空结果" 找不到对应结果 &gt;_&lt; 试试别的关键词吧。</div>`
	result, err = Parse(strings.NewReader(emptyPage))
	if err != nil || result.Items == nil || len(result.Items) != 0 {
		t.Fatal("独立空结果页识别失败", result, err)
	}
	if _, err = Parse(strings.NewReader(`<div>找不到对应结果</div>`)); !errors.Is(err, ErrParse) {
		t.Fatal("不完整页面误认为零结果", err)
	}
}
func TestRejectBrokenPagesAndLinks(t *testing.T) {
	row := testRow(testHash, "标题", "2026/09/17 21:31")
	changed := testPage(strings.ReplaceAll(row, "js-search-results-row", "changed-row"))
	if _, err := Parse(strings.NewReader(changed)); !errors.Is(err, ErrParse) {
		t.Fatal("行标记变化被当成空结果", err)
	}
	for _, page := range []string{"<html>Just a moment...</html>", testPage(strings.Replace(row, "urn:btih:", "urn:bad:", 2)), testPage(strings.Replace(row, "href=\"/Home/Episode/", "href=\"https://evil.test/Home/Episode/", 1)), testPage("<tr class=\"js-search-results-row\"><td>结构变化</td></tr>")} {
		if _, err := Parse(strings.NewReader(page)); !errors.Is(err, ErrParse) {
			t.Fatal("异常页面被当成正常结果", err)
		}
	}
	for _, link := range []string{"javascript:alert(1)", "//evil.test/Home/Episode/x", "https://mikanani.me@evil.test/Home/Episode/x", "https://mikanani.me/Home/Episode/x?token=secret"} {
		if siteLink(link, "/Home/Episode/") != "" {
			t.Fatal(link)
		}
	}
}
func TestClientHTTPAndCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/Home/Search" || r.URL.Query().Get("searchstr") != "Re：从零" || r.URL.Query().Get("subgroupid") != "615" {
			t.Error(r.URL.String())
		}
		fmt.Fprint(w, testPage(testRow(testHash, "Re：从零", "2026/09/17 21:31")))
	}))
	defer server.Close()
	client := New()
	client.BaseURL = server.URL
	if result, err := client.Search(context.Background(), "Re：从零", "615"); err != nil || len(result.Items) != 1 {
		t.Fatal(result, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.Search(ctx, "Re：从零", "615"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for _, q := range []string{"", strings.Repeat("字", 201), "标题\n伪造日志"} {
		if _, err := NormalizeQuery(q); err == nil {
			t.Fatal(q)
		}
	}
	if ValidGroup("615&x=1") {
		t.Fatal("字幕组参数未校验")
	}
}
func TestBlockedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(403) }))
	defer server.Close()
	client := New()
	client.BaseURL = server.URL
	if _, err := client.Search(context.Background(), "测试", ""); !errors.Is(err, ErrBlocked) {
		t.Fatal(err)
	}
}

// 实站检查只读公开搜索页，默认跳过，不下载种子或提交云下载任务。
func TestLiveSearch(t *testing.T) {
	if os.Getenv("MIKAN_LIVE") != "1" {
		t.Skip("未启用公开站点检查")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	for _, q := range []string{"女生万岁", "Re：从零开始的异世界生活"} {
		result, err := New().Search(ctx, q, "")
		if err != nil || len(result.Items) == 0 {
			t.Fatalf("%s: %v", q, err)
		}
		t.Logf("%s：%d 条资源，%d 个字幕组", q, len(result.Items), len(result.Groups))
	}
	result, err := New().Search(ctx, "Cinema115-no-result-98c4b19e80f342f9", "")
	if err != nil || len(result.Items) != 0 {
		t.Fatal("实站零结果识别失败", err)
	}
	result, err = New().Search(ctx, "Re：从零开始的异世界生活", "615")
	if err != nil || len(result.Items) == 0 {
		t.Fatal("实站字幕组筛选失败", err)
	}
	t.Logf("字幕组 615 筛选：%d 条资源", len(result.Items))
}
