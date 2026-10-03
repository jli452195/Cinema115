package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
	"strm115/backend/internal/service"
	"strm115/backend/internal/storage"
	"testing"
	"time"
)

type poolProtocolDrive struct {
	proxyCacheFake
	failNext bool
	attempts []string
	checks   int
}

func (f *poolProtocolDrive) CheckCookie(context.Context, string) (bool, error) {
	f.checks++
	return true, nil
}
func (*poolProtocolDrive) DriveFiles(context.Context, string, int, string) (*p115.SharePage, error) {
	return &p115.SharePage{}, nil
}
func (f *poolProtocolDrive) DownloadInfoWithUA(_ context.Context, _ *p115.ShareInfo, _ string, cookie, _ string) (*p115.DownloadInfo, error) {
	f.attempts = append(f.attempts, p115.CookieUserID(cookie))
	if f.failNext {
		f.failNext = false
		return nil, p115.ErrShareLoginRequired
	}
	if p115.CookieUserID(cookie) != "456" && p115.CookieUserID(cookie) != "789" {
		return nil, p115.ErrShareLoginRequired
	}
	return &p115.DownloadInfo{URL: "https://cdn.115.com/pool"}, nil
}

func TestPoolProtocolNegotiationRedirectAndStop(t *testing.T) {
	ctx := context.Background()
	const uid = "ff7da35afaaa40cd80e0c6c9a6bce2a6"
	accepted := true
	bodySeen := ""
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/emby/Users/" + uid:
			fmt.Fprintf(w, `{"Id":%q,"Policy":{"EnableMediaPlayback":true,"EnableVideoPlaybackTranscoding":false,"EnableAudioPlaybackTranscoding":false,"EnablePlaybackRemuxing":false,"EnableSyncTranscoding":false,"EnableMediaConversion":false}}`, uid)
		case "/emby/Users/" + uid + "/Items/movie":
			fmt.Fprint(w, `{"Id":"movie","PlayAccess":"Full","MediaSources":[{"Id":"src","Path":"http://nas/api/?share_code=abc&id=123&receive_code=pass","SupportsDirectPlay":true}]}`)
		case "/emby/Items/movie/PlaybackInfo":
			fmt.Fprint(w, `{"PlaySessionId":"session","MediaSources":[{"Id":"src","Path":"http://nas/api/?share_code=abc&id=123&receive_code=pass","SupportsDirectPlay":true}]}`)
		case "/emby/Sessions/Playing/Stopped":
			data, _ := io.ReadAll(r.Body)
			bodySeen = string(data)
			if accepted {
				w.WriteHeader(204)
			} else {
				w.WriteHeader(401)
			}
		default:
			w.WriteHeader(204)
		}
	}))
	defer upstream.Close()
	accounts, primary := proxyTestAccounts(t)
	target, err := accounts.Add("pool")
	if err != nil {
		t.Fatal(err)
	}
	cookie := "UID=456_B; SEID=friend-private"
	if err = accounts.Update(target, 1, "pool", &cookie); err != nil {
		t.Fatal(err)
	}
	if err = accounts.UpdatePlaybackDirectory(target, 2, "123"); err != nil {
		t.Fatal(err)
	}
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "pool.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = db.SaveEmbyProfile(ctx, model.PortalUser{ID: "owner", ServerID: "server", EmbyID: uid, Username: "viewer"}); err != nil {
		t.Fatal(err)
	}
	if err = db.SaveMemberPlayback(ctx, model.MemberPlayback{UserID: "owner", ServerID: "server", EmbyURL: upstream.URL, EmbyID: uid, Source: "assigned", Stage: "ready", Provisioned: true}); err != nil {
		t.Fatal(err)
	}
	if err = db.SaveMemberPlaybackSettings(ctx, model.MemberPlaybackSettings{Enabled: true, ServerID: "server", EmbyURL: upstream.URL}); err != nil {
		t.Fatal(err)
	}
	store, err := service.NewSessionStore(filepath.Join(t.TempDir(), "emby.enc"), "secret")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(service.EmbyConfig{URL: upstream.URL, ServerID: "server", APIKey: "admin"})
	if err = store.Save(string(raw)); err != nil {
		t.Fatal(err)
	}
	es, err := service.NewEmbyService(store, db)
	if err != nil {
		t.Fatal(err)
	}
	drive := &poolProtocolDrive{}
	members, err := service.NewMemberPlayback(ctx, db, accounts, es, drive, drive, nil)
	if err != nil {
		t.Fatal(err)
	}
	members.ProxyStatus = func() service.EmbyProxyStatus {
		return service.EmbyProxyStatus{Listening: true, Config: service.EmbyProxyConfig{Enabled: true, URL: upstream.URL}}
	}
	cs, err := service.NewSessionStore(filepath.Join(t.TempDir(), "cache.enc"), "secret")
	if err != nil {
		t.Fatal(err)
	}
	logs := service.NewEmbyProxyLogs(&service.GenerationLogs{})
	cache, err := service.NewPlaybackCache(ctx, cs, accounts, drive, logs)
	if err != nil {
		t.Fatal(err)
	}
	cache.Members = members
	members.Cache = cache
	h := NewEmbyCachedProxyHandler(service.EmbyProxyConfig{URL: upstream.URL, AccountID: primary, DirectLinkCacheMinutes: 9}, accounts, NewShareAPI(func() string { return "" }), cache, logs).(*embyProxyHandler)
	defer h.Close()
	if err = h.identities.Remember(upstream.URL, "token", uid); err != nil {
		t.Fatal(err)
	}
	call := func(method, path, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
		return w
	}
	w := call("GET", "/emby/Items/movie/PlaybackInfo?api_key=token", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "PlaySessionId=session") {
		t.Fatal("session hint missing", w.Code, w.Body.String())
	}
	d, err := db.PlaybackPool(ctx)
	if err != nil || len(d.Leases) != 0 {
		t.Fatal("negotiation reserved capacity", err)
	}
	path := "/cinema115/play?ItemId=movie&MediaSourceId=src&PlaySessionId=session&api_key=token"
	for i := 0; i < 2; i++ {
		w = call("GET", path, "")
		if w.Code != 302 || w.Header().Get("Location") != "https://cdn.115.com/pool" {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	d, err = db.PlaybackPool(ctx)
	if err != nil || len(d.Leases) != 1 {
		t.Fatal("repeat redirect double-counted", err)
	}
	settings := members.Pool.Settings()
	limited := settings
	limited.MaxSessions = 1
	if err := members.ConfigurePool(ctx, limited); err != nil {
		t.Fatal(err)
	}
	before := len(drive.attempts)
	beforeChecks := drive.checks
	w = call("GET", strings.Replace(path, "PlaySessionId=session", "PlaySessionId=capacity-denied", 1), "")
	if w.Code != 503 || !strings.Contains(w.Body.String(), "共享小号会话额度已满") || strings.Contains(w.Body.String(), "UID=") || len(drive.attempts) != before || drive.checks != beforeChecks || w.Header().Get("Location") != "" {
		t.Fatal("busy request reached cloud or leaked diagnostics", w.Code, w.Body.String())
	}
	for _, entry := range logs.Snapshot() {
		encoded, err := json.Marshal(entry)
		if err != nil {
			t.Fatal(err)
		}
		line := string(encoded)
		if strings.Contains(line, "friend-private") {
			t.Fatal("credential in diagnostic")
		}
	}
	if err := members.ConfigurePool(ctx, settings); err != nil {
		t.Fatal(err)
	}
	stop := `{"ItemId":"movie","PlaySessionId":"session","UserId":"forged"}`
	accepted = false
	w = call("POST", "/emby/Sessions/Playing/Stopped?api_key=token", stop)
	if w.Code != 401 || bodySeen != stop {
		t.Fatal("event body changed")
	}
	if len(members.UserPlayback.List()) != 1 {
		t.Fatal("rejected stop released user slot")
	}
	d, _ = db.PlaybackPool(ctx)
	for _, l := range d.Leases {
		if l.Stopped {
			t.Fatal("failed upstream event released lease")
		}
	}
	accepted = true
	w = call("POST", "/emby/Sessions/Playing/Stopped?api_key=token", stop)
	if w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	if len(members.UserPlayback.List()) != 0 {
		t.Fatal("stop did not release user slot")
	}
	d, _ = db.PlaybackPool(ctx)
	for _, l := range d.Leases {
		if !l.Stopped || time.Until(time.UnixMilli(l.ExpiresAt)) > 2*time.Minute {
			t.Fatal("accepted stop not applied")
		}
	}
	// A real client may reuse its playback URL after pressing stop and replay.
	// Even with a single pool slot this must reuse the residual target.
	if err := members.ConfigurePool(ctx, limited); err != nil {
		t.Fatal(err)
	}
	w = call("GET", path, "")
	if w.Code != 302 || len(members.UserPlayback.List()) != 1 {
		t.Fatal("in-place replay blocked", w.Code, w.Body.String())
	}
	d, _ = db.PlaybackPool(ctx)
	if len(d.Leases) != 1 {
		t.Fatal("replay duplicated pool occupancy")
	}
	w = call("POST", "/emby/Sessions/Playing/Stopped?api_key=token", stop)
	if w.Code != 204 || len(members.UserPlayback.List()) != 0 {
		t.Fatal("replay stop failed", w.Code)
	}
	// Some clients omit PlaySessionId on stop. The authenticated token and item
	// fallback must still release the current generation.
	w = call("GET", path, "")
	if w.Code != 302 || len(members.UserPlayback.List()) != 1 {
		t.Fatal("fallback replay failed", w.Code)
	}
	withoutSession := `{"ItemId":"movie"}`
	w = call("POST", "/emby/Sessions/Playing/Stopped?api_key=token", withoutSession)
	if w.Code != 204 || len(members.UserPlayback.List()) != 0 {
		t.Fatal("stop without PlaySessionId did not release", w.Code)
	}
	d, _ = db.PlaybackPool(ctx)
	for _, l := range d.Leases {
		if !l.Stopped {
			t.Fatal("fallback stop did not mark pool residual")
		}
	}
	if err := members.ConfigurePool(ctx, settings); err != nil {
		t.Fatal(err)
	}
	other, err := accounts.Add("second")
	if err != nil {
		t.Fatal(err)
	}
	otherCookie := "UID=789_A"
	if err = accounts.Update(other, 1, "second", &otherCookie); err != nil {
		t.Fatal(err)
	}
	if err = accounts.UpdatePlaybackDirectory(other, 2, "123"); err != nil {
		t.Fatal(err)
	}
	drive.failNext = true
	drive.attempts = nil
	w = call("GET", strings.Replace(path, "PlaySessionId=session", "PlaySessionId=new-session", 1), "")
	if w.Code != 302 || len(drive.attempts) != 2 || drive.attempts[0] == drive.attempts[1] {
		t.Fatal("bounded target failover failed", w.Code, drive.attempts, w.Body.String())
	}
	if len(members.UserPlayback.List()) != 1 {
		t.Fatal("failover lost user slot")
	}
	for _, attempt := range drive.attempts {
		if attempt == "123" {
			t.Fatal("pool fell back to primary")
		}
	}
	metrics := members.Pool.Metrics()
	if metrics.Requests != 6 || metrics.Succeeded != 5 || metrics.Failed != 1 || metrics.Switches != 1 || metrics.Samples != 6 {
		t.Fatal("redirect metrics lost retries or double-counted", metrics)
	}
	// Missing token identity still fails before pool admission; no budget change
	// may turn client-supplied UserId into a recognized identity.
	w = call("GET", strings.Replace(path, "api_key=token", "api_key=unknown&UserId="+uid, 1), "")
	if w.Code != 401 || members.Pool.Metrics().Requests != 6 {
		t.Fatal("unrecognized identity reached pool", w.Code)
	}
	// A valid second playback by the same user is admitted when UID capacity
	// allows it; stopping it leaves the previous playback intact.
	w = call("GET", strings.Replace(path, "PlaySessionId=session", "PlaySessionId=parallel-session", 1), "")
	if w.Code != 302 || len(members.UserPlayback.List()) != 2 {
		t.Fatal("same user parallel playback blocked", w.Code, w.Body.String())
	}
	w = call("POST", "/emby/Sessions/Playing/Stopped?api_key=token", `{"ItemId":"movie","PlaySessionId":"parallel-session"}`)
	if w.Code != 204 || len(members.UserPlayback.List()) != 1 {
		t.Fatal("parallel stop affected previous playback", w.Code)
	}
}
