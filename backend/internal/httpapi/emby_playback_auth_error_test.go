package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"strm115/backend/internal/model"
	"strm115/backend/internal/service"
	"strm115/backend/internal/storage"
)

func TestPlaybackAuthorizationFailureClassification(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
	}{
		{context.Canceled, 502}, {context.DeadlineExceeded, 504},
		{service.ErrPlaybackIdentityUnavailable, 502}, {service.ErrLogin, 403}, {service.ErrMemberPlayback, 403},
	} {
		_, status := playbackAuthorizationFailure(context.Background(), fmt.Errorf("wrapped: %w", tc.err))
		if status != tc.status {
			t.Fatal(tc.err, status)
		}
	}
}

// Exercise every membership check, especially cancellation after media lookup:
// this previously turned a canceled request into a false binding-denial 403.
func TestPlaybackMembershipLookupFailureAndRecovery(t *testing.T) {
	for _, stage := range []int32{1, 2, 3} {
		for _, mode := range []string{"cancel", "unavailable", "disabled"} {
			t.Run(fmt.Sprintf("check-%d/%s", stage, mode), func(t *testing.T) {
				const userID = "ff7da35afaaa40cd80e0c6c9a6bce2a6"
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				var calls atomic.Int32
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					switch r.URL.Path {
					case "/emby/Users/" + userID:
						if r.Header.Get("X-Emby-Token") == "admin-secret" && calls.Add(1) == stage {
							switch mode {
							case "cancel":
								cancel()
								<-r.Context().Done()
								return
							case "unavailable":
								http.Error(w, "private upstream details", 503)
								return
							case "disabled":
								fmt.Fprintf(w, `{"Id":%q,"Policy":{"IsDisabled":true}}`, userID)
								return
							}
						}
						fmt.Fprintf(w, `{"Id":%q,"Policy":{"EnableMediaPlayback":true,"IsDisabled":false,"EnableVideoPlaybackTranscoding":false,"EnableAudioPlaybackTranscoding":false,"EnablePlaybackRemuxing":false,"EnableSyncTranscoding":false,"EnableMediaConversion":false}}`, userID)
					case "/emby/Users/" + userID + "/Items/movie":
						fmt.Fprint(w, `{"Id":"movie","PlayAccess":"Full"}`)
					case "/emby/Items/movie/PlaybackInfo":
						fmt.Fprint(w, `{"MediaSources":[{"Id":"src","Path":"http://old.invalid/api/redirect115/abc123","SupportsDirectPlay":true}]}`)
					default:
						w.WriteHeader(404)
					}
				}))
				defer upstream.Close()
				accounts, primary := proxyTestAccounts(t)
				db, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "members.db"))
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				background := context.Background()
				if err := db.SaveEmbyProfile(background, model.PortalUser{ID: "owner", ServerID: "server", EmbyID: userID, Username: "viewer"}); err != nil {
					t.Fatal(err)
				}
				if err := db.SaveMemberPlayback(background, model.MemberPlayback{UserID: "owner", ServerID: "server", EmbyURL: upstream.URL, EmbyID: userID, Source: "primary", Stage: "ready", Provisioned: true}); err != nil {
					t.Fatal(err)
				}
				if err := db.SaveMemberPlaybackSettings(background, model.MemberPlaybackSettings{Enabled: true, ServerID: "server", EmbyURL: upstream.URL}); err != nil {
					t.Fatal(err)
				}
				secret, err := service.NewSessionStore(filepath.Join(t.TempDir(), "emby.enc"), "secret")
				if err != nil {
					t.Fatal(err)
				}
				raw, err := json.Marshal(service.EmbyConfig{URL: upstream.URL, ServerID: "server", APIKey: "admin-secret"})
				if err != nil {
					t.Fatal(err)
				}
				if err := secret.Save(string(raw)); err != nil {
					t.Fatal(err)
				}
				embyService, err := service.NewEmbyService(secret, db)
				if err != nil {
					t.Fatal(err)
				}
				members, err := service.NewMemberPlayback(background, db, accounts, embyService, nil, playbackIdentityChecker{}, nil)
				if err != nil {
					t.Fatal(err)
				}
				members.ProxyStatus = func() service.EmbyProxyStatus {
					return service.EmbyProxyStatus{Listening: true, Config: service.EmbyProxyConfig{Enabled: true, URL: upstream.URL}}
				}
				cacheStore, err := service.NewSessionStore(filepath.Join(t.TempDir(), "cache.enc"), "secret")
				if err != nil {
					t.Fatal(err)
				}
				logs := service.NewEmbyProxyLogs(&service.GenerationLogs{})
				cache, err := service.NewPlaybackCache(background, cacheStore, accounts, nil, logs)
				if err != nil {
					t.Fatal(err)
				}
				cache.Members = members
				h := NewEmbyCachedProxyHandler(service.EmbyProxyConfig{URL: upstream.URL, AccountID: primary, Debug: true}, accounts, NewShareAPI(func() string { return "" }), cache, logs).(*embyProxyHandler)
				defer h.Close()
				if err := h.identities.Remember(upstream.URL, "viewer-token", userID); err != nil {
					t.Fatal(err)
				}
				links := 0
				h.pickDownload = func(context.Context, string, string, string) (string, error) {
					links++
					return "https://cdn.115.com/test", nil
				}
				request := func(ctx context.Context) *httptest.ResponseRecorder {
					w := httptest.NewRecorder()
					h.ServeHTTP(w, httptest.NewRequest("GET", "/cinema115/play?ItemId=movie&MediaSourceId=src&api_key=viewer-token", nil).WithContext(ctx))
					return w
				}
				w := request(ctx)
				want := 403
				if mode == "cancel" {
					want = 499
				}
				if mode == "unavailable" {
					want = 502
				}
				if w.Code != want || links != 0 {
					t.Fatal("incorrect failure or premature cloud access", w.Code, links, w.Body.String())
				}
				for _, entry := range logs.Snapshot() {
					encoded, _ := json.Marshal(entry)
					if strings.Contains(string(encoded), "admin-secret") || strings.Contains(string(encoded), "private upstream details") {
						t.Fatal("upstream secrets leaked")
					}
					if mode == "cancel" && (entry.Level == "ERROR" || entry.Level == "WARNING" || strings.Contains(string(encoded), "HTTP 403")) {
						t.Fatal("cancellation misreported", entry.Level)
					}
				}
				if next := request(background); next.Code != 302 || links != 1 {
					t.Fatal("healthy follow-up failed", next.Code, next.Body.String())
				}
			})
		}
	}
}

type playbackIdentityChecker struct{}

func (playbackIdentityChecker) CheckCookie(context.Context, string) (bool, error) { return true, nil }
