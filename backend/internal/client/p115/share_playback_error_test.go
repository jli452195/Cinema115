package p115

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestShareDownloadFailureClassification(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		want       error
	}{
		{"review", `{"state":false,"msg":"分享正在审核中"}`, 200, ErrShareNotReadable},
		{"owner only", `{"state":false,"error":"文件违规，仅自己可见"}`, 200, ErrShareNotReadable},
		{"invisible", `{"state":false,"error":"文件不存在"}`, 200, ErrShareNotReadable},
		{"deleted share", `{"state":false,"errno":4100026,"error":"该文件分享链接不存在或已被删除"}`, 200, ErrShareNotReadable},
		{"permission", `{"state":false,"error":"没有权限访问该文件"}`, 200, ErrShareNotReadable},
		{"login before review", `{"state":false,"error":"登录失效","data":{"share_state":0}}`, 200, ErrShareLoginRequired},
		{"throttled before visibility", `{"state":false,"error":"文件不存在","msg":"请求频繁"}`, 200, ErrShareThrottled},
		{"password", `{"state":false,"error":"提取码错误"}`, 200, ErrSharePassword},
		{"unknown", `{"state":false,"errno":1234567,"error":"secret-cookie https://cdn.115.com/private-signature"}`, 200, ErrShareLoginRequired},
		{"unknown code", `{"state":false,"errno":1234567,"error":"private-secret"}`, 200, ErrShareDownloadRejected},
		{"http forbidden", `{"error":"文件不存在"}`, 403, nil},
		{"http rate limit", `{"error":"文件不存在"}`, 429, nil},
		{"http timeout", `{"error":"文件不存在"}`, 504, nil},
		{"malformed", `{`, 200, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != "/os_windows/2.0/share/downurl" || r.Method != "GET" || r.URL.Query().Get("dl") != "0" || r.Header.Get("Cookie") != "friend-cookie" || r.UserAgent() != "player-UA" {
					t.Error("wrong request")
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			client := newTestShareClient()
			client.AppURL = server.URL
			_, err := client.DownloadInfoWithUA(context.Background(), &ShareInfo{Code: "share"}, "1", "friend-cookie", "player-UA")
			if err == nil {
				t.Fatal("expected failure")
			}
			if calls != 1 {
				t.Fatal("share failure retried or used another endpoint")
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatal("wrong classification", err)
			}
			if tc.want == nil && errors.Is(err, ErrShareNotReadable) {
				t.Fatal("unsafe fallback")
			}
			reason := SharePlaybackFailureReason(err)
			if strings.Contains(reason, "private-secret") || strings.Contains(reason, "https://") || strings.Contains(reason, "secret-cookie") {
				t.Fatal("upstream detail leaked")
			}
		})
	}
}

func TestDirectShareURLCannotRequireCookie(t *testing.T) {
	for _, tc := range []struct {
		url     string
		allowed bool
	}{
		{"https://cdn.115.com/file", true}, {"https://cdn.115.com/file?f=1", true},
		{"https://cdn.115.com/file?f=3", false}, {"https://cdn.115.com/file?f=1&f=3", false},
		{"https://cdn.115.com/file?f=%XX", false}, {"https://evil.example/file", false},
	} {
		if AllowedDirectPlaybackURL(tc.url) != tc.allowed {
			t.Fatal("wrong URL validation")
		}
	}
}
