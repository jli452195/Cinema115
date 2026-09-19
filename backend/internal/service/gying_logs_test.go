package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"strm115/backend/internal/client/gying"
)

func loggedGyingFixture(t *testing.T, handler http.HandlerFunc) *GyingService {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := gying.New(server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewSessionStore(filepath.Join(t.TempDir(), "gying.enc"), "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	return &GyingService{Logs: &GenerationLogs{}, client: client, store: store, doc: gyingDocument{Version: 1, BaseURL: server.URL, Username: "private-user", Password: "private-password"}}
}

func gyingMessages(entries []GenerationLog) string {
	var messages strings.Builder
	for _, entry := range entries {
		messages.WriteString(entry.Message)
		messages.WriteByte('\n')
	}
	return messages.String()
}

func TestGyingLogsSearchReloginCacheAndRefresh(t *testing.T) {
	var loggedIn atomic.Bool
	var searches, logins atomic.Int32
	svc := loggedGyingFixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/search":
			searches.Add(1)
			if !loggedIn.Load() {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			fmt.Fprint(w, `<script>_obj.search={"l":{"title":["Movie"],"d":["mv"],"i":["one"]}};</script>`)
		case "/user/login":
			logins.Add(1)
			loggedIn.Store(true)
			fmt.Fprint(w, `{"code":200}`)
		case "/res/downurl/mv/one":
			fmt.Fprint(w, `{"code":200,"downlist":{"list":{"m":["0123456789abcdef0123456789abcdef01234567"]}},"panlist":{"url":["https://115.com/s/test"]}}`)
		default:
			fmt.Fprint(w, `{}`)
		}
	})
	input := GyingSearchInput{CacheKey: "movie:42", OriginalTitle: "Movie", IMDBID: "tt12345"}
	ctx, finish := StartGyingOperation(context.Background(), svc.Logs, "网页搜索", "搜索：电影 · TMDB 42")
	result, err := svc.Search(ctx, input)
	finish(err)
	if err != nil || result.Cached || searches.Load() != 2 || logins.Load() != 1 {
		t.Fatalf("first search: %v %+v", err, result)
	}
	entries := svc.Logs.Snapshot("ALL", "观影")
	for _, entry := range entries {
		if entry.TaskID != entries[0].TaskID || entry.Source != "网页搜索" {
			t.Fatal("operation IDs or sources split during re-login")
		}
	}
	text := gyingMessages(entries)
	for _, expected := range []string{"会话失效", "自动重新登录成功", "磁力 1 条、网盘 1 条", "TMDB 42", "搜索结果已缓存"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("missing %s: %s", expected, text)
		}
	}
	result, err = svc.Search(context.Background(), input)
	if err != nil || !result.Cached || searches.Load() != 2 {
		t.Fatalf("cache made upstream request: %v", err)
	}
	if !strings.Contains(gyingMessages(svc.Logs.Snapshot("ALL", "观影")), "未访问观影站") {
		t.Fatal("missing cached origin")
	}
	input.Refresh = true
	if _, err := svc.Search(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	if searches.Load() != 3 || logins.Load() != 1 {
		t.Fatal("forced refresh did not bypass cache or caused extra login")
	}
	if len(svc.Logs.Snapshot("ERROR", "观影")) != 0 {
		t.Fatal("successful flow logged errors")
	}
}

