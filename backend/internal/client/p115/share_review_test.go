package p115

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPublicShareReviewMessagesStopWithoutRetry(t *testing.T) {
	for _, tc := range []struct {
		name, body     string
		review, failed bool
	}{
		{"错误提示", `{"state":false,"error":"该分享正在审核中，请稍后访问"}`, true, true},
		{"真实审核状态仍返回列表", `{"state":true,"errno":0,"error":"","data":{"share_state":0,"shareinfo":{"share_state":0,"forbid_reason":""},"count":1,"list":[{"cid":"123","n":"目录"}]}}`, true, true},
		{"嵌套字符串审核状态", `{"state":true,"data":{"shareinfo":{"share_state":"0"},"count":1,"list":[{"fid":"1","n":"film.mkv","s":"10"}]}}`, true, true},
		{"真实可用对照状态", `{"state":true,"data":{"share_state":1,"shareinfo":{"share_state":1},"count":0,"list":[]}}`, false, false},
		{"缺少审核字段不能默认零", `{"state":true,"data":{"shareinfo":{},"count":0,"list":[]}}`, false, false},
		{"审核字段为 null", `{"state":true,"data":{"share_state":null,"shareinfo":{"share_state":null},"count":0,"list":[]}}`, false, false},
		{"分享限制原因提示审核", `{"state":true,"data":{"shareinfo":{"forbid_reason":"该分享正在审核中"},"count":0,"list":[]}}`, true, true},
		{"message 提示", `{"state":0,"message":"分享内容等待审核"}`, true, true},
		{"数字 error 和 msg", `{"state":false,"error":123,"msg":"分享正在审核，请耐心等待"}`, true, true},
		{"独立错误字段", `{"state":false,"error_msg":"分享审核尚未完成"}`, true, true},
		{"成功状态附带审核提示", `{"state":true,"message":"分享审核中","data":{"count":0,"list":[]}}`, true, true},
		{"英文提示", `{"state":false,"message":"Share is under review"}`, true, true},
		{"分享已失效", `{"state":false,"errno":4100026,"error":"该文件分享链接不存在或已被删除"}`, false, true},
		{"提取码错误", `{"state":false,"error":"提取码错误"}`, false, true},
		{"审核已通过", `{"state":true,"message":"分享审核通过","data":{"count":0,"list":[]}}`, false, false},
		{"文件名不能触发审核", `{"state":true,"data":{"count":1,"list":[{"fid":"1","n":"待审核.mkv","s":"10"}]}}`, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Write([]byte(tc.body))
			}))
			defer server.Close()
			client := newTestShareClient()
			client.WebURL = server.URL
			client.ReadRetryDelays = []time.Duration{time.Millisecond, time.Millisecond}
			err := client.CheckPublicShareAccess(context.Background(), &ShareInfo{Code: "test", Password: "abcd"})
			if errors.Is(err, ErrShareUnderReview) != tc.review || (err != nil) != tc.failed || calls != 1 {
				t.Fatalf("审核分类或重试次数错误：%v，次数 %d", err, calls)
			}
		})
	}
}

func TestAnonymousCheckCannotUseOwnerSession(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "" {
			w.Write([]byte(`{"state":true,"data":{"count":0,"list":[]}}`))
			return
		}
		q := r.URL.Query()
		if r.Method != http.MethodGet || r.URL.Path != "/share/snap" || q.Get("limit") != "1" || q.Get("cid") != "0" || q.Get("receive_code") != "a+b&c" {
			t.Error("匿名检查参数错误")
		}
		w.Write([]byte(`{"state":false,"error":"分享审核中"}`))
	}))
	defer server.Close()
	client := newTestShareClient()
	client.WebURL = server.URL
	share := &ShareInfo{Code: "test", Password: "a+b&c"}
	if _, err := client.List(context.Background(), share, "0", 0, "owner-cookie"); err != nil {
		t.Fatal(err)
	}
	if err := client.CheckPublicShareAccess(context.Background(), share); !errors.Is(err, ErrShareUnderReview) {
		t.Fatal("所有者列表成功不能代表分享可访问", err)
	}
}

func TestHTTP405IsNotShareReview(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusMethodNotAllowed)
		w.Write([]byte(`{"error":"审核中"}`))
	}))
	defer server.Close()
	client := newTestShareClient()
	client.WebURL = server.URL
	err := client.CheckPublicShareAccess(context.Background(), &ShareInfo{Code: "test"})
	var upstream *HTTPError
	if errors.Is(err, ErrShareUnderReview) || !errors.As(err, &upstream) || upstream.Status != 405 {
		t.Fatal("HTTP 状态被误判成审核", err)
	}
}
