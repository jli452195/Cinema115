package woniu

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const searchFixture = `<title>搜索你的名字 - 蜗牛 I 4K</title><a class="video-card" href="/voddetail/34033/" title="你的名字"><div class="video-poster"><img src="/upload/vod/16ba17a96691e6dd57ac40d8236bf9ca.jpg"></div><div class="video-meta">2016 · JP · 电影</div></a><a class="video-card" href="/voddetail/8603/" title="你的名字。"><div class="video-meta">2016 · 电影</div></a><a href="/vodsearch/你的名字----------2---/">下一页</a>`
const detailFixture = `<h1 class="premium-title">你的名字。</h1><div class="meta-item"><span class="m-label">更新时间</span><span class="m-val">2026-10-01 17:06</span></div><div class="pan-group"><h3>115离线下载</h3><div class="pan-link-item" data-pan-item><div class="pan-link-title">4K WEB HDR[12.73G]</div><div class="pan-link-meta">https://115cdn.com/s/abc?password=AbCD</div><a class="pan-link-btn" href="https://115cdn.com/s/abc?password=AbCD">打开</a><button data-copy="https://115cdn.com/s/abc?password=AbCD">复制</button></div><div class="pan-link-item"><div class="pan-link-title">另一个版本</div><a href="https://115cdn.com/s/abc?password=abcd">打开</a></div><div class="pan-link-item"><span class="pan-link-title">百度版本</span><span class="pan-link-meta">https://pan.baidu.com/s/share 提取码：X1yZ</span></div></div>`

func localClient(t *testing.T, handler http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := New(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	client.searchInterval = 0
	return client, server
}

func TestWoniuParseSearchPaginationAndOrigins(t *testing.T) {
	base, _ := url.Parse(BaseURL)
	fixture := searchFixture + `<a class="video-card" href="https://evil.test/voddetail/1/" title="evil"></a><a class="video-card" href="/voddetail/34033/" title="重复"></a>`
	result, err := parseSearch([]byte(fixture), base, "你的名字", "title", 1)
	if err != nil || len(result.Items) != 2 || !result.HasMore || result.NextPage != 2 || result.Items[0].ID != "34033" || result.Items[0].Year != 2016 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if result.Items[0].CoverURL != "/api/woniu/image/vod/16ba17a96691e6dd57ac40d8236bf9ca.jpg" {
		t.Fatal(result.Items[0].CoverURL)
	}
	result, err = parseSearch([]byte(strings.ReplaceAll(searchFixture, "----------2---", "----------5---")), base, "你的名字", "title", 4)
	if err != nil || !result.Truncated || result.HasMore {
		t.Fatalf("cap: %+v %v", result, err)
	}
}

func TestWoniuEncodedChineseNextPage(t *testing.T) {
	base, _ := url.Parse(BaseURL)
	fixture := strings.ReplaceAll(searchFixture, "/vodsearch/你的名字----------2---/", "/vodsearch/%E4%BD%A0%E7%9A%84%E5%90%8D%E5%AD%97----------2---/")
	result, err := parseSearch([]byte(fixture), base, "你的名字", "title", 1)
	if err != nil || !result.HasMore {
		t.Fatal(result.HasMore, err)
	}
	client, _ := localClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/vodsearch/你的名字----------2---/" {
			t.Error("wrong decoded path", r.URL.Path)
		}
		fmt.Fprint(w, `<title>搜索你的名字 - 蜗牛 I 4K</title>`)
	})
	if _, err := client.Search(context.Background(), "你的名字", "title", 2); err != nil {
		t.Fatal(err)
	}
}

func TestWoniuDatedPosterDirectories(t *testing.T) {
	base, _ := url.Parse(BaseURL)
	name := "8c1b8e2da15ab048447c27af382f141b.jpg"
	for _, path := range []string{"/upload/vod/20260621-1/" + name, "/upload/vod/20260621/" + name} {
		fixture := strings.ReplaceAll(searchFixture, "/upload/vod/16ba17a96691e6dd57ac40d8236bf9ca.jpg", path)
		result, err := parseSearch([]byte(fixture), base, "你的名字", "title", 1)
		if err != nil || result.Items[0].CoverURL != strings.Replace(path, "/upload/vod/", "/api/woniu/image/", 1) {
			t.Fatal(path, result, err)
		}
	}
	for _, path := range []string{"/upload/vod/../../" + name, "/upload/vod/20260621-1/nested/" + name, "https://evil.test/upload/vod/20260621-1/" + name, "/upload/vod/not-a-date/" + name} {
		fixture := strings.ReplaceAll(searchFixture, "/upload/vod/16ba17a96691e6dd57ac40d8236bf9ca.jpg", path)
		result, err := parseSearch([]byte(fixture), base, "你的名字", "title", 1)
		if err != nil || len(result.Items) != 2 || result.Items[0].ID != "34033" || result.Items[0].CoverURL != "" {
			t.Fatal("unsafe poster", path, result, err)
		}
	}
}

