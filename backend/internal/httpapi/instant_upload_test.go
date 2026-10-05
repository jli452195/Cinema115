package httpapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/service"
)

type instantAPIClient struct{ reads, writes int }

func (f *instantAPIClient) DownloadInstantInfoWithUA(_ context.Context, share *p115.ShareInfo, id, cookie, ua string) (*p115.DownloadInfo, error) {
	f.reads++
	return &p115.DownloadInfo{Name: "sample.mkv", SHA1: strings.Repeat("A", 40), Size: 100, URL: "https://cdn.115.com/private-link"}, nil
}
func (f *instantAPIClient) InstantUpload(_ context.Context, _ *p115.DownloadInfo, cid, cookie string, progress func(p115.InstantProgress)) (*p115.InstantUploadResult, error) {
	f.writes++
	return &p115.InstantUploadResult{PickCode: "pick1234"}, nil
}

func TestInstantHTTPRequiresSavedExplicitSelectionAndValidSTRM(t *testing.T) {
	accounts, id := proxyTestAccounts(t)
	store, err := service.NewSessionStore(filepath.Join(t.TempDir(), "instant.enc"), "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	fake := &instantAPIClient{}
	s, err := service.NewInstantUploads(store, accounts, fake, nil)
	if err != nil {
		t.Fatal(err)
	}
	target, err := accounts.Add("target")
	if err != nil {
		t.Fatal(err)
	}
	cookie := "UID=456_A; SEID=target"
	if err := accounts.Update(target, 1, "target", &cookie); err != nil {
		t.Fatal(err)
	}
	if err := accounts.UpdatePlaybackDirectory(target, 2, "0"); err != nil {
		t.Fatal(err)
	}
	playbackStore, err := service.NewSessionStore(filepath.Join(t.TempDir(), "playback.enc"), "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	s.Playback, err = service.NewPlaybackCache(context.Background(), playbackStore, accounts, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Playback.ApplyBindings("http://emby:8096", []service.PlaybackBinding{{UserID: "user-000000000001", AccountID: target}}); err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	share := NewShareAPI(func() string { return "forbidden-fallback" })
	RegisterInstantUploads(r.Group("/api/admin"), s, share, func(context.Context, string) error { return nil })
	call := func(path string, payload any) *httptest.ResponseRecorder {
		t.Helper()
		raw, _ := json.Marshal(payload)
		req := httptest.NewRequest("POST", "/api/admin/emby/instant-upload"+path, strings.NewReader(string(raw)))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	config := service.InstantUploadConfig{UserID: "user-000000000001", SourceAccountID: id, AccountID: target, TargetCID: "0"}
	bad := config
	bad.UserID = ""
	if w := call("", bad); w.Code != 400 {
		t.Fatalf("空账号未被拒绝: %d", w.Code)
	}
	if w := call("", config); w.Code != 200 {
		t.Fatalf("保存失败 %d %s", w.Code, w.Body.String())
	}
	for _, strm := range []string{"http://cinema:8115/api/?share_code=abc&id=invalid", "http://cinema:8115/api/stream/share/badtoken", "/outside/sample.strm", strings.Repeat("x", 21000)} {
		w := call("/run", gin.H{"id": "http-test-request-001", "strm": strm})
		if w.Code != 400 {
			t.Fatalf("异常 STRM 未被拒绝: %d", w.Code)
		}
	}
	if fake.reads != 0 || fake.writes != 0 {
		t.Fatal("无效输入产生上游请求")
	}
	for i := 0; i < 2; i++ {
		w := call("/run", gin.H{"id": "http-test-request-002", "strm": "http://cinema:8115/api/share115/abc/0123456789ABCDEF0123456789ABCDEF01234567/private/123"})
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"success"`) || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("验证接口失败: %d %s", w.Code, w.Body.String())
		}
		for _, secret := range []string{"private", "SEID", "cookie", "forbidden-fallback"} {
			if strings.Contains(w.Body.String(), secret) {
				t.Fatal("响应包含敏感信息")
			}
		}
	}
	if fake.reads != 1 || fake.writes != 1 {
		t.Fatal("相同请求被重复执行")
	}
	if w := call("", config); w.Code != 200 {
		t.Fatalf("宽松模式无需来源地址: %d %s", w.Code, w.Body.String())
	}
	w := call("/run", gin.H{"id": "http-test-request-003", "strm": "https://other-nas:9443/api/share115/abc/0123456789ABCDEF0123456789ABCDEF01234567/pass/123"})
	if w.Code != 200 || fake.writes != 2 {
		t.Fatal("不同来源未进入秒传验证", w.Code, w.Body.String())
	}
	w = call("/run", gin.H{"id": "http-test-request-004", "strm": "http://other-tool:9527/s/abc_pass_123.mp4?/movie.mp4"})
	if w.Code != 200 || fake.writes != 3 {
		t.Fatal("路径形式未进入单文件秒传验证", w.Code)
	}
	w = call("/run", gin.H{"id": "http-test-request-005", "strm": "http://legacy/api/?share_code=abc&id=123&receive_code=pass&sha1=untrusted"})
	if w.Code != 200 || fake.writes != 4 {
		t.Fatal("旧格式未进入单文件秒传验证", w.Code)
	}
}
