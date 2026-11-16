package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/client/animefeed"
	"strm115/backend/internal/client/dmhy"
	"strm115/backend/internal/service"
	"strm115/backend/internal/storage"
)

type dmhyHTTPTransport struct{ calls int }

func (s *dmhyHTTPTransport) Fetch(_ context.Context, endpoint string, _ int64) ([]byte, error) {
	s.calls++
	if strings.HasPrefix(endpoint, "/topics/view/") {
		id := strings.TrimSuffix(strings.TrimPrefix(endpoint, "/topics/view/"), "_.html")
		return []byte(`<div class="topic-title"><li class="share"><a href="/js/share.html?url=http://share.dmhy.org/topics/view/` + id + `_.html">share</a></li></div><div id="resource-tabs"><a data-magnet="magnet:?xt=urn:btih:ff95cb490af2134cfcacfd02e04776f032ff10fc">Magnet</a></div>`), nil
	}
	return []byte(`<form><input name="keyword"></form><p>沒有可顯示資源</p>`), nil
}

func dmhyHTTPFixture(t *testing.T) (*gin.Engine, *dmhyHTTPTransport, *service.GenerationLogs, string, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "users.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	auth := portalTestAuth(t, db, "admin", "admin-password", "test-secret")
	adminToken, _, err := auth.LoginAdmin(ctx, "admin", "admin-password")
	if err != nil {
		t.Fatal(err)
	}
	portalTestUser(t, auth, "reader", "reader-password")
	userToken, _, err := auth.Login(ctx, "reader", "reader-password")
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	admin := router.Group("/api/admin", PortalAuthenticated(auth), PortalAdminOnly())
	transport := &dmhyHTTPTransport{}
	logs := &service.GenerationLogs{}
	site := service.NewDmhyService(transport)
	site.Logs = logs
	RegisterDmhy(admin, site)
	return router, transport, logs, adminToken, userToken
}

func TestDmhyAdminSearchValidationCacheAndLogs(t *testing.T) {
	router, transport, logs, adminToken, userToken := dmhyHTTPFixture(t)
	const query = "request-cookie=private-query"
	for _, tc := range []struct {
		name, token, body string
		status            int
	}{
		{"anonymous", "", `{"query":"test"}`, 401},
		{"ordinary user", userToken, `{"query":"test"}`, 403},
		{"admin", adminToken, `{"query":"` + query + `"}`, 200},
		{"cached", adminToken, `{"query":"` + query + `"}`, 200},
		{"empty query", adminToken, `{"query":""}`, 400},
		{"bad group", adminToken, `{"query":"test","group":"45&evil=1"}`, 400},
		{"bad page", adminToken, `{"query":"test","page":1001}`, 400},
		{"bad fallback", adminToken, `{"query":"test","fallback":"` + strings.Repeat("x", 201) + `"}`, 400},
		{"unknown field", adminToken, `{"query":"test","url":"https://evil.test"}`, 400},
		{"malformed", adminToken, `{broken`, 400},
		{"trailing object", adminToken, `{"query":"test"} {}`, 400},
		{"trailing junk", adminToken, `{"query":"test"} x`, 400},
		{"body limit", adminToken, `{"query":"` + strings.Repeat("x", 5000) + `"}`, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/admin/dmhy/search", strings.NewReader(tc.body))
			request.Header.Set("Authorization", "Bearer "+tc.token)
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != tc.status {
				t.Fatalf("状态码 %d，预期 %d：%s", response.Code, tc.status, response.Body.String())
			}
			if tc.status == 200 {
				var result service.DmhySearchResult
				if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || result.Items == nil || result.Groups == nil || result.Specs == nil || len(result.Attempts) != 1 {
					t.Fatal("空结果数据结构不完整", result, err)
				}
				if result.Query != query || result.Cached != (tc.name == "cached") {
					t.Fatal("查询或缓存标记错误", result)
				}
			}
			if tc.token == adminToken && response.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("缺少缓存保护")
			}
		})
	}
	if transport.calls != 1 {
		t.Fatal("鉴权、输入校验或缓存未阻止多余请求", transport.calls)
	}
	entries := logs.Snapshot("ALL", "搜索")
	if len(entries) == 0 {
		t.Fatal("缺少搜索操作日志")
	}
	foundCached := false
	for _, entry := range entries {
		if entry.Source != "动漫花园" || strings.Contains(entry.Message, query) || strings.Contains(entry.Message, "evil.test") || strings.Contains(entry.Message, adminToken) {
			t.Fatal("操作日志记录了输入或身份信息", entry)
		}
		foundCached = foundCached || entry.Level == "DEBUG" && strings.Contains(entry.Message, "命中缓存")
	}
	if !foundCached {
		t.Fatal("未记录缓存命中")
	}
}

