package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/service"
	"strm115/backend/internal/strm"
)

func TestED2KSTRMMediaSourceIsSelfContained(t *testing.T) {
	value := strm.ED2K{Hash: strings.Repeat("a", 32), SHA1: strings.Repeat("B", 40), Size: 123456789, Name: "影片 #1+%.mkv"}
	raw, err := strm.ED2KURL("https://retired.invalid", value)
	if err != nil {
		t.Fatal(err)
	}
	path, _ := json.Marshal(raw)
	h := &embyProxyHandler{share: NewShareAPI(func() string { return "" })}
	claim, err := h.playbackClaim(map[string]json.RawMessage{"Path": path})
	if err != nil || ed2kClaim(claim) != value || claim.PickCode != "" || claim.FileID != "" || claim.Code != "" {
		t.Fatalf("资源声明没有独立解析：%+v %v", claim, err)
	}
	for _, bad := range []string{raw + "?", raw + "?offline=1", raw + "#x", strings.Replace(raw, value.Hash, strings.Repeat("g", 32), 1)} {
		if _, ok := parsePickCodeSTRMClaim(bad); ok {
			t.Errorf("接收了无效 ED2K 声明 %q", bad)
		}
	}
}

type ed2kPinnedSourceClient struct {
	service.ED2KSTRMClient
	t                      *testing.T
	claim                  strm.ED2K
	searches, reads, links int
}

func (f *ed2kPinnedSourceClient) checkCookie(cookie string) {
	f.t.Helper()
	if cookie != "UID=123_A; SEID=private" {
		f.t.Fatal("ED2K 来源在请求中途改用了新凭据")
	}
}

func (f *ed2kPinnedSourceClient) FindPlaybackFileBySHA1Quick(_ context.Context, hash, cookie string) (*p115.DrivePlaybackFile, error) {
	f.checkCookie(cookie)
	f.searches++
	if hash != f.claim.SHA1 {
		f.t.Fatal("来源内容声明变化")
	}
	return &p115.DrivePlaybackFile{ID: "123", PickCode: "source123", SHA1: f.claim.SHA1, Size: f.claim.Size, Name: f.claim.Name}, nil
}

func (f *ed2kPinnedSourceClient) PickCodeInstantInfo(_ context.Context, pc, cookie string) (*p115.DownloadInfo, error) {
	f.checkCookie(cookie)
	f.reads++
	if pc != "source123" {
		f.t.Fatal("来源文件变化")
	}
	return &p115.DownloadInfo{FileID: "123", SHA1: f.claim.SHA1, Size: f.claim.Size, Name: f.claim.Name, URL: "https://cdn.115.com/pinned-source"}, nil
}

func (f *ed2kPinnedSourceClient) VerifiedPlaybackURL(_ context.Context, file p115.DrivePlaybackFile, cookie, ua string) (string, error) {
	f.checkCookie(cookie)
	f.links++
	if file.ID != "123" || ua != "player-UA" {
		f.t.Fatal("来源文件或播放器 UA 变化")
	}
	return "https://cdn.115.com/pinned-source", nil
}

type ed2kChallengeCacheFake struct {
	*proxyCacheFake
	challenges int
}

func (f *ed2kChallengeCacheFake) UploadPlayback(ctx context.Context, info *p115.DownloadInfo, scope, cid, cookie string, ticket *p115.PlaybackUploadTicket, progress func(p115.InstantProgress)) (*p115.InstantUploadResult, error) {
	f.challenges++
	if _, err := info.ResolveRangeSHA1(ctx, 1, 3); err != nil {
		return nil, err
	}
	return f.proxyCacheFake.UploadPlayback(ctx, info, scope, cid, cookie, ticket, progress)
}

func (f *ed2kChallengeCacheFake) PlaybackPickCodeSource(context.Context, string, string, string) (*p115.DownloadInfo, error) {
	f.t.Fatal("没有已知副本时访问了其他账号来源")
	return nil, p115.ErrDriveFileMissing
}

func (f *ed2kChallengeCacheFake) PlaybackRangeSHA1(_ context.Context, info *p115.DownloadInfo, _, _ int64) (string, error) {
	if info.URL != "https://cdn.115.com/pinned-source" {
		f.t.Fatal("二次校验未沿用固定主来源")
	}
	return strings.Repeat("C", 40), nil
}

