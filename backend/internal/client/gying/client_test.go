package gying

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
)

func TestExtractObjectAfterHandlesNestedBraces(t *testing.T) {
	got, ok := extractObjectAfter(`prefix _obj.search = {"l":{"title":["A {cut}"],"i":["1"]}}; suffix`, "_obj.search")
	if !ok {
		t.Fatal("未提取到 JSON")
	}
	var value map[string]any
	if err := json.Unmarshal([]byte(got), &value); err != nil {
		t.Fatalf("提取结果不是 JSON: %v", err)
	}
}

func TestDetailLinksClassifiesAllLinkTypes(t *testing.T) {
	detail := detailData{}
	detail.Panlist.URL = []string{"https://115.com/s/abc", "https://pan.baidu.com/s/def", "https://115.com/s/abc"}
	detail.Panlist.Type = []int{5, 1, 5}
	detail.Panlist.Pass = []string{"abcd", "", "abcd"}
	detail.Panlist.Name = []string{"115 版本", "百度版本"}
	detail.Panlist.Time = []string{"今天", "昨天"}
	detail.Downlist.List.Hash = []string{"0123456789ABCDEF0123456789ABCDEF01234567", "0123456789ABCDEF0123456789ABCDEF01234567"}
	detail.Downlist.List.Title = []string{"种子"}
	detail.Downlist.List.Size = []string{"1 GB"}
	links := detailLinks(detail)
	if len(links) != 3 {
		t.Fatalf("重复链接没有去重或有效链接被丢弃: %#v", links)
	}
	if links[0].Type != "115" || links[0].Password != "abcd" {
		t.Fatalf("115 链接解析错误: %#v", links[0])
	}
	if links[1].Type != "baidu" {
		t.Fatalf("百度链接解析错误: %#v", links[1])
	}
	if links[2].Category != "torrent" || !strings.HasPrefix(links[2].URL, "magnet:?xt=urn:btih:") {
		t.Fatalf("磁力链接解析错误: %#v", links[2])
	}
}

