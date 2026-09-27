package p115

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestInstantSubmissionFailuresKeepSafeDiagnostic(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
		http             int
		uncertain        bool
	}{
		{"http401", "", "HTTP 401", 401, true},
		{"http429", "", "HTTP 429", 429, true},
		{"timeout", "", "请求超时", 0, true},
		{"connection", "", "秒传接口连接", 0, true},
		{"decrypt", "", "AES 解密失败", 200, true},
		{"lz4", "", "LZ4 解压失败", 200, true},
		{"unsupported", `{"status":1,"statuscode":0,"message":"SEID=secret"}`, "要求普通上传", 200, false},
		{"rejected", `{"status":0,"statuscode":701,"message":"SEID=secret"}`, "status=0，statuscode=701", 200, false},
		{"missing-id", `{"status":2}`, "缺少有效 file_id 和 pickcode", 200, true},
		{"range", `{"status":7,"sign_key":"secret","sign_check":"0-999"}`, "校验范围", 200, false},
		{"malformed", `{"status":true,"message":"SEID=secret"}`, "JSON 格式", 200, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestShareClient()
			posts := 0
			c.HTTP.Transport = metadataTransport(func(r *http.Request) (*http.Response, error) {
				switch r.URL.Path {
				case "/files":
					return instantResponse(200, []byte(`{"state":true,"cid":"0"}`)), nil
				case "/files/uploadinfo":
					return instantResponse(200, []byte(`{"user_id":22,"userkey":"secret"}`)), nil
				case "/4.0/initupload.php":
					posts++
					if tc.name == "timeout" {
						return nil, context.DeadlineExceeded
					}
					if tc.name == "connection" {
						return nil, errors.New("transport secret")
					}
					body := []byte("invalid")
					if tc.name == "lz4" {
						var err error
						body, err = AESEncryptCBC([]byte{255, 255, 0})
						if err != nil {
							t.Fatal(err)
						}
					}
					if tc.body != "" {
						body = encryptedInstantResponse(t, tc.body)
					}
					return instantResponse(tc.http, body), nil
				default:
					t.Fatal("unexpected request", r.URL.Path)
					return nil, nil
				}
			})
			_, err := c.InstantUpload(context.Background(), testInstantInfo(), "0", "UID=22_A; SEID=secret", nil)
			reason := InstantUploadFailureReason(err)
			if err == nil || posts != 1 || !strings.Contains(reason, "第 1 次秒传提交") || !strings.Contains(reason, tc.want) {
				t.Fatal("lost failure stage", reason, posts)
			}
			if errors.Is(err, ErrInstantUncertain) != tc.uncertain {
				t.Fatal("uncertainty classification changed", err)
			}
			if strings.Contains(reason, "secret") || strings.Contains(reason, "UID=") {
				t.Fatal("secret leaked", reason)
			}
		})
	}
}

func TestInstantDiagnosticIgnoresUntrustedText(t *testing.T) {
	if got := instantResponseCodes([]byte(`{"status":"secret","statuscode":"701","errno":401,"error":"SEID=secret"}`)); got != "statuscode=701，errno=401" {
		t.Fatal(got)
	}
	if got := InstantUploadFailureReason(errors.New("Cookie=secret")); strings.Contains(got, "secret") {
		t.Fatal(got)
	}
}
