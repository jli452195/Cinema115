package p115

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestRenameEntriesWithReceiptRequiresExactCompleteReport(t *testing.T) {
	const first, second = "3516637914517734609", "3516637914517734610"
	const names = `"3516637914517734610":"作品 & 字幕.zh-CN.ass","3516637914517734609":"作品 & 字幕.mkv"`
	entries := []RenameTarget{{first, "作品 & 字幕.mkv"}, {second, "作品 & 字幕.zh-CN.ass"}}
	for _, tc := range []struct {
		name, body string
		confirmed  bool
		failed     bool
	}{
		{"complete", `{"state":true,"error":"","errno":"","data":{` + names + `}}`, true, false},
		{"numeric-state", `{"state":1,"code":"0","data":{` + names + `}}`, true, false},
		{"message", `{"state":true,"msg":"成功","message":"","data":{` + names + `}}`, true, false},
		{"state-only", `{"state":true}`, false, false},
		{"null-data", `{"state":true,"data":null}`, false, false},
		{"empty-object", `{"state":true,"data":{}}`, false, false},
		{"empty-array", `{"state":true,"data":[]}`, false, false},
		{"partial", `{"state":true,"data":{"3516637914517734609":"作品 & 字幕.mkv"}}`, false, false},
		{"wrong-name", `{"state":true,"data":{"3516637914517734609":"other.mkv","3516637914517734610":"作品 & 字幕.zh-CN.ass"}}`, false, false},
		{"wrong-id", `{"state":true,"data":{"3516637914517734611":"作品 & 字幕.mkv","3516637914517734610":"作品 & 字幕.zh-CN.ass"}}`, false, false},
		{"extra-id", `{"state":true,"data":{` + names + `,"3516637914517734611":"other.mkv"}}`, false, false},
		{"duplicate-id", `{"state":true,"data":{` + names + `,"3516637914517734609":"作品 & 字幕.mkv"}}`, false, false},
		{"item-status", `{"state":true,"data":{"3516637914517734609":{"state":true},"3516637914517734610":{"state":true}}}`, false, false},
		{"array", `{"state":true,"data":["作品 & 字幕.mkv","作品 & 字幕.zh-CN.ass"]}`, false, false},
		{"encoded-object", `{"state":true,"data":"{}"}`, false, false},
		{"null-name", `{"state":true,"data":{"3516637914517734609":null,"3516637914517734610":"作品 & 字幕.zh-CN.ass"}}`, false, false},
		{"unknown-field", `{"state":true,"task_id":"123","data":{` + names + `}}`, false, false},
		{"unknown-false-field", `{"state":true,"async":false,"data":{` + names + `}}`, false, false},
		{"duplicate-state", `{"state":true,"state":true,"data":{` + names + `}}`, false, false},
		{"duplicate-data", `{"state":true,"data":{},"data":{` + names + `}}`, false, false},
		{"contradictory-code", `{"state":true,"errno":1,"data":{` + names + `}}`, false, false},
		{"error-field", `{"state":true,"error":"partial","data":{` + names + `}}`, false, false},
		{"invalid-message", `{"state":true,"message":{},"data":{` + names + `}}`, false, false},
		{"invalid-msg", `{"state":true,"msg":null,"data":{` + names + `}}`, false, false},
		{"invalid-code", `{"state":true,"errno":{},"data":{` + names + `}}`, false, false},
		{"rejected", `{"state":false,"errno":10004,"message":"拒绝改名"}`, false, true},
		{"missing-state", `{"data":{` + names + `}}`, false, true},
		{"malformed", `{"state":true`, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := newTestShareClient()
			calls := 0
			client.HTTP.Transport = metadataTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Method != http.MethodPost || r.URL.Path != "/files/batch_rename" || r.URL.RawQuery != "" || r.Header.Get("Cookie") != "fixed-cookie" {
					t.Fatal("回执入口只能发送一次原改名请求", r.Method, r.URL.Path)
				}
				if err := r.ParseForm(); err != nil {
					t.Fatal(err)
				}
				if len(r.PostForm) != 2 || r.PostForm.Get("files_new_name["+first+"]") != entries[0].Name || r.PostForm.Get("files_new_name["+second+"]") != entries[1].Name {
					t.Fatal("编号或名称在表单中发生变化", r.PostForm)
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(tc.body)), Header: make(http.Header)}, nil
			})
			confirmed, err := client.RenameEntriesWithReceipt(context.Background(), entries, "fixed-cookie")
			if confirmed != tc.confirmed || (err != nil) != tc.failed || calls != 1 {
				t.Fatal("不完整或异常回执不得确认全部改名，操作错误应保留", confirmed, calls, err)
			}
		})
	}
}

