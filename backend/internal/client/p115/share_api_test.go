package p115

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestShareListing(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.ContentLength != 0 || r.URL.Path != "/share/snap" || r.URL.Query().Get("share_code") != "abc" || r.URL.Query().Get("receive_code") != "1234" || r.URL.Query().Get("offset") != "100" || r.URL.Query().Get("cid") != "0" {
			t.Error("分享请求参数不正确")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"state":true,"data":{"count":102,"list":[{"fid":"3516463718579832420","sha":"3c57fcfd33b9476b43c6bced7d20b5887efb2a4e","n":"sample.mkv","s":1955026554},{"cid":"123","n":"folder"}]}}`))
	}))
	defer upstream.Close()
	client := newTestShareClient()
	client.WebURL = upstream.URL
	page, err := client.List(context.Background(), &ShareInfo{Code: "abc", Password: "1234"}, "0", 100, "")
	if err != nil {
		t.Fatal(err)
	}
	if page.Files[0].SHA1 != "3C57FCFD33B9476B43C6BCED7D20B5887EFB2A4E" || page.Files[1].SHA1 != "" || page.Count != 102 || len(page.Files) != 2 || page.Files[0].ID != "3516463718579832420" || !page.Files[1].Directory {
		t.Fatalf("文件归一化错误: %+v", page)
	}
}

func TestShareUpstreamRejection(t *testing.T) {
	for _, body := range []string{`{"state":false,"error":"提取码错误"}`, `<html>failure</html>`} {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) }))
		client := newTestShareClient()
		client.WebURL = upstream.URL
		if _, err := client.List(context.Background(), &ShareInfo{Code: "abc"}, "0", 0, ""); err == nil {
			t.Error("上游错误未被处理")
		}
		upstream.Close()
	}
}

func TestShareHTTPFailureIncludesRequestWithoutCredentialsOrRetry(t *testing.T) {
	for _, status := range []int{http.StatusMethodNotAllowed, http.StatusTooManyRequests} {
		calls := 0
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			if r.Method != http.MethodGet {
				t.Error("分享列表应使用 GET")
			}
			w.WriteHeader(status)
			w.Write([]byte("private response body"))
		}))
		client := newTestShareClient()
		client.WebURL = upstream.URL
		_, err := client.List(context.Background(), &ShareInfo{Code: "private-share", Password: "private-password"}, "0", 0, "private-cookie")
		upstream.Close()
		if err == nil || !strings.Contains(err.Error(), "HTTP "+strconv.Itoa(status)) || !strings.Contains(err.Error(), "GET ") || !strings.Contains(err.Error(), "/share/snap") || strings.Contains(err.Error(), "private") || calls != 1 {
			t.Fatal("访问限制处理错误", err, calls)
		}
	}
}

func TestShareRequestsNeverFollowRedirects(t *testing.T) {
	for _, status := range []int{301, 302, 303, 307, 308} {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			t.Run(strconv.Itoa(status)+method, func(t *testing.T) {
				calls := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					if r.URL.Path == "/redirect-target" {
						t.Error("不应跟随重定向，避免改变方法或泄漏 Cookie")
						w.WriteHeader(500)
						return
					}
					if r.Method != method {
						t.Error("初始请求方法错误")
					}
					w.Header().Set("Location", "/redirect-target")
					w.WriteHeader(status)
				}))
				defer server.Close()
				client := newTestShareClient()
				client.WebURL = server.URL
				var err error
				share := &ShareInfo{Code: "demo", Password: "test-password"}
				if method == http.MethodGet {
					_, err = client.List(context.Background(), share, "0", 0, "test-cookie")
				} else {
					err = client.ReceiveShare(context.Background(), share, []string{"1"}, "2", "test-cookie")
				}
				if err == nil || !strings.Contains(err.Error(), "HTTP "+strconv.Itoa(status)) || calls != 1 {
					t.Fatal("重定向未被直接返回", err, calls)
				}
			})
		}
	}
}

func TestMediaDomainBoundary(t *testing.T) {
	for _, raw := range []string{"http://127.0.0.1/a", "https://115.com.attacker.test/a", "file:///x", "https://user@115cdn.com/a", "https://115cdn.com:3002/a"} {
		if AllowedMediaURL(raw) {
			t.Errorf("接受了无效域名 %s", raw)
		}
	}
	if !AllowedMediaURL("https://cdn.115cdn.com/video") {
		t.Fatal("拒绝了媒体域名")
	}
}