func TestWoniuParseDetailShareLabelsPasswordsAndSources(t *testing.T) {
	item, err := parseDetail([]byte(detailFixture), SearchItem{ID: "8603", Title: "你的名字。"})
	if err != nil || len(item.Links) != 3 || item.DetailStatus != "ready" {
		t.Fatalf("%+v %v", item, err)
	}
	if item.Links[0].Category != "pan" || item.Links[0].Type != "115" || item.Links[0].Password != "AbCD" || item.Links[2].Password != "X1yZ" {
		t.Fatal(item.Links)
	}
	if item.Links[0].UpdatedAt != "2026-10-01 17:06" {
		t.Fatal(item.Links)
	}
	for _, fixture := range []string{`<div class="pan-lock-box"></div>`, `<h1 class="premium-title">电影</h1><div class="pan-link-meta">https://******（登录后可见）</div>`} {
		if _, err := parseDetail([]byte(fixture), item); !errors.Is(err, ErrSession) {
			t.Fatal(err)
		}
	}
}

func TestWoniuLinkValidation(t *testing.T) {
	for _, raw := range []string{"javascript:alert(1)", "https://115.com.evil.test/s/abc", "https://evil.test/?redirect=115.com/s/abc", "https://user:pass@115.com/s/a", "http://127.0.0.1/a", "https://115.com:443/s/a", "magnet:?xt=urn:btih:invalid"} {
		link, ok := normalizeLink(raw, raw, "资源", "电影")
		if ok && link.Type == "115" || strings.HasPrefix(raw, "magnet:") && ok {
			t.Fatalf("unsafe classified: %s %+v", raw, link)
		}
	}
	link, ok := normalizeLink("magnet:?xt=urn:btih:"+strings.Repeat("a", 40), "", "磁力", "电影")
	if !ok || link.Category != "torrent" || link.Type != "magnet" {
		t.Fatalf("%+v %v", link, ok)
	}
}

func TestWoniuMagnetCanonicalizationAndDetailFormat(t *testing.T) {
	for _, raw := range []string{
		"MAGNET:?dn=fixture&xt=URN:BTIH:" + strings.Repeat("A", 40),
		"magnet:?dn=fixture&xt=urn:btih:" + strings.Repeat("A", 32),
	} {
		link, ok := normalizeLink(raw, "", "磁力", "电影")
		if !ok || !strings.HasPrefix(link.URL, "magnet:?xt=urn:btih:") || !strings.HasSuffix(link.URL, "&dn=fixture") {
			t.Fatal(link, ok)
		}
		hash := strings.Split(strings.TrimPrefix(link.URL, "magnet:?xt=urn:btih:"), "&")[0]
		if len(hash) != 40 || hash != strings.ToLower(hash) {
			t.Fatal("noncanonical torrent hash", hash)
		}
	}
	if _, err := parseDetail([]byte(`<h1 class="premium-title">电影</h1><p>页面维护</p>`), SearchItem{ID: "1"}); !errors.Is(err, ErrFormat) {
		t.Fatal("changed page treated as empty", err)
	}
	item, err := parseDetail([]byte(`<h1 class="premium-title">电影</h1><h2>网盘资源</h2><p>暂无资源</p>`), SearchItem{ID: "1"})
	if err != nil || item.DetailStatus != "empty" {
		t.Fatal(item, err)
	}
}

