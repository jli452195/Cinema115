package p115

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAccountCheckCookie(t *testing.T) {
	for _, tc := range []struct {
		name, body     string
		code           int
		valid, wantErr bool
	}{
		{"布尔有效", `{"state":true}`, 200, true, false},
		{"数值有效", `{"state":1}`, 200, true, false},
		{"布尔失效", `{"state":false}`, 200, false, false},
		{"数值失效", `{"state":0}`, 200, false, false},
		{"字段缺失", `{}`, 200, false, true},
		{"字段空值", `{"state":null}`, 200, false, true},
		{"字段异常", `{"state":"true"}`, 200, false, true},
		{"非法响应", `<html>`, 200, false, true},
		{"服务错误", `unavailable`, 503, false, true},
		{"跳转登录", ``, 302, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Cookie") != "UID=test; CID=test; SEID=test" || r.Header.Get("User-Agent") == "" {
					t.Error("缺少登录验证所需的请求头")
				}
				w.WriteHeader(tc.code)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			client := NewAccountClient()
			client.StatusURL = server.URL
			valid, err := client.CheckCookie(context.Background(), "UID=test; CID=test; SEID=test")
			if valid != tc.valid || (err != nil) != tc.wantErr {
				t.Fatalf("valid=%v err=%v", valid, err)
			}
		})
	}
}

func TestAccountRejectsEmptyAndMultilineCookie(t *testing.T) {
	// 使用未配置的客户端，确保无效输入不会发起任何网络请求。
	client := &AccountClient{}
	for _, value := range []string{"", "  ", "UID=test\r\nX-Test: value", "UID=test\nCID=test"} {
		valid, err := client.CheckCookie(context.Background(), value)
		if valid || err != nil {
			t.Fatalf("空值和多行输入应判为无效：%v", err)
		}
	}
}

func TestAccountRespectsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client := NewAccountClient()
	valid, err := client.CheckCookie(ctx, "UID=test")
	if valid || !errors.Is(err, context.Canceled) {
		t.Fatalf("未保留请求取消错误：%v", err)
	}
}
