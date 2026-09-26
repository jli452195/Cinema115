package httpapi

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"strm115/backend/internal/service"
)

func TestEmbyLoginIdentityAndPerRequestRevalidation(t *testing.T) {
	accounts, account := proxyTestAccounts(t)
	status, policy, returnedID := 200, `{"EnableMediaPlayback":true}`, "owner"
	userCalls, itemCalls := 0, 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/emby/Users/AuthenticateByName" {
			if r.Header.Get("Accept-Encoding") != "" {
				t.Error("login compression was not removed")
			}
			fmt.Fprint(w, `{"AccessToken":"player-secret","User":{"Id":"owner"},"SessionInfo":{"UserId":"owner"}}`)
			return
		}
		if r.Header.Get("X-Emby-Token") != "player-secret" {
			t.Error("wrong credential")
		}
		switch r.URL.Path {
		case "/emby/Users/owner":
			userCalls++
			w.WriteHeader(status)
			if status == 200 {
				fmt.Fprintf(w, `{"Id":%q,"Policy":%s}`, returnedID, policy)
			} else {
				fmt.Fprint(w, "private-error api_key=player-secret")
			}
		case "/emby/Users/owner/Items/movie":
			itemCalls++
			w.WriteHeader(403)
		default:
			t.Error("unexpected endpoint", r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer upstream.Close()
	logs := service.NewEmbyProxyLogs(&service.GenerationLogs{})
	h := NewEmbyProxyHandler(service.EmbyProxyConfig{URL: upstream.URL, AccountID: account}, accounts, NewShareAPI(func() string { return "" }), logs).(*embyProxyHandler)
	// The default transport may add gzip on the wire, but the proxy removes the client value.
	h.http.Transport.(*http.Transport).DisableCompression = true
	call := func(method, path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(method, path, nil))
		return w
	}
	play := "/cinema115/play?ItemId=movie&api_key=player-secret&UserId=friend&DeviceId=friend-device&SessionId=friend-session"
	if w := call("GET", play); w.Code != 401 || userCalls != 0 {
		t.Fatal("unknown token trusted", w.Code)
	}
	if w := call("POST", "/emby/Users/AuthenticateByName"); w.Code != 200 || !strings.Contains(w.Body.String(), "player-secret") {
		t.Fatal("login response changed")
	}
	if w := call("GET", play); w.Code != 403 || userCalls != 1 || itemCalls != 1 {
		t.Fatal("identity or item authorization wrong", w.Code)
	}
	for _, code := range []int{401, 403, 404, 500, 503} {
		status = code
		w := call("GET", play)
		want := code
		if code >= 500 {
			want = 502
		}
		if w.Code != want || !strings.Contains(w.Body.String(), fmt.Sprintf("上游 HTTP %d", code)) || !strings.Contains(w.Body.String(), "/Users/{Id}") {
			t.Fatal("missing diagnostic", w.Code, w.Body.String())
		}
	}
	status = 200
	for _, p := range []string{`{"IsDisabled":true,"EnableMediaPlayback":true}`, `{"EnableMediaPlayback":false}`, `{}`} {
		policy = p
		if w := call("GET", play); w.Code != 403 {
			t.Fatal("policy bypass", w.Code)
		}
	}
	policy, returnedID = `{"EnableMediaPlayback":true}`, "friend"
	if w := call("GET", play); w.Code != 403 {
		t.Fatal("identity mismatch accepted", w.Code)
	}
	if itemCalls != 1 {
		t.Fatal("failed validation reached item lookup")
	}
	for _, entry := range logs.Snapshot() {
		for _, secret := range []string{"player-secret", "private-error", "friend-device", "friend-session"} {
			if strings.Contains(entry.Message, secret) {
				t.Fatal("secret in logs")
			}
		}
	}
}

func TestEmbyIdentityOnlyFromSuccessfulLogin(t *testing.T) {
	for _, tc := range []struct {
		name, method, path, body string
		status                   int
		valid                    bool
	}{
		{"name login", "POST", "/Users/AuthenticateByName", `{"AccessToken":"token","User":{"Id":"owner"}}`, 200, true},
		{"id login", "POST", "/emby/Users/owner/Authenticate", `{"AccessToken":"token","User":{"Id":"owner"}}`, 200, true},
		{"get", "GET", "/Users/AuthenticateByName", `{"AccessToken":"token","User":{"Id":"owner"}}`, 200, false},
		{"failed login", "POST", "/Users/AuthenticateByName", `{"AccessToken":"token","User":{"Id":"owner"}}`, 401, false},
		{"other endpoint", "POST", "/Sessions", `{"AccessToken":"token","User":{"Id":"owner"}}`, 200, false},
		{"mismatch", "POST", "/Users/AuthenticateByName", `{"AccessToken":"token","User":{"Id":"owner"},"SessionInfo":{"UserId":"other"}}`, 200, false},
		{"missing user", "POST", "/Users/AuthenticateByName", `{"AccessToken":"token"}`, 200, false},
		{"malformed", "POST", "/Users/AuthenticateByName", `{`, 200, false},
		{"oversized", "POST", "/Users/AuthenticateByName", strings.Repeat("x", (1<<20)+100), 200, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); fmt.Fprint(w, tc.body) }))
			defer upstream.Close()
			h := NewEmbyProxyHandler(service.EmbyProxyConfig{URL: upstream.URL}, nil, NewShareAPI(func() string { return "" })).(*embyProxyHandler)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
			if w.Code != tc.status || w.Body.String() != tc.body {
				t.Fatal("response changed")
			}
			if got := h.identities.UserID(upstream.URL, "token"); (got == "owner") != tc.valid {
				t.Fatal("untrusted identity", got)
			}
		})
	}
}
