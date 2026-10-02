package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

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
			_, claim := h.itemPlaybackSource(sources, tc.selected)
			if (claim != nil) != tc.ok {
				t.Fatal("unexpected eligibility")
			}
			if tc.ok {
				if _, claim := h.itemPlaybackSource(append(sources, sources[0]), tc.selected); claim != nil {
					t.Fatal("ambiguous source accepted")
				}
			}
		})
	}
}
