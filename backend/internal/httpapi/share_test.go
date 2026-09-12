package httpapi

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func TestQueryPlaybackWithoutSignature(t *testing.T) {
	api := NewShareAPI("test-secret", func() string { return "" })
	claim := playClaim{Code: "share-code", Password: "a+b&c", FileID: "3515174619050935343", Expires: time.Now().Add(time.Hour).Unix()}
	u, err := url.Parse(api.queryURL(claim))
	if err != nil {
		t.Fatal(err)
	}
	got, err := api.parseQuery(u.Query())
	if err != nil {
		t.Fatal(err)
	}
	if got.Code != claim.Code || got.Password != claim.Password || got.FileID != claim.FileID {
		t.Fatal("分享参数未正确往返")
	}
	if len(u.Query()) != 3 {
		t.Fatal("播放链接应只包含三个分享参数")
	}
	for _, key := range []string{"share_code", "id", "receive_code"} {
		q := u.Query()
		q.Del(key)
		if _, err := api.parseQuery(q); err == nil {
			t.Errorf("缺少参数 %s 时未报错", key)
		}
		q = u.Query()
		q.Add(key, "duplicate")
		if _, err := api.parseQuery(q); err == nil {
			t.Errorf("接受重复参数 %s", key)
		}
	}
	q := u.Query()
	q.Set("receive_code", "")
	if _, err := api.parseQuery(q); err != nil {
		t.Fatal("应支持空提取码")
	}
}

func TestGenerateAndRouteQuerySTRM(t *testing.T) {
	gin.SetMode(gin.TestMode)
	api := NewShareAPI("test-secret", func() string { return "" })
	api.BaseURL = func() string { return "http://192.168.0.24:3002" }
	r := gin.New()
	api.Register(r, r.Group("/api/admin"))
	request := httptest.NewRequest("POST", "/api/admin/p115/share/strm", strings.NewReader(`{"url":"https://115.com/s/abc?password=1234","fileId":"123","name":"sample.mkv"}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	r.ServeHTTP(recorder, request)
	if recorder.Code != 200 {
		t.Fatalf("生成失败: %s", recorder.Body.String())
	}
	var generated struct {
		Content string
		PlayURL string
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &generated); err != nil {
		t.Fatal(err)
	}
	if generated.Content != "http://192.168.0.24:3002/api/?share_code=abc&id=123&receive_code=1234\n" {
		t.Fatal("未生成查询参数形式")
	}
	for _, method := range []string{"GET", "HEAD"} {
		recorder = httptest.NewRecorder()
		r.ServeHTTP(recorder, httptest.NewRequest(method, generated.PlayURL, nil))
		if recorder.Code != 503 {
			t.Fatalf("三参数链接应到达 115 登录检查，实际 %d", recorder.Code)
		}
		recorder = httptest.NewRecorder()
		r.ServeHTTP(recorder, httptest.NewRequest(method, "/api/?share_code=abc&receive_code=1234", nil))
		if recorder.Code != 400 {
			t.Fatal("缺少文件 ID 的请求未被拒绝")
		}
	}
}

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestPlaybackTokenBoundary(t *testing.T) {
	api := NewShareAPI("test-secret", func() string { return "" })
	claim := playClaim{Code: "abc", FileID: "123", Expires: time.Now().Add(time.Hour).Unix()}
	data, _ := json.Marshal(claim)
	payload := base64.RawURLEncoding.EncodeToString(data)
	token := payload + "." + base64.RawURLEncoding.EncodeToString(api.sign(payload))
	if _, err := api.parseToken(token); err != nil {
		t.Fatal(err)
	}
	if _, err := api.parseToken("x" + token); err == nil {
		t.Fatal("接受了篡改凭证")
	}
	claim.Expires = 1
	data, _ = json.Marshal(claim)
	payload = base64.RawURLEncoding.EncodeToString(data)
	if _, err := api.parseToken(payload + "." + base64.RawURLEncoding.EncodeToString(api.sign(payload))); err == nil {
		t.Fatal("接受了过期凭证")
	}
}

func TestMediaRangeAndHEAD(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, method := range []string{"GET", "HEAD"} {
		api := NewShareAPI("test-secret", func() string { return "" })
		api.MediaHTTP = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.Method != method || r.Header.Get("Range") != "bytes=4-7" || r.Header.Get("Cookie") != "" {
				t.Error("请求方法或 Range 不正确，或泄露 Cookie")
			}
			return &http.Response{StatusCode: 206, Header: http.Header{"Content-Range": {"bytes 4-7/10"}, "Content-Length": {"4"}, "Accept-Ranges": {"bytes"}}, Body: io.NopCloser(strings.NewReader("4567"))}, nil
		})}
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(method, "/", nil)
		c.Request.Header.Set("Range", "bytes=4-7")
		if err := api.proxy(c, "https://cdn.115cdn.com/movie", "movie.mkv"); err != nil {
			t.Fatal(err)
		}
		c.Writer.WriteHeaderNow()
		if recorder.Code != 206 || recorder.Header().Get("Content-Range") != "bytes 4-7/10" {
			t.Fatal("丢失 Range 响应")
		}
		if method == "GET" && recorder.Body.String() != "4567" {
			t.Fatal("分段内容不匹配")
		}
		if method == "HEAD" && recorder.Body.Len() != 0 {
			t.Fatal("HEAD 返回了响应体")
		}
	}
}
