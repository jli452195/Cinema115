package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func TestGenerateAndRouteShareSTRM(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cookie := "selected-cookie"
	api := NewShareAPI(func() string { return cookie })
	api.BaseURL = func() string { return "http://nas:8115" }
	r := gin.New()
	api.Register(r, r.Group("/api/admin"))
	for _, password := range []string{"pass", "fwgl"} {
		body := `{"url":"https://115.com/s/abc","password":"` + password + `","sha1":"0123456789abcdef0123456789abcdef01234567","fileId":"123","name":"sample.mkv"}`
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("POST", "/api/admin/p115/share/strm", strings.NewReader(body)))
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		var result struct{ Content, PlayURL string }
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		segment := password
		if segment == "" {
			segment = "-"
		}
		want := "/api/share115/abc/0123456789ABCDEF0123456789ABCDEF01234567/" + segment + "/123"
		if result.PlayURL != want || result.Content != "http://nas:8115"+want+"\n" {
			t.Fatal(result)
		}
		claim, err := api.parseSTRMURL(strings.TrimSpace(result.Content))
		if err != nil || claim.Password != password || claim.SHA1 != "0123456789ABCDEF0123456789ABCDEF01234567" {
			t.Fatal(claim, err)
		}
		cookie = ""
		for _, method := range []string{"GET", "HEAD"} {
			for _, tc := range []struct {
				path   string
				status int
			}{
				{want, 503}, {want + "?extra=1", 400}, {want + "?", 400},
				{"/api/share115/abc/bad/pass/123", 400},
				{"/api/?share_code=abc&id=123&receive_code=pass", 503},
				{"/api/stream/share/obsolete.payload", 404},
			} {
				w := httptest.NewRecorder()
				r.ServeHTTP(w, httptest.NewRequest(method, tc.path, nil))
				if w.Code != tc.status {
					t.Fatalf("%s %s: %d", method, tc.path, w.Code)
				}
			}
		}
		cookie = "selected-cookie"
	}
	for _, hash := range []string{"", "bad", strings.Repeat("G", 40)} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("POST", "/api/admin/p115/share/strm", strings.NewReader(`{"url":"https://115.com/s/abc?password=pass","sha1":"`+hash+`","fileId":"123","name":"sample.mkv"}`)))
		if w.Code != 400 {
			t.Fatal("invalid SHA1 accepted", w.Code)
		}
	}
	cookie = ""
	for _, endpoint := range []string{"strm", "files"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("POST", "/api/admin/p115/share/"+endpoint, strings.NewReader(`{"url":"https://115.com/s/abc","fileId":"123","name":"sample.mkv"}`)))
		if w.Code != 400 {
			t.Fatal("missing account accepted", w.Code)
		}
	}
}

func TestEmptySharePasswordRejectedBeforeUpstream(t *testing.T) {
	a := NewShareAPI(func() string { return "configured" })
	a.Client.HTTP = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Error("empty password reached upstream")
		return nil, io.EOF
	})}
	r := gin.New()
	a.Register(r, r.Group("/api/admin"))
	for _, password := range []string{"", " ", "-"} {
		body, err := json.Marshal(map[string]string{"url": "https://115.com/s/abc", "password": password, "sha1": strings.Repeat("A", 40), "fileId": "123", "name": "movie.mp4"})
		if err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("POST", "/api/admin/p115/share/strm", strings.NewReader(string(body))))
		if w.Code != 400 {
			t.Fatal("accepted empty/invalid password", w.Code)
		}
	}
	for _, path := range []string{
		"/api/?share_code=abc&id=123&receive_code=", "/api/?share_code=abc&id=123",
		"/api/?share_code=abc&id=123&receive_code=pass&receive_code=pass",
		"/api/share115/abc/" + strings.Repeat("A", 40) + "/-/123",
	} {
		for _, method := range []string{"GET", "HEAD"} {
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(method, path, nil))
			if w.Code != 400 {
				t.Fatal(path, method, w.Code)
			}
		}
	}
}

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestMediaRangeAndHEAD(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, method := range []string{"GET", "HEAD"} {
		api := NewShareAPI(func() string { return "" })
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
