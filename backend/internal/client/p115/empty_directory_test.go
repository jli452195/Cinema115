package p115

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

func TestDeleteEntriesBatchUsesOneExactForm(t *testing.T) {
	const first, second, parent = "3516637914517734609", "3516637914517734610", "3516637914517734611"
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if err := r.ParseForm(); err != nil {
			t.Error(err)
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/rb/delete" || r.URL.RawQuery != "" || r.Header.Get("Cookie") != "fixed-cookie" {
			t.Error("批量删除必须使用固定凭据和精确 POST 接口")
		}
		if len(r.PostForm) != 3 || r.PostForm.Get("pid") != parent || r.PostForm.Get("fid[0]") != first || r.PostForm.Get("fid[1]") != second {
			t.Error("必须在同一表单提交多个精确 ID", r.PostForm)
		}
		_, _ = w.Write([]byte(`{"state":true}`))
	}))
	defer server.Close()
	client := newTestShareClient()
	client.WebURL = server.URL
	if err := client.DeleteEntriesBatch(context.Background(), parent, []string{first, second}, "fixed-cookie"); err != nil || calls != 1 {
		t.Fatal("一个批次应只发送一次删除", calls, err)
	}
}

func TestDeleteEntriesBatchRejectsEntireInvalidBatchBeforeWrite(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
	defer server.Close()
	client := newTestShareClient()
	client.WebURL = server.URL
	for _, tc := range []struct {
		parent string
		ids    []string
	}{
		{"99", nil}, {"bad", []string{"1"}}, {"99", []string{"1", "bad"}},
		{"99", []string{"1", "0"}}, {"99", []string{"1", "1"}},
		{"99", []string{"1", "99"}}, {"99", []string{"1", "2,3"}},
	} {
		if err := client.DeleteEntriesBatch(context.Background(), tc.parent, tc.ids, "cookie"); err == nil {
			t.Fatal("非法批次被接受", tc)
		}
	}
	ids := make([]string, DriveMutationBatchLimit+1)
	for i := range ids {
		ids[i] = strconv.Itoa(i + 1)
	}
	if err := client.DeleteEntriesBatch(context.Background(), "99", ids, "cookie"); err == nil || calls != 0 {
		t.Fatal("所有编号必须在首次写入前完成校验", calls, err)
	}
}

func TestDeleteEntriesBatchNeverRetriesWrites(t *testing.T) {
	for _, status := range []int{http.StatusMethodNotAllowed, http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(status) }))
			defer server.Close()
			client := newTestShareClient()
			client.WebURL = server.URL
			client.ReadRetryDelays = []time.Duration{time.Millisecond, time.Millisecond}
			if err := client.DeleteEntriesBatch(context.Background(), "99", []string{"1", "2"}, "cookie"); err == nil || calls != 1 {
				t.Fatal("不明确写入不得自动重试", calls, err)
			}
		})
	}
}

