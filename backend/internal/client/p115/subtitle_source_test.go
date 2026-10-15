package p115

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestSubtitleSourceDownloadIntegrityBoundsAndCredentials(t *testing.T) {
	digest := sha1.Sum([]byte("subtitle"))
	hash := hex.EncodeToString(digest[:])
	for _, mode := range []string{"valid", "unknown-length", "truncated", "extra-bytes", "header-size", "hash", "redirect", "http-error", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			c := newTestShareClient()
			calls := 0
			c.HTTP.Transport = metadataTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" || r.UserAgent() != PlaybackUserAgent {
					t.Fatal("CDN received credentials or inconsistent UA")
				}
				if r.Context().Err() != nil {
					return nil, r.Context().Err()
				}
				content := "subtitle"
				if mode == "hash" {
					content = "changed!"
				}
				if mode == "truncated" {
					content = "sub"
				}
				if mode == "extra-bytes" {
					content += "extra"
				}
				response := &http.Response{StatusCode: 200, Header: make(http.Header), ContentLength: 8, Body: io.NopCloser(strings.NewReader(content)), Request: r}
				if mode == "unknown-length" || mode == "extra-bytes" {
					response.ContentLength = -1
				}
				if mode == "header-size" {
					response.ContentLength = MaxSubtitleSourceBytes + 1
				}
				if mode == "http-error" {
					response.StatusCode = 403
				}
				if mode == "redirect" {
					response.StatusCode = 302
					response.Header.Set("Location", "http://127.0.0.1/private")
				}
				return response, nil
			})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "cancel" {
				cancel()
			}
			data, err := c.readSubtitleSourceURL(ctx, "https://cdn.115cdn.com/source?signature=private", 8, hash)
			if mode == "valid" || mode == "unknown-length" {
				if err != nil || string(data) != "subtitle" {
					t.Fatal("valid file failed", err)
				}
			} else if err == nil {
				t.Fatal("invalid source accepted")
			}
			if mode == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation lost", err)
			}
			if err != nil && strings.Contains(err.Error(), "signature=private") {
				t.Fatal("signed URL leaked")
			}
			if calls > 1 {
				t.Fatal("followed forbidden redirect")
			}
		})
	}
}

func TestSubtitleSourceRefusesChangedIdentityBeforeGettingLink(t *testing.T) {
	for _, mode := range []string{"parent", "name", "size", "file-id"} {
		t.Run(mode, func(t *testing.T) {
			c := newTestShareClient()
			calls := 0
			c.HTTP.Transport = metadataTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.Path != "/files/get_info" {
					t.Fatal("downloaded unverified file")
				}
				parent, name, id, size := "10", "subs.7z", "123", 8
				switch mode {
				case "parent":
					parent = "11"
				case "name":
					name = "changed.7z"
				case "size":
					size++
				case "file-id":
					id = "124"
				}
				return instantResponse(200, []byte(fmt.Sprintf(`{"state":true,"data":[{"file_id":"%s","category_id":"%s","pick_code":"pick123","sha1":"%s","file_name":"%s","file_size":%d}]}`, id, parent, strings.Repeat("A", 40), name, size))), nil
			})
			_, err := c.ReadSubtitleSource(context.Background(), "10", ShareFile{ID: "123", Name: "subs.7z", Size: 8}, "UID=22_A")
			if err == nil || calls != 1 {
				t.Fatal("identity mismatch accepted", calls, err)
			}
		})
	}
}
