package p115

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

const batchMagnet = "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567"
const batchED2K = "ed2k://|file|movie.mkv|123|0123456789abcdef0123456789abcdef|/"

func TestCloudClientSubmitURLRequiresConfirmation(t *testing.T) {
	for _, example := range []struct {
		name string
		body string
		want error
	}{
		{"接受", `{"state":true,"data":{}}`, nil},
		{"匹配任务标识", `{"info_hash":"0123456789abcdef0123456789abcdef01234567"}`, nil},
		{"账号验证", `{"state":false,"errcode":911,"message":"敏感内容"}`, ErrCloudSubmissionRejected},
		{"内层拒绝", `{"state":true,"data":{"state":false,"errcode":911}}`, ErrCloudSubmissionRejected},
		{"外层非零错误码", `{"errno":911,"info_hash":"0123456789abcdef0123456789abcdef01234567"}`, ErrCloudSubmissionRejected},
		{"内层非零错误码", `{"state":true,"data":{"errno":0,"errcode":911,"info_hash":"0123456789abcdef0123456789abcdef01234567"}}`, ErrCloudSubmissionRejected},
		{"空响应", "", ErrCloudSubmissionUncertain},
		{"缺少确认字段", `{}`, ErrCloudSubmissionUncertain},
		{"响应截断", `{"state":true,`, ErrCloudSubmissionUncertain},
		{"加密数据损坏", `{"state":true,"data":"invalid"}`, ErrCloudSubmissionUncertain},
	} {
		t.Run(example.name, func(t *testing.T) {
			payload, err := json.Marshal(map[string]string{
				"ac": "add_task_url", "app_ver": "36.2.28", "wp_path_id": "123", "url": batchMagnet,
			})
			if err != nil {
				t.Fatal(err)
			}
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodPost || r.URL.Path != "/lixianssp/" {
					t.Errorf("单条提交请求有误: %s %s", r.Method, r.URL.Path)
				}
				if err := r.ParseForm(); err != nil || r.Form.Get("data") != RSAEncrypt(payload) {
					t.Error("单条提交参数有误")
				}
				_, _ = io.WriteString(w, example.body)
			}))
			defer server.Close()
			client := NewCloudClient()
			client.HTTP, client.BaseURL = server.Client(), server.URL
			err = client.SubmitURL(context.Background(), "UID=123_115A1", batchMagnet, "123")
			if !errors.Is(err, example.want) || requests.Load() != 1 {
				t.Fatalf("单条回执分类或请求次数有误: %v, %d", err, requests.Load())
			}
			if errors.Is(err, ErrCloudSubmissionRejected) && (!strings.Contains(err.Error(), "911") || strings.Contains(err.Error(), "敏感内容")) {
				t.Fatalf("拒绝结果未正确保留错误码或未脱敏: %v", err)
			}
		})
	}
}

func TestCloudClientAddURLsUsesOneSSPRequest(t *testing.T) {
	payload, err := json.Marshal(map[string]string{
		"ac": "add_task_urls", "app_ver": "36.2.28", "wp_path_id": "123",
		"url[0]": batchMagnet, "url[1]": batchED2K,
	})
	if err != nil {
		t.Fatal(err)
	}
	const cookie = "UID=123_115A1"
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/lixianssp/" || r.Header.Get("Cookie") != cookie {
			t.Errorf("批量提交请求有误: %s %s", r.Method, r.URL.Path)
		}
		if err := r.ParseForm(); err != nil || r.Form.Get("data") != RSAEncrypt(payload) || len(r.Form) != 1 {
			t.Error("提交没有按批量接口编码索引和统一目标目录")
		}
		_, _ = io.WriteString(w, `{"state":true,"data":{"result":[{"url":"`+batchED2K+`","state":true},{"url":"`+batchMagnet+`","state":false}]}}`)
	}))
	defer server.Close()
	client := NewCloudClient()
	client.HTTP, client.BaseURL = server.Client(), server.URL
	results, err := client.AddURLs(context.Background(), cookie, []string{" " + batchMagnet + " ", batchED2K}, " 123 ")
	want := []CloudURLAddResult{
		{URL: batchED2K, Confirmed: true},
		{URL: batchMagnet, Rejected: true},
	}
	if err != nil || !reflect.DeepEqual(results, want) || requests.Load() != 1 {
		t.Fatalf("批量请求或回执有误: 回执数量 %d，请求数量 %d，错误 %v", len(results), requests.Load(), err)
	}
}

func TestCloudClientAddURLsDistinguishesRejectedAndUnknown(t *testing.T) {
	for _, example := range []struct {
		name   string
		body   string
		status int
		want   error
	}{
		{"明确拒绝", `{"state":false,"errcode":10100,"message":"敏感内容"}`, http.StatusOK, ErrCloudSubmissionRejected},
		{"无结果数组", `{"state":true}`, http.StatusOK, ErrCloudSubmissionUncertain},
		{"字段格式损坏", `{"state":true,"result":{"info_hash":"x"}}`, http.StatusOK, ErrCloudSubmissionUncertain},
		{"响应截断", `{"state":true,`, http.StatusOK, ErrCloudSubmissionUncertain},
		{"HTTP 限速", "", http.StatusTooManyRequests, nil},
	} {
		t.Run(example.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(example.status)
				_, _ = io.WriteString(w, example.body)
			}))
			defer server.Close()
			client := NewCloudClient()
			client.HTTP, client.BaseURL = server.Client(), server.URL
			_, err := client.AddURLs(context.Background(), "UID=123_115A1", []string{batchMagnet, batchED2K}, "0")
			if example.want != nil {
				if !errors.Is(err, example.want) || strings.Contains(err.Error(), "敏感内容") {
					t.Fatalf("批量回执分类有误: %v", err)
				}
				return
			}
			var upstream *HTTPError
			if !errors.As(err, &upstream) || upstream.Status != http.StatusTooManyRequests || errors.Is(err, ErrCloudSubmissionUncertain) {
				t.Fatalf("没有保留 HTTP 限速信息: %v", err)
			}
		})
	}
}
