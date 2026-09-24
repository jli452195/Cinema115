package p115

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDriveBatchFormsAndLargeIDs(t *testing.T) {
	const first, second, target = "3516637914517734609", "3516637914517734610", "3516637914517734611"
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if err := r.ParseForm(); err != nil {
			t.Error(err)
			return
		}
		if r.Method != "POST" || r.URL.RawQuery != "" || r.Header.Get("Cookie") != "fixed-cookie" {
			t.Error("写操作必须使用固定凭据和 POST 表单")
		}
		switch r.URL.Path {
		case "/files/batch_rename":
			if len(r.PostForm) != 2 || r.PostForm.Get("files_new_name["+first+"]") != "剧集 & 字幕.mkv" || r.PostForm.Get("files_new_name["+second+"]") != "剧集 & 字幕.zh-CN.srt" {
				t.Error("改名应在单次请求中传递两个精确编号和名称", r.PostForm)
			}
		case "/files/move":
			if len(r.PostForm) != 3 || r.PostForm.Get("pid") != target || r.PostForm.Get("fid[0]") != first || r.PostForm.Get("fid[1]") != second || r.PostForm.Has("fid") {
				t.Error("移动应使用索引字段而非单个 fid", r.PostForm)
			}
		default:
			t.Error("未知接口", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"state":true}`))
	}))
	defer server.Close()
	client := newTestShareClient()
	client.WebURL = server.URL
	if err := client.RenameEntries(context.Background(), []RenameTarget{{first, "剧集 & 字幕.mkv"}, {second, "剧集 & 字幕.zh-CN.srt"}}, "fixed-cookie"); err != nil {
		t.Fatal(err)
	}
	if err := client.MoveEntriesBatch(context.Background(), []string{first, second}, target, "fixed-cookie"); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatal("每批每种操作应只发送一次请求", calls)
	}
}

func TestDriveBatchValidatesEntireBatchBeforeRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("非法批次不应发出任何写请求") }))
	defer server.Close()
	client := newTestShareClient()
	client.WebURL = server.URL
	for _, entries := range [][]RenameTarget{nil, {{"1", "good.mkv"}, {"0", "bad.mkv"}}, {{"1", "good.mkv"}, {"2", "../bad"}}, {{"1", "good.mkv"}, {"1", "other.mkv"}}} {
		if client.RenameEntries(context.Background(), entries, "cookie") == nil {
			t.Fatal("接受非法改名批次", entries)
		}
	}
	for _, ids := range [][]string{nil, {"1", "0"}, {"1", "1"}, {"1", "2,3"}, {"1", "99"}} {
		if client.MoveEntriesBatch(context.Background(), ids, "99", "cookie") == nil {
			t.Fatal("接受非法移动批次", ids)
		}
	}
	ids := make([]string, DriveMutationBatchLimit+1)
	entries := make([]RenameTarget, len(ids))
	for i := range ids {
		ids[i] = fmt.Sprint(i + 1)
		entries[i] = RenameTarget{ids[i], "valid.mkv"}
	}
	if client.RenameEntries(context.Background(), entries, "cookie") == nil || client.MoveEntriesBatch(context.Background(), ids, "99", "cookie") == nil {
		t.Fatal("接受超出本地批次上限的请求")
	}
}

func TestDriveBatchNeverRetriesAmbiguousWrites(t *testing.T) {
	for _, status := range []int{429, 503} {
		for _, rename := range []bool{true, false} {
			t.Run(fmt.Sprintf("%d/rename=%v", status, rename), func(t *testing.T) {
				calls := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(status) }))
				defer server.Close()
				client := newTestShareClient()
				client.WebURL = server.URL
				client.ReadRetryDelays = []time.Duration{time.Millisecond}
				var err error
				if rename {
					err = client.RenameEntries(context.Background(), []RenameTarget{{"1", "a.mkv"}, {"2", "b.mkv"}}, "cookie")
				} else {
					err = client.MoveEntriesBatch(context.Background(), []string{"1", "2"}, "99", "cookie")
				}
				if err == nil || calls != 1 {
					t.Fatal("批量写操作不得自动重试", calls, err)
				}
			})
		}
	}
}
