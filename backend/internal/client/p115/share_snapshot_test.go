package p115

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSnapshotPendingClassificationAndCookie(t *testing.T) {
	for _, tc := range []struct {
		name, path, body string
		pending, review  bool
	}{
		{"pending", "/share/snap", `{"state":false,"error":"正在生成文件快照"}`, true, false},
		{"message", "/share/snap", `{"state":false,"message":"正在生成文件快照"}`, true, false},
		{"login", "/share/snap", `{"state":false,"error":"请重新登录"}`, false, false},
		{"review", "/share/snap", `{"state":false,"error":"分享审核中"}`, false, true},
		{"other_endpoint", "/files", `{"state":false,"error":"正在生成文件快照"}`, false, false},
		{"filename", "/share/snap", `{"state":true,"data":{"count":1,"list":[{"fid":"1","n":"正在生成文件快照.mkv","s":100}]}}`, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Header.Get("Cookie") != "UID=42_A; SEID=test" {
					t.Error("快照请求缺少指定 Cookie")
				}
				_, _ = w.Write([]byte(tc.body))
			}))
			defer upstream.Close()
			client := newTestShareClient()
			_, err := client.requestEnvelope(context.Background(), http.MethodGet, upstream.URL+tc.path, "UID=42_A; SEID=test", nil)
			if errors.Is(err, ErrShareSnapshotPending) != tc.pending || errors.Is(err, ErrShareUnderReview) != tc.review {
				t.Fatalf("错误分类不正确: %v", err)
			}
			if calls != 1 {
				t.Fatalf("底层不应自动重试业务响应: %d", calls)
			}
			if tc.name == "filename" && err != nil {
				t.Fatalf("文件名被误判: %v", err)
			}
		})
	}
}
