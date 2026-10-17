package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
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
	failNext   bool
	attempts   []string
	checks     int
	checkErr   error
	shaQueries int
}

func (f *poolProtocolDrive) FindPlaybackFileBySHA1Quick(_ context.Context, hash, cookie string) (*p115.DrivePlaybackFile, error) {
	f.shaQueries++
	if hash != strings.Repeat("A", 40) || p115.CookieUserID(cookie) != "456" {
		return nil, p115.ErrDriveInvalidResponse
	}
	return &p115.DrivePlaybackFile{ID: "88", CID: "0", PickCode: "pick123", SHA1: hash, Size: 100, Name: "movie.mkv"}, nil
}

func (f *poolProtocolDrive) CheckCookie(context.Context, string) (bool, error) {
	f.checks++
	if f.checkErr != nil {
		return false, f.checkErr
	}
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

func (f *poolProtocolDrive) VerifiedPlaybackURL(ctx context.Context, file p115.DrivePlaybackFile, cookie, ua string) (string, error) {
	if file.ID != "88" || file.Size != 100 || file.SHA1 != strings.Repeat("A", 40) {
		return "", p115.ErrDriveInvalidResponse
	}
	info, err := f.DownloadInfoWithUA(ctx, nil, "", cookie, ua)
	if err != nil {
		return "", err
	}
	return info.URL, nil
}

func TestPickCodeReuseCanaryPoolHTTP(t *testing.T) {
	checkPoolProtocolNegotiationRedirectAndStop(t, false, true)
}

func TestRedirect115TargetFirstCanaryHTTP(t *testing.T) {
	checkPoolProtocolNegotiationRedirectAndStop(t, false, true, true)
}

func TestPlay115TargetFirstCanaryHTTP(t *testing.T) {
	checkPoolProtocolNegotiationRedirectAndStop(t, false, true, true, true)
}

func TestPoolProtocolNegotiationRedirectAndStop(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprintf("legacy=%t", legacy), func(t *testing.T) { checkPoolProtocolNegotiationRedirectAndStop(t, legacy) })
	}
}