func TestDmhyAdminMagnetValidationCacheAndLogs(t *testing.T) {
	router, transport, logs, adminToken, userToken := dmhyHTTPFixture(t)
	for _, tc := range []struct {
		name, token, body string
		status            int
	}{
		{"anonymous", "", `{"id":"160765"}`, 401},
		{"ordinary user", userToken, `{"id":"160765"}`, 403},
		{"admin", adminToken, `{"id":"160765"}`, 200},
		{"cached", adminToken, `{"id":"160765"}`, 200},
		{"missing ID", adminToken, `{}`, 400},
		{"zero ID", adminToken, `{"id":"0"}`, 400},
		{"arbitrary URL", adminToken, `{"id":"https://evil.test/post"}`, 400},
		{"unknown field", adminToken, `{"id":"160765","url":"https://evil.test"}`, 400},
		{"number ID", adminToken, `{"id":160765}`, 400},
		{"trailing object", adminToken, `{"id":"160765"} {}`, 400},
		{"body limit", adminToken, `{"id":"` + strings.Repeat("1", 5000) + `"}`, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/admin/dmhy/magnet", strings.NewReader(tc.body))
			request.Header.Set("Authorization", "Bearer "+tc.token)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != tc.status {
				t.Fatalf("状态码 %d，预期 %d：%s", response.Code, tc.status, response.Body.String())
			}
			if tc.status == 200 {
				var result service.DmhyMagnetResult
				if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || result.ID != "160765" || result.Magnet != "magnet:?xt=urn:btih:ff95cb490af2134cfcacfd02e04776f032ff10fc" || result.Cached != (tc.name == "cached") || result.FetchedAt.IsZero() || result.ExpiresAt.Sub(result.FetchedAt) != 30*time.Minute {
					t.Fatal("原帖磁力或缓存信息错误", result, err)
				}
			}
			if tc.token == adminToken && response.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("缺少缓存保护")
			}
		})
	}
	if transport.calls != 1 {
		t.Fatal("磁力读取触发了多余请求", transport.calls)
	}
	foundCached := false
	for _, entry := range logs.Snapshot("ALL", "搜索") {
		if entry.Source != "动漫花园" || strings.Contains(entry.Message, "ff95cb49") || strings.Contains(entry.Message, adminToken) || strings.Contains(entry.Message, "evil.test") {
			t.Fatal("磁力读取日志泄露输入或身份", entry)
		}
		foundCached = foundCached || entry.Level == "DEBUG" && strings.Contains(entry.Message, "命中缓存")
	}
	if !foundCached {
		t.Fatal("磁力读取未记录缓存命中")
	}
}

func TestDmhySearchErrorsHideUpstreamAndPreserveRetry(t *testing.T) {
	gin.SetMode(gin.TestMode)
	private := errors.New("cookie=secret https://user:password@internal.invalid")
	for _, tc := range []struct {
		name   string
		err    error
		status int
		retry  bool
	}{
		{"input", fmt.Errorf("%w: %v", dmhy.ErrInput, private), 400, false},
		{"timeout", errors.Join(context.DeadlineExceeded, private), 504, false},
		{"canceled", errors.Join(context.Canceled, private), 408, false},
		{"blocked", errors.Join(dmhy.ErrBlocked, private), 502, false},
		{"parse", errors.Join(dmhy.ErrParse, private), 502, false},
		{"network", errors.Join(dmhy.ErrNetwork, private), 502, false},
		{"unknown", private, 502, false},
		{"rate limit", &animefeed.HTTPError{Site: "dmhy", Status: 429, RetryAt: time.Now().Add(10 * time.Second)}, 429, true},
		{"rate limit default", &animefeed.HTTPError{Site: "dmhy", Status: 429}, 429, true},
		{"busy retry", errors.Join(dmhy.ErrBlocked, &animefeed.HTTPError{Site: "dmhy", Status: 503, RetryAt: time.Now().Add(10 * time.Second)}), 429, true},
		{"busy no retry", errors.Join(dmhy.ErrBlocked, &animefeed.HTTPError{Site: "dmhy", Status: 503}), 502, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(response)
			dmhyRequestError(c, tc.err)
			if response.Code != tc.status {
				t.Fatalf("状态码 %d，预期 %d：%s", response.Code, tc.status, response.Body.String())
			}
			for _, secret := range []string{"secret", "password", "internal.invalid"} {
				if strings.Contains(response.Body.String(), secret) {
					t.Fatal("响应泄露上游错误", response.Body.String())
				}
			}
			retry := response.Header().Get("Retry-After")
			if tc.retry {
				seconds, err := strconv.Atoi(retry)
				if err != nil || seconds < 1 || seconds > 60 {
					t.Fatal("重试提示无效", retry, err)
				}
			} else if retry != "" {
				t.Fatal("非限流响应包含重试提示", retry)
			}
		})
	}
}

func TestDmhyUnavailableService(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterDmhy(router.Group("/api/admin"), nil)
	for _, endpoint := range []string{"search", "magnet"} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/admin/dmhy/"+endpoint, strings.NewReader(`{}`)))
		if response.Code != 503 || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatal(response.Code, response.Body.String())
		}
	}
}
