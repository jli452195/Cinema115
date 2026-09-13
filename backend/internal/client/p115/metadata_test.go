package p115

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type metadataTransport func(*http.Request) (*http.Response, error)

func (f metadataTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestMetadataDownloadBoundsAndHeaders(t *testing.T) {
	for _, kind := range []string{"success", "http-error", "oversize", "redirect", "cancelled"} {
		t.Run(kind, func(t *testing.T) {
			calls := 0
			c := NewShareClient()
			c.HTTP.Transport = metadataTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" || r.UserAgent() != PlaybackUserAgent {
					t.Fatal("下载携带登录凭证或 User-Agent 不一致")
				}
				if r.Context().Err() != nil {
					return nil, r.Context().Err()
				}
				response := &http.Response{StatusCode: 200, Header: make(http.Header), ContentLength: 3, Body: io.NopCloser(strings.NewReader("srt")), Request: r}
				switch kind {
				case "http-error":
					response.StatusCode = 403
				case "oversize":
					response.ContentLength = MaxMetadataSize + 1
				case "redirect":
					response.StatusCode = 302
					response.Header.Set("Location", "http://127.0.0.1/private")
				}
				return response, nil
			})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if kind == "cancelled" {
				cancel()
			}
			data, err := c.readMetadataURL(ctx, "https://cdn.115cdn.com/subtitle?token=private")
			if kind == "success" {
				if err != nil || string(data) != "srt" {
					t.Fatal(err)
				}
			} else if err == nil {
				t.Fatal("异常下载未拦截")
			}
			if kind == "cancelled" && !errors.Is(err, context.Canceled) {
				t.Fatal("取消未传递")
			}
			if err != nil && strings.Contains(err.Error(), "token=private") {
				t.Fatal("泄漏下载签名")
			}
			if calls > 1 {
				t.Fatal("请求了不允许的重定向地址")
			}
		})
	}
}
