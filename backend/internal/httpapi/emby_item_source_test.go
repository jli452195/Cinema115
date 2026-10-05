package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/service"
)

func TestItemSourceSkipsRepeatedPlaybackInfoAndPreservesNegotiation(t *testing.T) {
	const sourceURL = "http://retired.invalid/api/redirect115/abc123/movie.mkv"
	const full = `{"PlaySessionId":"session","CustomSessionField":{"keep":true},"MediaSources":[{"Id":"src","Path":"` + sourceURL + `","Container":"mkv","SupportsDirectPlay":true,"SupportsDirectStream":true,"RunTimeTicks":123456,"DefaultAudioStreamIndex":1,"DefaultSubtitleStreamIndex":2,"MediaStreams":[{"Index":0,"Type":"Video","Codec":"hevc","Width":3840,"Height":2160,"VideoRange":"HDR","VideoRangeType":"DOVIWithHDR10","DvProfile":8},{"Index":1,"Type":"Audio","Codec":"eac3","Channels":6,"Language":"jpn"},{"Index":2,"Type":"Subtitle","Codec":"srt","IsExternal":true,"Language":"chi","DeliveryUrl":"/Videos/movie/src/Subtitles/2/Stream.srt"}],"UnknownMediaField":{"retain":123}}]}`
	infoCalls, itemCalls, linkCalls := 0, 0, 0
	denied := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Emby-Token") != "viewer" && !strings.HasSuffix(r.URL.Path, "AuthenticateByName") {
			t.Error("request did not use player credentials")
		}
		switch r.URL.Path {
		case "/Users/AuthenticateByName":
			fmt.Fprint(w, `{"AccessToken":"viewer","User":{"Id":"user"}}`)
		case "/emby/Users/user":
			fmt.Fprint(w, `{"Id":"user","Policy":{"EnableMediaPlayback":true}}`)
		case "/emby/Users/user/Items/movie":
			itemCalls++
			if r.URL.Query().Get("Fields") != "MediaSources" {
				t.Error("missing source projection")
			}
			if denied {
				w.WriteHeader(403)
				return
			}
			// Real STRM item responses can lack stream metadata. They must only
			// locate the file, never replace the player's complete negotiation.
			fmt.Fprintf(w, `{"Id":"movie","PlayAccess":"Full","MediaSources":[{"Id":"src","Path":%q,"Container":"strm","MediaStreams":[]}]}`, sourceURL)
		case "/Items/movie/PlaybackInfo", "/emby/Items/movie/PlaybackInfo":
			infoCalls++
			if infoCalls > 1 {
				t.Error("repeated PlaybackInfo reached blocked upstream")
				w.WriteHeader(504)
				return
			}
			fmt.Fprint(w, full)
		case "/Videos/movie/src/Subtitles/2/Stream.srt":
			fmt.Fprint(w, "subtitle")
		case "/Sessions/Playing/Progress":
			w.WriteHeader(204)
		default:
			w.WriteHeader(404)
		}
	}))
	defer upstream.Close()
	accounts, accountID := proxyTestAccounts(t)
	h := NewEmbyProxyHandler(service.EmbyProxyConfig{URL: upstream.URL, AccountID: accountID, DirectLinkCacheMinutes: 9}, accounts, NewShareAPI(func() string { return "" })).(*embyProxyHandler)
	defer h.Close()
	h.pickDownload = func(_ context.Context, pc, cookie, ua string) (string, error) {
		linkCalls++
		if pc != "abc123" || cookie != "UID=123_A; SEID=private" {
			t.Error("wrong file or credentials")
		}
		return "https://cdn.115.com/movie", nil
	}
	call := func(method, path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, nil)
		r.Header.Set("X-Emby-Token", "viewer")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := call("POST", "/Users/AuthenticateByName"); w.Code != 200 {
		t.Fatal(w.Code)
	}
	w := call("GET", "/Items/movie/PlaybackInfo")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var expected, actual map[string]any
	if err := json.Unmarshal([]byte(full), &expected); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(w.Body.Bytes(), &actual); err != nil {
		t.Fatal(err)
	}
	want := expected["MediaSources"].([]any)[0].(map[string]any)
	got := actual["MediaSources"].([]any)[0].(map[string]any)
	for _, key := range []string{"Container", "MediaStreams", "RunTimeTicks", "DefaultAudioStreamIndex", "DefaultSubtitleStreamIndex", "UnknownMediaField"} {
		if !reflect.DeepEqual(got[key], want[key]) {
			t.Errorf("negotiation field changed: %s", key)
		}
	}
	for _, key := range []string{"PlaySessionId", "CustomSessionField"} {
		if !reflect.DeepEqual(actual[key], expected[key]) {
			t.Errorf("session field changed: %s", key)
		}
	}
	entry := got["DirectStreamUrl"].(string)
	for _, method := range []string{"GET", "HEAD"} {
		if w := call(method, entry); w.Code != 302 || w.Header().Get("Location") != "https://cdn.115.com/movie" {
			t.Fatal("item source failed", w.Code, w.Body.String())
		}
	}
	if infoCalls != 1 || itemCalls != 2 || linkCalls != 1 {
		t.Fatalf("wrong request counts: PlaybackInfo=%d item=%d link=%d", infoCalls, itemCalls, linkCalls)
	}
	if w := call("GET", "/Videos/movie/src/Subtitles/2/Stream.srt"); w.Code != 200 || w.Body.String() != "subtitle" {
		t.Fatal("subtitle forwarding changed")
	}
	if w := call("POST", "/Sessions/Playing/Progress"); w.Code != 204 {
		t.Fatal("progress forwarding changed")
	}
	denied = true
	if w := call("GET", entry); w.Code != 403 || linkCalls != 1 || infoCalls != 1 {
		t.Fatal("cached source/link bypassed current item authorization", w.Code)
	}
}

