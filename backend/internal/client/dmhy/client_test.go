package dmhy

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/client/animefeed"
)

const fixtureHash = "439874fe083e1d0effab4eb4d5deaa8aefb33311"

func fixture(t testing.TB, name string) []byte {
	t.Helper()
	body, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestPaths(t *testing.T) {
	path, err := SearchPath("  幽游白书 & RAW  ", "648", 2)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(path)
	if u.Path != "/topics/list/page/2" || u.Query().Get("keyword") != "幽游白书 & RAW" || u.Query().Get("team_id") != "648" || u.Query().Get("sort_id") != "" || !validFetchPath(path) {
		t.Fatalf("搜索条件错误: %s", path)
	}
	for _, query := range []string{"", " \t ", "片名\n另一行", strings.Repeat("中", 201)} {
		if _, err := NormalizeQuery(query); !errors.Is(err, ErrInput) {
			t.Fatalf("非法词被接受: %q", query)
		}
	}
	for _, page := range []int{0, -1, MaxPage + 1} {
		if _, err := SearchPath("片名", "", page); !errors.Is(err, ErrInput) {
			t.Fatalf("非法页码: %d", page)
		}
	}
	for _, group := range []string{"0", "-1", "1/2", "abc", strings.Repeat("1", 13)} {
		if ValidGroup(group) {
			t.Fatalf("非法字幕组: %s", group)
		}
	}
	if path, err := DetailPath("658424"); err != nil || path != "/topics/view/658424_.html" || !validFetchPath(path) {
		t.Fatalf("详情路径错误: %s %v", path, err)
	}
	for _, path := range []string{"https://evil.test/", "//evil.test/", "/topics/view/658424_.html?url=https://evil.test", "/topics/view/1_.html#x", "/topics/list/page/1001?keyword=x&order=date-desc", "/topics/list?keyword=x&order=date-desc&keyword=y", "/topics/list?keyword=x&order=date-desc&sort_id=2", "/topics/list?keyword=x&order=date-desc&team_id=-1", "/topics/view/%31_.html"} {
		if validFetchPath(path) {
			t.Fatalf("不受控路径: %s", path)
		}
	}
}

func TestParseSearchCategoriesAndMissingMagnets(t *testing.T) {
	out, err := ParseSearch(fixture(t, "search.html"), 1)
	if err != nil || len(out.Items) != 7 || out.RawCount != 7 {
		t.Fatalf("实站片段失败: %#v %v", out, err)
	}
	categories := make(map[int]string, 5)
	for _, item := range out.Items {
		categories[item.CategoryID] = item.CategoryScope
		if item.Title == "" || item.Publisher == "" || item.UpdatedAt == "" || item.Size == "" || !strings.HasPrefix(item.URL, BaseURL+"/topics/view/") {
			t.Fatalf("缺少字段: %#v", item)
		}
	}
	for _, id := range []int{2, 31, 6, 7} {
		if categories[id] != "film" {
			t.Fatalf("影视分类遗漏: %d", id)
		}
	}
	if categories[3] != "other" || CategoryScope(999) != "supplement" || CategoryScope(43) != "supplement" {
		t.Fatal("未知或非影视分类错误")
	}
	for _, item := range out.Items[2:] {
		if item.Seeders != -1 || item.Leechers != -1 || item.Completed != -1 {
			t.Fatalf("未知统计未保留: %#v", item)
		}
	}
	for _, expected := range []struct {
		index    int
		id, hash string
	}{
		{5, "162636", "b8ac8b9f5e3a3eb1e98e744db8f72d8a9604b136"},
		{6, "160765", "ff95cb490af2134cfcacfd02e04776f032ff10fc"},
	} {
		if item := out.Items[expected.index]; item.ID != expected.id || item.Magnet != "magnet:?xt=urn:btih:"+expected.hash || item.MagnetIssue != "" {
			t.Fatalf("真实旧帖备用磁力丢失: %#v", item)
		}
	}
	if out.Items[5].GroupID != "331" {
		t.Fatal("旧帖字幕组丢失", out.Items[5])
	}
	// 无磁力旧帖仍保留查看入口；只按原帖编号去重，不合并同标题。
	body := "<table id='topic_list'><tbody>" + row("123", 999, "") + row("124", 31, "magnet:?xt=urn:btih:"+fixtureHash) + row("124", 31, "magnet:?xt=urn:btih:"+fixtureHash) + "</tbody></table>"
	out, err = ParseSearch([]byte(body), 1)
	if err != nil || len(out.Items) != 2 || out.RawCount != 3 || out.Items[0].Magnet != "" || out.Items[0].MagnetIssue != "missing" || out.Items[0].CategoryScope != "supplement" {
		t.Fatalf("无磁力或原帖身份过滤错误: %#v %v", out, err)
	}
	encoded := strings.ReplaceAll(row("125", 2, ""), "125_Show.html", "125_Show%21.html")
	if out, err := ParseSearch([]byte("<table id='topic_list'>"+encoded+"</table>"), 1); err != nil || out.Items[0].URL != BaseURL+"/topics/view/125_Show%21.html" {
		t.Fatalf("转义标题链接丢失: %#v %v", out, err)
	}
	longMagnet := "magnet:?xt=urn:btih:LIVRUWOWBG5YFGXHVM42FG3A2VA3FFEU" + strings.Repeat("&tr=https%3A%2F%2Ftracker.example%2Fannounce", 1000)
	if out, err := ParseSearch([]byte("<table id='topic_list'>"+row("251156", 31, longMagnet)+"</table>"), 1); err != nil || out.Items[0].Magnet != "magnet:?xt=urn:btih:5a2b1a59d609bb829ae7ab39a29b60d541b29494" {
		t.Fatalf("旧帖冗长 tracker 导致资源丢失: %#v %v", out, err)
	}
}

func TestSearchMagnetSourcesAndPerItemFailures(t *testing.T) {
	const hexMagnet = "magnet:?xt=urn:btih:ff95cb490af2134cfcacfd02e04776f032ff10fc"
	const base32Magnet = "magnet:?xt=urn:btih:76K4WSIK6IJUZ7FM7UBOAR3W6AZP6EH4"
	const otherMagnet = "magnet:?xt=urn:btih:1111111111111111111111111111111111111111"
	for _, tc := range []struct{ name, links, issue string }{
		{"备用磁力", `<a href=""></a><a href="javascript:void(0);" data-magnet="` + hexMagnet + `"></a>`, ""},
		{"同一元素双来源", `<a href="` + base32Magnet + `" data-magnet="` + hexMagnet + `"></a>`, ""},
		{"两种编码一致", `<a href="` + base32Magnet + `"></a><a data-magnet="` + hexMagnet + `"></a>`, ""},
		{"没有提供", `<a href=""></a><a href="javascript:void(0);"></a>`, "missing"},
		{"站外链接不作为磁力", `<a href="https://evil.test/?magnet=` + hexMagnet + `"></a>`, "missing"},
		{"坏备用值", `<a data-magnet="javascript:alert(1)"></a>`, "invalid"},
		{"合法值旁有坏值", `<a href="` + base32Magnet + `"></a><a data-magnet="magnet:?xt=broken"></a>`, "invalid"},
		{"重复xt", `<a data-magnet="` + hexMagnet + `&amp;xt=urn:btih:ff95cb490af2134cfcacfd02e04776f032ff10fc"></a>`, "invalid"},
		{"多身份冲突", `<a href="` + base32Magnet + `"></a><a data-magnet="` + otherMagnet + `"></a>`, "conflict"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			first := strings.Replace(row("160765", 2, ""), "</td><td>1 GB", tc.links+"</td><td>1 GB", 1)
			body := `<a data-magnet="` + otherMagnet + `"></a><table id="topic_list">` + first + row("123", 31, hexMagnet) + `</table>`
			out, err := ParseSearch([]byte(body), 1)
			if err != nil || out.RawCount != 2 || len(out.Items) != 2 {
				t.Fatalf("单条异常影响了整页: %#v %v", out, err)
			}
			item := out.Items[0]
			if item.MagnetIssue != tc.issue || tc.issue == "" && item.Magnet != hexMagnet || tc.issue != "" && item.Magnet != "" || out.Items[1].Magnet != hexMagnet || out.Items[1].MagnetIssue != "" {
				t.Fatalf("磁力未正确验证或隔离: %#v", out.Items)
			}
		})
	}
}