func TestClientSearchKeepsMultipleItems(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/search":
			_, _ = w.Write([]byte(`<script>_obj.search = {"q":"原名","l":{"title":["第一季","第二季"],"year":[2020,2021],"d":["tv","tv"],"i":["one","two"],"info":["A","B"],"daoyan":["导演A","导演B"],"zhuyan":["主演A","主演B"]}};</script>`))
		case "/res/downurl/tv/one":
			_, _ = w.Write([]byte(`{"code":200,"panlist":{"url":["https://115.com/s/one"],"type":[5],"p":["a1b2"]}}`))
		case "/res/downurl/tv/two":
			_, _ = w.Write([]byte(`{"code":200,"panlist":{"url":["https://pan.baidu.com/s/two"],"type":[1],"p":["c3d4"]}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := New(server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Search(context.Background(), "原名")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 2 {
		t.Fatalf("多条标题结果没有全部保留: %#v", result.Items)
	}
	if result.Items[0].Title != "第一季" || result.Items[1].Title != "第二季" {
		t.Fatalf("结果顺序或标题错误: %#v", result.Items)
	}
}

func TestLoginAcceptsStringCodeAndUsesHomePageFirst(t *testing.T) {
	steps := make([]string, 0, 3)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		steps = append(steps, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/":
			w.Header().Add("Set-Cookie", "PHPSESSID=test; Path=/; HttpOnly")
			_, _ = w.Write([]byte(`<html>login</html>`))
		case r.Method == http.MethodPost && r.URL.Path == "/user/login":
			if err := r.ParseForm(); err != nil || r.Form.Get("username") != "user" || r.Form.Get("password") != "password" {
				t.Fatalf("登录表单错误: %v %#v", err, r.Form)
			}
			_, _ = w.Write([]byte(`{"code":"200"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/mv/wkMn":
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := New(server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Login(context.Background(), "user", "password"); err != nil {
		t.Fatal(err)
	}
	want := []string{"GET /", "POST /user/login", "GET /mv/wkMn"}
	if strings.Join(steps, ",") != strings.Join(want, ",") {
		t.Fatalf("登录步骤错误: got=%v want=%v", steps, want)
	}
}

func TestSearchByTitleFallsBackToIMDb(t *testing.T) {
	queries := make([]string, 0, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search" {
			http.NotFound(w, r)
			return
		}
		queries = append(queries, r.URL.Query().Get("q"))
		if r.URL.Query().Get("q") != "tt33332385" {
			_, _ = w.Write([]byte(`<script>_obj.search = {"q":"原名","l":{"title":[],"year":[],"d":[],"i":[]}};</script>`))
			return
		}
		_, _ = w.Write([]byte(`<script>_obj.search = {"q":"tt33332385","l":{"title":["Window's Bay"],"year":[2026],"d":["tv"],"i":["window"],"info":[]}};</script>`))
	}))
	defer server.Close()
	client, err := New(server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	// The fallback query needs a detail response as well; this test only checks
	// query selection, so an empty detail endpoint is intentionally tolerated.
	result, err := client.SearchByTitle(context.Background(), "原名", "tt33332385")
	if err != nil {
		t.Fatal(err)
	}
	if len(queries) != 2 || queries[0] != "原名" || queries[1] != "tt33332385" {
		t.Fatalf("查询顺序错误: %#v", queries)
	}
	if result.MatchedBy != "imdb" {
		t.Fatalf("兜底来源错误: %#v", result)
	}
}

func TestComputeNonces(t *testing.T) {
	challenge := challengeData{ID: "id", Salt: "salt", Diff: 2}
	for nonce := 0; nonce <= challenge.Diff; nonce++ {
		sum := sha256.Sum256([]byte(string(rune('0'+nonce)) + challenge.Salt))
		challenge.Challenge = append(challenge.Challenge, hex.EncodeToString(sum[:]))
	}
	got, err := computeNonces(context.Background(), challenge)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0] != 0 || got[1] != 1 || got[2] != 2 {
		t.Fatalf("nonce 计算错误: %#v", got)
	}
}
func TestNewRejectsQueryAndPath(t *testing.T) {
	for _, raw := range []string{"https://example.com/path", "https://example.com/?x=1", "example.com"} {
		if _, err := New(raw, nil); err == nil {
			t.Fatalf("应拒绝站点地址 %q", raw)
		}
	}
	if _, err := url.Parse("https://example.com"); err != nil {
		t.Fatal(err)
	}
}

func TestRemotePoWPostsVerificationToPowEndpoint(t *testing.T) {
	verified := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/" && !verified:
			_, _ = w.Write([]byte(`<html><title>浏览器安全验证</title><script src="powSolve-test.js"></script>安全验证</html>`))
		case r.URL.Path == "/res/pow" && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"N":"11","x":"2","t":1}`))
		case r.URL.Path == "/res/pow" && r.Method == http.MethodPost:
			if err := r.ParseForm(); err != nil || r.Form.Get("y") != "4" || r.Form.Get("action") != "" {
				t.Fatalf("远程 PoW 提交内容错误: %v %#v", err, r.Form)
			}
			verified = true
			_, _ = w.Write([]byte(`{"success":true}`))
		case r.URL.Path == "/" && verified:
			_, _ = w.Write([]byte(`<html>home</html>`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := New(server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !verified {
		t.Fatal("没有提交远程 PoW")
	}
}

func TestLiveChallengeProtocol(t *testing.T) {
	if os.Getenv("GYING_LIVE_TEST") != "1" {
		t.Skip("set GYING_LIVE_TEST=1 to verify the current public challenge")
	}
	client, err := New(DefaultLiveBaseURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = client.Check(context.Background())
	if err != nil && !errors.Is(err, ErrForbidden) {
		t.Fatalf("安全验证协议失败: %v", err)
	}
}

const DefaultLiveBaseURL = "https://www.xn--wcv59z.com"
