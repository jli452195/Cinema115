package p115

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTransferAPIFormsAndLargeIDs(t *testing.T) {
	const id = "3516637914517734609"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "test-cookie" {
			t.Error("缺少固定账号凭证")
		}
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		if r.URL.Path == "/files" {
			if r.Method != http.MethodGet || r.ContentLength != 0 {
				t.Error("目录查询应使用 GET Query")
			}
		} else if r.Method != http.MethodPost || r.URL.RawQuery != "" || len(r.PostForm) == 0 || r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
			t.Error("写操作应使用 POST 表单，不应将参数放入 Query")
		}
		switch r.URL.Path {
		case "/files/add":
			if r.Form.Get("pid") != "123" || r.Form.Get("cname") != "临时目录" {
				t.Error("新建目录参数错误")
			}
			w.Write([]byte(`{"state":true,"cid":3516637914517734609}`))
		case "/files":
			if r.Form.Get("cid") != id {
				t.Error("大整数 ID 丢失精度")
			}
			w.Write([]byte(`{"state":1,"count":"1","data":[{"fid":3516637914517734610,"n":"视频.mkv","s":"100","sha":"0123456789abcdef0123456789abcdef01234567","pc":"abc123"}]}`))
		case "/share/receive":
			if r.Form.Get("file_id") != "1,2" || r.Form.Get("cid") != id || r.Form.Get("receive_code") != "abcd" {
				t.Error("转存参数错误")
			}
			w.Write([]byte(`{"state":true}`))
		case "/share/send":
			if r.Form.Get("file_ids") != "3516637914517734610" {
				t.Error("分享没有使用新文件 ID")
			}
			w.Write([]byte(`{"state":true,"data":{"share_code":"own","receive_code":"abcd"}}`))
		case "/rb/delete":
			if r.Form.Get("fid") != id {
				t.Error("清理目录不正确")
			}
			w.Write([]byte(`{"state":true}`))
		default:
			t.Error("未知接口")
		}
	}))
	defer upstream.Close()
	c := newTestShareClient()
	c.WebURL = upstream.URL
	ctx := context.Background()
	cid, err := c.CreateDirectory(ctx, "123", "临时目录", "test-cookie")
	if err != nil || cid != id {
		t.Fatal(cid, err)
	}
	if err := c.ReceiveShare(ctx, &ShareInfo{Code: "source", Password: "abcd"}, []string{"1", "2"}, cid, "test-cookie"); err != nil {
		t.Fatal(err)
	}
	page, err := c.DriveFiles(ctx, cid, 0, "test-cookie")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Files) != 1 || page.Files[0].ID != "3516637914517734610" || page.Files[0].SHA1 != "0123456789ABCDEF0123456789ABCDEF01234567" || page.Files[0].PickCode != "abc123" || page.Files[0].Size != 100 {
		t.Fatal("目录列表丢失 STRM 字段或大整数精度", page)
	}
	share, err := c.SendShare(ctx, []string{page.Files[0].ID}, "test-cookie")
	if err != nil || share.URL != "https://115cdn.com/s/own?password=abcd" {
		t.Fatal(share, err)
	}
	if err := c.DeleteDirectory(ctx, cid, "test-cookie"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"0", "", "1,2", "-1"} {
		if err := c.DeleteDirectory(ctx, id, "test-cookie"); err == nil {
			t.Fatal("接受了不安全的删除 ID")
		}
	}
}

func TestSharePermanentIsExplicitAndVerified(t *testing.T) {
	for _, duration := range []string{`-1`, `"-1"`, `15`, `null`} {
		t.Run(duration, func(t *testing.T) {
			updated := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/share/updateshare" {
					if err := r.ParseForm(); err != nil {
						t.Error(err)
					}
					if r.Method != "POST" || r.URL.RawQuery != "" || r.PostForm.Get("share_duration") != "-1" || r.PostForm.Get("share_code") != "own" || r.Form.Has("receive_code") {
						t.Error("没有单独设置长期有效，或修改了提取码")
					}
					updated = true
					w.Write([]byte(`{"state":true}`))
					return
				}
				if r.Method != http.MethodGet || r.URL.Path != "/share/shareinfo" || !updated || r.URL.Query().Get("share_code") != "own" {
					t.Error("未回读实际有效期")
				}
				w.Write([]byte(`{"state":true,"data":{"share_duration":` + duration + `}}`))
			}))
			defer server.Close()
			client := newTestShareClient()
			client.WebURL = server.URL
			err := client.SetSharePermanent(context.Background(), &ShareInfo{Code: "own"}, "cookie")
			wantOK := duration == `-1` || duration == `"-1"`
			if (err == nil) != wantOK {
				t.Fatal("有效期核实不正确", err)
			}
		})
	}
}

func TestDeleteEntriesMovesOnlyExactItemsToRecycleBin(t *testing.T) {
	deleted := make([]string, 0, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rb/delete" || r.Method != http.MethodPost {
			t.Fatalf("request=%s %s", r.Method, r.URL.Path)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		deleted = append(deleted, r.Form.Get("fid"))
		w.Write([]byte(`{"state":true}`))
	}))
	defer server.Close()
	client := newTestShareClient()
	client.WebURL = server.URL
	if err := client.DeleteEntries(context.Background(), []string{"11", "3516637914517734609"}, "cookie"); err != nil {
		t.Fatal(err)
	}
	if len(deleted) != 2 || deleted[0] != "11" || deleted[1] != "3516637914517734609" {
		t.Fatalf("deleted=%v", deleted)
	}
	for _, ids := range [][]string{nil, {"0"}, {"1", "bad"}} {
		if err := client.DeleteEntries(context.Background(), ids, "cookie"); err == nil {
			t.Fatalf("accepted unsafe ids=%v", ids)
		}
	}
}