func checkPoolProtocolNegotiationRedirectAndStop(t *testing.T, legacy bool, pickcode ...bool) {
	fileReuse := len(pickcode) > 0 && pickcode[0]
	coldTarget := len(pickcode) > 1 && pickcode[1]
	sourcePath := "http://nas/api/share115/abc/0123456789ABCDEF0123456789ABCDEF01234567/pass/123"
	if fileReuse {
		sourcePath = "http://nas/api/redirect115/abc123/" + strings.Repeat("A", 40)
	}
	if len(pickcode) > 2 && pickcode[2] {
		sourcePath = "http://nas/play115/abc123/999/" + strings.Repeat("A", 40) + "/100/movie.mkv"
	}
	if legacy {
		sourcePath = "http://nas/api/?share_code=abc&id=123&receive_code=pass&filename=movie.mkv"
	}
	ctx := context.Background()
	const uid = "ff7da35afaaa40cd80e0c6c9a6bce2a6"
	profileID := fmt.Sprintf("%x", sha256.Sum256([]byte("server\x00"+uid)))
	accepted := true
	acceptedPing := true
	bodySeen := ""
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/emby/Users/" + uid:
			fmt.Fprintf(w, `{"Id":%q,"Policy":{"EnableMediaPlayback":true,"EnableVideoPlaybackTranscoding":false,"EnableAudioPlaybackTranscoding":false,"EnablePlaybackRemuxing":false,"EnableSyncTranscoding":false,"EnableMediaConversion":false}}`, uid)
		case "/emby/Users/" + uid + "/Items/movie":
			fmt.Fprintf(w, `{"Id":"movie","PlayAccess":"Full","MediaSources":[{"Id":"src","Path":"%s","SupportsDirectPlay":true}]}`, sourcePath)
		case "/emby/Items/movie/PlaybackInfo":
			fmt.Fprintf(w, `{"PlaySessionId":"session","MediaSources":[{"Id":"src","Path":"%s","SupportsDirectPlay":true}]}`, sourcePath)
		case "/emby/Sessions/Playing/Stopped":
			data, _ := io.ReadAll(r.Body)
			bodySeen = string(data)
			if accepted {
				w.WriteHeader(204)
			} else {
				w.WriteHeader(401)
			}
		case "/emby/Sessions/Playing/Progress":
			if accepted {
				w.WriteHeader(204)
			} else {
				w.WriteHeader(401)
			}
		case "/emby/Sessions/Playing/Ping":
			if acceptedPing {
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
	if err = db.SaveEmbyProfile(ctx, model.PortalUser{ID: profileID, ServerID: "server", EmbyID: uid, Username: "viewer"}); err != nil {
		t.Fatal(err)
	}
	if err = db.SaveMemberPlayback(ctx, model.MemberPlayback{UserID: profileID, ServerID: "server", EmbyURL: upstream.URL, EmbyID: uid, Source: "assigned", Stage: "ready", Provisioned: true}); err != nil {
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
	if fileReuse && !coldTarget {
		hash := strings.Repeat("A", 40)
		materialID := service.DirectLinkKey("material-v1", hash, "100")
		seed := service.PlaybackCacheStatus{
			Materials: []service.PlaybackMaterial{{ID: materialID, SHA1: hash, Size: 100, Name: "movie.mkv", VerifiedAt: time.Now().UTC()}},
			Entries:   []service.PlaybackCacheEntry{{Key: service.DirectLinkKey("copy", "456", "88"), UID: "456", AccountID: target, MaterialID: materialID, Status: "ready", File: p115.DrivePlaybackFile{ID: "88", PickCode: "pick123", SHA1: hash, Size: 100, Name: "movie.mkv"}}},
		}
		raw, err := json.Marshal(seed)
		if err != nil {
			t.Fatal(err)
		}
		if err := cs.Save(string(raw)); err != nil {
			t.Fatal(err)
		}
	}
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
	if fileReuse {
		// Canary uses a real HTTP ingress and the full pool/user authorization
		// stack, with isolated upstreams and no possible cloud writes.
		server := httptest.NewServer(h)
		defer server.Close()
		client := server.Client()
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		call = func(method, path, body string) *httptest.ResponseRecorder {
			req, err := http.NewRequest(method, server.URL+path, strings.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			w := httptest.NewRecorder()
			for k, values := range resp.Header {
				w.Header()[k] = values
			}
			w.WriteHeader(resp.StatusCode)
			if _, err := io.Copy(w, resp.Body); err != nil {
				t.Fatal(err)
			}
			return w
		}
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
	checksAtStart := drive.checks
	for i := 0; i < 2; i++ {
		w = call("GET", path, "")
		if w.Code != 302 || w.Header().Get("Location") != "https://cdn.115.com/pool" {
			t.Fatal(w.Code, w.Body.String())
		}
		wantChecks := checksAtStart + i
		if drive.checks != wantChecks || len(drive.attempts) != 1 {
			t.Fatal("cold share must read once without probe; cache hit must freshly probe", drive.checks, len(drive.attempts))
		}
	}
	d, err = db.PlaybackPool(ctx)
	if err != nil || len(d.Leases) != 1 {
		t.Fatal("repeat redirect double-counted", err)
	}
	t.Run("local-proxy-pause-blocks-cached-and-new-playback", func(t *testing.T) {
		beforeChecks, beforeReads := drive.checks, len(drive.attempts)
		beforeUsers := members.UserPlayback.List()
		state, err := members.SetMemberProxyPlayback(ctx, profileID, false, 0)
		if err != nil || state.Enabled {
			t.Fatal(state, err)
		}
		t.Cleanup(func() {
			if _, err := members.SetMemberProxyPlayback(ctx, profileID, true, state.Revision); err != nil {
				t.Error(err)
			}
		})
		for _, request := range []struct{ method, path string }{
			{"GET", path}, {"HEAD", path},
			{"GET", strings.Replace(path, "PlaySessionId=session", "PlaySessionId=paused-new", 1)},
			{"GET", "/emby/Items/movie/PlaybackInfo?api_key=token"},
		} {
			w := call(request.method, request.path, "")
			if w.Code != 403 || w.Header().Get("Location") != "" {
				t.Fatal("paused playback bypassed local gate", request, w.Code, w.Body.String())
			}
		}
		after, err := db.PlaybackPool(ctx)
		if err != nil || !reflect.DeepEqual(d.Leases, after.Leases) || !reflect.DeepEqual(beforeUsers, members.UserPlayback.List()) {
			t.Fatal("local pause released or changed issued capacity", err)
		}
		if drive.checks != beforeChecks || len(drive.attempts) != beforeReads {
			t.Fatal("paused playback reached 115")
		}
		if _, err := members.SetMemberProxyPlayback(ctx, profileID, true, state.Revision); err != nil {
			t.Fatal(err)
		}
		// Negotiation after resuming passes without reserving extra capacity.
		if w := call("GET", "/emby/Items/movie/PlaybackInfo?api_key=token", ""); w.Code != 200 {
			t.Fatal("fresh negotiation did not resume", w.Code, w.Body.String())
		}
	})
	t.Run("cached-share-rejects-expired-cookie", func(t *testing.T) {
		drive.checkErr = p115.ErrShareLoginRequired
		before := len(drive.attempts)
		w := call("GET", path, "")
		if w.Code == 302 || w.Header().Get("Location") != "" || len(drive.attempts) != before {
			t.Fatal("cached link bypassed current cookie failure", w.Code)
		}
		drive.checkErr = nil
		if err := members.CheckPoolAccount(ctx, target); err != nil {
			t.Fatal(err)
		}
	})
	if fileReuse {
		if coldTarget && (drive.shaQueries != 1 || drive.pickReads != 0 || drive.uploads != 0 || len(cache.Status().Materials) != 1 || len(cache.Status().Materials[0].Origins) != 0) {
			t.Fatal("target-first canary did not bypass source or repeated search")
		}
		h.ClearDirectLinks("")
		beforeChecks := drive.checks
		beforeReads := len(drive.attempts)
		if w := call("HEAD", path, ""); w.Code != 302 {
			t.Fatal("repeat file reuse failed", w.Code, w.Body.String())
		}
		if drive.checks != beforeChecks || len(drive.attempts) != beforeReads+1 || drive.pickReads != 0 || drive.uploads != 0 {
			t.Fatal("reuse performed directory/source/upload work")
		}
		t.Logf("canary: target-first=%t shaQueries=%d source=0 uploads=0; repeated reuse=1 target read; cached link=1 fresh probe; expired cookie rejected", coldTarget, drive.shaQueries)
		return
	}
	if members.UserPlayback.List()[0].PlaybackState != "" {
		t.Fatal("redirect alone claimed playback")
	}
	w = call("POST", "/emby/Sessions/Playing/Progress?api_key=token", `{"ItemId":"movie","PlaySessionId":"session","IsPaused":true}`)
	if w.Code != 204 || members.UserPlayback.List()[0].PlaybackState != "paused" {
		t.Fatal("accepted pause not exposed", w.Code)
	}
	accepted = false
	w = call("POST", "/emby/Sessions/Playing/Progress?api_key=token", `{"ItemId":"movie","PlaySessionId":"session","IsPaused":false}`)
	if w.Code != 401 || members.UserPlayback.List()[0].PlaybackState != "paused" {
		t.Fatal("rejected progress changed display state", w.Code)
	}
	accepted = true
	w = call("POST", "/emby/Sessions/Playing/Progress?api_key=token", `{"ItemId":"movie","PlaySessionId":"session","IsPaused":"unknown"}`)
	if w.Code != 204 || members.UserPlayback.List()[0].PlaybackState != "paused" {
		t.Fatal("invalid optional display metadata affected lifecycle or supplied state", w.Code)
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
	for _, event := range []string{"Progress", "Stopped"} {
		w = call("POST", "/emby/Sessions/Playing/"+event+"?api_key=token", `{"ItemId":"movie","PlaySessionId":"session","playsessionid":"other"}`)
		if w.Code != 400 || len(members.UserPlayback.List()) != 1 {
			t.Fatal("conflicting event changed user session", w.Code)
		}
		d, err = db.PlaybackPool(ctx)
		if err != nil || len(d.Leases) != 1 {
			t.Fatal("conflicting event changed pool lease", err)
		}
		for _, lease := range d.Leases {
			if lease.Stopped {
				t.Fatal("conflicting event stopped pool lease")
			}
		}
	}
	stop := `{"ItemId":"movie","PlaySessionId":"session","playsessionid":"session","UserId":"forged"}`
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
	if metrics.Requests != 10 || metrics.Succeeded != 5 || metrics.Failed != 5 || metrics.Switches != 2 || metrics.Samples != 10 {
		t.Fatal("redirect metrics lost retries or double-counted", metrics)
	}
	// Missing token identity still fails before pool admission; no budget change
	// may turn client-supplied UserId into a recognized identity.
	w = call("GET", strings.Replace(path, "api_key=token", "api_key=unknown&UserId="+uid, 1), "")
	if w.Code != 401 || members.Pool.Metrics().Requests != 10 {
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
	// Simulate forty seconds without playback evidence, then exercise the full
	// redirect path with an identity learned from an upstream login response.
	if err := h.identities.RememberDevice(upstream.URL, "token", uid, "phone", "player"); err != nil {
		t.Fatal(err)
	}
	ud, err := db.UserPlayback(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var oldEvent string
	for key, l := range ud.Leases {
		l.Device = h.identities.Device(upstream.URL, "token")
		l.IssuedAt = time.Now().Add(-40 * time.Second).UnixMilli()
		l.UpdatedAt, l.LastPlaybackAt = l.IssuedAt, l.IssuedAt
		ud.Leases[key] = l
		oldEvent = l.EventKey
	}
	if err := db.SaveUserPlayback(ctx, ud); err != nil {
		t.Fatal(err)
	}
	members.UserPlayback, err = service.NewUserPlayback(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	settings.MaxSessions = 10 // Allow unconfirmed next-item prefetch independently.
	if err := members.ConfigurePool(ctx, settings); err != nil {
		t.Fatal(err)
	}
	w = call("GET", strings.Replace(path, "PlaySessionId=session", "PlaySessionId=handoff-session", 1), "")
	if w.Code != 302 || len(members.UserPlayback.List()) != 2 {
		t.Fatal("prefetch did not preserve current playback", w.Code, w.Body.String())
	}
	w = call("POST", "/emby/Sessions/Playing?api_key=token", `{"ItemId":"movie","PlaySessionId":"handoff-session"}`)
	if w.Code != 204 || len(members.UserPlayback.List()) != 1 {
		t.Fatal("device handoff failed", w.Code, w.Body.String())
	}
	ud, err = db.UserPlayback(ctx)
	if err != nil || ud.Stopped[oldEvent] == 0 {
		t.Fatal("handoff did not fence old playback", err)
	}
	w = call("POST", "/emby/Sessions/Playing/Stopped?api_key=token", `{"ItemId":"movie","PlaySessionId":"new-session"}`)
	if w.Code != 204 || len(members.UserPlayback.List()) != 1 {
		t.Fatal("old stop released successor", w.Code)
	}
	// Exercise the real HTTP authorization/commit path with authenticated device
	// identity. GET/HEAD-only next-item requests must not multiply UID capacity.
	for i := 0; i < 100; i++ {
		method := "GET"
		if i%2 == 0 {
			method = "HEAD"
		}
		nextPath := strings.Replace(path, "PlaySessionId=session", fmt.Sprintf("PlaySessionId=device-next-%d", i), 1)
		w = call(method, nextPath, "")
		if w.Code != 302 {
			t.Fatal("device slot redirect failed", i, w.Code, w.Body.String())
		}
		doc, err := db.PlaybackPool(ctx)
		if err != nil {
			t.Fatal(err)
		}
		deviceUIDs := make(map[string]bool)
		for _, lease := range doc.Leases {
			if lease.SlotKey != "" && !lease.Stopped && lease.ExpiresAt > time.Now().UnixMilli() {
				deviceUIDs[lease.UID] = true
			}
		}
		if len(deviceUIDs) != 1 || len(members.UserPlayback.List()) > 2 {
			t.Fatal("HTTP device slot drift or unbounded candidate history")
		}
	}
	t.Run("heartbeat-policy-http-gray", func(t *testing.T) {
		if err := members.UserPlayback.ConfigureDevicePolicy(ctx, model.PlaybackDevicePolicy{Mode: service.DevicePolicyHeartbeat, SilenceSeconds: 90}); err != nil {
			t.Fatal(err)
		}
		if err := h.identities.RememberDevice(upstream.URL, "tv-token", uid, "television", "player"); err != nil {
			t.Fatal(err)
		}
		before := len(drive.attempts)
		beforeChecks := drive.checks
		bPath := strings.ReplaceAll(strings.ReplaceAll(path, "api_key=token", "api_key=tv-token"), "PlaySessionId=session", "PlaySessionId=tv-session")
		for i := 0; i < 20; i++ {
			method := "GET"
			if i%2 == 0 {
				method = "HEAD"
			}
			w := call(method, bPath, "")
			if w.Code != 409 || w.Header().Get("Location") != "" {
				t.Fatal("second device issued link", w.Code, w.Body.String())
			}
		}
		if len(drive.attempts) != before || drive.checks != beforeChecks {
			t.Fatal("rejected device reached cloud")
		}
		currentPath := strings.Replace(path, "PlaySessionId=session", "PlaySessionId=device-next-99", 1)
		if w := call("GET", currentPath, ""); w.Code != 302 {
			t.Fatal("same device cache refresh rejected", w.Code, w.Body.String())
		}
		if w := call("POST", "/emby/Sessions/Playing/Ping?api_key=token&PlaySessionId=device-next-99", ""); w.Code != 204 {
			t.Fatal("ping rejected", w.Code)
		}
		ud, err := db.UserPlayback(ctx)
		if err != nil {
			t.Fatal(err)
		}
		matched := false
		for _, l := range ud.Leases {
			if l.AuthSession == service.DirectLinkKey("token", "device-next-99") {
				matched = l.LastPlaybackAt > 0
			}
		}
		if !matched {
			t.Fatal("accepted ping did not renew exact session")
		}
		acceptedPing = false
		if w := call("POST", "/emby/Sessions/Playing/Ping?api_key=token&PlaySessionId=device-next-99", ""); w.Code != 401 {
			t.Fatal("rejected ping accepted", w.Code)
		}
		acceptedPing = true
		if w := call("POST", "/emby/Sessions/Playing/Ping?api_key=tv-token&PlaySessionId=device-next-99", ""); w.Code != 204 {
			t.Fatal(w.Code)
		}
		afterPing, err := db.UserPlayback(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for key, l := range ud.Leases {
			if afterPing.Leases[key].LastPlaybackAt != l.LastPlaybackAt {
				t.Fatal("rejected or wrong-token ping renewed playback")
			}
		}
		if err := h.identities.Remember(upstream.URL, "no-device", uid); err != nil {
			t.Fatal(err)
		}
		if w := call("GET", strings.ReplaceAll(bPath, "tv-token", "no-device"), ""); w.Code != 403 || !strings.Contains(w.Body.String(), "PLAYBACK_DEVICE_REQUIRED") {
			t.Fatal("missing device bypass", w.Code, w.Body.String())
		}
		// At capacity=1, cross-device preparation must inherit the old slot.
		settings := members.Pool.Settings()
		settings.MaxSessions = 1
		if err := members.ConfigurePool(ctx, settings); err != nil {
			t.Fatal(err)
		}
		// Simulate a silent interval by aging the persisted evidence and restarting
		// the user ledger. Keep real HTTP handling and pool transactions intact.
		for key, l := range afterPing.Leases {
			l.FirstIssuedAt = time.Now().Add(-100 * time.Second).UnixMilli()
			l.LastPlaybackAt = time.Now().Add(-91 * time.Second).UnixMilli()
			afterPing.Leases[key] = l
		}
		if err := db.SaveUserPlayback(ctx, afterPing); err != nil {
			t.Fatal(err)
		}
		// Earlier cases intentionally created device-less records. Let those
		// unrelated residuals expire; the transfer must never guess their owner
		// device or use them as a substitute for authenticated slot capacity.
		agedPool, err := db.PlaybackPool(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for key, l := range agedPool.Leases {
			if l.SlotKey == "" {
				l.ExpiresAt = time.Now().Add(-time.Second).UnixMilli()
				agedPool.Leases[key] = l
			}
		}
		if err := db.SavePlaybackPool(ctx, agedPool); err != nil {
			t.Fatal(err)
		}
		members.Pool, err = service.NewDynamicPool(ctx, db)
		if err != nil {
			t.Fatal(err)
		}
		members.UserPlayback, err = service.NewUserPlayback(ctx, db)
		if err != nil {
			t.Fatal(err)
		}
		if w := call("GET", bPath, ""); w.Code != 302 {
			t.Fatal("HTTP timed takeover failed", w.Code, w.Body.String())
		}
		poolDoc, err := db.PlaybackPool(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(poolDoc.Leases) != 1 {
			t.Fatal("HTTP transfer left old pool occupancy", len(poolDoc.Leases))
		}
		if w := call("GET", currentPath, ""); w.Code != 409 {
			t.Fatal("retired URL remained usable", w.Code, w.Body.String())
		}
		newAPath := strings.Replace(path, "PlaySessionId=session", "PlaySessionId=return-after-takeover", 1)
		if w := call("GET", newAPath, ""); w.Code != 409 {
			t.Fatal("A took back active B", w.Code, w.Body.String())
		}
		if err := members.UserPlayback.ConfigureDevicePolicy(ctx, model.PlaybackDevicePolicy{Mode: service.DevicePolicyParallel}); err != nil {
			t.Fatal(err)
		}
		settings.MaxSessions = 10
		if err := members.ConfigurePool(ctx, settings); err != nil {
			t.Fatal(err)
		}
		if w := call("GET", newAPath, ""); w.Code != 302 {
			t.Fatal("parallel rollback denied A", w.Code, w.Body.String())
		}
		if w := call("GET", bPath, ""); w.Code != 302 {
			t.Fatal("parallel rollback did not restore B", w.Code, w.Body.String())
		}
		t.Log("20 GET/HEAD requests blocked before cloud work; exact Ping only; persisted silent evidence permits HTTP takeover; retired A fenced; parallel rollback restores both devices")
	})
}
