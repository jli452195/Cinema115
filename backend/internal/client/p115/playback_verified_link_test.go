package p115

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestPlaybackCandidateSkipsMetadataUntilDownload(t *testing.T) {
	c := newTestShareClient()
	calls := 0
	c.HTTP.Transport = metadataTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Path != "/files/shasearch" {
			t.Error("unnecessary metadata query")
		}
		return instantResponse(200, []byte(`{"state":true,"data":{"file_id":"123","category_id":"0","pick_code":"pick123","file_size":100,"file_name":"v.mkv"}}`)), nil
	})
	f, err := c.FindPlaybackCandidateBySHA1(context.Background(), strings.Repeat("A", 40), "UID=22_A")
	if err != nil || f == nil || calls != 1 || f.Name != "v.mkv" {
		t.Fatal("candidate search failed", err, calls)
	}
}

func TestPlaybackDownloadIdentityAndConditionalMetadata(t *testing.T) {
	for _, mode := range []string{"valid", "missing-sha", "wrong-sha", "wrong-size", "wrong-id", "missing-sha-deleted", "restricted"} {
		t.Run(mode, func(t *testing.T) {
			expected := DrivePlaybackFile{ID: "123", PickCode: "pick123", SHA1: strings.Repeat("A", 40), Size: 100}
			hash, id, size, link := expected.SHA1, expected.ID, 100, "https://cdn.115.com/test"
			switch mode {
			case "missing-sha", "missing-sha-deleted":
				hash = ""
			case "wrong-sha":
				hash = strings.Repeat("B", 40)
			case "wrong-size":
				size++
			case "wrong-id":
				id = "124"
			case "restricted":
				link += "?f=3"
			}
			// Actual download schema has ID in the map key, no file_id field.
			raw := fmt.Sprintf(`{"%s":{"file_name":"v.mkv","file_size":%d,"pick_code":"pick123","sha1":"%s","url":{"url":"%s"}}}`, id, size, hash, link)
			info, gotID, err := parsePickCodeInfo([]byte(raw), expected.PickCode)
			calls := 0
			if err == nil {
				_, err = verifiedPlaybackDownload(expected, info, gotID, func() error {
					calls++
					if mode == "missing-sha-deleted" {
						return ErrDriveFileMissing
					}
					return nil
				})
			}
			if (err == nil) != (mode == "valid" || mode == "missing-sha") {
				t.Fatal(mode, err)
			}
			want := 0
			if strings.HasPrefix(mode, "missing-sha") {
				want = 1
			}
			if calls != want {
				t.Fatal("unnecessary metadata or skipped fallback", calls)
			}
			if mode == "missing-sha-deleted" && !errors.Is(err, ErrDriveFileMissing) {
				t.Fatal("lost deletion error")
			}
		})
	}
}
