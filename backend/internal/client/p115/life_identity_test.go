package p115

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLifeEventParentChecksExactIdentityAndParentAliases(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
		missing          bool
	}{
		{name: "compact file", body: `{"state":true,"data":[{"aid":1,"fid":"300","cid":"200"}]}`, want: "200"},
		{name: "long file", body: `{"state":true,"data":[{"file_id":300,"category_id":200}]}`, want: "200"},
		{name: "mixed aliases", body: `{"state":true,"data":[{"fid":"300","file_id":300,"cid":"200","category_id":200,"parent_id":200}]}`, want: "200"},
		{name: "root file", body: `{"state":true,"data":[{"fid":"300","cid":0}]}`, want: "0"},
		{name: "directory", body: `{"state":true,"data":[{"cid":"300","pid":"200"}]}`, want: "200"},
		{name: "zero fid directory", body: `{"state":true,"data":[{"fid":0,"cid":300,"pid":0}]}`, want: "0"},
		{name: "long directory", body: `{"state":true,"data":[{"category_id":300,"parent_id":200}]}`, want: "200"},
		{name: "conflicting file aliases", body: `{"state":true,"data":[{"fid":300,"file_id":301,"cid":200}]}`},
		{name: "conflicting parent aliases", body: `{"state":true,"data":[{"fid":300,"cid":200,"category_id":201}]}`},
		{name: "conflicting directory aliases", body: `{"state":true,"data":[{"cid":300,"category_id":301,"pid":200}]}`},
		{name: "duplicate identity", body: `{"state":true,"data":[{"fid":300,"fid":301,"cid":200}]}`},
		{name: "directory without parent", body: `{"state":true,"data":[{"cid":300}]}`},
		{name: "file without parent", body: `{"state":true,"data":[{"fid":300}]}`},
		{name: "redirect", body: `{"state":true,"data":[{"fid":301,"cid":200}]}`},
		{name: "empty success", body: `{"state":true,"data":[]}`},
		{name: "extra row", body: `{"state":true,"data":[{"fid":300,"cid":200},{"fid":301,"cid":200}]}`},
		{name: "self parent", body: `{"state":true,"data":[{"fid":300,"cid":300}]}`},
		{name: "wrong account", body: `{"state":true,"uid":43,"data":[{"fid":300,"cid":200}]}`},
		{name: "unknown area", body: `{"state":true,"data":[{"aid":99,"fid":300,"cid":200}]}`},
		{name: "tombstone", body: `{"state":true,"data":[{"aid":120,"fid":300}]}`, missing: true},
		{name: "wrong tombstone", body: `{"state":true,"data":[{"aid":120,"fid":301}]}`},
		{name: "missing error", body: `{"state":false,"errno":20018}`, missing: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.Method != http.MethodGet || r.URL.Path != "/files/get_info" || r.URL.Query().Get("file_id") != "300" || len(r.URL.Query()) != 1 || r.ContentLength > 0 || r.Header.Get("Cookie") != "UID=42_A" {
					t.Error("父目录核验请求不符合只读精确查询约定")
				}
				fmt.Fprint(w, tc.body)
			}))
			defer server.Close()
			client := newTestShareClient()
			client.WebURL = server.URL
			client.HTTP = server.Client()
			parent, err := client.LifeEventParent(context.Background(), "300", "UID=42_A")
			if tc.want != "" {
				if err != nil || parent != tc.want {
					t.Fatal(parent, err)
				}
			} else if tc.missing {
				if !errors.Is(err, ErrDriveFileMissing) {
					t.Fatal("明确删除未保留类型", err)
				}
			} else if !errors.Is(err, ErrLifeEventIdentity) {
				t.Fatal("歧义身份未拒绝", parent, err)
			}
			if requests != 1 {
				t.Fatal("精确查询重复请求", requests)
			}
		})
	}
}

func TestLifeEventParentPreservesCancellationAndUpstreamFailures(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{name: "login", status: 200, body: `{"state":false,"errno":990001}`, want: ErrShareLoginRequired},
		{name: "unknown missing", status: 200, body: `{"state":false,"errno":20004}`, want: ErrDriveRejected},
		{name: "http missing", status: 404, body: `{}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); fmt.Fprint(w, tc.body) }))
			defer server.Close()
			client := newTestShareClient()
			client.WebURL = server.URL
			client.HTTP = server.Client()
			_, err := client.LifeEventParent(context.Background(), "300", "UID=42_A")
			if err == nil || errors.Is(err, ErrLifeEventIdentity) || errors.Is(err, ErrDriveFileMissing) || tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatal("请求失败被当成身份结论", err)
			}
		})
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer server.Close()
	client := newTestShareClient()
	client.WebURL = server.URL
	client.HTTP = server.Client()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := client.LifeEventParent(ctx, "300", "UID=42_A"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("请求未传递超时", err)
	}
	if _, err := client.LifeEventParent(context.Background(), "../300", "UID=42_A"); !errors.Is(err, ErrLifeEventIdentity) {
		t.Fatal("接受了无效文件ID", err)
	}
}
