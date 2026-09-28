package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/service"
	"testing"
)

type proxyCacheFake struct {
	pickReads, shareReads int
	directURL             string
	sourceReads           int
	files                 []p115.DrivePlaybackFile
	uploads, links        int
	t                     *testing.T
}

func (f *proxyCacheFake) ValidateCacheParent(context.Context, string, string) error { return nil }

func (f *proxyCacheFake) DownloadInfoWithUA(_ context.Context, _ *p115.ShareInfo, _ string, cookie, ua string) (*p115.DownloadInfo, error) {
	f.shareReads++
	if cookie == "UID=456_B; SEID=friend-private" {
		if ua != "player-UA" {
			f.t.Fatal("朋友分享取链未使用播放器 UA")
		}
		if f.directURL != "" {
			return &p115.DownloadInfo{URL: f.directURL}, nil
		}
		return nil, p115.ErrShareNotReadable
	}
	f.sourceReads++
	if cookie != "UID=123_A; SEID=private" {
		f.t.Fatal("源账号错误")
	}
	return &p115.DownloadInfo{Name: "movie", Size: 100, SHA1: strings.Repeat("A", 40), URL: "https://cdn.115.com/source"}, nil
}

func (f *proxyCacheFake) PickCodeInstantInfo(_ context.Context, pc, cookie string) (*p115.DownloadInfo, error) {
	f.pickReads++
	if pc != "abc123" || cookie != "UID=123_A; SEID=private" {
		f.t.Fatal("pickcode source account mismatch")
	}
	return &p115.DownloadInfo{Name: "movie", Size: 100, SHA1: strings.Repeat("A", 40), URL: "https://cdn.115.com/source"}, nil
}
func (f *proxyCacheFake) CreateDirectory(context.Context, string, string, string) (string, error) {
	return "9", nil
}
func (f *proxyCacheFake) CacheDirectoryFiles(context.Context, string, string) ([]p115.DrivePlaybackFile, error) {
	return f.files, nil
}
func (f *proxyCacheFake) InstantUploadToEmptyDirectory(_ context.Context, info *p115.DownloadInfo, cid, cookie string, _ func(p115.InstantProgress)) (*p115.InstantUploadResult, error) {
	if cookie != "UID=456_B; SEID=friend-private" {
		f.t.Fatal("错误目标账号")
	}
	f.uploads++
	f.files = []p115.DrivePlaybackFile{{ID: "88", CID: cid, Name: info.Name, SHA1: info.SHA1, Size: info.Size, PickCode: "pick1234"}}
	return &p115.InstantUploadResult{FileID: "88", PickCode: "pick1234"}, nil
}
func (f *proxyCacheFake) DriveDownloadURLWithUA(_ context.Context, id, pc, cookie, ua string) (string, error) {
	if cookie != "UID=456_B; SEID=friend-private" || ua != "player-UA" || id != "88" {
		f.t.Fatal("目标取链账号、UA 或文件错误")
	}
	f.links++
	return "https://cdn.115.com/friend?private-link", nil
}
func (f *proxyCacheFake) DeleteEntries(context.Context, []string, string) error {
	f.t.Fatal("播放期间不应删除缓存")
	return nil
}
func TestFriendPlayback302AuthorizesBeforeCloudWritesAndNeverFallsBack(t *testing.T) {
	const uid = "8be403a10974486c94a987b8036c8484"
	accounts, source := proxyTestAccounts(t)
	target, _ := accounts.Add("朋友")
	cookie := "UID=456_B; SEID=friend-private"
	if err := accounts.Update(target, 1, "朋友", &cookie); err != nil {
		t.Fatal(err)
	}
	if err := accounts.UpdatePlaybackDirectory(target, 2, "0"); err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/emby/Users/AuthenticateByName" {
			fmt.Fprintf(w, `{"AccessToken":"viewer","User":{"Id":"%s"},"SessionInfo":{"UserId":"%s"}}`, uid, uid)
			return
		}
		if r.Header.Get("X-Emby-Token") != "viewer" {
			w.WriteHeader(401)
			return
		}
		switch r.URL.Path {
		case "/emby/Users/" + uid:
			fmt.Fprintf(w, `{"Id":"%s","Name":"liuqianqian","Policy":{"EnableMediaPlayback":true}}`, uid)
		case "/emby/Users/" + uid + "/Items/denied":
			w.WriteHeader(403)
		case "/emby/Users/" + uid + "/Items/movie":
			fmt.Fprint(w, `{"Id":"movie","PlayAccess":"Full"}`)
		case "/emby/Items/movie/PlaybackInfo":
			fmt.Fprint(w, `{"MediaSources":[{"Id":"src","Path":"https://nas-other:9443/api/?share_code=abc&id=123&receive_code=pass&filename=movie.mkv&extra=ignored"}]}`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer upstream.Close()
	store, err := service.NewSessionStore(filepath.Join(t.TempDir(), "cache.enc"), "secret")
	if err != nil {
		t.Fatal(err)
	}
	fake := &proxyCacheFake{t: t}
	logs := service.NewEmbyProxyLogs(&service.GenerationLogs{})
	cache, err := service.NewPlaybackCache(context.Background(), store, accounts, fake, logs)
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()
	if err := cache.Apply(service.PlaybackCacheConfig{EmbyURL: upstream.URL, RetentionHours: 72, Bindings: []service.PlaybackBinding{{UserID: uid, AccountID: target, ParentCID: "0"}}}); err != nil {
		t.Fatal(err)
	}
	h := NewEmbyCachedProxyHandler(service.EmbyProxyConfig{URL: upstream.URL, AccountID: source}, accounts, NewShareAPI(func() string { return "forbidden" }), cache, logs).(*embyProxyHandler)
	login := httptest.NewRecorder()
	h.ServeHTTP(login, httptest.NewRequest("POST", "/emby/Users/AuthenticateByName", nil))
	if login.Code != 200 {
		t.Fatal("登录失败", login.Code)
	}
	h.download = func(context.Context, *p115.ShareInfo, string, string, string) (string, error) {
		t.Fatal("绑定用户回退源账号取链")
		return "", nil
	}
	call := func(method, item, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/cinema115/play?ItemId="+item+"&MediaSourceId=src&UserId=forged&api_key="+token, nil)
		req.Header.Set("User-Agent", "player-UA")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}
	if w := call("GET", "movie", "bad"); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := call("GET", "denied", "viewer"); w.Code != 403 {
		t.Fatal(w.Code)
	}
	if fake.uploads != 0 {
		t.Fatal("未授权即秒传")
	}
	fake.directURL = "https://cdn.115.com/share?friend-direct"
	noSource := ""
	if err := accounts.Update(source, 2, "播放账号", &noSource); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"GET", "HEAD"} {
		w := call(method, "movie", "viewer")
		if w.Code != 302 || w.Header().Get("Location") != fake.directURL || w.Body.Len() != 0 {
			t.Fatal("朋友直取失败", w.Code)
		}
	}
	if fake.uploads != 0 || fake.sourceReads != 0 || len(cache.Status().Entries) != 0 {
		t.Fatal("朋友直取仍使用源账号或创建缓存")
	}
	sourceCookie := "UID=123_A; SEID=private"
	if err := accounts.Update(source, 3, "播放账号", &sourceCookie); err != nil {
		t.Fatal(err)
	}
	fake.directURL = ""
	for _, method := range []string{"GET", "HEAD"} {
		w := call(method, "movie", "viewer")
		if w.Code != 302 || w.Header().Get("Location") != "https://cdn.115.com/friend?private-link" || w.Body.Len() != 0 {
			t.Fatalf("302 不正确: %d %s", w.Code, w.Body.String())
		}
	}
	if fake.uploads != 1 || fake.links != 2 {
		t.Fatal("缓存未复用")
	}
	empty := ""
	if err := accounts.Update(target, 3, "朋友", &empty); err != nil {
		t.Fatal(err)
	}
	if w := call("GET", "movie", "viewer"); w.Code != 503 {
		t.Fatal("目标账号失效未停止", w.Code)
	}
	resultCounts := make(map[string]int)
	for _, entry := range logs.Snapshot() {
		if strings.HasPrefix(entry.Message, "播放取链结果：") {
			if !strings.Contains(entry.Message, `用户="liuqianqian"`) || !strings.Contains(entry.Message, "媒体源=src") || !strings.Contains(entry.Message, `取链账号="朋友"（UID 456`) {
				t.Fatal("播放结果缺少用户、实际媒体源或目标账号", entry.Message)
			}
			for _, mode := range []string{"朋友分享直取", "新建秒传文件", "复用同 SHA1 文件"} {
				if strings.Contains(entry.Message, "播放方式="+mode) {
					resultCounts[mode]++
					if strings.Contains(entry.Message, "源读取账号=") != (mode != "朋友分享直取") {
						t.Fatal("错误标记源账号参与取链")
					}
					if mode != "朋友分享直取" && !strings.Contains(entry.Message, `源读取账号="播放账号"（UID 123`) {
						t.Fatal("源账号标识错误")
					}
				}
			}
		}
		for _, secret := range []string{"friend-private", "private-link", "receive_code", "api_key"} {
			if strings.Contains(entry.Message, secret) {
				t.Fatal("日志泄漏凭据")
			}
		}
	}
	if resultCounts["朋友分享直取"] != 2 || resultCounts["新建秒传文件"] != 1 || resultCounts["复用同 SHA1 文件"] != 1 {
		t.Fatal("成功分支或失败请求的结果日志错误", resultCounts)
	}
	// The next request recovers using the same binding, without an unlock action.
	if err := accounts.Update(target, 4, "朋友", &cookie); err != nil {
		t.Fatal(err)
	}
	fake.directURL = "https://cdn.115.com/recovered"
	if w := call("GET", "movie", "viewer"); w.Code != 302 || w.Header().Get("Location") != fake.directURL {
		t.Fatal("restored account did not recover playback", w.Code)
	}
	if err := accounts.Delete(target, 5); err != nil {
		t.Fatal(err)
	}
	if w := call("GET", "movie", "viewer"); w.Code != 503 {
		t.Fatal("deleted bound account did not block playback", w.Code)
	}
	if err := cache.UpdateBinding(upstream.URL, service.PlaybackBinding{UserID: uid}, target, true); err != nil {
		t.Fatal(err)
	}
	primaryReads := 0
	h.download = func(_ context.Context, _ *p115.ShareInfo, _, selectedCookie, ua string) (string, error) {
		if selectedCookie != sourceCookie || ua != "player-UA" {
			t.Fatal("unbound playback used the wrong primary credentials")
		}
		primaryReads++
		return "https://cdn.115.com/primary", nil
	}
	if w := call("GET", "movie", "viewer"); w.Code != 302 || w.Header().Get("Location") != "https://cdn.115.com/primary" {
		t.Fatal("explicitly unbound user did not use primary", w.Code)
	}
	if primaryReads != 1 {
		t.Fatal("primary playback was not invoked")
	}
}

