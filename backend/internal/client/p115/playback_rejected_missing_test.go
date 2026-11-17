package p115

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestPlaybackRejectedLinkRequiresExactMissingProof(t *testing.T) {
	for _, method := range []string{"verified-url", "instant-info"} {
		for _, mode := range []string{"missing-code", "deleted-area", "exists", "wrong-id", "empty", "exact-rejected", "exact-login", "exact-throttled", "exact-network", "download-login", "download-network"} {
			t.Run(method+"/"+mode, func(t *testing.T) {
				c := newTestShareClient()
				expected := DrivePlaybackFile{ID: "123", PickCode: "pick123", Size: 100, SHA1: strings.Repeat("A", 40)}
				downloads, exact := 0, 0
				c.HTTP.Transport = metadataTransport(func(r *http.Request) (*http.Response, error) {
					if r.Header.Get("Cookie") != "UID=22_A" {
						t.Error("精确核验替换了固定凭据")
					}
					switch r.URL.Path {
					case "/app/chrome/downurl":
						downloads++
						if mode == "download-login" {
							return instantResponse(200, []byte(`{"state":false,"errno":990001}`)), nil
						}
						if mode == "download-network" {
							return nil, errors.New("network unavailable")
						}
						return instantResponse(200, []byte(`{"state":false,"code":50015}`)), nil
					case "/files/get_info":
						exact++
						if r.Method != http.MethodGet || r.URL.Query().Get("file_id") != expected.ID {
							t.Error("未对原固定文件 ID 进行只读精确查询")
						}
						body := fmt.Sprintf(`{"state":true,"data":[{"aid":1,"fid":"123","pc":"pick123","sha":"%s","s":100}]}`, expected.SHA1)
						switch mode {
						case "missing-code":
							body = `{"state":false,"code":20018}`
						case "deleted-area":
							body = `{"state":true,"data":[{"aid":120,"fid":"123"}]}`
						case "wrong-id":
							body = `{"state":true,"data":[{"aid":120,"fid":"124"}]}`
						case "empty":
							body = `{"state":true,"data":[]}`
						case "exact-rejected":
							body = `{"state":false,"code":99}`
						case "exact-login":
							body = `{"state":false,"errno":990001}`
						case "exact-throttled":
							body = `{"state":false,"error":"请求频繁"}`
						case "exact-network":
							return nil, errors.New("network unavailable")
						}
						return instantResponse(200, []byte(body)), nil
					default:
						t.Errorf("拒绝核验访问了其他接口 %s", r.URL.Path)
						return instantResponse(500, nil), nil
					}
				})
				var err error
				if method == "verified-url" {
					_, err = c.VerifiedPlaybackURL(context.Background(), expected, "UID=22_A", "player-UA")
				} else {
					_, err = c.PickCodeInstantInfoForFile(context.Background(), expected, "UID=22_A")
				}
				wantMissing := mode == "missing-code" || mode == "deleted-area"
				if err == nil || errors.Is(err, ErrDriveFileMissing) != wantMissing {
					t.Fatal("拒绝或不确定响应被误当成文件缺失", err)
				}
				wantExact := 1
				if strings.HasPrefix(mode, "download-") {
					wantExact = 0
				}
				if downloads != 1 || exact != wantExact {
					t.Fatal("精确核验时机或次数不正确", downloads, exact)
				}
				if (mode == "download-login" || mode == "exact-login") && !errors.Is(err, ErrShareLoginRequired) {
					t.Fatal("鉴权失败分类丢失", err)
				}
				if mode == "exact-throttled" && !errors.Is(err, ErrShareThrottled) {
					t.Fatal("限流分类丢失", err)
				}
				if mode == "exists" && !errors.Is(err, ErrDriveRejected) {
					t.Fatal("文件仍存在时吞掉了原取链拒绝", err)
				}
			})
		}
	}
}

func TestPickCodeWithoutFileIDNeverInfersMissingFromRejection(t *testing.T) {
	c := newTestShareClient()
	calls := 0
	c.HTTP.Transport = metadataTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Path != "/app/chrome/downurl" {
			t.Error("没有固定 ID 仍尝试精确查询")
		}
		return instantResponse(200, []byte(`{"state":false,"code":50015}`)), nil
	})
	_, err := c.PickCodeInstantInfo(context.Background(), "pick123", "UID=22_A")
	if !errors.Is(err, ErrDriveRejected) || errors.Is(err, ErrDriveFileMissing) || calls != 1 {
		t.Fatal("无固定 ID 的泛拒绝被误判为不存在", err, calls)
	}
}
