package p115

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

func TestSharePlaybackDesktopEndpointAndMetadata(t *testing.T) {
	for _, linkJSON := range []string{`"https://cdn.115.com/video?f=1"`, `{"url":"https://cdn.115.com/video?f=1"}`} {
		t.Run(linkJSON, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				want := url.Values{"share_code": {"share123"}, "receive_code": {"a&b+"}, "file_id": {"3465764618595469251"}, "dl": {"0"}}
				if r.Method != http.MethodGet || r.URL.Path != "/os_windows/2.0/share/downurl" || !reflect.DeepEqual(r.URL.Query(), want) {
					t.Error("unexpected playback API or parameters")
				}
				if r.Header.Get("Cookie") != "friend-cookie" || r.UserAgent() != "player-UA" {
					t.Error("credentials or user agent changed")
				}
				body, _ := io.ReadAll(r.Body)
				if len(body) != 0 {
					t.Error("unexpected request body")
				}
				_, _ = fmt.Fprintf(w, `{"state":true,"data":{"fn":"episode.mkv","fs":"5292623203","sha1":"%s","url":%s}}`, strings.Repeat("a", 40), linkJSON)
			}))
			defer server.Close()
			client := NewShareClient()
			client.AppURL = server.URL
			info, err := client.DownloadInfoWithUA(context.Background(), &ShareInfo{Code: "share123", Password: "a&b+"}, "3465764618595469251", "friend-cookie", "player-UA")
			if err != nil {
				t.Fatal(err)
			}
			if calls != 1 || info.URL != "https://cdn.115.com/video?f=1" || info.Name != "episode.mkv" || info.Size != 5292623203 || info.SHA1 != strings.Repeat("A", 40) {
				t.Fatal("unexpected download metadata or extra request")
			}
		})
	}
}

func TestSharePlaybackInvalidResponseNeverFallsBack(t *testing.T) {
	for _, data := range []string{`null`, `"old-encrypted-response"`, `{}`, `{"url":"https://untrusted.example/video"}`, `{"url":42}`} {
		t.Run(data, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != "/os_windows/2.0/share/downurl" {
					t.Error("unsafe fallback endpoint")
				}
				_, _ = fmt.Fprintf(w, `{"state":true,"data":%s}`, data)
			}))
			defer server.Close()
			client := NewShareClient()
			client.AppURL = server.URL
			_, err := client.DownloadInfoWithUA(context.Background(), &ShareInfo{Code: "share"}, "1", "friend-cookie", "player-UA")
			if err == nil || calls != 1 {
				t.Fatal("invalid response accepted or another endpoint requested")
			}
		})
	}
}