func TestGyingLogsEmptyFallbackAndPartial(t *testing.T) {
	var queries atomic.Int32
	svc := loggedGyingFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/search" {
			queries.Add(1)
			fmt.Fprint(w, `<script>_obj.search={"l":{}};</script>`)
			return
		}
		t.Error("unexpected request")
	})
	result, err := svc.Search(context.Background(), GyingSearchInput{OriginalTitle: "Movie", IMDBID: "tt12345"})
	if err != nil || len(result.Items) != 0 || queries.Load() != 2 {
		t.Fatalf("empty fallback: %v", err)
	}
	logs := gyingMessages(svc.Logs.Snapshot("ALL", "观影"))
	if !strings.Contains(logs, "IMDb 兜底查询") || !strings.Contains(logs, "未发现可用资源") || len(svc.Logs.Snapshot("ERROR", "观影")) != 0 {
		t.Fatalf("empty result logging: %s", logs)
	}
	partial := loggedGyingFixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/search":
			fmt.Fprint(w, `<script>_obj.search={"l":{"title":["one","two"],"d":["mv","mv"],"i":["one","two"]}};</script>`)
		case "/res/downurl/mv/one":
			fmt.Fprint(w, `{"panlist":{"url":["https://115.com/s/test"]}}`)
		default:
			w.WriteHeader(http.StatusBadGateway)
			fmt.Fprint(w, "private-password")
		}
	})
	result, err = partial.Search(context.Background(), GyingSearchInput{OriginalTitle: "Movie"})
	if err != nil || !result.Partial || result.CacheExpiresAt != nil {
		t.Fatalf("partial: %v", err)
	}
	if !strings.Contains(gyingMessages(partial.Logs.Snapshot("WARNING", "观影")), "1 个资源详情读取失败") {
		t.Fatal("missing partial warning")
	}
}

func TestGyingLogsErrorRedactionAndCancellation(t *testing.T) {
	for _, tc := range []struct {
		err           error
		level, reason string
	}{
		{fmt.Errorf("password=leaked cookie=leaked: %w", gying.ErrLogin), "ERROR", "拒绝登录"},
		{fmt.Errorf("token=leaked: %w", gying.ErrChallenge), "ERROR", "安全验证"},
		{context.Canceled, "WARNING", "已取消"},
		{context.DeadlineExceeded, "ERROR", "超时"},
		{errors.New("HTTP 503 https://example.org?token=leaked"), "ERROR", "HTTP 503"},
		{errors.New("upstream Cookie: leaked"), "ERROR", "已隐藏"},
	} {
		logs := &GenerationLogs{}
		ctx, finish := StartGyingOperation(context.Background(), logs, "测试", "搜索")
		_, nested := StartGyingOperation(ctx, logs, "嵌套", "重复操作")
		nested(tc.err)
		finish(tc.err)
		entries := logs.Snapshot("ALL", "观影")
		if len(entries) != 2 || entries[0].Level != tc.level || !strings.Contains(entries[0].Message, tc.reason) || strings.Contains(gyingMessages(entries), "leaked") {
			t.Fatalf("unsafe/duplicate log: %+v", entries)
		}
	}
	doc := gyingDocument{Username: "private-user", Password: "private-password", Cookies: []gying.SessionCookie{{Name: "sid", Value: "private-cookie"}}}
	query := gyingLogQuery("Movie private-user private-password private-cookie https://example.org?token=leaked\r\nCookie: hidden", doc)
	for _, forbidden := range []string{"private-user", "private-password", "private-cookie", "leaked", "\n", "\r", "hidden"} {
		if strings.Contains(query, forbidden) {
			t.Fatalf("unsafe query: %s", query)
		}
	}
}

func TestGyingLogsConfiguration(t *testing.T) {
	svc := loggedGyingFixture(t, func(w http.ResponseWriter, r *http.Request) { t.Error("unexpected network request") })
	if _, err := svc.Configure(context.Background(), GyingConfigInput{}); err == nil {
		t.Fatal("accepted invalid config")
	}
	if _, err := svc.UpdateResourceOrder([]string{"magnet", "115"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Clear(); err != nil {
		t.Fatal(err)
	}
	logs := gyingMessages(svc.Logs.Snapshot("ALL", "观影"))
	for _, expected := range []string{"登录并保存配置", "调整资源分类顺序完成", "清除观影登录信息完成"} {
		if !strings.Contains(logs, expected) {
			t.Fatal(logs)
		}
	}
}
