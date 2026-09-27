package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/gin-gonic/gin"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/service"
	"strm115/backend/internal/storage"
)

func proxyTestAccounts(t *testing.T) (*service.P115Accounts, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.Open(context.Background(), filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	a, err := service.OpenP115AccountsSQLite(context.Background(), db, dir, "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	id, err := a.Add("播放账号")
	if err != nil {
		t.Fatal(err)
	}
	cookie := "UID=123_A; SEID=private"
	if err := a.Update(id, 1, "播放账号", &cookie); err != nil {
		t.Fatal(err)
	}
	return a, id
}

type embyProxyErrorTransport struct{ err error }

func (t embyProxyErrorTransport) RoundTrip(*http.Request) (*http.Response, error) { return nil, t.err }

func TestEmbyProxyCancellationAndUpstreamFailures(t *testing.T) {
	for _, tc := range []struct {
		name           string
		cancel         bool
		err            error
		status         int
		level, message string
	}{
		{"client canceled", true, fmt.Errorf("request interrupted: %w", context.Canceled), 499, "DEBUG", "请求上下文已取消"},
		{"unexplained cancellation", false, fmt.Errorf("transport: %w", context.Canceled), 502, "WARNING", "取消来源未确认"},
		{"timeout", false, fmt.Errorf("upstream: %w", context.DeadlineExceeded), 504, "ERROR", "Emby 转发超时"},
		{"connection failure", false, fmt.Errorf("private upstream?api_key=hidden-secret"), 502, "ERROR", "Emby 转发失败"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			shared := &service.GenerationLogs{}
			logs := service.NewEmbyProxyLogs(shared)
			h := NewEmbyProxyHandler(service.EmbyProxyConfig{URL: "http://emby:8096"}, nil, NewShareAPI(func() string { return "" }), logs).(*embyProxyHandler)
			h.proxy.Transport = embyProxyErrorTransport{tc.err}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.cancel {
				cancel()
			}
			req := httptest.NewRequest("GET", "/Items/item/Images/Primary?api_key=hidden-secret", nil).WithContext(ctx)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			entries := logs.Snapshot()
			if tc.cancel {
				if w.Code != 499 || len(entries) != 0 {
					t.Fatalf("图片取消仍产生噪音日志: %d %+v", w.Code, entries)
				}
				// Playback-related cancellations remain available for diagnosing interrupted requests.
				h.proxyError(w, httptest.NewRequest("GET", "/Items/movie/PlaybackInfo", nil).WithContext(ctx), tc.err)
				entries = logs.Snapshot()
				if len(entries) != 1 || entries[0].Level != "DEBUG" || !strings.Contains(entries[0].Message, "播放信息 PlaybackInfo") {
					t.Fatal("播放取消诊断丢失", entries)
				}
				return
			}
			if w.Code != tc.status || len(entries) != 1 || entries[0].Level != tc.level || !strings.Contains(entries[0].Message, tc.message) || !strings.Contains(entries[0].Message, "GET，图片，耗时") {
				t.Fatalf("错误分类不正确: %d %+v", w.Code, entries)
			}
			if strings.Contains(entries[0].Message, "hidden-secret") {
				t.Fatal("日志泄露请求或上游凭据")
			}
		})
	}
}

func TestEmbyProxyActualClientCancellation(t *testing.T) {
	started := make(chan struct{})
	stopped := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done(); close(stopped) }))
	defer upstream.Close()
	logs := service.NewEmbyProxyLogs(&service.GenerationLogs{})
	h := NewEmbyProxyHandler(service.EmbyProxyConfig{URL: upstream.URL}, nil, NewShareAPI(func() string { return "" }), logs)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan struct{})
	w := httptest.NewRecorder()
	go func() {
		defer close(done)
		h.ServeHTTP(w, httptest.NewRequest("GET", "/Items/item/Images/Primary", nil).WithContext(ctx))
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("上游未收到请求")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(4 * time.Second):
		t.Fatal("取消后代理未退出")
	}
	select {
	case <-stopped:
	case <-time.After(4 * time.Second):
		t.Fatal("取消未传播到上游")
	}
	entries := logs.Snapshot()
	if w.Code != 499 || len(entries) != 0 {
		t.Fatalf("真实图片取消仍写入日志: %d %+v", w.Code, entries)
	}
}

