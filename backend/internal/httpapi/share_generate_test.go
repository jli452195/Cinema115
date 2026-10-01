package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestOutputDirectoryBrowserUsesVirtualSTRMRoot(t *testing.T) {
	gin.SetMode(gin.TestMode)
	root := t.TempDir()
	for _, directory := range []string{"已整理", "待整理", filepath.Join("待整理", "电影")} {
		if err := os.MkdirAll(filepath.Join(root, directory), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "不是目录.txt"), []byte("ignore"), 0600); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	_ = os.Symlink(outside, filepath.Join(root, "越界链接"))

	api := NewShareAPI(func() string { return "cookie" })
	api.OutputRoot = root
	r := gin.New()
	api.Register(r, r.Group("/api/admin"))
	call := func(path string) (int, struct {
		Path        string                 `json:"path"`
		DisplayPath string                 `json:"displayPath"`
		Directories []outputDirectoryEntry `json:"directories"`
	}) {
		t.Helper()
		response := httptest.NewRecorder()
		r.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		var body struct {
			Path        string                 `json:"path"`
			DisplayPath string                 `json:"displayPath"`
			Directories []outputDirectoryEntry `json:"directories"`
		}
		if response.Code == http.StatusOK {
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
		}
		return response.Code, body
	}

	code, body := call("/api/admin/p115/share/output-directories")
	if code != http.StatusOK || body.Path != "" || body.DisplayPath != "/strm" || len(body.Directories) != 2 {
		t.Fatalf("媒体库根目录响应错误：code=%d body=%+v", code, body)
	}
	if body.Directories[0].Name != "已整理" || body.Directories[0].Path != "已整理" || body.Directories[0].DisplayPath != "/strm/已整理" {
		t.Fatal("没有返回统一的 /strm 虚拟路径", body.Directories)
	}
	code, body = call("/api/admin/p115/share/output-directories?path=.")
	if code != http.StatusOK || body.Path != "" || body.DisplayPath != "/strm" {
		t.Fatalf("explicit root=%+v code=%d", body, code)
	}
	code, body = call("/api/admin/p115/share/output-directories?path=" + url.QueryEscape("待整理"))
	if code != http.StatusOK || body.Path != "待整理" || body.DisplayPath != "/strm/待整理" || len(body.Directories) != 1 || body.Directories[0].Path != "待整理/电影" {
		t.Fatalf("子目录浏览错误：code=%d body=%+v", code, body)
	}
	if code, _ = call("/api/admin/p115/share/output-directories?path=../escape"); code != http.StatusBadRequest {
		t.Fatal("目录浏览接受了越界路径", code)
	}
}

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
	api := NewShareAPI(func() string { return "cookie" })
	api.BaseURL = func() string { return "http://192.168.0.24:3002" }
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
	if w.Code != 200 || result.Count != 1 || strings.Contains(w.Body.String(), `"existing"`) {
		t.Fatal("重复生成应返回本次写入数量，不返回已存在统计")
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

func TestReviewReturnsActionableErrorToPage(t *testing.T) {
	for _, endpoint := range []string{"generate", "generate-stream"} {
		t.Run(endpoint, func(t *testing.T) {
			requests := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				w.Write([]byte(`{"state":false,"error":"分享正在审核中"}`))
			}))
			defer upstream.Close()
			api := NewShareAPI(func() string { return "test-cookie" })
			api.OutputRoot = t.TempDir()
			api.Client.WebURL = upstream.URL
			r := gin.New()
			api.Register(r, r.Group("/api/admin"))
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/api/admin/p115/share/"+endpoint, strings.NewReader(`{"url":"https://115.com/s/demo?password=abcd","outputDir":"strm"}`))
			r.ServeHTTP(response, request)
			if requests != 1 || !strings.Contains(response.Body.String(), "审核中") || !strings.Contains(response.Body.String(), "已停止") {
				t.Fatal("网页未收到明确审核结果", response.Code)
			}
			if endpoint == "generate" && response.Code != 422 {
				t.Fatal("审核应返回 422")
			}
			if endpoint == "generate-stream" && (!strings.Contains(response.Body.String(), `"event":"error"`) || !strings.Contains(response.Body.String(), `"status":422`) || strings.Contains(response.Body.String(), `"event":"result"`)) {
				t.Fatal("流式生成未终止为错误事件")
			}
		})
	}
}

func TestSingleSTRMUsesSelectedBaseURL(t *testing.T) {
	gin.SetMode(gin.TestMode)
	api := NewShareAPI(func() string { return "selected-cookie" })
	api.BaseURL = func() string { return "http://old-server:3002" }
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
