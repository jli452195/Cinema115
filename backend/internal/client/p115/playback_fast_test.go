package p115

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestPlaybackPreparationCachesAreBoundedAndCredentialScoped(t *testing.T) {
	c := newTestShareClient()
	reads, dirs := 0, 0
	c.HTTP.Transport = metadataTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/files/uploadinfo" {
			reads++
			return instantResponse(200, []byte(`{"user_id":22,"userkey":"private-key"}`)), nil
		}
		if r.URL.Path == "/files" {
			dirs++
			return instantResponse(200, []byte(`{"state":true,"cid":"9","count":0,"data":[]}`)), nil
		}
		return instantResponse(500, nil), nil
	})
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if _, err := c.PreparePlaybackAccount(ctx, "account1", "UID=22_A"); err != nil {
			t.Fatal(err)
		}
		if err := c.ValidatePlaybackDirectory(ctx, "account1", "9", "UID=22_A"); err != nil {
			t.Fatal(err)
		}
	}
	if reads != 1 || dirs != 1 {
		t.Fatal("preflight cache missed")
	}
	for _, pair := range [][2]string{{"account2", "UID=22_A"}, {"account1", "UID=22_B"}} {
		if _, err := c.PreparePlaybackAccount(ctx, pair[0], pair[1]); err != nil {
			t.Fatal(err)
		}
		if err := c.ValidatePlaybackDirectory(ctx, pair[0], "9", pair[1]); err != nil {
			t.Fatal(err)
		}
	}
	if reads != 3 || dirs != 3 {
		t.Fatal("account/cookie isolation lost")
	}
	v := c.playbackPreparation.accounts[playbackPreparationKey("account1", "UID=22_A")]
	v.expires = time.Now().Add(-time.Second)
	c.playbackPreparation.accounts[playbackPreparationKey("account1", "UID=22_A")] = v
	c.playbackPreparation.directories[playbackPreparationKey("account1\x009", "UID=22_A")] = time.Now().Add(-time.Second)
	ticket, err := c.PreparePlaybackAccount(ctx, "account1", "UID=22_A")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.ValidatePlaybackDirectory(ctx, "account1", "9", "UID=22_A"); err != nil {
		t.Fatal(err)
	}
	if reads != 4 || dirs != 4 {
		t.Fatal("expired cache used")
	}
	if fmt.Sprint(ticket) != "[115 upload credentials]" {
		t.Fatal("ticket can leak secret")
	}
	if _, err := c.UploadPlayback(ctx, testInstantInfo(), "account2", "9", "UID=22_A", ticket, nil); err == nil {
		t.Fatal("cross-account ticket accepted")
	}
	if _, err := c.UploadPlayback(ctx, testInstantInfo(), "account1", "0", "UID=22_A", ticket, nil); err == nil {
		t.Fatal("root accepted")
	}
	if _, err := c.UploadPlayback(ctx, testInstantInfo(), "account1", "9", "UID=22_A", ticket, nil); err == nil {
		t.Fatal("write failure hidden")
	}
	if _, exists := c.playbackPreparation.accounts[playbackPreparationKey("account1", "UID=22_A")]; exists {
		t.Fatal("failed credentials cached")
	}
}

func TestPlaybackPreflightErrorsNeverSubmit(t *testing.T) {
	for _, mode := range []string{"directory", "expired", "cookie"} {
		t.Run(mode, func(t *testing.T) {
			c := newTestShareClient()
			posts := 0
			c.HTTP.Transport = metadataTransport(func(r *http.Request) (*http.Response, error) {
				if r.Method == http.MethodPost {
					posts++
				}
				if mode == "directory" {
					return instantResponse(200, []byte(`{"state":true,"cid":"0"}`)), nil
				}
				return instantResponse(200, []byte(`{"state":true,"cid":"9"}`)), nil
			})
			ticket := &PlaybackUploadTicket{scope: "account", uid: "22", key: "secret", cookieHash: playbackCredentialHash("UID=22_A"), expires: time.Now().Add(time.Minute)}
			if mode == "expired" {
				ticket.expires = time.Now().Add(-time.Second)
			}
			if mode == "cookie" {
				ticket.cookieHash = "wrong"
			}
			_, err := c.UploadPlayback(context.Background(), testInstantInfo(), "account", "9", "UID=22_A", ticket, nil)
			var preflight *PlaybackPreflightError
			if !errors.As(err, &preflight) || posts != 0 {
				t.Fatal("preflight misclassified", err, posts)
			}
			if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "UID=") {
				t.Fatal("secret leaked")
			}
		})
	}
}

func TestSharedDirectoryPaginationAndForeignFolders(t *testing.T) {
	for _, mode := range []string{"valid", "changed-count", "duplicate", "short-page"} {
		t.Run(mode, func(t *testing.T) {
			c := newTestShareClient()
			calls := 0
			c.HTTP.Transport = metadataTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
				count := 101
				if mode == "changed-count" && offset > 0 {
					count = 102
				}
				data := make([]map[string]any, 0, 100)
				end := offset + 100
				if end > 101 {
					end = 101
				}
				if mode == "short-page" {
					end = 1
				}
				for i := offset; i < end; i++ {
					if i == 0 {
						data = append(data, map[string]any{"cid": "88", "n": "unrelated folder"})
						continue
					}
					id := i
					if mode == "duplicate" && offset > 0 {
						id = 1
					}
					data = append(data, map[string]any{"fid": strconv.Itoa(id), "cid": "9", "n": "movie", "s": "100", "sha": strings.Repeat("A", 40), "pc": "pick1234"})
				}
				raw, _ := json.Marshal(map[string]any{"state": true, "cid": "9", "count": count, "data": data})
				return instantResponse(200, raw), nil
			})
			files, err := c.SharedCacheDirectoryFiles(context.Background(), "9", "UID=22_A")
			if mode == "valid" {
				if err != nil || len(files) != 100 || calls != 2 {
					t.Fatal("pagination failed", err, len(files), calls)
				}
			} else if err == nil {
				t.Fatal("incomplete scan accepted")
			}
		})
	}
}
