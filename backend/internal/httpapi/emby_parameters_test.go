package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"strm115/backend/internal/service"
)

func TestPlaybackParametersEqualDuplicatesAndConflicts(t *testing.T) {
	for _, tc := range []struct {
		query, want string
		reject      bool
	}{
		{"MediaSourceId=one&mediasourceid=one", "one", false},
		{"MediaSourceId=one&MediaSourceId=one", "one", false},
		{"%4DediaSourceId=o%6Ee&MEDIASOURCEID=one", "one", false},
		{"MediaSourceId=&mediasourceid=", "", false},
		{"MediaSourceId=one&mediasourceid=two", "", true},
		{"MediaSourceId=one&MediaSourceId=ONE", "", true},
		{"MediaSourceId=&mediasourceid=one", "", true},
		{"MediaSourceId=%zz", "", true},
	} {
		t.Run(tc.query, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/cinema115/play?"+tc.query+"&Tag=a&Tag=b", nil)
			err := normalizePlaybackQuery(r)
			if (err != nil) != tc.reject {
				t.Fatalf("rejection=%v", err)
			}
			if tc.reject {
				return
			}
			q := r.URL.Query()
			if q.Get("MediaSourceId") != tc.want || len(q["MediaSourceId"]) != 1 || len(q["Tag"]) != 2 {
				t.Fatal("canonical value or unrelated values changed")
			}
			if len(q) != 2 {
				t.Fatal("duplicate case variant retained")
			}
		})
	}
}

func TestPlaybackBodyDuplicateIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, body, query string
		reject            bool
	}{
		{"equal", `{"MediaSourceId":"one","mediasourceid":"one","MediaSourceId":"one","Streams":[1,2]}`, "MediaSourceId=one", false},
		{"different", `{"MediaSourceId":"one","mediasourceid":"two"}`, "", true},
		{"exact-key", `{"PlaySessionId":"one","PlaySessionId":"two"}`, "", true},
		{"body-query", `{"MediaSourceId":"two"}`, "MediaSourceId=one", true},
		{"null-optional", `{"MediaSourceId":null}`, "MediaSourceId=one", false},
		{"duplicate-null", `{"MediaSourceId":null,"mediasourceid":null}`, "", false},
		{"null-conflict", `{"MediaSourceId":null,"mediasourceid":"one"}`, "", true},
		{"wrong-type", `{"ItemId":123}`, "", true},
		{"trailing", `{} {}`, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/Items/movie/PlaybackInfo?"+tc.query, strings.NewReader(tc.body))
			err := disablePlaybackTranscoding(r)
			if (err != nil) != tc.reject {
				t.Fatalf("rejection=%v", err)
			}
			if tc.reject {
				return
			}
			defer r.Body.Close()
			var body map[string]json.RawMessage
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if string(body["EnableTranscoding"]) != "false" {
				t.Fatal("transcoding enabled")
			}
			if _, ok := body["mediasourceid"]; ok {
				t.Fatal("case duplicate forwarded")
			}
			if tc.name == "equal" && (string(body["Streams"]) != "[1,2]" || jsonString(body["MediaSourceId"]) != "one") {
				t.Fatal("selection or streams changed")
			}
		})
	}
}

// Loopback canary exercises the actual reverse proxy boundary, including event
// replay and rejection before any request can reach Emby or a cloud resolver.
func TestPlaybackParameterCanaryHTTP(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method == "POST" {
			data, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
			}
			w.Write(data)
			return
		}
		w.WriteHeader(204)
	}))
	defer upstream.Close()
	accounts, id := proxyTestAccounts(t)
	h := NewEmbyProxyHandler(service.EmbyProxyConfig{URL: upstream.URL, AccountID: id}, accounts, NewShareAPI(func() string { return "" })).(*embyProxyHandler)
	defer h.Close()
	server := httptest.NewServer(h)
	defer server.Close()
	for _, tc := range []struct {
		path, body string
		want       int
	}{
		{"/Sessions/Playing/Stopped", `{"ItemId":"movie","PlaySessionId":"same","playsessionid":"same"}`, 200},
		{"/Sessions/Playing/Progress", "", 200},
		{"/Sessions/Playing/Progress", `{"ItemId":"movie","itemid":"other"}`, 400},
		{"/Sessions/Playing/Stopped", `{"PlaySessionId":"old","PlaySessionId":"new"}`, 400},
		{"/emby/Sessions/Playing/Ping?PlaySessionId=one&playsessionid=two", "", 400},
		{"/Items/movie/PlaybackInfo", `{"MediaSourceId":"one","mediasourceid":"two"}`, 400},
		{"/cinema115/play?ItemId=movie&MediaSourceId=one&mediasourceid=two", "", 400},
		{"/Sessions/Playing?api_key=secret-a&API_KEY=secret-b", "", 400},
	} {
		before := calls.Load()
		resp, err := server.Client().Post(server.URL+tc.path, "application/json", strings.NewReader(tc.body))
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || resp.StatusCode != tc.want {
			t.Fatalf("%s: status=%d err=%v", tc.path, resp.StatusCode, err)
		}
		if tc.want == 400 && calls.Load() != before {
			t.Fatal("conflict reached upstream")
		}
		if tc.want == 200 && string(body) != tc.body {
			t.Fatal("event body changed")
		}
		if strings.Contains(string(body), "secret-") {
			t.Fatal("token leaked in error")
		}
	}
}
