package p115

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReadDirectoryUsesExactBreadcrumbAndOneRow(t *testing.T) {
	for _, tc := range []struct {
		name, cid, body, path string
		valid                 bool
	}{
		{"large-id", "3516637914517734609", `{"state":true,"cid":"3516637914517734609","path":[{"cid":"0","name":"根目录"},{"cid":"3516637914517734609","name":"动漫"}]}`, "/动漫", true},
		{"root", "0", `{"state":true,"cid":"0","path":[]}`, "/", true},
		{"redirect", "123", `{"state":true,"cid":"0","path":[{"cid":"0"}]}`, "", false},
		{"missing", "123", `{"state":true,"count":0,"data":[]}`, "", false},
		{"wrong-path", "123", `{"state":true,"cid":"123","path":[{"cid":"124","name":"other"}]}`, "", false},
		{"cycle", "123", `{"state":true,"cid":"123","path":[{"cid":"123","name":"a"},{"cid":"123","name":"b"}]}`, "", false},
		{"invalid-name", "123", `{"state":true,"cid":"123","path":[{"cid":"123","name":"a/b"}]}`, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "GET" || r.URL.Path != "/files" || r.URL.Query().Get("limit") != "1" || r.URL.Query().Get("cid") != tc.cid || r.Header.Get("Cookie") != "synthetic-cookie" {
					t.Error("unbounded or wrong request")
				}
				w.Write([]byte(tc.body))
			}))
			defer server.Close()
			c := newTestShareClient()
			c.WebURL = server.URL
			v, err := c.ReadDirectory(context.Background(), tc.cid, "synthetic-cookie")
			if (err == nil) != tc.valid || tc.valid && (v.CID != tc.cid || v.Path != tc.path) || calls != 1 {
				t.Fatal(v, calls, err)
			}
		})
	}
}
