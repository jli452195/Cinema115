package p115

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

func TestReadDirectoryFilesCompletesStrictSnapshot(t *testing.T) {
	const prefix = `{"state":true,"cid":"9","path":[{"cid":"0"},{"cid":"9","name":"来源"}],`
	for _, empty := range []bool{false, true} {
		t.Run(strconv.FormatBool(empty), func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				query := r.URL.Query()
				if r.Method != http.MethodGet || r.URL.Path != "/files" || query.Get("cid") != "9" || query.Get("offset") != "0" || query.Get("limit") != "1000" || query.Get("show_dir") != "1" || r.Header.Get("Cookie") != "fixed-cookie" {
					t.Error("必须读取同一目录的完整直接子项")
				}
				if empty {
					_, _ = fmt.Fprint(w, prefix+`"count":0,"offset":0,"data":[]}`)
					return
				}
				_, _ = fmt.Fprint(w, prefix+`"count":2,"offset":0,"data":[{"fid":"3516637914517734609","cid":"9","n":"视频.mkv","s":123,"sha":"abc","pc":"pick"},{"cid":"10","n":"子目录"}]}`)
			}))
			defer server.Close()
			client := newTestShareClient()
			client.WebURL = server.URL
			directory, files, err := client.ReadDirectoryFiles(context.Background(), "9", "fixed-cookie")
			if err != nil || calls != 1 || directory.CID != "9" || directory.Path != "/来源" || files == nil || empty && len(files) != 0 || !empty && len(files) != 2 {
				t.Fatal(directory, files, calls, err)
			}
			if !empty && (files[0].ID != "3516637914517734609" || files[0].SHA1 != "ABC" || files[0].Size != 123 || files[0].Directory || !files[1].Directory) {
				t.Fatal("文件字段或目录类型丢失", files)
			}
		})
	}
}

func TestReadDirectoryFilesRejectsUnprovenAbsence(t *testing.T) {
	const prefix = `{"state":true,"cid":"9","path":[{"cid":"0"},{"cid":"9","name":"来源"}],`
	for _, tc := range []struct{ name, body string }{
		{"missing-identity", `{"state":true,"count":0,"offset":0,"data":[]}`},
		{"missing-breadcrumb", `{"state":true,"cid":"9","count":0,"offset":0,"data":[]}`},
		{"redirect", `{"state":true,"cid":"8","path":[{"cid":"0"},{"cid":"8","name":"其他"}],"count":0,"offset":0,"data":[]}`},
		{"missing-count", prefix + `"offset":0,"data":[]}`},
		{"missing-offset", prefix + `"count":0,"data":[]}`},
		{"wrong-offset", prefix + `"count":0,"offset":1,"data":[]}`},
		{"negative-offset-after-delete", prefix + `"count":20,"offset":-1000,"limit":1000,"data":[]}`},
		{"negative-count", prefix + `"count":-1,"offset":0,"data":[]}`},
		{"fractional-count", prefix + `"count":0.5,"offset":0,"data":[]}`},
		{"invalid-offset", prefix + `"count":0,"offset":"invalid","data":[]}`},
		{"invalid-offset-with-empty-page", prefix + `"count":20,"offset":"invalid","data":[]}`},
		{"missing-data", prefix + `"count":0,"offset":0}`},
		{"null-data", prefix + `"count":0,"offset":0,"data":null}`},
		{"incomplete-data", prefix + `"count":1,"offset":0,"data":[]}`},
		{"hidden-child", prefix + `"count":0,"offset":0,"data":[{"cid":"10","n":"子目录"}]}`},
		{"duplicate", prefix + `"count":2,"offset":0,"data":[{"cid":"10","n":"甲"},{"cid":"10","n":"乙"}]}`},
		{"root-child", prefix + `"count":1,"offset":0,"data":[{"cid":"0","n":"根目录"}]}`},
		{"self-child", prefix + `"count":1,"offset":0,"data":[{"cid":"9","n":"自身"}]}`},
		{"invalid-name", prefix + `"count":1,"offset":0,"data":[{"cid":"10","n":"../其他"}]}`},
		{"invalid-size", prefix + `"count":1,"offset":0,"data":[{"fid":"10","n":"视频.mkv","s":-1}]}`},
		{"foreign-file", prefix + `"count":1,"offset":0,"data":[{"fid":"10","cid":"8","n":"视频.mkv"}]}`},
		{"too-large", prefix + `"count":20001,"offset":0,"data":[]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = fmt.Fprint(w, tc.body) }))
			defer server.Close()
			client := newTestShareClient()
			client.WebURL = server.URL
			_, files, err := client.ReadDirectoryFiles(context.Background(), "9", "cookie")
			if err == nil || files != nil {
				t.Fatal("不完整列表不能作为条目消失的证明", files, err)
			}
			changed := tc.name == "wrong-offset" || tc.name == "negative-offset-after-delete" || tc.name == "incomplete-data" || tc.name == "hidden-child"
			if errors.Is(err, ErrDriveDirectorySnapshotChanged) != changed {
				t.Fatal("仅结构有效但分页事实矛盾的响应可以分类为暂态快照变化", tc.name, err)
			}
		})
	}
}

func TestReadDirectoryFilesChecksEveryPage(t *testing.T) {
	for _, mode := range []string{"valid", "count-change", "duplicate", "path-change", "wrong-offset", "missing-identity", "later-read-failure"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				count, offset := 2, calls-1
				name, id := "来源", strconv.Itoa(calls+10)
				if r.URL.Query().Get("offset") != strconv.Itoa(offset) {
					t.Error("下一页必须紧跟已读取条目")
				}
				if calls == 2 {
					switch mode {
					case "count-change":
						count = 3
					case "duplicate":
						id = "11"
					case "path-change":
						name = "改名"
					case "wrong-offset":
						offset = 0
					case "missing-identity":
						_, _ = fmt.Fprint(w, `{"state":true,"count":2,"offset":1,"data":[]}`)
						return
					case "later-read-failure":
						w.WriteHeader(http.StatusServiceUnavailable)
						return
					}
				}
				_ = json.NewEncoder(w).Encode(map[string]any{
					"state": true, "cid": "9", "path": []map[string]string{{"cid": "0"}, {"cid": "9", "name": name}},
					"count": count, "offset": offset, "data": []map[string]string{{"fid": id, "n": id + ".mkv"}},
				})
			}))
			defer server.Close()
			client := newTestShareClient()
			client.WebURL = server.URL
			_, files, err := client.ReadDirectoryFiles(context.Background(), "9", "cookie")
			if calls != 2 || mode == "valid" && (err != nil || len(files) != 2) || mode != "valid" && (err == nil || files != nil) {
				t.Fatal("每页必须保持完整且身份一致，失败不得泄漏部分快照", mode, calls, files, err)
			}
			if errors.Is(err, ErrDriveDirectorySnapshotChanged) != (mode == "count-change" || mode == "wrong-offset") {
				t.Fatal("跨页计数或偏移变化应可识别，身份和读取错误不应误分类", mode, err)
			}
		})
	}
}

func TestReadDirectoryFilesRejectsCancellationAndInvalidID(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
	defer server.Close()
	client := newTestShareClient()
	client.WebURL = server.URL
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, files, err := client.ReadDirectoryFiles(ctx, "9", "cookie"); !errors.Is(err, context.Canceled) || files != nil {
		t.Fatal(files, err)
	}
	if _, files, err := client.ReadDirectoryFiles(context.Background(), "bad", "cookie"); err == nil || files != nil || calls != 0 {
		t.Fatal("已取消或非法读取不应访问网络", calls, files, err)
	}
}
