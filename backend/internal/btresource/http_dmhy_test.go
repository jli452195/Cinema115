package btresource

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"strm115/backend/internal/client/animefeed"
)

func TestDMHYTorrentURL(t *testing.T) {
	const hash = "57294b2c4f26b6c6f2fa71416194ead5a82adc7c"
	const endpoint = "https://dl.dmhy.org/2023/01/21/" + hash + ".torrent"
	for _, raw := range []string{endpoint, strings.Replace(endpoint, hash, strings.ToUpper(hash), 1)} {
		if got, err := TorrentURL("dmhy", hash, raw); err != nil || got != raw {
			t.Fatalf("valid DMHY torrent rejected: %q, %v", got, err)
		}
	}
	for _, tc := range []struct{ post, url string }{
		{"628479", endpoint},
		{strings.Repeat("a", 40), endpoint},
		{hash, strings.Replace(endpoint, "https://", "http://", 1)},
		{hash, strings.Replace(endpoint, "dl.dmhy.org", "share.dmhy.org", 1)},
		{hash, strings.Replace(endpoint, "dl.dmhy.org", "dl.dmhy.org:443", 1)},
		{hash, strings.Replace(endpoint, "/2023/01/21/", "/20230121/", 1)},
		{hash, endpoint + "?token=private"},
		{hash, endpoint + "#fragment"},
	} {
		if _, err := TorrentURL("dmhy", tc.post, tc.url); !errors.Is(err, ErrInvalid) {
			t.Fatalf("unbound DMHY URL accepted: %q, %v", tc.url, err)
		}
	}
}

func TestDMHYTorrentHTTPReader(t *testing.T) {
	const hash = "57294b2c4f26b6c6f2fa71416194ead5a82adc7c"
	const endpoint = "https://dl.dmhy.org/2023/01/21/" + hash + ".torrent"
	for _, status := range []int{http.StatusOK, http.StatusFound, http.StatusForbidden} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			calls := 0
			reader := &HTTPReader{HTTP: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				if req.URL.String() != endpoint || req.Header.Get("Cookie") != "" || req.Header.Get("Authorization") != "" {
					t.Fatal("unexpected torrent request")
				}
				return &http.Response{StatusCode: status, Header: http.Header{"Location": {endpoint}}, Body: io.NopCloser(strings.NewReader("torrent")), Request: req}, nil
			})}}
			body, err := reader.ReadTorrent(context.Background(), "dmhy", hash, endpoint)
			if calls != 1 {
				t.Fatalf("expected one request, got %d", calls)
			}
			if status == http.StatusOK {
				if err != nil || string(body) != "torrent" {
					t.Fatalf("torrent read failed: %q, %v", body, err)
				}
				return
			}
			var upstream *animefeed.HTTPError
			if !errors.As(err, &upstream) || upstream.Site != "dmhy" || upstream.Status != status {
				t.Fatalf("upstream rejection lost: %v", err)
			}
		})
	}
}
