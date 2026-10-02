package httpapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"strm115/backend/internal/service"
)

func TestSTRMNegotiationPreservesClientPlaybackIntent(t *testing.T) {
	for _, source := range []string{
		"http://retired.invalid:8115/api/redirect115/abc123/" + strings.Repeat("a", 40) + "/movie.mkv",
		"http://retired.invalid:12366" + play115TestPath + "movie.mkv",
		"http://retired.invalid:8115/api/redirect115/d6ch24icg2307x29g/84a23f57f6540139a0fab14eaa73c3cfb86a21e3/[ばにぃうぉ~か~] OVA催眠性指導 #1 小幡優衣の場合 [GLOD-0097].mp4",
		"http://retired.invalid:12366" + play115TestPath + "episode #1.mkv",
		"http://retired.invalid/s/share_code_123.mkv",
	} {
		t.Run(source, func(t *testing.T) {
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				var body map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					w.WriteHeader(400)
					return
				}
				if r.URL.Query().Get("IsPlayback") != "true" || r.URL.Query().Get("AutoOpenLiveStream") != "true" || string(body["isplayback"]) != "true" || string(body["AUTOOPENLIVESTREAM"]) != "true" || r.URL.Query().Get("EnableTranscoding") != "false" {
					t.Error("client playback intent changed or transcoding allowed")
					w.WriteHeader(500)
					return
				}
				if r.Header.Get("X-Emby-Token") != "viewer" || jsonString(body["MediaSourceId"]) != "src" || string(body["DeviceProfile"]) != `{"Name":"Yamby"}` || string(body["EnableTranscoding"]) != "false" {
					t.Error("lost credentials, selection or device capabilities")
				}
				fmt.Fprintf(w, `{"MediaSources":[{"Id":"src","SupportsDirectPlay":true,"Path":%q}],"PlaySessionId":"session"}`, source)
			}))
			defer upstream.Close()
			accounts, id := proxyTestAccounts(t)
			h := NewEmbyProxyHandler(service.EmbyProxyConfig{URL: upstream.URL, AccountID: id}, accounts, NewShareAPI(func() string { return "" })).(*embyProxyHandler)
			defer h.Close()
			r := httptest.NewRequest("POST", "/Items/movie/PlaybackInfo?IsPlayback=true&AutoOpenLiveStream=true", strings.NewReader(`{"isplayback":true,"AUTOOPENLIVESTREAM":true,"EnableTranscoding":true,"MediaSourceId":"src","DeviceProfile":{"Name":"Yamby"}}`))
			r.Header.Set("X-Emby-Token", "viewer")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != 200 || !strings.Contains(w.Body.String(), "/cinema115/play?") || !strings.Contains(w.Body.String(), `"PlaySessionId":"session"`) || calls.Load() != 1 {
				t.Fatalf("negotiation failed: %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestSTRMNegotiationKeepsOrdinaryMediaAndDenials(t *testing.T) {
	for _, status := range []int{200, 403} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				data, _ := io.ReadAll(r.Body)
				if r.URL.Query().Get("IsPlayback") != "true" || !strings.Contains(string(data), `"IsPlayback":true`) || !strings.Contains(string(data), `"EnableTranscoding":false`) {
					t.Error("lost original request or enabled transcoding")
				}
				w.WriteHeader(status)
				fmt.Fprint(w, `{"MediaSources":[{"Id":"src","SupportsDirectPlay":true,"Path":"/media/local.mkv"}]}`)
			}))
			defer upstream.Close()
			accounts, id := proxyTestAccounts(t)
			h := NewEmbyProxyHandler(service.EmbyProxyConfig{URL: upstream.URL, AccountID: id}, accounts, NewShareAPI(func() string { return "" })).(*embyProxyHandler)
			defer h.Close()
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("POST", "/Items/movie/PlaybackInfo?IsPlayback=true", strings.NewReader(`{"IsPlayback":true}`)))
			wantCalls := int32(1)
			if w.Code != status || calls.Load() != wantCalls {
				t.Fatalf("status=%d calls=%d", w.Code, calls.Load())
			}
		})
	}
}

func TestClientPlaybackInfoKeepsFalseAndAbsentFlags(t *testing.T) {
	for _, method := range []string{"GET", "POST"} {
		for _, flag := range []string{"", "false", "true"} {
			t.Run(method+"/"+flag, func(t *testing.T) {
				var calls atomic.Int32
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					if r.URL.Query().Get("IsPlayback") != flag || r.URL.Query().Get("AutoOpenLiveStream") != flag || r.URL.Query().Get("EnableTranscoding") != "false" {
						t.Error("query flags changed")
					}
					if method == "POST" {
						var body map[string]json.RawMessage
						if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
							t.Error(err)
						}
						if string(body["IsPlayback"]) != flag || string(body["AutoOpenLiveStream"]) != flag || string(body["EnableTranscoding"]) != "false" {
							t.Error("body flags changed")
						}
					}
					fmt.Fprint(w, `{"MediaSources":[],"ErrorCode":"NoCompatibleStream"}`)
				}))
				defer upstream.Close()
				accounts, id := proxyTestAccounts(t)
				h := NewEmbyProxyHandler(service.EmbyProxyConfig{URL: upstream.URL, AccountID: id}, accounts, NewShareAPI(func() string { return "" })).(*embyProxyHandler)
				defer h.Close()
				path, body := "/Items/movie/PlaybackInfo?EnableTranscoding=true", `{"EnableTranscoding":true}`
				if flag != "" {
					path += "&IsPlayback=" + flag + "&AutoOpenLiveStream=" + flag
					body = `{"EnableTranscoding":true,"IsPlayback":` + flag + `,"AutoOpenLiveStream":` + flag + `}`
				}
				r := httptest.NewRequest(method, path, nil)
				if method == "POST" {
					r = httptest.NewRequest(method, path, strings.NewReader(body))
				}
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				if w.Code != 200 || calls.Load() != 1 || !strings.Contains(w.Body.String(), "NoCompatibleStream") {
					t.Fatal("negotiation duplicated or denial lost", w.Code)
				}
			})
		}
	}
}
