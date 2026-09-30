package p115

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestPlaybackDeletionRequiresExactEndpointEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, path, body string
		status           int
		missing          bool
		cause            error
	}{
		{"exact-missing", "/files/get_info", `{"state":false,"code":20018}`, 200, true, ErrDriveFileMissing},
		{"download-not-proof", "/app/chrome/downurl", `{"state":false,"code":20018}`, 200, false, ErrDriveRejected},
		{"generic-file-error", "/files/get_info", `{"state":false,"error":"文件错误"}`, 200, false, ErrDriveRejected},
		{"login", "/files/get_info", `{"state":false,"errno":990001,"error":"secret 登录失效"}`, 200, false, ErrShareLoginRequired},
		{"rate-wins", "/files/get_info", `{"state":false,"code":20018,"error":"secret 请求频繁"}`, 200, false, ErrShareThrottled},
		{"conflicting-codes", "/files/get_info", `{"state":false,"code":20018,"errno":99}`, 200, false, ErrDriveRejected},
		{"http404", "/files/get_info", `{"state":false,"code":20018}`, 404, false, nil},
		{"unknown", "/files/get_info", `{"state":false,"error":"secret arbitrary"}`, 200, false, ErrDriveRejected},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestShareClient()
			c.HTTP.Transport = metadataTransport(func(*http.Request) (*http.Response, error) { return instantResponse(tc.status, []byte(tc.body)), nil })
			_, err := c.requestEnvelopeOnce(context.Background(), http.MethodGet, c.WebURL+tc.path, "UID=22_A", nil)
			if err == nil || errors.Is(err, ErrDriveFileMissing) != tc.missing || tc.cause != nil && !errors.Is(err, tc.cause) {
				t.Fatal("wrong classification", err)
			}
			if strings.Contains(err.Error(), "secret") || strings.Contains(SharePlaybackFailureReason(err), "secret") {
				t.Fatal("upstream text leaked")
			}
		})
	}
}

func TestPlaybackRecoveryBypassesMetadataCacheAndRejectsAmbiguity(t *testing.T) {
	for _, mode := range []string{"missing", "exists", "empty", "wrong-hash", "wrong-size"} {
		t.Run(mode, func(t *testing.T) {
			c := newTestShareClient()
			calls := 0
			f := DrivePlaybackFile{ID: "123", PickCode: "pick123", Size: 100, SHA1: strings.Repeat("A", 40)}
			c.HTTP.Transport = metadataTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.Path != "/files/get_info" || r.URL.Query().Get("file_id") != f.ID || r.Header.Get("Cookie") != "UID=22_A" {
					t.Error("wrong exact query")
				}
				if mode == "missing" {
					return instantResponse(200, []byte(`{"state":false,"code":20018}`)), nil
				}
				if mode == "empty" {
					return instantResponse(200, []byte(`{"state":true,"data":[]}`)), nil
				}
				hash, size := f.SHA1, f.Size
				if mode == "wrong-hash" {
					hash = strings.Repeat("B", 40)
				}
				if mode == "wrong-size" {
					size++
				}
				return instantResponse(200, []byte(fmt.Sprintf(`{"state":true,"data":[{"file_id":"123","pick_code":"pick123","sha1":"%s","file_size":%d}]}`, hash, size))), nil
			})
			for i := 0; i < 2; i++ {
				err := c.VerifyPlaybackFile(context.Background(), f, "UID=22_A")
				if (err == nil) != (mode == "exists") || errors.Is(err, ErrDriveFileMissing) != (mode == "missing") {
					t.Fatal(mode, err)
				}
			}
			if calls != 2 {
				t.Fatal("existence query used cache")
			}
		})
	}
}

func TestPlaybackRecoverySearchExcludesOnlyConfirmedIDs(t *testing.T) {
	for _, mode := range []string{"alternative", "only-stale", "incomplete"} {
		t.Run(mode, func(t *testing.T) {
			c := newTestShareClient()
			hash := strings.Repeat("A", 40)
			c.HTTP.Transport = metadataTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path != "/files/search" {
					t.Error("wrong recovery endpoint")
				}
				items := fmt.Sprintf(`{"fid":"123","cid":"0","pc":"pick123","sha":"%s","s":100}`, hash)
				count := 1
				if mode == "alternative" {
					items += "," + fmt.Sprintf(`{"fid":"124","cid":"0","pc":"pick124","sha":"%s","s":100}`, hash)
					count = 2
				}
				if mode == "incomplete" {
					count = 200
				}
				return instantResponse(200, []byte(fmt.Sprintf(`{"state":true,"count":%d,"data":[%s]}`, count, items))), nil
			})
			f, err := c.FindPlaybackFileBySHA1Excluding(context.Background(), hash, "UID=22_A", map[string]bool{"123": true})
			if (err != nil) != (mode == "incomplete") {
				t.Fatal(mode, err)
			}
			if mode == "alternative" && (f == nil || f.ID != "124") {
				t.Fatal("missed valid alternative")
			}
			if mode != "alternative" && f != nil {
				t.Fatal("reused stale candidate")
			}
		})
	}
}

func TestPlaybackQuickRejectedMetadataReturnsOnlyRecoveryCandidate(t *testing.T) {
	c := newTestShareClient()
	c.HTTP.Transport = metadataTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/files/shasearch" {
			return instantResponse(200, []byte(`{"state":true,"data":{"file_id":"123","category_id":"0","file_size":"100","pick_code":"pick123"}}`)), nil
		}
		return instantResponse(200, []byte(`{"state":false,"error":"文件错误"}`)), nil
	})
	f, err := c.FindPlaybackFileBySHA1Quick(context.Background(), strings.Repeat("A", 40), "UID=22_A")
	if f == nil || f.ID != "123" || !errors.Is(err, ErrDriveRejected) || errors.Is(err, ErrDriveFileMissing) {
		t.Fatal("unverified candidate became success or miss", f, err)
	}
}

func TestPlaybackExactInfoCompactFieldsAndPermanentDeletion(t *testing.T) {
	for _, mode := range []string{"live", "instant", "deleted", "deleted-wrong-id", "conflicting-id", "unknown-area", "wrong-size"} {
		t.Run(mode, func(t *testing.T) {
			c := newTestShareClient()
			hash := strings.Repeat("A", 40)
			f := DrivePlaybackFile{ID: "3528401704296907843", PickCode: "pick123", SHA1: hash, Size: 100}
			area, id, size, extra := "1", f.ID, "100", ""
			switch mode {
			case "instant":
				area = "12"
			case "deleted":
				area = "120"
			case "deleted-wrong-id":
				area = "120"
				id = "3528401704296907844"
			case "conflicting-id":
				area = "120"
				extra = `,"file_id":"999"`
			case "unknown-area":
				area = "999"
			case "wrong-size":
				size = "101"
			}
			c.HTTP.Transport = metadataTransport(func(*http.Request) (*http.Response, error) {
				return instantResponse(200, []byte(fmt.Sprintf(`{"state":true,"code":0,"data":[{"aid":%s,"fid":"%s","pc":"pick123","sha":"%s","s":"%s","cid":"0","n":"movie.mkv"%s}]}`, area, id, hash, size, extra))), nil
			})
			err := c.VerifyPlaybackFile(context.Background(), f, "UID=22_A")
			if (err == nil) != (mode == "live" || mode == "instant") || errors.Is(err, ErrDriveFileMissing) != (mode == "deleted") {
				t.Fatal(mode, err)
			}
			if err != nil && SharePlaybackFailureReason(err) == "网络请求失败或响应无效" {
				t.Fatal("local diagnostic lost", err)
			}
		})
	}
}