func TestED2KEmbyPinsSourceCredentialsBeforeAuthorizationIO(t *testing.T) {
	for _, mode := range []string{"primary", "target-challenge"} {
		t.Run(mode, func(t *testing.T) {
			const userID = "8be403a10974486c94a987b8036c8484"
			accounts, sourceID := proxyTestAccounts(t)
			claim := strm.ED2K{Hash: strings.Repeat("a", 32), SHA1: strings.Repeat("A", 40), Size: 100, Name: "movie.mkv"}
			raw, err := strm.ED2KURL("https://old-host.invalid", claim)
			if err != nil {
				t.Fatal(err)
			}
			updated := false
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/emby/Users/AuthenticateByName":
					fmt.Fprintf(w, `{"AccessToken":"viewer","User":{"Id":"%s"},"SessionInfo":{"UserId":"%s"}}`, userID, userID)
				case "/emby/Users/" + userID:
					fmt.Fprintf(w, `{"Id":"%s","Policy":{"EnableMediaPlayback":true}}`, userID)
				case "/emby/Users/" + userID + "/Items/movie":
					if !updated {
						cookie := "UID=123_A; SEID=changed"
						if err := accounts.Update(sourceID, 2, "播放账号", &cookie); err != nil {
							t.Error(err)
							w.WriteHeader(500)
							return
						}
						updated = true
					}
					fmt.Fprintf(w, `{"Id":"movie","PlayAccess":"Full","MediaSources":[{"Id":"src","Path":%q}]}`, raw)
				default:
					w.WriteHeader(403)
				}
			}))
			defer upstream.Close()
			logs := service.NewEmbyProxyLogs(&service.GenerationLogs{})
			client := &ed2kPinnedSourceClient{t: t, claim: claim}
			share := NewShareAPI(func() string { return "" })
			share.ED2K = &service.ED2KSTRMService{Accounts: accounts, Client: client}
			var cache *service.PlaybackCache
			var target *ed2kChallengeCacheFake
			if mode == "target-challenge" {
				targetID, err := accounts.Add("friend")
				if err != nil {
					t.Fatal(err)
				}
				cookie := "UID=456_B; SEID=friend-private"
				if err := accounts.Update(targetID, 1, "friend", &cookie); err != nil {
					t.Fatal(err)
				}
				if err := accounts.UpdatePlaybackDirectory(targetID, 2, "0"); err != nil {
					t.Fatal(err)
				}
				store, err := service.NewSessionStore(filepath.Join(t.TempDir(), "cache.enc"), "secret")
				if err != nil {
					t.Fatal(err)
				}
				target = &ed2kChallengeCacheFake{proxyCacheFake: &proxyCacheFake{t: t}}
				cache, err = service.NewPlaybackCache(context.Background(), store, accounts, target, logs)
				if err != nil {
					t.Fatal(err)
				}
				defer cache.Close()
				if err := cache.ApplyBindings(upstream.URL, []service.PlaybackBinding{{UserID: userID, AccountID: targetID}}); err != nil {
					t.Fatal(err)
				}
			}
			h := NewEmbyCachedProxyHandler(service.EmbyProxyConfig{URL: upstream.URL, AccountID: sourceID}, accounts, share, cache, logs).(*embyProxyHandler)
			defer h.Close()
			call := func(method, path string) *httptest.ResponseRecorder {
				r := httptest.NewRequest(method, path, nil)
				r.Header.Set("User-Agent", "player-UA")
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				return w
			}
			if w := call("POST", "/emby/Users/AuthenticateByName"); w.Code != 200 {
				t.Fatal(w.Code)
			}
			w := call("GET", "/cinema115/play?ItemId=movie&MediaSourceId=src&api_key=viewer")
			if w.Code != 302 || !updated {
				t.Fatal("固定来源播放失败", w.Code, w.Body.String())
			}
			if client.searches != 1 {
				t.Fatal("固定主来源未查询")
			}
			if mode == "primary" {
				if client.links != 1 || client.reads != 0 || w.Header().Get("Location") != "https://cdn.115.com/pinned-source" {
					t.Fatal("主账号取链路径错误")
				}
			} else if client.links != 0 || client.reads != 1 || target.challenges != 1 || target.uploads != 1 || w.Header().Get("Location") != "https://cdn.115.com/friend?private-link" {
				t.Fatal("小号二次校验路径错误")
			}
		})
	}
}