func row(id string, category int, magnet string) string {
	link := ""
	if magnet != "" {
		link = "<a href='" + magnet + "'>磁力</a>"
	}
	return "<tr><td>2026/11/16 02:00<span style='display:none'>2026/11/16 02:00</span></td><td><a href='/topics/list/sort_id/" + strconv.Itoa(category) + "'>分类</a></td><td><a href='/topics/list/team_id/648'>字幕组</a><a href='/topics/view/" + id + "_Show.html'>Show &amp; 合集</a><a href='/topics/view/" + id + "_Show.html#comments'>评论</a></td><td>" + link + "</td><td>1 GB</td><td>1,234</td><td>0</td><td>20</td><td>发布者</td></tr>"
}

func TestSearchPagingEmptyChallengeAndMalformedRows(t *testing.T) {
	body := "<table id='topic_list'><tbody>" + row("123", 2, "magnet:?xt=urn:btih:"+fixtureHash) + "</tbody></table><a href='/topics/list/page/3?keyword=Show'>下一頁</a>"
	out, err := ParseSearch([]byte(body), 2)
	if err != nil || !out.HasMore || out.Items[0].Seeders != 1234 || out.Items[0].Title != "Show & 合集" {
		t.Fatalf("分页或字段错误: %#v %v", out, err)
	}
	if out, err := ParseSearch([]byte(body), 1); err != nil || out.HasMore {
		t.Fatalf("非连续下一页错误: %#v %v", out, err)
	}
	if out, err := ParseSearch([]byte("<input name='keyword'><div>沒有可顯示資源</div>"), 1); err != nil || len(out.Items) != 0 || out.HasMore {
		t.Fatalf("零结果错误: %#v %v", out, err)
	}
	for _, body := range []string{"<html>维护中</html>", "<div>沒有可顯示資源</div>", "<table id='topic_list'></table>", "<table id='topic_list'><tr><td>异常条目</td></tr></table>", "<table id='topic_list'>" + strings.ReplaceAll(row("123", 2, "magnet:?xt=urn:btih:"+fixtureHash), "/topics/view/123_Show.html", "https://evil.test/topics/view/123_Show.html") + "</table>"} {
		if _, err := ParseSearch([]byte(body), 1); !errors.Is(err, ErrParse) {
			t.Fatalf("异常页被接受: %s %v", body, err)
		}
	}
	for _, body := range []string{"<title>Just a moment...</title>", "<form id='challenge-form'></form>"} {
		if _, err := ParseSearch([]byte(body), 1); !errors.Is(err, ErrBlocked) {
			t.Fatalf("验证页未识别: %v", err)
		}
	}
}