func TestRenameEntriesWithReceiptRejectsInvalidOrCanceledBatchBeforeWrite(t *testing.T) {
	client := newTestShareClient()
	calls := 0
	client.HTTP.Transport = metadataTransport(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("无效批次不允许请求")
	})
	batches := [][]RenameTarget{
		nil,
		{{"1", "good.mkv"}, {"0", "bad.mkv"}},
		{{"1", "good.mkv"}, {"2,3", "bad.mkv"}},
		{{"1", "good.mkv"}, {"1", "duplicate.mkv"}},
		{{"1", "good.mkv"}, {"2", "../bad.mkv"}},
		{{"1", "good.mkv"}, {"2", "bad\\name.mkv"}},
		{{"1", "good.mkv"}, {"2", " "}},
		{{"1", "good.mkv"}, {"2", "bad\x00name.mkv"}},
		{{"1", "good.mkv"}, {"2", "bad\r\nname.mkv"}},
		{{"1", "good.mkv"}, {"2", strings.Repeat("长", 201)}},
	}
	tooMany := make([]RenameTarget, DriveMutationBatchLimit+1)
	for i := range tooMany {
		tooMany[i] = RenameTarget{ID: fmt.Sprint(i + 1), Name: "valid.mkv"}
	}
	batches = append(batches, tooMany)
	for _, entries := range batches {
		if confirmed, err := client.RenameEntriesWithReceipt(context.Background(), entries, "cookie"); err == nil || confirmed {
			t.Fatal("非法参数必须整批拒绝", confirmed, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if confirmed, err := client.RenameEntriesWithReceipt(ctx, []RenameTarget{{"1", "valid.mkv"}}, "cookie"); confirmed || !errors.Is(err, context.Canceled) {
		t.Fatal("已取消请求应保留取消原因", confirmed, err)
	}
	if calls != 0 {
		t.Fatal("取消或非法参数不允许发出写入", calls)
	}
}

func TestRenameEntriesWithReceiptNeverRetriesUncertainWrites(t *testing.T) {
	for _, scenario := range []string{"timeout", "connection", "405", "429", "503"} {
		t.Run(scenario, func(t *testing.T) {
			client := newTestShareClient()
			client.ReadRetryDelays = []time.Duration{time.Millisecond, time.Millisecond}
			calls := 0
			connectionErr := errors.New("模拟连接中断")
			client.HTTP.Transport = metadataTransport(func(*http.Request) (*http.Response, error) {
				calls++
				switch scenario {
				case "timeout":
					return nil, context.DeadlineExceeded
				case "connection":
					return nil, connectionErr
				}
				status := map[string]int{"405": http.StatusMethodNotAllowed, "429": http.StatusTooManyRequests, "503": http.StatusServiceUnavailable}[scenario]
				return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
			})
			confirmed, err := client.RenameEntriesWithReceipt(context.Background(), []RenameTarget{{"1", "valid.mkv"}}, "cookie")
			if confirmed || err == nil || calls != 1 {
				t.Fatal("回执未知时只允许一次写入", confirmed, calls, err)
			}
			if scenario == "timeout" && !errors.Is(err, context.DeadlineExceeded) || scenario == "connection" && !errors.Is(err, connectionErr) {
				t.Fatal("写入错误链必须保留原始原因", err)
			}
		})
	}
}
