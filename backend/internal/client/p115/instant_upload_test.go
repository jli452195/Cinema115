package p115

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func instantResponse(status int, raw []byte) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(raw))}
}

func encryptedInstantResponse(t *testing.T, value string, tails ...[]byte) []byte {
	t.Helper()
	// A literal-only LZ4 block, independently framed with a 16-bit length and terminator.
	block := []byte{byte(min(len(value), 15) << 4)}
	if len(value) >= 15 {
		n := len(value) - 15
		for n >= 255 {
			block = append(block, 255)
			n -= 255
		}
		block = append(block, byte(n))
	}
	block = append(block, []byte(value)...)
	framed := make([]byte, 2)
	binary.LittleEndian.PutUint16(framed, uint16(len(block)))
	framed = append(framed, block...)
	tail := []byte{0, 0}
	if len(tails) > 0 {
		tail = tails[0]
	}
	framed = append(framed, tail...)
	result, err := AESEncryptCBC(framed)
	if err != nil {
		t.Fatal(err)
	}
	// Real initupload responses can include a non-AES trailer.
	return append(result, []byte("extra-tail12")...)
}

func testInstantInfo() *DownloadInfo {
	return &DownloadInfo{Name: "video.mkv", Size: 100, SHA1: strings.Repeat("A", 40), URL: "https://cdn.115cdn.net/video?secret=private"}
}

func TestInstantUploadChallengeAndDestinationIsolation(t *testing.T) {
	client := NewShareClient()
	posts := 0
	ranges := 0
	client.HTTP.Transport = metadataTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "cdn.115cdn.net" {
			ranges++
			if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" || r.Header.Get("Range") != "bytes=1-3" || r.Header.Get("User-Agent") != PlaybackUserAgent {
				t.Fatal("片段请求凭据或范围错误")
			}
			resp := instantResponse(206, []byte("abc"))
			resp.Header.Set("Content-Range", "bytes 1-3/100")
			return resp, nil
		}
		if r.Header.Get("Cookie") != "UID=22_A; SEID=destination-secret" {
			t.Fatal("未固定目标凭据")
		}
		switch r.URL.Path {
		case "/files":
			return instantResponse(200, []byte(`{"state":true,"cid":"0","path":[{"cid":"0"}]}`)), nil
		case "/files/uploadinfo":
			return instantResponse(200, []byte(`{"user_id":22,"userkey":"target-userkey"}`)), nil
		case "/4.0/initupload.php":
			posts++
			body, _ := io.ReadAll(r.Body)
			plain, err := AESDecryptCBC(body)
			if err != nil {
				t.Fatal(err)
			}
			form, err := url.ParseQuery(string(plain))
			if err != nil {
				t.Fatal(err)
			}
			if form.Get("userid") != "22" || form.Get("target") != "U_1_0" || form.Get("userkey") != "target-userkey" || form.Get("fileid") != strings.Repeat("A", 40) || r.URL.Query().Get("k_ec") == "" {
				t.Fatal("上传账号或源元数据错误")
			}
			if posts == 1 {
				if form.Get("sign_val") != "" {
					t.Fatal("首请求不应包含片段哈希")
				}
				return instantResponse(200, encryptedInstantResponse(t, `{"status":7,"sign_key":"key1","sign_check":"1-3"}`)), nil
			}
			if form.Get("sign_key") != "key1" || form.Get("sign_val") != "A9993E364706816ABA3E25717850C26C9CD0D89D" {
				t.Fatal("二次校验未提交正确片段 SHA1")
			}
			return instantResponse(200, encryptedInstantResponse(t, `{"status":2,"pickcode":"abc12345","file_id":"999"}`)), nil
		default:
			t.Fatalf("出现非预期请求: %s", r.URL.Path)
			return nil, nil
		}
	})
	result, err := client.InstantUpload(context.Background(), testInstantInfo(), "0", "UID=22_A; SEID=destination-secret", nil)
	if err != nil || result == nil || result.FileID != "999" || !result.Challenged || posts != 2 || ranges != 1 {
		t.Fatalf("result=%+v err=%v posts=%d ranges=%d", result, err, posts, ranges)
	}
}

