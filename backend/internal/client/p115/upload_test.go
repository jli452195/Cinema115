package p115

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type subtitleRoundTrip func(*http.Request) (*http.Response, error)

func (f subtitleRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSubtitleUploadProtocol(t *testing.T) {
	client := NewShareClient()
	client.RequestInterval = 0
	requests := 0
	client.HTTP.Transport = subtitleRoundTrip(func(r *http.Request) (*http.Response, error) {
		requests++
		body := `{"state":true}`
		if requests == 1 {
			if r.URL.String() != "https://uplb.115.com/3.0/sampleinitupload.php" || r.Header.Get("Cookie") != "UID=selected-account" {
				t.Fatalf("wrong initialization")
			}
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if r.PostForm.Get("target") != "U_1_12345" || r.PostForm.Get("filename") != "剧集.zh-CN.srt" || r.PostForm.Get("filesize") != "8" {
				t.Fatalf("form=%v", r.PostForm)
			}
			body = `{"host":"http://bucket.oss-cn-shenzhen.aliyuncs.com/","object":"object-key","policy":"policy-secret","accessid":"access-id","callback":"callback-secret","signature":"signature-secret"}`
		} else {
			if r.URL.Scheme != "https" || r.URL.Host != "bucket.oss-cn-shenzhen.aliyuncs.com" || r.Header.Get("Cookie") != "" {
				t.Fatal("invalid storage destination or leaked cookie")
			}
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Fatal(err)
			}
			defer r.MultipartForm.RemoveAll()
			for key, value := range map[string]string{"key": "object-key", "policy": "policy-secret", "signature": "signature-secret", "callback": "callback-secret", "OSSAccessKeyId": "access-id", "success_action_status": "200"} {
				if r.FormValue(key) != value {
					t.Errorf("wrong %s", key)
				}
			}
			file, header, err := r.FormFile("file")
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			data, err := io.ReadAll(file)
			if err != nil || string(data) != "subtitle" || header.Filename != "剧集.zh-CN.srt" {
				t.Fatal("upload payload changed")
			}
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: r}, nil
	})
	if err := client.UploadSubtitle(context.Background(), "12345", "剧集.zh-CN.srt", []byte("subtitle"), "UID=selected-account"); err != nil {
		t.Fatal(err)
	}
	if requests != 2 {
		t.Fatal("unexpected retries")
	}
}

func TestSubtitleUploadRejectsUnsafeHostAndDoesNotRetry(t *testing.T) {
	for _, host := range []string{"https://127.0.0.1/", "https://evil.test/", "https://aliyuncs.com.evil.test/", "https://u:p@bucket.aliyuncs.com/", "https://bucket.aliyuncs.com:8443/"} {
		t.Run(host, func(t *testing.T) {
			client := NewShareClient()
			client.RequestInterval = 0
			calls := 0
			client.HTTP.Transport = subtitleRoundTrip(func(r *http.Request) (*http.Response, error) {
				calls++
				data, _ := json.Marshal(map[string]any{"host": host, "object": "x", "policy": "x", "accessid": "x", "signature": "x", "callback": "x"})
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(data))), Header: make(http.Header)}, nil
			})
			if err := client.UploadSubtitle(context.Background(), "1", "test.srt", []byte("x"), "cookie"); err == nil || calls != 1 {
				t.Fatalf("err=%v calls=%d", err, calls)
			}
		})
	}
	client := NewShareClient()
	client.RequestInterval = 0
	calls := 0
	client.HTTP.Transport = subtitleRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 429, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("signature-secret"))}, nil
	})
	err := client.UploadSubtitle(context.Background(), "1", "test.srt", []byte("x"), "secret-cookie")
	if err == nil || calls != 1 || strings.Contains(err.Error(), "secret") {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}
