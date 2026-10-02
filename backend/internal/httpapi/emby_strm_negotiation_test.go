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

func TestSTRMNegotiationNeverOpensRetiredTool(t *testing.T) {
	for _, source := range []string{
		"http://retired.invalid:8115/api/redirect115/abc123/" + strings.Repeat("a", 40) + "/movie.mkv",
		"http://retired.invalid:12366" + play115TestPath + "movie.mkv",
		"http://retired.invalid/s/share_code_123.mkv",
	} {
		t.Run(source, func(t *testing.T) {
			var probes atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					w.WriteHeader(400)
					return
				}
				if r.URL.Query().Get("IsPlayback") != "false" || r.URL.Query().Get("AutoOpenLiveStream") != "false" || string(body["IsPlayback"]) != "false" || string(body["AutoOpenLiveStream"]) != "false" {
					probes.Add(1)
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
			if w.Code != 200 || !strings.Contains(w.Body.String(), "/cinema115/play?") || !strings.Contains(w.Body.String(), `"PlaySessionId":"session"`) || probes.Load() != 0 {
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
				call := calls.Add(1)
				data, _ := io.ReadAll(r.Body)
				if call == 2 && (r.URL.Query().Get("IsPlayback") != "true" || !strings.Contains(string(data), `"IsPlayback":true`) || !strings.Contains(string(data), `"EnableTranscoding":false`)) {
					t.Error("fallback lost original request or enabled transcoding")
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
			wantCalls := int32(2)
			if status == 403 {
				wantCalls = 1
			}
			if w.Code != status || calls.Load() != wantCalls {
				t.Fatalf("status=%d calls=%d", w.Code, calls.Load())
			}
		})
	}
}