func TestStaticShareFailureCanaryPreservesFallbackAndVersions(t *testing.T) {
	const good = "http://nas/api/share115/abc/AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA/pass/123"
	const bad = "http://nas/api/share115/abc/AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA/-/123"
	for _, tc := range []struct {
		name, path, extra, selected string
		status, info                int
	}{
		{"valid-equal-duplicates", good, "", "src&mediasourceid=src", 302, 0},
		{"malformed-static", bad, "", "src", 422, 0},
		{"malformed-legacy", "http://nas/api/?share_code=abc&id=123&receive_code=", "", "src", 422, 0},
		{"unknown", "https://other.invalid/movie", "", "src", 302, 1},
		{"dynamic", bad, `,"RequiresOpening":true`, "src", 302, 1},
		{"missing-metadata", "", "", "src", 302, 1},
		{"local-missing", "missing.strm", "", "src", 302, 1},
		{"local-malformed", "bad.strm", "", "src", 422, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "bad.strm"), []byte(bad), 0600); err != nil {
				t.Fatal(err)
			}
			path := tc.path
			if strings.HasSuffix(path, ".strm") {
				path = "/media/" + path
			}
			infoCalls, links := 0, 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/Users/AuthenticateByName":
					fmt.Fprint(w, `{"AccessToken":"viewer","User":{"Id":"user"}}`)
				case "/emby/Users/user":
					fmt.Fprint(w, `{"Id":"user","Policy":{"EnableMediaPlayback":true}}`)
				case "/emby/Users/user/Items/movie":
					fmt.Fprintf(w, `{"Id":"movie","PlayAccess":"Full","MediaSources":[{"Id":"src","Path":%q%s},{"Id":"other","Path":%q}]}`, path, tc.extra, bad)
				case "/emby/Items/movie/PlaybackInfo", "/Items/movie/PlaybackInfo":
					infoCalls++
					fmt.Fprintf(w, `{"PlaySessionId":"session","MediaSources":[{"Id":"src","Path":%q,"SupportsDirectPlay":true,"MediaStreams":[{"Index":1,"Type":"Audio"}]},{"Id":"other","Path":%q,"SupportsDirectPlay":true,"MediaStreams":[{"Index":2,"Type":"Audio"}]}]}`, good, good)
				default:
					w.WriteHeader(404)
				}
			}))
			defer upstream.Close()
			accounts, id := proxyTestAccounts(t)
			share := NewShareAPI(func() string { return "" })
			share.OutputRoot = root
			h := NewEmbyProxyHandler(service.EmbyProxyConfig{URL: upstream.URL, AccountID: id, EmbyPathPrefix: "/media"}, accounts, share).(*embyProxyHandler)
			defer h.Close()
			h.download = func(context.Context, *p115.ShareInfo, string, string, string) (string, error) {
				links++
				return "https://cdn.115.com/movie", nil
			}
			call := func(method, path string) *httptest.ResponseRecorder {
				r := httptest.NewRequest(method, path, nil)
				r.Header.Set("X-Emby-Token", "viewer")
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				return w
			}
			if w := call("POST", "/Users/AuthenticateByName"); w.Code != 200 {
				t.Fatal(w.Code)
			}
			w := call("GET", "/cinema115/play?ItemId=movie&MediaSourceId="+tc.selected)
			if w.Code != tc.status || infoCalls != tc.info {
				t.Fatalf("status=%d info=%d body=%s", w.Code, infoCalls, w.Body.String())
			}
			if tc.status == 422 && links != 0 {
				t.Fatal("malformed source reached cloud")
			}
			w = call("GET", "/Items/movie/PlaybackInfo")
			var info struct{ MediaSources []map[string]json.RawMessage }
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &info) != nil || len(info.MediaSources) != 2 {
				t.Fatal("media versions hidden")
			}
			for i, source := range info.MediaSources {
				if jsonString(source["DirectStreamUrl"]) == "" || !strings.Contains(string(source["MediaStreams"]), fmt.Sprint(i+1)) {
					t.Fatal("version lost selection or streams")
				}
			}
		})
	}
}

func TestItemPlaybackSourceRequiresUnambiguousStaticSTRM(t *testing.T) {
	h := &embyProxyHandler{share: NewShareAPI(func() string { return "" })}
	for _, tc := range []struct {
		name, fields, selected string
		ok                     bool
	}{
		{"minimal", ``, "src", true},
		{"implicit-single", ``, "", true},
		{"other-id", ``, "other", false},
		{"dynamic", `,"RequiresOpening":true`, "src", false},
		{"open-token", `,"OpenToken":"opaque"`, "src", false},
		{"live", `,"LiveStreamId":"live"`, "src", false},
		{"placeholder", `,"Type":"Placeholder"`, "src", false},
		{"incompatible", `,"SupportsDirectPlay":false`, "src", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var sources []map[string]json.RawMessage
			if err := json.Unmarshal([]byte(`[{"Id":"src","Path":"http://old/api/redirect115/abc123"`+tc.fields+`}]`), &sources); err != nil {
				t.Fatal(err)
			}
			_, claim, _ := h.itemPlaybackSource(sources, tc.selected)
			if (claim != nil) != tc.ok {
				t.Fatal("unexpected eligibility")
			}
			if tc.ok {
				if _, claim, _ := h.itemPlaybackSource(append(sources, sources[0]), tc.selected); claim != nil {
					t.Fatal("ambiguous source accepted")
				}
			}
		})
	}
}
