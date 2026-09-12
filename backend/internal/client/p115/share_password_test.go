package p115

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSharePasswordAndDurationAreUpdatedAndVerified(t *testing.T) {
	for _, tc := range []struct {
		name, password, duration string
		updateStatus             int
		success                  bool
	}{
		{"成功", "ab12", "-1", 200, true},
		{"未应用密码", "old0", "-1", 200, false},
		{"未返回密码", "", "-1", 200, false},
		{"未应用长期有效", "ab12", "7", 200, false},
		{"设置被拒绝", "ab12", "-1", 405, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writes := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/share/updateshare" {
					writes++
					if err := r.ParseForm(); err != nil {
						t.Error(err)
					}
					if r.Method != "POST" || r.URL.RawQuery != "" || r.PostForm.Get("receive_code") != "ab12" || r.PostForm.Get("share_duration") != "-1" || r.PostForm.Get("share_code") != "own" {
						t.Error("密码/长期设置请求错误")
					}
					w.WriteHeader(tc.updateStatus)
					w.Write([]byte(`{"state":true}`))
					return
				}
				if r.Method != "GET" || r.URL.Path != "/share/shareinfo" || writes != 1 {
					t.Error("未核实设置")
				}
				w.Write([]byte(`{"state":true,"data":{"receive_code":"` + tc.password + `","share_duration":` + tc.duration + `}}`))
			}))
			defer server.Close()
			c := newTestShareClient()
			c.WebURL = server.URL
			share := &ShareInfo{Code: "own", Password: "old0", URL: "https://115cdn.com/s/own?password=old0"}
			err := c.ConfigureShare(context.Background(), share, "ab12", "cookie")
			if (err == nil) != tc.success || writes != 1 {
				t.Fatal("设置核实错误", err, writes)
			}
			if tc.updateStatus == 200 && (share.Password != "ab12" || !strings.HasSuffix(share.URL, "password=ab12")) {
				t.Fatal("未更新用于生成和任务恢复的链接")
			}
		})
	}
}