func TestParseDetailResourceBlockAndTorrentIdentity(t *testing.T) {
	body := fixture(t, "detail.html")
	out, err := ParseDetail(body, "658424")
	if err != nil || out.ID != "658424" || out.Magnet != "magnet:?xt=urn:btih:"+fixtureHash || out.TorrentURL != "https://dl.dmhy.org/2023/12/14/"+fixtureHash+".torrent" {
		t.Fatalf("实站详情片段错误: %#v %v", out, err)
	}
	advert := []byte("<a href='magnet:?xt=urn:btih:1111111111111111111111111111111111111111'>广告</a><a href='https://evil.test/x.torrent'>广告</a>")
	if _, err := ParseDetail(append(advert, body...), "658424"); err != nil {
		t.Fatalf("正文外广告干扰: %v", err)
	}
	if _, err := ParseDetail(body, "658425"); !errors.Is(err, ErrParse) {
		t.Fatalf("详情原帖编号未独立校验: %v", err)
	}
	for _, changed := range [][]byte{
		bytes.ReplaceAll(body, []byte("topic-title"), []byte("sidebar")),
		bytes.ReplaceAll(body, []byte("http://share.dmhy.org/topics/view/"), []byte("http://evil.test/topics/view/")),
		bytes.ReplaceAll(body, []byte("/js/share.html?"), []byte("//evil.test/js/share.html?")),
		append([]byte("<link rel='canonical' href='https://share.dmhy.org/topics/view/1_.html'>"), body...),
	} {
		if _, err := ParseDetail(changed, "658424"); !errors.Is(err, ErrParse) {
			t.Fatalf("缺失、站外或冲突原帖证据被接受: %v", err)
		}
	}
	for _, body := range [][]byte{bytes.ReplaceAll(body, []byte("dl.dmhy.org"), []byte("evil.test")), bytes.ReplaceAll(body, []byte(fixtureHash+".torrent"), []byte(strings.Repeat("1", 40)+".torrent")), []byte("<div id='resource-tabs'><a href='magnet:?xt=urn:btih:" + fixtureHash + "'>磁力</a></div>"), []byte("<div id='resource-tabs'><a href='magnet:?xt=urn:btih:" + fixtureHash + "'>磁力</a><a href='magnet:?xt=urn:btih:" + strings.Repeat("1", 40) + "'>冲突</a></div>")} {
		if _, err := ParseDetail(body, "658424"); !errors.Is(err, ErrParse) {
			t.Fatalf("无效详情被接受: %v", err)
		}
	}
}

