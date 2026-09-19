package gying

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResponseErrorsDistinguishUnavailableFromSession(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		detail     bool
		want       error
	}{
		{"版权下架", `{"code":403,"msg":"应版权方要求，已删除相关资源"}`, 200, true, ErrResourceUnavailable},
		{"HTTP403下架", `{"code":403,"msg":"资源已下架"}`, 403, true, ErrResourceUnavailable},
		{"字符串错误码", `{"code":"403","message":"资源不存在"}`, 200, true, ErrResourceUnavailable},
		{"明确未登录", `{"code":403,"msg":"未登录，访问受限"}`, 200, true, ErrForbidden},
		{"登录壳", `<script>_BT.PC.HTML('login')</script>`, 200, true, ErrForbidden},
		{"HTTP401", ``, 401, true, ErrForbidden},
		{"未知403", `{"code":403,"msg":"权限不足"}`, 200, true, ErrAccessDenied},
		{"裸403", ``, 403, false, ErrAccessDenied},
		{"限制页", `<html>Access denied</html>`, 403, true, ErrAccessDenied},
		{"限流", ``, 429, true, ErrAccessDenied},
		{"正常标题包含下架字样", `{"code":200,"panlist":{"name":["应版权方要求 未登录，访问受限"]}}`, 200, true, nil},
		{"正常搜索带登录控件", `<script>_obj.search={};_BT.PC.HTML('login')</script>`, 200, false, nil},
		{"验证信封", `{"code":419,"msg":"验证"}`, 200, false, ErrChallenge},
		{"服务器错误不是下架", `{"code":500,"msg":"temporary"}`, 200, true, ErrAccessDenied},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := responseError([]byte(tc.body), tc.status, tc.detail); !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
}

func TestUnavailableDetailsKeepOtherResultsAndFallback(t *testing.T) {
	for _, mode := range []string{"mixed", "all", "failed"} {
		t.Run(mode, func(t *testing.T) {
			queries := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/search":
					queries++
					if r.URL.Query().Get("q") == "tt0103369" {
						fmt.Fprint(w, `<script>_obj.search={"l":{"title":[],"d":[],"i":[]}};</script>`)
						return
					}
					fmt.Fprint(w, `<script>_obj.search={"l":{"title":["下架","正常"],"d":["mv","ac"],"i":["gone","ok"]}};</script>`)
				case "/res/downurl/mv/gone":
					fmt.Fprint(w, `{"code":403,"msg":"应版权方要求，已删除相关资源"}`)
				case "/res/downurl/ac/ok":
					if mode == "all" {
						fmt.Fprint(w, `{"code":403,"msg":"资源已下架"}`)
					} else if mode == "failed" {
						fmt.Fprint(w, `{"code":403,"msg":"权限不足"}`)
					} else {
						fmt.Fprint(w, `{"code":200,"panlist":{"url":["https://115.com/s/ok"]}}`)
					}
				default:
					t.Error("不应访问其他接口", r.URL.Path)
					w.WriteHeader(500)
				}
			}))
			defer server.Close()
			client, err := New(server.URL, nil)
			if err != nil {
				t.Fatal(err)
			}
			result, err := client.SearchByTitle(context.Background(), "美少女戦士セーラームーン", "tt0103369")
			if err != nil {
				t.Fatal(err)
			}
			if mode == "mixed" && (len(result.Items) != 1 || queries != 1 || result.UnavailableDetails != 1 || result.Partial) {
				t.Fatal(result, queries)
			}
			if mode == "all" && (len(result.Items) != 0 || queries != 2 || result.UnavailableDetails != 2 || result.Attempts[0].Unavailable != 2 || result.Partial) {
				t.Fatal(result, queries)
			}
			if mode == "failed" && (!result.Partial || result.FailedDetails != 1 || result.UnavailableDetails != 1 || queries != 2) {
				t.Fatal(result, queries)
			}
		})
	}
}

func TestUnknown403RemainsFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/search" {
			fmt.Fprint(w, `<script>_obj.search={"l":{"title":["权限不足"],"d":["ac"],"i":["one"]}};</script>`)
			return
		}
		fmt.Fprint(w, `{"code":403,"msg":"权限不足"}`)
	}))
	defer server.Close()
	client, err := New(server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Search(context.Background(), "测试")
	if !errors.Is(err, ErrAccessDenied) || errors.Is(err, ErrForbidden) {
		t.Fatal(err)
	}
}

func TestLoginNonJSONUsesTypedSafeError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/user/login" {
			fmt.Fprint(w, `<html>private-response cookie=secret</html>`)
			return
		}
		fmt.Fprint(w, `<html>home</html>`)
	}))
	defer server.Close()
	client, err := New(server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = client.Login(context.Background(), "user", "password")
	if !errors.Is(err, ErrLoginFormat) || errors.Is(err, ErrLogin) || strings.Contains(err.Error(), "private-response") {
		t.Fatal(err)
	}
}