func (f *proxyCacheFake) DownloadInstantInfoWithUA(ctx context.Context, share *p115.ShareInfo, id, cookie, ua string) (*p115.DownloadInfo, error) {
	return f.DownloadInfoWithUA(ctx, share, id, cookie, ua)
}

func (f *proxyCacheFake) FindPlaybackFileBySHA1(_ context.Context, hash, cookie string) (*p115.DrivePlaybackFile, error) {
	for _, v := range f.files {
		if strings.EqualFold(v.SHA1, hash) {
			return &v, nil
		}
	}
	return nil, nil
}
func (f *proxyCacheFake) PreparePlaybackAccount(context.Context, string, string) (*p115.PlaybackUploadTicket, error) {
	return &p115.PlaybackUploadTicket{}, nil
}
func (f *proxyCacheFake) ValidatePlaybackDirectory(context.Context, string, string, string) error {
	return nil
}
func (f *proxyCacheFake) UploadPlayback(ctx context.Context, info *p115.DownloadInfo, scope, cid, cookie string, ticket *p115.PlaybackUploadTicket, progress func(p115.InstantProgress)) (*p115.InstantUploadResult, error) {
	return f.InstantUploadToEmptyDirectory(ctx, info, cid, cookie, progress)
}
func (f *proxyCacheFake) CompletePlaybackUpload(ctx context.Context, info *p115.DownloadInfo, cid string, result *p115.InstantUploadResult, cookie, ua string) (p115.DrivePlaybackFile, string, error) {
	file := f.files[0]
	link, err := f.DriveDownloadURLWithUA(ctx, file.ID, file.PickCode, cookie, ua)
	return file, link, err
}