func TestDetailMagnetResolutionWithoutTorrent(t *testing.T) {
	const magnet = "magnet:?xt=urn:btih:ff95cb490af2134cfcacfd02e04776f032ff10fc"
	const other = "magnet:?xt=urn:btih:1111111111111111111111111111111111111111"
	prefix := `<link rel="canonical" href="https://share.dmhy.org/topics/view/160765_.html"><a data-magnet="` + other + `"></a><div id="resource-tabs">`
	for _, tc := range []struct {
		name, links string
		valid       bool
	}{
		{"详情只提供备用磁力", `<a href="javascript:void(0);" data-magnet="` + magnet + `"></a>`, true},
		{"详情只提供普通磁力", `<a href="` + magnet + `"></a>`, true},
		{"详情缺少磁力", `<a href=""></a>`, false},
		{"详情备用磁力坏值", `<a href="` + magnet + `"></a><a data-magnet="magnet:?xt=broken"></a>`, false},
		{"详情身份冲突", `<a href="` + magnet + `"></a><a data-magnet="` + other + `"></a>`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := ParseDetail([]byte(prefix+tc.links+`</div>`), "160765")
			if !tc.valid {
				if !errors.Is(err, ErrParse) {
					t.Fatalf("不可靠磁力被接受: %#v %v", out, err)
				}
				return
			}
			if err != nil || out.ID != "160765" || out.Magnet != magnet || out.TorrentURL != "" {
				t.Fatalf("解析磁力不应要求种子地址或受正文外广告影响: %#v %v", out, err)
			}
		})
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestHTTPFetchBoundariesAndNoCredentials(t *testing.T) {
	jar, _ := cookiejar.New(nil)
	base, _ := url.Parse(BaseURL)
	jar.SetCookies(base, []*http.Cookie{{Name: "secret", Value: "do-not-send"}})
	calls := 0
	client := &Client{HTTP: &http.Client{Jar: jar, Transport: roundTrip(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.URL.Host != "share.dmhy.org" || req.URL.Scheme != "https" || req.Header.Get("Cookie") != "" || req.Header.Get("Authorization") != "" || req.Header.Get("User-Agent") == "" {
			t.Fatalf("请求边界错误: %#v", req)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("body"))}, nil
	})}}
	path, _ := SearchPath("Show", "", 1)
	if body, err := client.Fetch(context.Background(), path, 4); err != nil || string(body) != "body" {
		t.Fatalf("精确大小读取失败: %q %v", body, err)
	}
	if _, err := client.Fetch(context.Background(), path, 3); !errors.Is(err, ErrParse) {
		t.Fatalf("超限被接受: %v", err)
	}
	if _, err := client.Fetch(context.Background(), "https://evil.test", 10); !errors.Is(err, ErrInput) || calls != 2 {
		t.Fatalf("站外请求: %d %v", calls, err)
	}
	client.HTTP.Transport = roundTrip(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": {"https://evil.test"}}, Body: io.NopCloser(strings.NewReader("redirect"))}, nil
	})
	if _, err := client.Fetch(context.Background(), path, 100); err == nil {
		t.Fatal("重定向被当作有效页面")
	}
	client.HTTP.Transport = roundTrip(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 429, Header: http.Header{"Retry-After": {"3"}}, Body: io.NopCloser(strings.NewReader("limited"))}, nil
	})
	_, err := client.Fetch(context.Background(), path, 100)
	var limited *animefeed.HTTPError
	if !errors.As(err, &limited) || limited.Status != 429 || !limited.RetryAt.After(time.Now()) {
		t.Fatalf("冷却信息丢失: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := New().Fetch(ctx, path, 100); !errors.Is(err, context.Canceled) {
		t.Fatalf("取消未退出: %v", err)
	}
}

func BenchmarkParseSearch(b *testing.B) {
	body := fixture(b, "search.html")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := ParseSearch(body, 1); err != nil {
			b.Fatal(err)
		}
	}
}
