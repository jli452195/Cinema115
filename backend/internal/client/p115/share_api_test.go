package p115

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestShareListing(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/share/snap" || r.URL.Query().Get("receive_code") != "1234" || r.URL.Query().Get("offset") != "100" {
			t.Error("分享请求参数不正确")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"state":true,"data":{"count":102,"list":[{"fid":"3516463718579832420","n":"sample.mkv","s":1955026554},{"cid":"123","n":"folder"}]}}`))
	}))
	defer upstream.Close()
	client := NewShareClient()
	client.WebURL = upstream.URL
	page, err := client.List(context.Background(), &ShareInfo{Code: "abc", Password: "1234"}, "0", 100, "")
	if err != nil {
		t.Fatal(err)
	}
	if page.Count != 102 || len(page.Files) != 2 || page.Files[0].ID != "3516463718579832420" || !page.Files[1].Directory {
		t.Fatalf("文件归一化错误: %+v", page)
	}
}

func TestShareUpstreamRejection(t *testing.T) {
	for _, body := range []string{`{"state":false,"error":"提取码错误"}`, `<html>failure</html>`} {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) }))
		client := NewShareClient()
		client.WebURL = upstream.URL
		if _, err := client.List(context.Background(), &ShareInfo{Code: "abc"}, "0", 0, ""); err == nil {
			t.Error("上游错误未被处理")
		}
		upstream.Close()
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
