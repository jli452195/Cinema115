package p115

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestRangeSourceDownloadOnlyLooksUpMissingHash(t *testing.T) {
	for _, mode := range []string{"complete", "missing", "malformed", "wrong-size", "wrong-pickcode", "deleted"} {
		t.Run(mode, func(t *testing.T) {
			c := newTestShareClient()
			c.nextRequest = time.Now().Add(time.Hour)
			calls := 0
			c.HTTP.Transport = metadataTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.Path != "/files/get_info" || r.URL.Query().Get("file_id") != "12" || r.Header.Get("Cookie") != "UID=22_A" {
					t.Fatal("unexpected metadata request")
				}
				pc, size, area := "pick123", 100, 1
				if mode == "wrong-size" {
					size++
				}
				if mode == "wrong-pickcode" {
					pc = "pick999"
				}
				if mode == "deleted" {
					area = 120
				}
				return instantResponse(200, []byte(fmt.Sprintf(`{"state":true,"data":[{"fid":"12","pc":"%s","s":%d,"aid":%d,"sha":"%s"}]}`, pc, size, area, strings.Repeat("A", 40)))), nil
			})
			hash := ""
			if mode == "complete" {
				hash = strings.Repeat("A", 40)
			}
			if mode == "malformed" {
				hash = "not-a-hash"
			}
			raw := fmt.Sprintf(`{"12":{"pick_code":"pick123","file_name":"v.mkv","file_size":100,"sha1":"%s","url":{"url":"https://cdn.115.com/video"}}}`, hash)
			info, id, err := parsePickCodeInfo([]byte(raw), "pick123")
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			got, err := c.completePickCodeInstantInfo(ctx, info, id, "pick123", "UID=22_A")
			wantOK := mode == "complete" || mode == "missing"
			if (err == nil) != wantOK {
				t.Fatal("identity validation failed", mode, err)
			}
			wantCalls := 1
			if mode == "complete" || mode == "malformed" {
				wantCalls = 0
			}
			if calls != wantCalls {
				t.Fatal("unnecessary request or missing fallback", calls)
			}
			if wantOK && (got.FileID != "12" || got.SHA1 != strings.Repeat("A", 40)) {
				t.Fatal("lost source identity")
			}
		})
	}
}
