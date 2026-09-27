package p115

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestInstantUploadEmptyDirectorySinglePreflight(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		valid     bool
	}{
		{"empty", `{"state":true,"cid":"9","count":0,"data":[]}`, true},
		{"path identity", `{"state":true,"path":[{"cid":"0"},{"cid":"9"}],"count":"0","data":[]}`, true},
		{"wrong directory", `{"state":true,"cid":"8","count":0,"data":[]}`, false},
		{"conflicting identity", `{"state":true,"cid":"9","path":[{"cid":"8"}],"count":0,"data":[]}`, false},
		{"missing identity", `{"state":true,"count":0,"data":[]}`, false},
		{"missing count", `{"state":true,"cid":"9","data":[]}`, false},
		{"missing entries", `{"state":true,"cid":"9","count":0}`, false},
		{"null entries", `{"state":true,"cid":"9","count":0,"data":null}`, false},
		{"rejected", `{"state":false,"cid":"9","count":0,"data":[]}`, false},
		{"nonempty", `{"state":true,"cid":"9","count":1,"data":[{"fid":"1"}]}`, false},
		{"hidden folder", `{"state":true,"cid":"9","count":0,"data":[{"cid":"10"}]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := NewShareClient()
			directories, identities, writes := 0, 0, 0
			c.HTTP.Transport = metadataTransport(func(r *http.Request) (*http.Response, error) {
				if r.Header.Get("Cookie") != "UID=22_A" {
					t.Fatal("wrong destination account")
				}
				switch r.URL.Path {
				case "/files":
					directories++
					if r.URL.Query().Get("cid") != "9" || r.URL.Query().Get("show_dir") != "1" {
						t.Fatal("wrong preflight scope")
					}
					return instantResponse(200, []byte(tc.raw)), nil
				case "/files/uploadinfo":
					identities++
					return instantResponse(200, []byte(`{"user_id":22,"userkey":"key"}`)), nil
				case "/4.0/initupload.php":
					writes++
					return instantResponse(200, encryptedInstantResponse(t, `{"status":2,"pickcode":"abc12345","file_id":"999"}`)), nil
				default:
					t.Fatal("unexpected request", r.URL.Path)
					return nil, nil
				}
			})
			_, err := c.InstantUploadToEmptyDirectory(context.Background(), testInstantInfo(), "9", "UID=22_A", nil)
			if (err == nil) != tc.valid || directories != 1 {
				t.Fatal("preflight result", err, directories)
			}
			want := 0
			if tc.valid {
				want = 1
			}
			if identities != want || writes != want {
				t.Fatal("unsafe or repeated write", identities, writes)
			}
		})
	}
}

func TestEmptyDirectoryRejectsRootAndCancellationBeforeNetwork(t *testing.T) {
	c := newTestShareClient()
	c.HTTP.Transport = metadataTransport(func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid destination or canceled request reached 115")
		return nil, nil
	})
	for _, cid := range []string{"0", "", "-1", "invalid"} {
		if _, err := c.InstantUploadToEmptyDirectory(context.Background(), testInstantInfo(), cid, "UID=22_A", nil); err == nil {
			t.Fatal("invalid cache destination accepted", cid)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.InstantUploadToEmptyDirectory(ctx, testInstantInfo(), "9", "UID=22_A", nil); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation lost", err)
	}
}

func TestEmptyDirectoryPreflightRetainsReadRetry(t *testing.T) {
	c := newTestShareClient()
	c.ReadRetryDelays = []time.Duration{time.Millisecond}
	reads, posts := 0, 0
	c.HTTP.Transport = metadataTransport(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/files":
			reads++
			if reads == 1 {
				return instantResponse(429, nil), nil
			}
			return instantResponse(200, []byte(`{"state":true,"cid":"9","count":0,"data":[]}`)), nil
		case "/files/uploadinfo":
			return instantResponse(200, []byte(`{"user_id":22,"userkey":"key"}`)), nil
		case "/4.0/initupload.php":
			posts++
			return nil, errors.New("write connection interrupted")
		default:
			t.Fatal("unexpected request")
			return nil, nil
		}
	})
	_, err := c.InstantUploadToEmptyDirectory(context.Background(), testInstantInfo(), "9", "UID=22_A", nil)
	if !errors.Is(err, ErrInstantUncertain) || reads != 2 || posts != 1 {
		t.Fatal("read retry or single write contract broken", err, reads, posts)
	}
}
