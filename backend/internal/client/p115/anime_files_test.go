package p115

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAnimeFilesStrictListingMetadata(t *testing.T) {
	for _, tc := range []struct {
		name, entry, id, kind string
		valid                 bool
	}{
		{"directory", `{"aid":"1","cid":"300","pid":"200","n":"resource"}`, "300", "directory", true},
		{"root parent", `{"cid":"300","pid":"0","n":"resource"}`, "300", "directory", true},
		{"file", `{"aid":"1","fid":"9007199254740993","cid":"200","n":"01.mkv","s":1234,"sha":"` + strings.Repeat("A", 40) + `"}`, "9007199254740993", "file", true},
		{"long aliases", `{"file_id":300,"category_id":200,"file_name":"01.mkv","file_size":"1234","sha1":""}`, "300", "file", true},
		{"zero fid directory", `{"fid":"0","cid":"300","pid":"200","n":"resource"}`, "300", "directory", true},
		{"zero byte attachment", `{"fid":"300","cid":"200","n":"readme.txt","s":0}`, "300", "file", true},
		{"deleted", `{"aid":120,"fid":300,"cid":200,"n":"01.mkv","s":10}`, "300", "", false},
		{"unknown area", `{"aid":99,"fid":300,"cid":200,"n":"01.mkv","s":10}`, "300", "", false},
		{"duplicate", `{"fid":300,"fid":301,"cid":200,"n":"01.mkv","s":10}`, "300", "", false},
		{"id aliases", `{"fid":300,"file_id":301,"cid":200,"n":"01.mkv","s":10}`, "300", "", false},
		{"name aliases", `{"fid":300,"cid":200,"n":"01.mkv","file_name":"02.mkv","s":10}`, "300", "", false},
		{"size aliases", `{"fid":300,"cid":200,"n":"01.mkv","s":10,"file_size":11}`, "300", "", false},
		{"hash aliases", `{"fid":300,"cid":200,"n":"01.mkv","s":10,"sha":"` + strings.Repeat("a", 40) + `","sha1":"` + strings.Repeat("b", 40) + `"}`, "300", "file", true},
		{"malformed hash", `{"fid":300,"cid":200,"n":"01.mkv","s":10,"sha":"invalid"}`, "300", "file", true},
		{"no size", `{"fid":300,"cid":200,"n":"01.mkv"}`, "300", "", false},
		{"null size", `{"fid":300,"cid":200,"n":"01.mkv","s":null}`, "300", "", false},
		{"negative size", `{"fid":300,"cid":200,"n":"01.mkv","s":-1}`, "300", "", false},
		{"no parent", `{"fid":300,"n":"01.mkv","s":10}`, "300", "", false},
		{"unsafe name", `{"fid":300,"cid":200,"n":"../01.mkv","s":10}`, "300", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseAnimeDriveEntry([]byte(tc.entry))
			if (err == nil) != tc.valid || tc.valid && (got.ID != tc.id || got.Kind != tc.kind) {
				t.Fatal(got, err)
			}
		})
	}
}

func TestAnimeFilesScopedPagesRequireCompleteFacts(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		valid      bool
	}{
		{"one file", `{"state":true,"cid":200,"offset":0,"count":1,"data":[{"fid":300,"cid":200,"n":"01.mkv","s":10}]}`, true},
		{"empty", `{"state":true,"cid":200,"offset":0,"count":0,"data":[]}`, true},
		{"no data", `{"state":true,"cid":200,"offset":0,"count":0}`, false},
		{"null data", `{"state":true,"cid":200,"offset":0,"count":0,"data":null}`, false},
		{"missing count", `{"state":true,"cid":200,"offset":0,"data":[]}`, false},
		{"missing offset", `{"state":true,"cid":200,"count":0,"data":[]}`, false},
		{"redirect cid", `{"state":true,"cid":201,"offset":0,"count":0,"data":[]}`, false},
		{"redirect path", `{"state":true,"cid":200,"path":[{"cid":201}],"offset":0,"count":0,"data":[]}`, false},
		{"wrong parent", `{"state":true,"cid":200,"offset":0,"count":1,"data":[{"fid":300,"cid":201,"n":"01.mkv","s":10}]}`, false},
		{"gap", `{"state":true,"cid":200,"offset":0,"count":1,"data":[]}`, false},
		{"excess count", `{"state":true,"cid":200,"offset":0,"count":4097,"data":[]}`, false},
		{"excess rows", `{"state":true,"cid":200,"offset":0,"count":0,"data":[{"fid":300,"cid":200,"n":"01.mkv","s":10}]}`, false},
		{"duplicate id", `{"state":true,"cid":200,"offset":0,"count":2,"data":[{"fid":300,"cid":200,"n":"01.mkv","s":10},{"fid":300,"cid":200,"n":"02.mkv","s":10}]}`, false},
		{"wrong account", `{"state":true,"uid":456,"cid":200,"offset":0,"count":0,"data":[]}`, false},
		{"rejected", `{"state":false,"cid":200,"offset":0,"count":0,"data":[]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.URL.Path != "/files" || r.URL.Query().Get("cid") != "200" || r.URL.Query().Get("limit") != "100" || r.Method != "GET" || r.Header.Get("Cookie") != "UID=123_A" {
					t.Error("unscoped page request")
				}
				fmt.Fprint(w, tc.body)
			}))
			defer server.Close()
			client := NewShareClient()
			client.HTTP = server.Client()
			client.WebURL = server.URL
			client.RequestInterval = 0
			got, err := client.ReadAnimeDrivePage(context.Background(), "200", 0, "UID=123_A")
			if (err == nil) != tc.valid || requests != 1 {
				t.Fatal(got, err, requests)
			}
		})
	}
}

func TestAnimeFilesPageRejectsMalformedEnvelopes(t *testing.T) {
	for _, body := range []string{
		`{"state":true,"state":false,"cid":200,"offset":0,"count":0,"data":[]}`,
		`{"state":true,"cid":200,"offset":0,"count":0,"data":[],"data":[]}`,
		`{"state":true,"cid":200,"offset":0,"count":0,"data":[]} {}`,
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
		client := NewShareClient()
		client.HTTP = server.Client()
		client.WebURL = server.URL
		client.RequestInterval = 0
		if got, err := client.ReadAnimeDrivePage(context.Background(), "200", 0, "UID=123_A"); err == nil {
			t.Fatal(got)
		}
		server.Close()
	}
}
