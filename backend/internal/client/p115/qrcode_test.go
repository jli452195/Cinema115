package p115

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestQRCodeConfirmationAndCookieExchange(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/1.0/web/1.0/token/":
			w.Write([]byte(`{"state":1,"data":{"uid":"test-id","time":123,"sign":"test-sign","qrcode":"https://115.com/scan/test-id"}}`))
		case "/get/status/":
			if r.URL.Query().Get("time") != "123" || r.URL.Query().Get("sign") != "test-sign" {
				t.Error("轮询缺少会话参数")
			}
			w.Write([]byte(`{"state":1,"data":{"status":2}}`))
		case "/app/1.0/wechatmini/1.0/login/qrcode/":
			if err := r.ParseForm(); err != nil {
				t.Error(err)
			}
			if r.Method != "POST" || r.Form.Get("account") != "test-id" {
				t.Error("兑换请求参数错误")
			}
			w.Write([]byte(`{"state":true,"data":{"cookie":{"UID":"test-user","CID":"test-cid","SEID":"test-seid"}}}`))
		default:
			t.Error("非预期请求路径")
			w.WriteHeader(404)
		}
	}))
	defer upstream.Close()
	client := NewClient()
	client.BaseURL = upstream.URL
	qr, err := client.CreateQRCode(context.Background(), "wechatmini")
	if err != nil {
		t.Fatal(err)
	}
	status, err := client.CheckQRCode(context.Background(), qr.ID)
	if err != nil || status.Status != "success" {
		t.Fatalf("确认状态错误: %v", err)
	}
	cookie, err := client.CompleteQRCodeLogin(context.Background(), qr.ID)
	if err != nil || !strings.Contains(cookie, "UID=test-user;") || !strings.Contains(cookie, "SEID=test-seid;") {
		t.Fatalf("未正确解析 Cookie 对象: %v", err)
	}
}

func TestQRCodeWaitingIsNotScanned(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"state":1,"data":{"status":0}}`)) }))
	defer upstream.Close()
	client := NewClient()
	client.BaseURL = upstream.URL
	client.sessions["test"] = session{Time: 123, Sign: "sign", App: "alipaymini"}
	status, err := client.CheckQRCode(context.Background(), "test")
	if err != nil || status.Status != "waiting" {
		t.Fatalf("请求成功被误判为已扫码: %v", err)
	}
}
