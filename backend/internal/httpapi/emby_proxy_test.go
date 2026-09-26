package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

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

func TestEmbyProxyAuthenticated302AndPassthrough(t *testing.T) {
	accounts, id := proxyTestAccounts(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/emby/Users/Me":
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
	share := NewShareAPI("secret", func() string { return "wrong-account" })
	h := NewEmbyProxyHandler(service.EmbyProxyConfig{URL: upstream.URL, AccountID: id, STRMBaseURL: "http://cinema:8115"}, accounts, share).(*embyProxyHandler)
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
	for _, path := range []string{"/System/Info/Public", "/Videos/movie/master.m3u8", "/Videos/movie/stream.mkv?Static=false", "/Videos/movie/stream.mkv?Static=true&MediaSourceId=local&api_key=viewer-token"} {
		if w := call("GET", path); w.Code != 206 || w.Header().Get("X-Upstream") != "yes" {
			t.Fatal("普通/转码请求未透传", path, w.Code)
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
	if jsonString(info.MediaSources[1]["DirectStreamUrl"]) != "/Videos/movie/master.m3u8" || !jsonTrue(info.MediaSources[2]["SupportsDirectPlay"]) {
		t.Fatal("错误改写转码/普通媒体源")
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
}

func TestEmbyProxySTRMMappingAndTokenHeaders(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "movie.strm"), []byte("http://cinema:8115/api/?share_code=abc&id=123&receive_code=pass\n"), 0600); err != nil {
		t.Fatal(err)
	}
	share := NewShareAPI("secret", func() string { return "" })
	share.OutputRoot = root
	h := &embyProxyHandler{config: service.EmbyProxyConfig{STRMBaseURL: "http://cinema:8115", EmbyPathPrefix: "/media"}, share: share}
	for _, tc := range []struct {
		path  string
		valid bool
	}{
		{"/media/movie.strm", true},
		{"/media/../outside.strm", false},
		{"/media2/movie.strm", false},
		{"http://evil/api/?share_code=abc&id=123&receive_code=pass", false},
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
