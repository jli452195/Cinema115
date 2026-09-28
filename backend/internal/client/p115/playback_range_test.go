package p115

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestInstantUploadUsesSavedRangeWithFreshChallenge(t *testing.T) {
	client := NewShareClient()
	posts, ranges := 0, 0
	client.HTTP.Transport = metadataTransport(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Cookie") != "UID=22_A; SEID=destination" {
			t.Fatal("wrong target credentials")
		}
		switch r.URL.Path {
		case "/files":
			return instantResponse(200, []byte(`{"state":true,"cid":"0","path":[{"cid":"0"}]}`)), nil
		case "/files/uploadinfo":
			return instantResponse(200, []byte(`{"user_id":22,"userkey":"fresh-userkey"}`)), nil
		case "/4.0/initupload.php":
			posts++
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatal(err)
			}
			plain, err := AESDecryptCBC(body)
			if err != nil {
				t.Fatal(err)
			}
			form, err := url.ParseQuery(string(plain))
			if err != nil {
				t.Fatal(err)
			}
			if posts == 1 {
				if form.Get("sign_key") != "" || form.Get("sign_val") != "" {
					t.Fatal("replayed old signature")
				}
				return instantResponse(200, encryptedInstantResponse(t, `{"status":7,"sign_key":"fresh-key","sign_check":"1-3"}`)), nil
			}
			if form.Get("sign_key") != "fresh-key" || form.Get("sign_val") != strings.Repeat("C", 40) {
				t.Fatal("wrong range/signature")
			}
			return instantResponse(200, encryptedInstantResponse(t, `{"status":2,"file_id":"999","pickcode":"abc12345"}`)), nil
		default:
			t.Fatal("saved range unexpectedly read a source URL", r.URL.Host)
			return nil, nil
		}
	})
	info := &DownloadInfo{Name: "v.mkv", Size: 100, SHA1: strings.Repeat("A", 40)}
	info.ResolveRangeSHA1 = func(_ context.Context, start, end int64) (string, error) {
		ranges++
		if start != 1 || end != 3 {
			t.Fatal("wrong range")
		}
		return strings.Repeat("c", 40), nil
	}
	result, err := client.InstantUpload(context.Background(), info, "0", "UID=22_A; SEID=destination", nil)
	if err != nil || result == nil || !result.Challenged || posts != 2 || ranges != 1 {
		t.Fatal("saved range upload failed", err)
	}
}
