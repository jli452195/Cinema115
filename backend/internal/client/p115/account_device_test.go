package p115

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCurrentLoginClientUsesOnlyUniqueCurrentDevice(t *testing.T) {
	for _, tc := range []struct{ name, list, want string }{
		{"current", `[{"icon":"ios","is_current":0},{"icon":"115ipad","is_current":1}]`, "115ipad"},
		{"boolean", `[{"icon":"wechatmini","is_current":true}]`, "wechatmini"},
		{"string", `[{"icon":"web","is_current":"1"}]`, "web"},
		{"none", `[{"icon":"115ipad","is_current":0}]`, ""},
		{"ambiguous", `[{"icon":"ios","is_current":1},{"icon":"115ipad","is_current":1}]`, ""},
		{"unknown", `[{"icon":"unrecognized","is_current":1}]`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Cookie") != "UID=123_A; SEID=test" {
					t.Error("wrong credential snapshot")
				}
				fmt.Fprintf(w, `{"state":true,"data":{"list":%s}}`, tc.list)
			}))
			defer server.Close()
			client := NewAccountClient()
			client.DevicesURL = server.URL
			got, err := client.CurrentLoginClient(context.Background(), "UID=123_A; SEID=test")
			if err != nil || got != tc.want {
				t.Fatalf("got %q, err %v", got, err)
			}
		})
	}
}