func TestEmbyProxyAuthenticated302AndPassthrough(t *testing.T) {
	accounts, id := proxyTestAccounts(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/emby/Users/AuthenticateByName":
			fmt.Fprint(w, `{"AccessToken":"viewer-token","User":{"Id":"user1"},"SessionInfo":{"UserId":"user1"}}`)
		case "/emby/Users/user1":
			if r.Header.Get("X-Emby-Token") == "invalid" {
				w.WriteHeader(401)
				return
			}
			if r.Header.Get("X-Emby-Token") != "viewer-token" {
				t.Error("未使用观看者凭据")
			}
			fmt.Fprint(w, `{"Id":"user1","Policy":{"EnableMediaPlayback":true}}`)
		case "/emby/Users/user1/Items/denied":
			w.WriteHeader(403)
		case "/emby/Users/user1/Items/movie":
			fmt.Fprint(w, `{"Id":"movie","PlayAccess":"Full"}`)
		case "/emby/Items/movie/PlaybackInfo", "/Items/movie/PlaybackInfo":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"PlaySessionId":"session","MediaSources":[{"Id":"source1","Path":"http://cinema:8115/api/?share_code=abc&id=123&receive_code=pass","SupportsDirectPlay":true,"SupportsDirectStream":true,"MediaStreams":[{"Type":"Video","Codec":"hevc"}]},{"Id":"source2","Path":"http://cinema:8115/api/?share_code=abc&id=456&receive_code=pass","SupportsDirectPlay":false,"SupportsDirectStream":true,"DirectStreamUrl":"/Videos/movie/master.m3u8"},{"Id":"local","Path":"/media/local.mkv","SupportsDirectPlay":true}]}`)
		default:
			if r.Header.Get("X-Cinema115-Emby-Proxy") != "1" {
				t.Error("普通反代缺少循环检测标志")
			}
			w.Header().Set("X-Upstream", "yes")
			w.WriteHeader(206)
			fmt.Fprint(w, "upstream")
		}
	}))
	defer upstream.Close()
	share := NewShareAPI(func() string { return "wrong-account" })
	logs := service.NewEmbyProxyLogs(&service.GenerationLogs{})
	h := NewEmbyProxyHandler(service.EmbyProxyConfig{URL: upstream.URL, AccountID: id}, accounts, share, logs).(*embyProxyHandler)
	calls := 0
	h.download = func(ctx context.Context, info *p115.ShareInfo, file, cookie, ua string) (string, error) {
		calls++
		if file != "123" || info.Code != "abc" || info.Password != "pass" || cookie != "UID=123_A; SEID=private" || ua != "test-player/1" {
			t.Error("源文件、独立账号或播放器 UA 错误")
		}
		return "https://cdn.115.com/video?temporary=1", nil
	}
	call := func(method, path string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, nil)
		r.Header.Set("User-Agent", "test-player/1")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := call("POST", "/emby/Users/AuthenticateByName"); w.Code != 200 {
		t.Fatal("登录失败", w.Code)
	}
	for _, method := range []string{"GET", "HEAD"} {
		w := call(method, "/cinema115/play?ItemId=movie&MediaSourceId=source1&api_key=viewer-token")
		if w.Code != 302 || w.Header().Get("Location") != "https://cdn.115.com/video?temporary=1" || w.Body.Len() != 0 || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("未返回安全 302", w.Code, w.Body.String())
		}
	}
	if w := call("GET", "/emby/Videos/movie/stream.mkv?Static=true&MediaSourceId=source1&api_key=viewer-token"); w.Code != 302 {
		t.Fatal("静态播放兼容失败", w.Code)
	}
	if w := call("GET", "/emby/cinema115/play?ItemId=movie&MediaSourceId=source1&api_key=viewer-token"); w.Code != 302 {
		t.Fatal("Emby 前缀兼容失败", w.Code)
	}
	before := calls
	for _, tc := range []struct {
		path   string
		status int
	}{
		{"/cinema115/play?ItemId=movie&MediaSourceId=source1", 401},
		{"/cinema115/play?ItemId=movie&MediaSourceId=source1&api_key=invalid", 401},
		{"/cinema115/play?ItemId=denied&MediaSourceId=source1&api_key=viewer-token", 403},
		{"/cinema115/play?ItemId=movie&api_key=viewer-token", 409},
		{"/cinema115/play?ItemId=movie&MediaSourceId=missing&api_key=viewer-token", 409},
	} {
		if w := call("GET", tc.path); w.Code != tc.status {
			t.Fatalf("权限/版本边界错误: %s %d", tc.path, w.Code)
		}
	}
	if calls != before {
		t.Fatal("被拒绝请求调用了 115")
	}
	for _, path := range []string{"/System/Info/Public", "/Videos/movie/source1/Subtitles/1/Stream.srt", "/Videos/movie/stream.mkv?Static=true&MediaSourceId=local&api_key=viewer-token"} {
		if w := call("GET", path); w.Code != 206 || w.Header().Get("X-Upstream") != "yes" {
			t.Fatal("普通/转码请求未透传", path, w.Code)
		}
	}
	for _, path := range []string{"/Videos/movie/master.m3u8", "/Videos/movie/stream.mkv?Static=false", "/emby/Videos/movie/hls1/main/0.ts", "/Audio/song/universal"} {
		if w := call("GET", path); w.Code != 403 {
			t.Fatal("转码请求未拦截", path, w.Code)
		}
	}
	w := call("POST", "/Items/movie/PlaybackInfo?api_key=viewer-token")
	if w.Code != 200 {
		t.Fatal("PlaybackInfo 未透传", w.Code, w.Body.String())
	}
	var info struct {
		PlaySessionID string `json:"PlaySessionId"`
		MediaSources  []map[string]json.RawMessage
	}
	if err := json.Unmarshal(w.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	if info.PlaySessionID != "session" || jsonTrue(info.MediaSources[0]["SupportsDirectPlay"]) || !strings.HasPrefix(jsonString(info.MediaSources[0]["DirectStreamUrl"]), "/cinema115/play?") {
		t.Fatal("未改写直放源")
	}
	if jsonString(info.MediaSources[1]["DirectStreamUrl"]) != "" || jsonTrue(info.MediaSources[1]["SupportsDirectStream"]) || jsonTrue(info.MediaSources[0]["SupportsTranscoding"]) || !jsonTrue(info.MediaSources[2]["SupportsDirectPlay"]) {
		t.Fatal("未禁用转码或错误改写普通直放源")
	}
	emptyCookie := ""
	if err := accounts.Update(id, 2, "播放账号", &emptyCookie); err != nil {
		t.Fatal(err)
	}
	if w := call("GET", "/cinema115/play?ItemId=movie&MediaSourceId=source1&api_key=viewer-token"); w.Code != 503 {
		t.Fatal("失效账号没有停止播放", w.Code)
	}
	if calls != before {
		t.Fatal("账号清空后回退了其他账号")
	}
	entries := logs.Snapshot()
	raw, err := json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"viewer-token", "UID=123_A", "SEID=private", "receive_code=pass", "temporary=1", "http://cinema:8115/api/"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("302 日志泄漏凭据", secret)
		}
	}
	for _, message := range []string{"开始直链播放解析", "权限校验通过", "STRM 解析成功", "115 直链获取成功", "返回 HTTP 302", "播放解析结束", "HTTP 401", "HTTP 403", "HTTP 409", "HTTP 503", "已禁用转码与封装转换"} {
		if !strings.Contains(string(raw), message) {
			t.Fatal("缺少播放诊断", message)
		}
	}
	trace := ""
	for _, entry := range entries {
		if strings.Contains(entry.Message, "返回 HTTP 302") {
			trace = entry.TaskID
			break
		}
	}
	foundStart := false
	for _, entry := range entries {
		if entry.TaskID == trace && strings.Contains(entry.Message, "开始直链播放解析") {
			foundStart = true
		}
	}
	if trace == "" || !foundStart {
		t.Fatal("同一播放请求缺少关联编号")
	}
}

func TestEmbyProxyLogEndpointAndErrorRedaction(t *testing.T) {
	shared := &service.GenerationLogs{}
	logs := service.NewEmbyProxyLogs(shared)
	ctx := logs.Trace(context.Background())
	logs.Add(ctx, "ERROR", proxyFailureReason(fmt.Errorf("https://cdn.115.com/private?sign=secret; Cookie=secret")))
	r := gin.New()
	admin := r.Group("/api/admin", func(c *gin.Context) {
		if c.GetHeader("Authorization") != "Bearer admin" {
			c.AbortWithStatus(401)
		}
	})
	RegisterLogs(admin, shared)
	for _, authorized := range []bool{false, true} {
		req := httptest.NewRequest("GET", "/api/admin/logs?module=Emby%20302&level=ALL", nil)
		if authorized {
			req.Header.Set("Authorization", "Bearer admin")
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if !authorized {
			if w.Code != 401 {
				t.Fatal("日志未受管理员鉴权保护")
			}
			continue
		}
		if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || strings.Contains(w.Body.String(), "secret") || !strings.Contains(w.Body.String(), "302-000001") {
			t.Fatal("日志接口未正确返回脱敏数据", w.Body.String())
		}
	}
}

func TestEmbyProxySTRMMappingAndTokenHeaders(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "movie.strm"), []byte("http://cinema:8115/api/?share_code=abc&id=123&receive_code=pass\n"), 0600); err != nil {
		t.Fatal(err)
	}
	share := NewShareAPI(func() string { return "" })
	share.OutputRoot = root
	h := &embyProxyHandler{config: service.EmbyProxyConfig{EmbyPathPrefix: "/media"}, share: share}
	for _, tc := range []struct {
		path  string
		valid bool
	}{
		{"/media/movie.strm", true},
		{"/media/../outside.strm", false},
		{"/media2/movie.strm", false},
		{"http://other-tool/api/?share_code=abc&id=123&receive_code=pass", true},
		{"http://cinema:8115/api/?share_code=abc&id=123&id=456&receive_code=pass", false},
	} {
		path, _ := json.Marshal(tc.path)
		claim, err := h.sourceClaim(map[string]json.RawMessage{"Path": path})
		if (err == nil) != tc.valid {
			t.Fatal("路径限制错误", tc.path, err)
		}
		if tc.valid && claim.FileID != "123" {
			t.Fatal("源文件解析错误")
		}
	}
	for _, header := range []string{"Authorization", "X-Emby-Authorization"} {
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set(header, `MediaBrowser Client="TV", Token="viewer"`)
		if embyToken(r) != "viewer" {
			t.Fatal("未识别播放器鉴权", header)
		}
	}
}

func TestEmbyProxyDisablesTranscodingNegotiation(t *testing.T) {
	req := httptest.NewRequest("POST", "/Items/movie/PlaybackInfo?enabletranscoding=true", strings.NewReader(`{"EnableTranscoding":true,"UserId":"user","DeviceProfile":{"Name":"player"}}`))
	if err := disablePlaybackTranscoding(req); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatal(err)
	}
	if req.URL.Query().Get("EnableTranscoding") != "false" || strings.Contains(string(data), `"EnableTranscoding":true`) || !strings.Contains(string(data), `"UserId":"user"`) || req.ContentLength != int64(len(data)) {
		t.Fatal("协商参数未关闭转码或其他字段丢失")
	}
	h := &embyProxyHandler{logs: service.NewEmbyProxyLogs(&service.GenerationLogs{})}
	resp := &http.Response{StatusCode: 200, Request: httptest.NewRequest("GET", "/Items/movie/PlaybackInfo?api_key=viewer", nil), Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"MediaSources":[{"Id":"source","SupportsDirectPlay":false,"SupportsDirectStream":true,"SupportsTranscoding":true,"TranscodingUrl":"/master.m3u8","DirectStreamUrl":"/stream"}]}`))}
	if err := h.rewritePlayback(resp); err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err = io.ReadAll(resp.Body)
	if err != nil || !strings.Contains(string(data), "NoCompatibleStream") || strings.Contains(string(data), "TranscodingUrl") || strings.Contains(string(data), "DirectStreamUrl") || strings.Contains(string(data), `"SupportsTranscoding":true`) {
		t.Fatal("不兼容来源仍提供了转码入口", string(data))
	}
}
