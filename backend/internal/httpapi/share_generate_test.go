package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestBatchGenerationHTTP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/share/snap" {
			t.Error("生成时不应下载或转存视频")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"state":true,"data":{"count":1,"list":[{"fid":"3515174619050935343","n":"电影.mkv","s":100}]}}`))
	}))
	defer upstream.Close()
	api := NewShareAPI("secret", "http://192.168.0.24:3002", func() string { return "cookie" })
	api.OutputRoot = t.TempDir()
	directory := filepath.Join(api.OutputRoot, "strm")
	api.Client.WebURL = upstream.URL
	r := gin.New()
	api.Register(r, r.Group("/api/admin"))
	w := httptest.NewRecorder()
	body, err := json.Marshal(map[string]any{"url": "https://115.com/s/demo?password=1234", "outputDir": "strm", "recursive": true})
	if err != nil {
		t.Fatal(err)
	}
	r.ServeHTTP(w, httptest.NewRequest("POST", "/api/admin/p115/share/generate", bytes.NewReader(body)))
	if w.Code != 200 {
		t.Fatalf("生成失败：%s", w.Body.String())
	}
	if !strings.Contains(w.Header().Get("Content-Type"), "application/json") || w.Header().Get("Content-Disposition") != "" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("应返回 JSON 生成结果，不下载附件")
	}
	var result struct {
		Directory string
		Count     int
		Existing  int
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Directory != directory || result.Count != 1 {
		t.Fatal("未返回固定生成目录和新增数量")
	}
	content, err := os.ReadFile(filepath.Join(directory, "电影.strm"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "http://192.168.0.24:3002/api/?share_code=demo&id=3515174619050935343&receive_code=1234\n" {
		t.Fatal("未采用配置地址或原始长 ID")
	}
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", "/api/admin/p115/share/generate", bytes.NewReader(body)))
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || result.Count != 0 || result.Existing != 1 {
		t.Fatal("重复生成未跳过已有文件")
	}
	for _, tc := range []struct {
		body string
		code int
	}{
		{`{`, 400}, {`{"url":"https://115.com/s/demo","outputDir":"../escape"}`, 400}, {`{"url":"https://115.com/s/demo","outputDir":"strm","videoExtensions":"avi"}`, 422},
	} {
		w = httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("POST", "/api/admin/p115/share/generate", strings.NewReader(tc.body)))
		if w.Code != tc.code {
			t.Fatalf("错误请求的状态码：%d", w.Code)
		}
	}
}

func TestSingleSTRMUsesSelectedBaseURL(t *testing.T) {
	gin.SetMode(gin.TestMode)
	api := NewShareAPI("secret", "http://old-server:3002", func() string { return "" })
	r := gin.New()
	api.Register(r, r.Group("/api/admin"))
	for _, tc := range []struct {
		base string
		code int
	}{{"http://selected-server:3002", 200}, {"http://selected-server:3002?token=secret", 400}} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("POST", "/api/admin/p115/share/strm", strings.NewReader(`{"url":"https://115.com/s/demo","fileId":"123","name":"movie.mkv","baseUrl":"`+tc.base+`"}`)))
		if w.Code != tc.code {
			t.Fatalf("单文件生成状态错误：%d", w.Code)
		}
		if tc.code == 200 && (!strings.Contains(w.Body.String(), "http://selected-server:3002/api/") || strings.Contains(w.Body.String(), "old-server")) {
			t.Fatal("单文件下载未使用页面选择的播放地址")
		}
	}
}
