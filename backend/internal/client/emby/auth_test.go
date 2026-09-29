package emby

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPortalAuthenticationUsesUserPasswordWithoutAdministratorKey(t *testing.T) {
	for _, invalid := range []bool{false, true} {
		t.Run(map[bool]string{false: "valid", true: "missing identity"}[invalid], func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/emby/Users/AuthenticateByName" || r.Method != "POST" || r.Header.Get("X-Emby-Token") != "" || r.URL.RawQuery != "" {
					t.Error("invalid authentication request")
				}
				if !strings.Contains(r.Header.Get("X-Emby-Authorization"), `Client="Cinema115"`) {
					t.Error("missing client identity")
				}
				var in map[string]string
				if json.NewDecoder(r.Body).Decode(&in) != nil || in["Username"] != "reader" || in["Pw"] != "private-password" {
					t.Error("invalid body")
				}
				if invalid {
					_, _ = w.Write([]byte(`{"User":{}}`))
					return
				}
				_, _ = w.Write([]byte(`{"ServerId":"server","AccessToken":"never-persist","User":{"Id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","Name":"reader","Policy":{"IsDisabled":false}}}`))
			}))
			defer server.Close()
			u, id, err := New(server.URL, "private-admin-key").AuthenticateUser(context.Background(), "reader", "private-password")
			if invalid {
				if err == nil {
					t.Fatal("invalid identity accepted")
				}
				return
			}
			if err != nil || id != "server" || u.Name != "reader" {
				t.Fatal(u, id, err)
			}
		})
	}
}
