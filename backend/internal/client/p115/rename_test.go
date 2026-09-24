package p115

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRenameEntryFormAndNoWriteRetries(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.URL.Path != "/files/batch_rename" || r.Method != "POST" || r.URL.RawQuery != "" || r.PostForm.Get("files_new_name[3516637914517734609]") != "电影 & 剧集.mkv" || r.Header.Get("Cookie") != "fixed-cookie" {
			t.Error("重命名请求参数无效")
		}
		w.WriteHeader(503)
	}))
	defer server.Close()
	client := newTestShareClient()
	client.WebURL = server.URL
	if err := client.RenameEntry(context.Background(), "3516637914517734609", "电影 & 剧集.mkv", "fixed-cookie"); err == nil || calls != 1 {
		t.Fatal("写请求失败不能自动重试", calls, err)
	}
	for _, name := range []string{"", " ", "../test", "a\\b", "a\n"} {
		if client.RenameEntry(context.Background(), "123", name, "fixed-cookie") == nil {
			t.Fatal("接受非法文件名", name)
		}
	}
	if client.RenameEntry(context.Background(), "0", "test", "fixed-cookie") == nil {
		t.Fatal("不能重命名根目录")
	}
	if calls != 1 {
		t.Fatal("非法参数不应发起请求")
	}
}

func TestDriveFilesRejectsRedirectedDirectory(t *testing.T) {
	for _, body := range []string{
		`{"state":true,"cid":"0","count":0,"data":[]}`,
		`{"state":true,"path":[{"cid":"0"}],"count":0,"data":[]}`,
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
		client := newTestShareClient()
		client.WebURL = server.URL
		_, err := client.DriveFiles(context.Background(), "123", 0, "fixed-cookie")
		server.Close()
		if err == nil {
			t.Fatal("不能把失效目录返回的根目录当作目标")
		}
	}
}
