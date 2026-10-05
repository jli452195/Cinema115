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
	checkErr error
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

func TestPoolProtocolNegotiationRedirectAndStop(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprintf("legacy=%t", legacy), func(t *testing.T) { checkPoolProtocolNegotiationRedirectAndStop(t, legacy) })
	}
}

func checkPoolProtocolNegotiationRedirectAndStop(t *testing.T, legacy bool) {
	sourcePath := "http://nas/api/share115/abc/0123456789ABCDEF0123456789ABCDEF01234567/pass/123"
	if legacy {
		sourcePath = "http://nas/api/?share_code=abc&id=123&receive_code=pass&filename=movie.mkv"
	}
	ctx := context.Background()
	const uid = "ff7da35afaaa40cd80e0c6c9a6bce2a6"
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
	checksAtStart := drive.checks
	for i := 0; i < 2; i++ {
		w = call("GET", path, "")
		if w.Code != 302 || w.Header().Get("Location") != "https://cdn.115.com/pool" {
			t.Fatal(w.Code, w.Body.String())
		}
		if drive.checks != checksAtStart+i || len(drive.attempts) != 1 {
			t.Fatal("cold share must read once without probe; cache hit must freshly probe", drive.checks, len(drive.attempts))
		}
	}
	d, err = db.PlaybackPool(ctx)
	if err != nil || len(d.Leases) != 1 {
		t.Fatal("repeat redirect double-counted", err)
	}
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
	if metrics.Requests != 7 || metrics.Succeeded != 5 || metrics.Failed != 2 || metrics.Switches != 2 || metrics.Samples != 7 {
		t.Fatal("redirect metrics lost retries or double-counted", metrics)
	}
	// Missing token identity still fails before pool admission; no budget change
	// may turn client-supplied UserId into a recognized identity.
	w = call("GET", strings.Replace(path, "api_key=token", "api_key=unknown&UserId="+uid, 1), "")
	if w.Code != 401 || members.Pool.Metrics().Requests != 7 {
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
