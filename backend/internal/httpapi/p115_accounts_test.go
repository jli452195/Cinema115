package httpapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/service"
	"strm115/backend/internal/storage"
)

type accountQRFake struct {
	count          int
	beforeComplete func()
}

func (f *accountQRFake) CreateQRCode(_ context.Context, app string) (*p115.QRCode, error) {
	f.count++
	return &p115.QRCode{ID: app, ImageURL: "https://115.com/test", ExpiresAt: time.Now().Add(time.Minute)}, nil
}
func (f *accountQRFake) CheckQRCode(context.Context, string) (*p115.QRCodeStatus, error) {
	return &p115.QRCodeStatus{Status: "success"}, nil
}
func (f *accountQRFake) CompleteQRCodeLogin(_ context.Context, id string) (string, error) {
	if f.beforeComplete != nil {
		f.beforeComplete()
	}
	return "UID=" + id + "; SEID=qr-private", nil
}

func setupAccountsAPI(t *testing.T, checker CookieChecker, qr accountQRClient) (*gin.Engine, *service.P115Accounts, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	db, err := storage.Open(context.Background(), filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	legacy, err := service.NewSessionStore(filepath.Join(dir, "p115-session.enc"), "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	if err := legacy.Save("legacy-cookie"); err != nil {
		t.Fatal(err)
	}
	s, err := service.OpenP115AccountsSQLite(context.Background(), db, dir, "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	first := "UID=111_A; SEID=first-private"
	if err := s.Update("default", 1, "默认账号", &first); err != nil {
		t.Fatal(err)
	}
	id, err := s.Add("第二账号")
	if err != nil {
		t.Fatal(err)
	}
	second := "UID=222_B; SEID=second-private"
	if err := s.Update(id, 1, "第二账号", &second); err != nil {
		t.Fatal(err)
	}
	if checker == nil {
		checker = cookieCheckFunc(func(_ context.Context, cookie string) (bool, error) { return cookie != "bad" && cookie != "", nil })
	}
	r := gin.New()
	RegisterP115Accounts(r.Group("/api/admin"), s, checker, qr)
	return r, s, id
}
func accountRequest(t *testing.T, r *gin.Engine, method, path, body string, code int) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(method, "/api/admin/p115"+path, strings.NewReader(body)))
	if w.Code != code {
		t.Fatalf("%s %s: code=%d want=%d", method, path, w.Code, code)
	}
	if code != 404 && w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("账号接口必须禁止缓存")
	}
	return w
}

func TestMultiAccountSaveSwitchAndClear(t *testing.T) {
	r, s, id := setupAccountsAPI(t, nil, nil)
	path := "/accounts/" + id
	accountRequest(t, r, "PUT", path, `{"name":"第二账号","cookie":"bad","revision":2}`, 422)
	accountRequest(t, r, "PUT", path, `{"name":"第二账号","cookie":"new"}`, 400)
	accountRequest(t, r, "PUT", path, `{"name":"重命名","cookie":"","revision":2}`, 200)
	second, _ := s.Get(id)
	if second.Cookie != "UID=222_B; SEID=second-private" || second.Name != "重命名" {
		t.Fatal("名称保存覆盖凭据或自动激活了账号")
	}
	accountRequest(t, r, "POST", path+"/activate", `{}`, 404)
	accountRequest(t, r, "DELETE", path+"/login", `{"revision":2}`, 409)
	accountRequest(t, r, "DELETE", path+"/login", `{"revision":3}`, 204)
	if s.CookieFor(id) != "" {
		t.Fatal("清除未生效")
	}
	accountRequest(t, r, "DELETE", path, `{"revision":4}`, 200)
	w := accountRequest(t, r, "GET", "/accounts", "", 200)
	if strings.Contains(w.Body.String(), "SEID") || strings.Contains(w.Body.String(), "cookie") || strings.Contains(w.Body.String(), "active") {
		t.Fatal("列表泄漏凭据")
	}
	accountRequest(t, r, "POST", "/accounts", `{"name":"新账号"}`, 200)
	if len(s.List()) != 2 || s.List()[1].Configured {
		t.Fatal("新增账号默认状态错误")
	}
}

func TestAccountEndpointsRequireExplicitID(t *testing.T) {
	r, s, _ := setupAccountsAPI(t, nil, nil)
	for _, item := range []struct{ method, path string }{
		{"GET", "/status"}, {"GET", "/cookie"}, {"POST", "/login"}, {"DELETE", "/login"},
		{"POST", "/qrcode"}, {"GET", "/qrcode/status"}, {"POST", "/accounts/default/activate"},
	} {
		accountRequest(t, r, item.method, item.path, `{}`, 404)
	}
	if s.CookieFor("") != "" {
		t.Fatal("空编号不能退回已保存的账号")
	}
}

func TestQRAccountBindingAndStaleCompletion(t *testing.T) {
	for _, change := range []string{"正常确认", "保存新凭据", "清除凭据", "删除账号", "刷新二维码"} {
		t.Run(change, func(t *testing.T) {
			qr := &accountQRFake{}
			r, s, id := setupAccountsAPI(t, nil, qr)
			path := "/accounts/" + id
			accountRequest(t, r, "POST", "/accounts/default/qrcode", `{"clientType":"115-ios"}`, 200)
			w := accountRequest(t, r, "POST", path+"/qrcode", `{"clientType":"115-android"}`, 200)
			var result struct {
				ID string `json:"qrId"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			qr.beforeComplete = func() {
				switch change {
				case "保存新凭据", "清除凭据":
					cookie := "manual-new"
					if change == "清除凭据" {
						cookie = ""
					}
					if err := s.Update(id, 2, "第二账号", &cookie); err != nil {
						t.Fatal(err)
					}
				case "删除账号":
					if err := s.Delete(id, 2); err != nil {
						t.Fatal(err)
					}
				case "刷新二维码":
					accountRequest(t, r, "POST", path+"/qrcode", `{"clientType":"115-ios"}`, 200)
				}
			}
			code := 409
			if change == "正常确认" {
				code = 200
			}
			if change == "删除账号" {
				code = 404
			}
			accountRequest(t, r, "GET", path+"/qrcode/status?qrId="+result.ID, "", code)
			first, _ := s.Get("default")
			if first.Cookie != "UID=111_A; SEID=first-private" {
				t.Fatal("扫码结果写入了其他卡片")
			}
			second, err := s.Get(id)
			if change == "正常确认" && (err != nil || second.Cookie != "UID=115android; SEID=qr-private") {
				t.Fatal("二维码未写回发起账号")
			}
			if change != "正常确认" && err == nil && strings.Contains(second.Cookie, "qr-private") {
				t.Fatal("旧二维码覆盖新状态")
			}
			qr.beforeComplete = nil
			// 另一张卡片的二维码仍独立有效，确认不会覆盖其他卡片。
			accountRequest(t, r, "GET", "/accounts/default/qrcode/status?qrId=115ios", "", 200)
			if change == "正常确认" && s.CookieFor(id) != second.Cookie {
				t.Fatal("扫码完成擅自切换当前账号")
			}
		})
	}
}
