package p115

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestPlaybackReceiptUsesPickcodeWithoutSearch(t *testing.T) {
	for _, mode := range []string{"pickcode-only", "no-sha", "id-only", "mismatch", "wrong-id", "link-error"} {
		t.Run(mode, func(t *testing.T) {
			source := testInstantInfo()
			metaCalls, linkCalls := 0, 0
			result := &InstantUploadResult{PickCode: "pick123"}
			if mode == "id-only" {
				result = &InstantUploadResult{FileID: "12"}
			}
			if mode == "wrong-id" {
				result.FileID = "13"
			}
			f, link, err := completePlaybackReceipt(source, "0", result, func(pc string) (*DownloadInfo, string, error) {
				linkCalls++
				if pc != "pick123" {
					t.Fatal("wrong pickcode")
				}
				if mode == "link-error" {
					return nil, "", errors.New("cookie=secret")
				}
				hash := source.SHA1
				if mode == "no-sha" {
					hash = ""
				}
				if mode == "mismatch" {
					hash = strings.Repeat("B", 40)
				}
				return &DownloadInfo{Name: "renamed", Size: 999, SHA1: hash, URL: "https://cdn.115.com/video"}, "12", nil
			}, func(id string) (DrivePlaybackFile, error) {
				metaCalls++
				if id != "12" {
					t.Fatal("wrong exact id")
				}
				return DrivePlaybackFile{ID: id, CID: "0", PickCode: "pick123", SHA1: source.SHA1, Name: "other", Size: 1}, nil
			})
			fail := mode == "mismatch" || mode == "wrong-id" || mode == "link-error"
			if (err != nil) != fail {
				t.Fatal(mode, err)
			}
			if fail {
				if strings.Contains(err.Error(), "secret") {
					t.Fatal("secret leaked")
				}
				return
			}
			want := 0
			if mode == "no-sha" || mode == "id-only" {
				want = 1
			}
			if f.ID != "12" || link == "" || linkCalls != 1 || metaCalls != want {
				t.Fatal("extra lookup", f, linkCalls, metaCalls)
			}
		})
	}
}

func TestPlaybackExactInfoAndLazySource(t *testing.T) {
	c := newTestShareClient()
	sourceReads, exactReads, linkReads := 0, 0, 0
	c.HTTP.Transport = metadataTransport(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/files/supervision":
			sourceReads++
			_ = r.ParseForm()
			if r.Form.Get("pickcode") != "pick123" {
				t.Fatal("wrong pickcode")
			}
			return instantResponse(200, []byte(fmt.Sprintf(`{"state":true,"data":{"file_id":"12","file_name":"v.mkv","file_size":"100","file_sha1":"%s"}}`, strings.Repeat("A", 40)))), nil
		case "/files/get_info":
			exactReads++
			if r.URL.Query().Get("file_id") != "12" {
				t.Fatal("wrong id")
			}
			return instantResponse(200, []byte(fmt.Sprintf(`{"state":true,"data":[{"file_id":"12","pick_code":"pick123","category_id":"0","sha1":"%s","file_name":"v.mkv","file_size":"100"}]}`, strings.Repeat("A", 40)))), nil
		case "/app/chrome/downurl":
			linkReads++
			return nil, errors.New("expected URL request")
		default:
			t.Fatal("unexpected endpoint", r.URL.Path)
			return nil, nil
		}
	})
	for _, pair := range [][2]string{{"src", "UID=22_A"}, {"src", "UID=22_A"}, {"other", "UID=22_A"}, {"src", "UID=22_B"}} {
		info, err := c.PlaybackPickCodeSource(context.Background(), pair[0], "pick123", pair[1])
		if err != nil {
			t.Fatal(err)
		}
		if info.URL != "" || info.loadPlaybackURL == nil {
			t.Fatal("URL fetched eagerly")
		}
	}
	if sourceReads != 3 || linkReads != 0 {
		t.Fatal("metadata cache isolation failed")
	}
	info, _ := c.PlaybackPickCodeSource(context.Background(), "src", "pick123", "UID=22_A")
	_, _ = info.loadPlaybackURL(context.Background())
	if linkReads != 1 {
		t.Fatal("lazy URL not requested")
	}
	f, err := c.playbackFileInfo(context.Background(), "12", "UID=22_A")
	if err != nil || f.ID != "12" || exactReads != 1 {
		t.Fatal(f, err)
	}
}

func TestPlaybackReadersDoNotWaitForBackgroundScans(t *testing.T) {
	c := newTestShareClient()
	c.nextRequest = time.Now().Add(time.Hour)
	c.HTTP.Transport = metadataTransport(func(r *http.Request) (*http.Response, error) {
		return instantResponse(200, []byte(`{"state":true,"count":0,"data":[]}`)), nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := c.FindPlaybackFileBySHA1(ctx, strings.Repeat("A", 40), "UID=22_A"); err != nil {
		t.Fatal("playback waited for scan", err)
	}
	a, b := c.playbackReader("UID=22_A"), c.playbackReader("UID=33_A")
	if a == b || a == c || a.HTTP != c.HTTP {
		t.Fatal("request queue or connection isolation failed")
	}
	a.nextRequest = time.Now().Add(time.Hour)
	if _, err := c.FindPlaybackFileBySHA1(ctx, strings.Repeat("A", 40), "UID=33_A"); err != nil {
		t.Fatal("other account blocked", err)
	}
}

func TestLazySourceURLOnlyLoadedForChallenge(t *testing.T) {
	for _, challenge := range []bool{false, true} {
		t.Run(fmt.Sprint(challenge), func(t *testing.T) {
			c := newTestShareClient()
			posts, loads := 0, 0
			info := testInstantInfo()
			link := info.URL
			info.URL = ""
			info.loadPlaybackURL = func(context.Context) (string, error) { loads++; return link, nil }
			c.HTTP.Transport = metadataTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host == "cdn.115cdn.net" {
					if r.Header.Get("Cookie") != "" || r.Header.Get("Range") != "bytes=1-3" {
						t.Fatal("wrong range or secret leaked")
					}
					resp := instantResponse(206, []byte("abc"))
					resp.Header.Set("Content-Range", "bytes 1-3/100")
					return resp, nil
				}
				switch r.URL.Path {
				case "/files":
					return instantResponse(200, []byte(`{"state":true,"cid":"0"}`)), nil
				case "/files/uploadinfo":
					return instantResponse(200, []byte(`{"user_id":22,"userkey":"key"}`)), nil
				case "/4.0/initupload.php":
					posts++
					if challenge && posts == 1 {
						return instantResponse(200, encryptedInstantResponse(t, `{"status":7,"sign_key":"key","sign_check":"1-3"}`)), nil
					}
					return instantResponse(200, encryptedInstantResponse(t, `{"status":2,"pickcode":"pick123"}`)), nil
				default:
					t.Fatal("unexpected endpoint", r.URL.Path)
					return nil, nil
				}
			})
			result, err := c.InstantUpload(context.Background(), info, "0", "UID=22_A", nil)
			want := 0
			if challenge {
				want = 1
			}
			if err != nil || result == nil || loads != want || posts != 1+want {
				t.Fatal("incorrect lazy upload flow", result, err, loads, posts)
			}
		})
	}
}
