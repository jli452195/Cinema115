package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

type cookieCheckFunc func(context.Context, string) (bool, error)

func (f cookieCheckFunc) CheckCookie(ctx context.Context, cookie string) (bool, error) {
	return f(ctx, cookie)
}

func TestAccountSavePreservesCredentialsOnFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name       string
		body       string
		valid      bool
		checkErr   error
		saveErr    error
		wantCode   int
		wantChecks int
		wantSaves  int
		wantCookie string
	}{
		{name: "空值", body: `{"cookie":"   "}`, wantCode: 400, wantCookie: "old"},
		{name: "格式错误", body: `{`, wantCode: 400, wantCookie: "old"},
		{name: "超长请求", body: `{"cookie":"` + strings.Repeat("a", 17<<10) + `"}`, wantCode: 400, wantCookie: "old"},
		{name: "无效凭证", body: `{"cookie":"new"}`, wantCode: 422, wantChecks: 1, wantCookie: "old"},
		{name: "验证失败", body: `{"cookie":"new"}`, checkErr: errors.New("上游超时"), wantCode: 502, wantChecks: 1, wantCookie: "old"},
		{name: "存储失败", body: `{"cookie":"new"}`, valid: true, saveErr: errors.New("磁盘不可写"), wantCode: 500, wantChecks: 1, wantSaves: 1, wantCookie: "old"},
		{name: "验证并保存", body: `{"cookie":"  new  "}`, valid: true, wantCode: 200, wantChecks: 1, wantSaves: 1, wantCookie: "new"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stored, checks, saves := "old", 0, 0
			r := gin.New()
			RegisterAccount(r.Group("/api/admin"), func() string { return stored }, func(value string) error {
				saves++
				if tc.saveErr != nil {
					return tc.saveErr
				}
				stored = value
				return nil
			}, cookieCheckFunc(func(_ context.Context, value string) (bool, error) {
				checks++
				if value != "new" {
					t.Errorf("待验证的 Cookie 未正确去除首尾空白")
				}
				return tc.valid, tc.checkErr
			}))
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/admin/p115/login", strings.NewReader(tc.body)))
			if w.Code != tc.wantCode || stored != tc.wantCookie || checks != tc.wantChecks || saves != tc.wantSaves {
				t.Fatalf("code=%d checks=%d saves=%d credentialPreserved=%v", w.Code, checks, saves, stored == tc.wantCookie)
			}
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Error("凭证响应必须禁止缓存")
			}
		})
	}
}

func TestAccountStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name   string
		cookie string
		valid  bool
		err    error
		code   int
	}{
		{name: "未配置", code: 200},
		{name: "有效", cookie: "saved", valid: true, code: 200},
		{name: "已失效", cookie: "saved", code: 200},
		{name: "上游异常", cookie: "saved", err: errors.New("无法连接"), code: 502},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := gin.New()
			RegisterAccount(r.Group("/api/admin"), func() string { return tc.cookie }, func(string) error { t.Fatal("状态查询不能修改凭证"); return nil }, cookieCheckFunc(func(_ context.Context, value string) (bool, error) {
				if tc.cookie == "" || value != tc.cookie {
					t.Error("状态查询的凭证不正确")
				}
				return tc.valid, tc.err
			}))
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/admin/p115/status", nil))
			var data struct {
				Configured bool  `json:"configured"`
				Connected  *bool `json:"connected"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
				t.Fatal(err)
			}
			if w.Code != tc.code || data.Configured != (tc.cookie != "") {
				t.Fatalf("错误的配置状态：%d", w.Code)
			}
			if tc.err != nil {
				if data.Connected != nil {
					t.Error("网络异常不能断言 Cookie 已失效")
				}
			} else if data.Connected == nil || *data.Connected != tc.valid {
				t.Error("连接状态不符合实际验证结果")
			}
		})
	}
}

func TestAccountTestDoesNotSave(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct{ body, checked string }{
		{`{}`, "saved"}, {`{"cookie":"  draft  "}`, "draft"},
	} {
		r := gin.New()
		RegisterAccount(r.Group("/api/admin"), func() string { return "saved" }, func(string) error { t.Fatal("测试操作不能写入凭证"); return nil }, cookieCheckFunc(func(_ context.Context, value string) (bool, error) {
			if value != tc.checked {
				t.Error("测试了错误的凭证")
			}
			return false, nil
		}))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/admin/p115/cookie/test", strings.NewReader(tc.body)))
		if w.Code != 200 || w.Body.String() != `{"valid":false}` {
			t.Fatalf("错误的测试结果：%d", w.Code)
		}
	}
}

func TestAccountDisconnect(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, failed := range []bool{false, true} {
		stored := "saved"
		r := gin.New()
		RegisterAccount(r.Group("/api/admin"), func() string { return stored }, func(value string) error {
			if failed {
				return errors.New("存储失败")
			}
			stored = value
			return nil
		}, cookieCheckFunc(func(context.Context, string) (bool, error) {
			t.Fatal("清除凭证不需要上游验证")
			return false, nil
		}))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/api/admin/p115/login", nil))
		if failed {
			if w.Code != 500 || stored != "saved" {
				t.Error("清除失败时应保留凭证并返回错误")
			}
		} else if w.Code != 204 || stored != "" {
			t.Error("清除成功后凭证应为空")
		}
	}
}
