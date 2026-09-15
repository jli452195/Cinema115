package p115

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSharePageExposesValidatedOwnerID(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{"42", "42"},
		{`"42"`, "42"},
		{"9007199254740993", "9007199254740993"},
		{`"9007199254740993"`, "9007199254740993"},
		{"null", ""}, {"0", ""}, {"-1", ""}, {"42.1", ""}, {`"invalid"`, ""},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprintf(w, `{"state":true,"data":{"userinfo":{"user_id":%s},"count":0,"list":[]}}`, tc.raw)
			}))
			defer server.Close()
			client := newTestShareClient()
			client.WebURL = server.URL
			page, err := client.ListForTransfer(context.Background(), &ShareInfo{Code: "source"}, "0", 0, "UID=42_A")
			if err != nil || page.OwnerID != tc.want {
				t.Fatal("分享者编号解析错误", err)
			}
		})
	}
}
