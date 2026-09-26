package httpapi

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"strm115/backend/internal/service"
	"testing"
)

func TestSTRMOriginOptionalAndStrictParameters(t *testing.T) {
	a := NewShareAPI(func() string { return "" })
	const query = "/api/?share_code=abc123&id=3516737870024934541&receive_code=pass"
	for _, extra := range []string{"&filename=movie.mkv", "&foo=1&foo=2&token=ignored", "&id2=other&name=%E7%94%B5%E5%BD%B1"} {
		got, err := a.parseSTRMURL("http://nas"+query+extra, false, "")
		if err != nil || got.Code != "abc123" || got.FileID != "3516737870024934541" || got.Password != "pass" {
			t.Fatal("额外参数影响必要参数解析", err)
		}
	}
	for _, origin := range []string{"http://192.168.0.3:8115", "https://other.example:9098", "http://192.168.0.24:3002"} {
		if _, err := a.parseSTRMURL(origin+query, false, ""); err != nil {
			t.Fatal("不同来源未识别", err)
		}
		_, err := a.parseSTRMURL(origin+query, true, "http://192.168.0.24:3002")
		if (err == nil) != (origin == "http://192.168.0.24:3002") {
			t.Fatal("严格来源匹配错误", err)
		}
	}
	for _, raw := range []string{
		"ftp://nas" + query, "file:///api/?share_code=abc&id=1&receive_code=pass", "//nas" + query, "http:///api/?share_code=abc&id=1&receive_code=pass",
		"http://user:pass@nas" + query, "http://nas" + query + "#fragment", "http://nas:99999" + query, "http://nas:" + query,
		"http://nas/other/?share_code=abc&id=1&receive_code=pass", "http://nas/%61pi/?share_code=abc&id=1&receive_code=pass",
		"http://nas/api/?share_code=abc&id=1", "http://nas/api/?share_code=abc&id=1&receive_code=pass&id=2",
		"http://nas/api/?share_code=abc&share_code=def&id=1&receive_code=pass",
		"http://nas/api/?share_code=abc&id=1&receive_code=pass&receive_code=pass",
		"http://nas/api/stream/share/obsolete.payload",
		"http://nas/api/stream/share/obsolete.payload?share_code=abc&id=1&receive_code=pass",
		"http://nas" + query + "&bad=%ZZ", "http://nas" + query + ";ignored=value",
		"http://nas/api/?share_code=abc%2Fdef&id=1&receive_code=pass", "http://nas/api/?share_code=abc&id=-1&receive_code=pass",
		"http://nas/api/?share_code=abc&id=0&receive_code=pass", "http://nas/api/?share_code=abc&id=18446744073709551616&receive_code=pass",
		"http://nas/api/?share_code=abc&id=1&receive_code=p%0Ass", "http://nas/api/?share_code=abc&id=1&receive_code=" + strings.Repeat("a", 33),
	} {
		if _, err := a.parseSTRMURL(raw, false, ""); err == nil {
			t.Errorf("接受了不支持或异常的 STRM: %s", raw)
		}
	}
	if _, err := a.parseSTRMURL("http://nas/api/?share_code=abc&id=1&receive_code=", false, ""); err != nil {
		t.Fatal("应允许显式空提取码", err)
	}
}

func TestRelaxedSTRMStillConfinedToLocalRoot(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "root")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	data := []byte("http://192.168.0.3:8115/api/?share_code=abc&id=123&receive_code=pass")
	if err := os.WriteFile(filepath.Join(root, "movie.strm"), data, 0600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(parent, "outside.strm")
	if err := os.WriteFile(outside, data, 0600); err != nil {
		t.Fatal(err)
	}
	a := NewShareAPI(func() string { return "" })
	a.OutputRoot = root
	h := &embyProxyHandler{share: a, config: service.EmbyProxyConfig{STRMBaseURL: "http://192.168.0.24:3002", EmbyPathPrefix: "/media"}}
	check := func(path string, want bool) {
		t.Helper()
		raw, _ := json.Marshal(path)
		_, err := h.sourceClaim(map[string]json.RawMessage{"Path": raw})
		if (err == nil) != want {
			t.Fatalf("路径 %s: %v", path, err)
		}
	}
	check("/media/movie.strm", true)
	check("/media/../outside.strm", false)
	check("/media2/movie.strm", false)
	check("/media/missing.strm", false)
	if err := os.Symlink(outside, filepath.Join(root, "escape.strm")); err == nil {
		check("/media/escape.strm", false)
	} else {
		t.Log("当前系统无法创建符号链接，跳过符号链接用例")
	}
	h.config.StrictSTRMOrigin = true
	check("/media/movie.strm", false)
}