func TestProbeEmptyDirectoryCombinesIdentityAndEmptyState(t *testing.T) {
	const prefix = `{"state":true,"cid":"9","path":[{"cid":"0"},{"cid":"8","name":"来源"},{"cid":"9","n":"子目录"}],`
	for _, tc := range []struct {
		name, body string
		valid      bool
		empty      bool
	}{
		{"empty", prefix + `"count":0,"data":[]}`, true, true},
		{"string-count", prefix + `"count":"0","offset":"0","data":[]}`, true, true},
		{"nonempty", prefix + `"count":4,"data":[{"fid":"10"}]}`, true, false},
		{"child-directory", prefix + `"count":1,"data":[{"cid":"10"}]}`, true, false},
		{"missing-count", prefix + `"data":[]}`, false, false},
		{"negative-count", prefix + `"count":-1,"data":[]}`, false, false},
		{"fractional-count", prefix + `"count":0.5,"data":[]}`, false, false},
		{"missing-data", prefix + `"count":0}`, false, false},
		{"null-data", prefix + `"count":0,"data":null}`, false, false},
		{"object-data", prefix + `"count":0,"data":{}}`, false, false},
		{"hidden-child", prefix + `"count":0,"data":[{"cid":"10"}]}`, false, false},
		{"incomplete-list", prefix + `"count":1,"data":[]}`, false, false},
		{"oversized-page", prefix + `"count":2,"data":[{"fid":"10"},{"fid":"11"}]}`, false, false},
		{"wrong-offset", prefix + `"count":0,"offset":1,"data":[]}`, false, false},
		{"negative-offset-after-delete", prefix + `"count":20,"offset":-1000,"data":[]}`, false, false},
		{"invalid-offset", prefix + `"count":0,"offset":"invalid","data":[]}`, false, false},
		{"invalid-offset-with-empty-page", prefix + `"count":20,"offset":"invalid","data":[]}`, false, false},
		{"redirect", `{"state":true,"cid":"8","path":[{"cid":"8","name":"其他"}],"count":0,"data":[]}`, false, false},
		{"wrong-breadcrumb", `{"state":true,"cid":"9","path":[{"cid":"8","name":"其他"}],"count":0,"data":[]}`, false, false},
		{"missing-breadcrumb", `{"state":true,"cid":"9","count":0,"data":[]}`, false, false},
		{"invalid-path", `{"state":true,"cid":"9","path":[{"cid":"9","name":"../其他"}],"count":0,"data":[]}`, false, false},
		{"rejected", `{"state":false,"count":0,"data":[]}`, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				query := r.URL.Query()
				if r.Method != http.MethodGet || r.URL.Path != "/files" || query.Get("cid") != "9" || query.Get("limit") != "1" || query.Get("offset") != "0" || query.Get("show_dir") != "1" || r.Header.Get("Cookie") != "fixed-cookie" {
					t.Error("查空应使用固定目录的一行读取")
				}
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			client := newTestShareClient()
			client.WebURL = server.URL
			directory, empty, err := client.ProbeEmptyDirectory(context.Background(), "9", "fixed-cookie")
			if calls != 1 || (err == nil) != tc.valid || empty != tc.empty {
				t.Fatal(directory, empty, calls, err)
			}
			changed := tc.name == "wrong-offset" || tc.name == "negative-offset-after-delete" || tc.name == "hidden-child" || tc.name == "incomplete-list"
			if errors.Is(err, ErrDriveDirectorySnapshotChanged) != changed {
				t.Fatal("查空仅将结构有效的计数、列表或偏移矛盾分类为快照变化", tc.name, err)
			}
			if tc.valid && (directory.CID != "9" || directory.ParentCID != "8" || directory.Name != "子目录" || directory.Path != "/来源/子目录") {
				t.Fatal("目录身份或路径未准确保留", directory)
			}
		})
	}
}

func TestEmptyDirectoryOperationsRejectCanceledContextBeforeNetwork(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
	defer server.Close()
	client := newTestShareClient()
	client.WebURL = server.URL
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := client.DeleteEntriesBatch(ctx, "99", []string{"1", "2"}, "cookie"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, _, err := client.ProbeEmptyDirectory(ctx, "9", "cookie"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for _, cid := range []string{"0", "", "bad", "1,2"} {
		if _, _, err := client.ProbeEmptyDirectory(context.Background(), cid, "cookie"); err == nil {
			t.Fatal("根目录或非法编号被接受", cid)
		}
	}
	if calls != 0 {
		t.Fatal("已取消或非法请求不应访问网络", calls)
	}
}

func TestProbeEmptyDirectoryKeepsBoundedReadRetry(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = fmt.Fprint(w, `{"state":true,"cid":"9","path":[{"cid":"0"},{"cid":"9","name":"来源"}],"count":0,"data":[]}`)
	}))
	defer server.Close()
	client := newTestShareClient()
	client.WebURL = server.URL
	client.ReadRetryDelays = []time.Duration{time.Millisecond}
	if _, empty, err := client.ProbeEmptyDirectory(context.Background(), "9", "cookie"); err != nil || !empty || calls != 2 {
		t.Fatal("只读核验应沿用有界重试", empty, calls, err)
	}
}