func TestInstantUploadNeverFallsBackToFullUploadOrReplaysWrites(t *testing.T) {
	for _, mode := range []string{"ordinary", "timeout", "missing_id", "rejected_directory", "wrong_user", "redirect", "repeated_challenge"} {
		t.Run(mode, func(t *testing.T) {
			client := NewShareClient()
			posts := 0
			client.HTTP.Transport = metadataTransport(func(r *http.Request) (*http.Response, error) {
				switch r.URL.Path {
				case "/files":
					if mode == "rejected_directory" {
						return instantResponse(200, []byte(`{"state":true,"cid":"5"}`)), nil
					}
					return instantResponse(200, []byte(`{"state":true,"cid":"0"}`)), nil
				case "/files/uploadinfo":
					if mode == "wrong_user" {
						return instantResponse(200, []byte(`{"user_id":"99","userkey":"key"}`)), nil
					}
					return instantResponse(200, []byte(`{"user_id":"22","userkey":"key"}`)), nil
				case "/4.0/initupload.php":
					posts++
					if mode == "timeout" {
						return nil, errors.New("private-userkey-cookie-signed-url")
					}
					if mode == "redirect" {
						resp := instantResponse(302, nil)
						resp.Header.Set("Location", "https://evil.test/secret")
						return resp, nil
					}
					response := `{"status":1,"pickcode":"abc12345","host":"https://oss.test"}`
					if mode == "missing_id" {
						response = `{"status":2}`
					}
					if mode == "repeated_challenge" {
						response = `{"status":7,"sign_key":"key","sign_check":"1-3"}`
					}
					return instantResponse(200, encryptedInstantResponse(t, response)), nil
				case "/video":
					resp := instantResponse(206, []byte("abc"))
					resp.Header.Set("Content-Range", "bytes 1-3/100")
					return resp, nil
				default:
					t.Fatal("不应发送普通上传或跟随 API 重定向")
					return nil, nil
				}
			})
			result, err := client.InstantUpload(context.Background(), testInstantInfo(), "0", "UID=22_A", nil)
			if result != nil || err == nil || strings.Contains(err.Error(), "private") {
				t.Fatalf("不正确的结果: %+v %v", result, err)
			}
			if mode == "ordinary" && !errors.Is(err, ErrInstantUnsupported) {
				t.Fatal(err)
			}
			if (mode == "timeout" || mode == "missing_id" || mode == "redirect") && !errors.Is(err, ErrInstantUncertain) {
				t.Fatal(err)
			}
			want := 1
			if mode == "wrong_user" || mode == "rejected_directory" {
				want = 0
			}
			if mode == "repeated_challenge" {
				want = 2
			}
			if posts != want {
				t.Fatalf("写请求重复: %d", posts)
			}
		})
	}
}

func TestInstantRangeLimitsAndResponseValidation(t *testing.T) {
	for _, value := range []string{"-10", "1-", "3-1", "0-100", "0-99999999999999999999999", "0-3,5-9", "0-8388608"} {
		if _, _, err := parseInstantRange(value, 100); err == nil {
			t.Errorf("接受异常范围 %s", value)
		}
	}
	for _, mode := range []string{"200", "wrong_total", "wrong_offset", "short", "long", "compressed", "redirect"} {
		t.Run(mode, func(t *testing.T) {
			client := NewShareClient()
			calls := 0
			client.HTTP.Transport = metadataTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				resp := instantResponse(206, []byte("abc"))
				resp.Header.Set("Content-Range", "bytes 1-3/100")
				switch mode {
				case "200":
					resp.StatusCode = 200
					resp.Body = io.NopCloser(failIfRead{t})
				case "wrong_total":
					resp.Header.Set("Content-Range", "bytes 1-3/101")
				case "wrong_offset":
					resp.Header.Set("Content-Range", "bytes 0-2/100")
				case "short":
					resp.Body = io.NopCloser(strings.NewReader("ab"))
				case "long":
					resp.Body = io.NopCloser(strings.NewReader("abcd"))
				case "compressed":
					resp.Header.Set("Content-Encoding", "gzip")
				case "redirect":
					resp.StatusCode = 302
					resp.Header.Set("Location", "http://127.0.0.1/private")
				}
				return resp, nil
			})
			if _, err := client.instantRangeHash(context.Background(), testInstantInfo().URL, 1, 3, 100); err == nil {
				t.Fatal("接受不正确 Range")
			}
			if calls != 1 {
				t.Fatal("不应跟随未知主机")
			}
		})
	}
	info := testInstantInfo()
	raw, _ := json.Marshal(info)
	if bytes.Contains(raw, []byte("private")) || bytes.Contains(raw, []byte("url")) {
		t.Fatal("直链被序列化")
	}
}

type failIfRead struct{ t *testing.T }

func (r failIfRead) Read([]byte) (int, error) {
	r.t.Fatal("上游忽略 Range 时不应读取媒体")
	return 0, io.EOF
}
