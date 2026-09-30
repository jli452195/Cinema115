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

func TestPlaybackQuickSearchMissContract(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		miss       bool
	}{
		{"documented", `{"state":false,"error":"文件错误"}`, true},
		{"reordered", `{"error":"文件错误", "state": false}`, true},
		{"login", `{"state":false,"error":"文件错误","errno":990001}`, false},
		{"extra", `{"state":false,"error":"文件错误","data":null}`, false},
		{"other-error", `{"state":false,"error":"系统繁忙"}`, false},
		{"numeric-state", `{"state":0,"error":"文件错误"}`, false},
		{"missing-state", `{"error":"文件错误"}`, false},
		{"empty-success", `{"state":true,"data":{}}`, false},
		{"malformed", `{`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestShareClient()
			calls := 0
			c.HTTP.Transport = metadataTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Method != "GET" || r.URL.Path != "/files/shasearch" || r.URL.Query().Get("sha1") != strings.Repeat("A", 40) || r.Header.Get("Cookie") != "UID=22_A" {
					t.Error("wrong query or credentials")
				}
				return instantResponse(200, []byte(tc.body)), nil
			})
			f, err := c.FindPlaybackFileBySHA1Quick(context.Background(), strings.Repeat("a", 40), "UID=22_A")
			if f != nil || (err == nil) != tc.miss || calls != 1 {
				t.Fatalf("miss=%v calls=%d error=%v", tc.miss, calls, err)
			}
		})
	}
}

func TestPlaybackQuickSearchVerifiesTargetIdentity(t *testing.T) {
	for _, mismatch := range []string{"", "id", "size", "sha1", "pickcode"} {
		t.Run(mismatch, func(t *testing.T) {
			c := newTestShareClient()
			calls := 0
			c.HTTP.Transport = metadataTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Header.Get("Cookie") != "UID=22_A" {
					t.Error("target credentials changed")
				}
				if r.URL.Path == "/files/shasearch" {
					return instantResponse(200, []byte(`{"state":true,"data":{"file_id":"123","category_id":"0","file_size":"100","pick_code":"pick123"}}`)), nil
				}
				if r.URL.Path != "/files/supervision" || r.Method != "POST" {
					t.Errorf("unexpected request %s", r.URL.Path)
				}
				if err := r.ParseForm(); err != nil || r.Form.Get("pickcode") != "pick123" {
					t.Error("wrong candidate")
				}
				id, size, hash, pc := "123", 100, strings.Repeat("A", 40), "pick123"
				switch mismatch {
				case "id":
					id = "124"
				case "size":
					size = 101
				case "sha1":
					hash = strings.Repeat("B", 40)
				case "pickcode":
					pc = "pick124"
				}
				body := fmt.Sprintf(`{"state":true,"data":{"file_id":"%s","file_size":%d,"file_sha1":"%s","pick_code":"%s","file_name":"v.mkv"}}`, id, size, hash, pc)
				return instantResponse(200, []byte(body)), nil
			})
			var summary PlaybackTiming
			ctx := WithPlaybackTiming(context.Background(), func(v PlaybackTiming) {
				if v.SearchSummary {
					summary = v
				}
			})
			f, err := c.FindPlaybackFileBySHA1Quick(ctx, strings.Repeat("A", 40), "UID=22_A")
			if calls != 2 || (err == nil) != (mismatch == "") {
				t.Fatal("identity check failed", calls, err)
			}
			if mismatch == "" && (f == nil || f.ID != "123" || f.CID != "0" || f.SHA1 != strings.Repeat("A", 40)) {
				t.Fatal("invalid verified file")
			}
			if mismatch != "" && f != nil {
				t.Fatal("unverified file exposed")
			}
			if summary.Requests != 2 || summary.Failed != (err != nil) {
				t.Fatal("incorrect timing", summary)
			}
		})
	}
}

func TestPlaybackQuickSearchFailureNeverBecomesMiss(t *testing.T) {
	for _, mode := range []string{"cancel", "deadline", "http", "rate-limit"} {
		t.Run(mode, func(t *testing.T) {
			c := newTestShareClient()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
			defer cancel()
			c.HTTP.Transport = metadataTransport(func(r *http.Request) (*http.Response, error) {
				switch mode {
				case "cancel":
					cancel()
				case "http":
					return instantResponse(503, []byte(`{"state":false,"error":"文件错误"}`)), nil
				case "rate-limit":
					return instantResponse(429, []byte(`{"state":false,"error":"文件错误"}`)), nil
				}
				<-r.Context().Done()
				return nil, r.Context().Err()
			})
			f, err := c.FindPlaybackFileBySHA1Quick(ctx, strings.Repeat("A", 40), "UID=22_A")
			if err == nil || f != nil {
				t.Fatal("failure treated as miss")
			}
			if mode == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if mode == "deadline" && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal(err)
			}
		})
	}
}

func TestPlaybackSHAMissDoesNotWeakenOtherEndpoints(t *testing.T) {
	c := newTestShareClient()
	c.HTTP.Transport = metadataTransport(func(*http.Request) (*http.Response, error) {
		return instantResponse(200, []byte(`{"state":false,"error":"文件错误"}`)), nil
	})
	if _, err := c.requestEnvelopeOnce(context.Background(), http.MethodGet, c.WebURL+"/files/search", "UID=22_A", nil); err == nil {
		t.Fatal("ordinary failure accepted")
	}
}
