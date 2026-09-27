package p115

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestPlaybackSHA1Search(t *testing.T) {
	hash := strings.Repeat("A", 40)
	for _, tt := range []struct {
		name, body string
		hit, fail  bool
	}{
		{"hit", fmt.Sprintf(`{"state":true,"count":1,"data":[{"fid":"12","cid":"0","n":"renamed","s":999,"sha":"%s","pc":"pick123"}]}`, strings.ToLower(hash)), true, false},
		{"miss", `{"state":true,"count":0,"data":[]}`, false, false},
		{"different_hash", fmt.Sprintf(`{"state":true,"count":1,"data":[{"fid":"12","cid":"0","n":"same","sha":"%s","pc":"pick123"}]}`, strings.Repeat("B", 40)), false, false},
		{"expired_cookie", `{"state":false,"error":"login required"}`, false, true},
		{"missing_data", `{"state":true,"count":0}`, false, true},
		{"incomplete", `{"state":true,"count":1,"data":[]}`, false, true},
		{"missing_pickcode", fmt.Sprintf(`{"state":true,"count":1,"data":[{"fid":"12","cid":"0","sha":"%s"}]}`, hash), false, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := newTestShareClient()
			c.HTTP.Transport = metadataTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path != "/files/search" || r.URL.Query().Get("search_value") != hash || r.URL.Query().Get("cid") != "0" || r.Header.Get("Cookie") != "UID=22_A" {
					t.Fatal("wrong search or account")
				}
				return instantResponse(200, []byte(tt.body)), nil
			})
			f, err := c.FindPlaybackFileBySHA1(context.Background(), hash, "UID=22_A")
			if (err != nil) != tt.fail || (f != nil) != tt.hit {
				t.Fatal(f, err)
			}
		})
	}
}

func TestPlaybackExplicitRootReachesUpload(t *testing.T) {
	c := newTestShareClient()
	posts := 0
	c.HTTP.Transport = metadataTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodPost {
			posts++
			return instantResponse(200, []byte("unconfirmed upload")), nil
		}
		if r.URL.Query().Get("cid") != "0" {
			t.Fatal("wrong parent")
		}
		return instantResponse(200, []byte(`{"state":true,"cid":"0"}`)), nil
	})
	ticket := &PlaybackUploadTicket{scope: "account", uid: "22", key: "secret", cookieHash: playbackCredentialHash("UID=22_A"), expires: time.Now().Add(time.Minute)}
	_, err := c.UploadPlayback(context.Background(), testInstantInfo(), "account", "0", "UID=22_A", ticket, nil)
	if posts != 1 || err == nil {
		t.Fatal("explicit root failed before submission", posts, err)
	}
}

func TestCompletePlaybackUploadSearchComparesOnlySHA1(t *testing.T) {
	c := newTestShareClient()
	info := testInstantInfo()
	c.HTTP.Transport = metadataTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/files/search" {
			return instantResponse(200, []byte(fmt.Sprintf(`{"state":true,"count":1,"data":[{"fid":"12","cid":"77","n":"renamed","s":1,"sha":"%s","pc":"pick123"}]}`, info.SHA1))), nil
		}
		if r.URL.Path != "/app/chrome/downurl" {
			t.Fatal("unexpected call", r.URL.Path)
		}
		// A transport failure here proves the SHA1-only match reached URL lookup.
		return nil, fmt.Errorf("expected downurl reached")
	})
	f, _, err := c.CompletePlaybackUpload(context.Background(), info, "0", &InstantUploadResult{}, "UID=22_A", "player")
	if f.ID != "12" || f.CID != "77" || err == nil {
		t.Fatal("same SHA1 different name/size/folder rejected", f, err)
	}
}