func TestWoniuHTTP200RateLimitIsNotEmpty(t *testing.T) {
	client, _ := localClient(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<title>跳转提示 - 蜗牛 I 4K</title>请不要频繁操作，搜索时间间隔为3秒`)
	})
	_, err := client.Search(context.Background(), "电影", "title", 1)
	if !errors.Is(err, ErrRateLimited) {
		t.Fatal(err)
	}
	base, _ := url.Parse(BaseURL)
	if _, err := parseSearch([]byte(`<title>网站维护</title>`), base, "电影", "title", 1); !errors.Is(err, ErrFormat) {
		t.Fatal(err)
	}
	result, err := parseSearch([]byte(`<title>搜索电影 - 蜗牛 I 4K</title>`), base, "电影", "title", 1)
	if err != nil || result.Items == nil || len(result.Items) != 0 {
		t.Fatal(result, err)
	}
}

func TestWoniuLoginSessionAndRedirectBoundary(t *testing.T) {
	var leaked atomic.Int32
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked.Add(1) }))
	defer evil.Close()
	client, _ := localClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/user/login/":
			fmt.Fprint(w, `<input name="user_name"><input name="user_pwd">`)
		case "/user/login.html":
			if r.Method != "POST" {
				t.Error("login must post")
			}
			if r.FormValue("user_name") != "tester" || r.FormValue("user_pwd") != "test-pass" {
				t.Error("login fields")
			}
			http.SetCookie(w, &http.Cookie{Name: "user_id", Value: "fixture-session", Path: "/"})
			fmt.Fprint(w, `{"code":1}`)
		case "/user/index.html":
			if _, err := r.Cookie("user_id"); err != nil {
				t.Error("missing login cookie")
			}
			fmt.Fprint(w, `<a href="/user/logout.html">退出</a>`)
		default:
			http.Redirect(w, r, evil.URL+"/steal", 302)
		}
	})
	if err := client.Login(context.Background(), "tester", "test-pass"); err != nil {
		t.Fatal(err)
	}
	if len(client.SnapshotSession()) != 1 {
		t.Fatal("session missing")
	}
	if _, err := client.Search(context.Background(), "电影", "title", 1); err == nil {
		t.Fatal("cross-origin redirect accepted")
	}
	if leaked.Load() != 0 {
		t.Fatal("credential origin boundary failed")
	}
}

func TestWoniuDetailsStableOrderPartialAndCancellation(t *testing.T) {
	var active, peak atomic.Int32
	client, _ := localClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/vodsearch/") {
			fmt.Fprint(w, searchFixture)
			return
		}
		n := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
		}
		if r.URL.Path == "/voddetail/34033/" {
			time.Sleep(20 * time.Millisecond)
			fmt.Fprint(w, detailFixture)
			return
		}
		w.WriteHeader(503)
	})
	result, err := client.Search(context.Background(), "你的名字", "title", 1)
	if err != nil || !result.Partial || result.FailedDetails != 1 || result.Items[0].ID != "34033" || result.Items[1].DetailStatus != "failed" {
		t.Fatalf("%+v %v", result, err)
	}
	if peak.Load() > 4 || active.Load() != 0 {
		t.Fatal("workers escaped")
	}
	client.searchInterval = time.Hour
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := client.Search(ctx, "你的名字", "title", 1); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}

func TestWoniuCanaryReadOnly(t *testing.T) {
	if os.Getenv("WONIU_LIVE_READONLY") != "1" {
		t.Skip("explicit read-only canary only")
	}
	client, err := New("", nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	data, _, err := client.request(ctx, "GET", "/vodsearch/-------------/?wd="+url.QueryEscape("大刑伺候"), nil)
	if err != nil {
		t.Fatal(PublicError(err))
	}
	result, err := parseSearch(data, client.base, "大刑伺候", "title", 1)
	if err != nil || len(result.Items) == 0 || result.Items[0].ID != "35487" {
		t.Fatalf("search parser: candidates=%d error=%v", len(result.Items), err)
	}
	_, err = client.detail(ctx, result.Items[0])
	if !errors.Is(err, ErrSession) {
		t.Fatalf("anonymous detail should require login: %v", err)
	}
	t.Log("live public search parsed; anonymous detail correctly requires login; no write requests")
}

func TestWoniuCanaryAuthenticatedBrowserFixtures(t *testing.T) {
	directory := os.Getenv("WONIU_BROWSER_FIXTURES")
	if directory == "" {
		t.Skip("optional authenticated browser HTML fixtures")
	}
	for _, sample := range []struct {
		id    string
		count int
	}{{"35487", 1}, {"35724", 2}} {
		data, err := os.ReadFile(filepath.Join(directory, sample.id+".html"))
		if err != nil {
			t.Fatal(err)
		}
		item, err := parseDetail(data, SearchItem{ID: sample.id, Title: "灰度样本"})
		if err != nil || len(item.Links) < sample.count {
			t.Fatalf("detail %s: links=%d error=%v", sample.id, len(item.Links), err)
		}
		for _, link := range item.Links {
			if link.Category != "pan" {
				t.Fatalf("detail %s: share incorrectly classified", sample.id)
			}
		}
		t.Logf("authenticated browser sample %s: parsed %d share links; no credential copy or write request", sample.id, len(item.Links))
	}
}

func TestWoniuCanaryPublicPagination(t *testing.T) {
	if os.Getenv("WONIU_LIVE_READONLY") != "1" {
		t.Skip("explicit read-only canary only")
	}
	client, err := New("", nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	for _, page := range []int{1, 2} {
		release, err := client.waitSearch(ctx)
		if err != nil {
			t.Fatal(err)
		}
		path := "/vodsearch/-------------/?wd=" + url.QueryEscape("的")
		if page == 2 {
			path = "/vodsearch/" + url.PathEscape("的") + "----------2---/"
		}
		data, _, requestErr := client.request(ctx, "GET", path, nil)
		release()
		if requestErr != nil {
			t.Fatal(PublicError(requestErr))
		}
		result, err := parseSearch(data, client.base, "的", "title", page)
		if err != nil || len(result.Items) != 24 || !result.HasMore || result.NextPage != page+1 {
			t.Fatalf("live page %d: items=%d next=%d err=%v", page, len(result.Items), result.NextPage, err)
		}
		t.Logf("live public page %d: 24 candidates; next-page route parsed; no detail or write requests", page)
	}
}
