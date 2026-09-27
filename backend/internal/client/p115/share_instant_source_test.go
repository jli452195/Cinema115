package p115

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestInstantShareMetadataUsesExactFileAndPinnedCookie(t *testing.T) {
	for _, tc := range []struct {
		name, id, size, filename, sha string
		ok                            bool
	}{
		{"match", "123", "2000", "video.mkv", strings.Repeat("a", 40), true},
		{"same name wrong ID", "999", "2000", "video.mkv", strings.Repeat("a", 40), false},
		{"size changed", "123", "2001", "video.mkv", strings.Repeat("a", 40), false},
		{"name changed", "123", "2000", "other.mkv", strings.Repeat("a", 40), false},
		{"missing hash", "123", "2000", "video.mkv", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "GET" || r.Header.Get("Cookie") != "source-cookie" {
					t.Error("wrong request identity")
				}
				switch r.URL.Path {
				case "/os_windows/2.0/share/downurl":
					if r.UserAgent() != PlaybackUserAgent {
						t.Error("wrong source UA")
					}
					fmt.Fprint(w, `{"state":true,"data":{"fn":"video.mkv","fs":"2000","url":{"url":"https://cdn.115.com/video"}}}`)
				case "/share/search":
					q := r.URL.Query()
					if q.Get("share_code") != "share" || q.Get("receive_code") != "password" || q.Get("search_value") != "video.mkv" {
						t.Error("search escaped original share")
					}
					fmt.Fprintf(w, `{"state":true,"data":{"count":1,"list":[{"fid":%q,"n":%q,"s":%q,"sha":%q}]}}`, tc.id, tc.filename, tc.size, tc.sha)
				default:
					t.Error("unexpected API")
					w.WriteHeader(500)
				}
			}))
			defer server.Close()
			client := NewShareClient()
			client.AppURL = server.URL
			client.WebURL = server.URL
			info, err := client.DownloadInstantInfoWithUA(context.Background(), &ShareInfo{Code: "share", Password: "password"}, "123", "source-cookie", PlaybackUserAgent)
			if (err == nil) != tc.ok || calls != 2 {
				t.Fatal("unexpected metadata result", err)
			}
			if tc.ok && (ValidateInstantSource(info) != nil || info.SHA1 != strings.Repeat("A", 40)) {
				t.Fatal("invalid instant source")
			}
		})
	}
}
