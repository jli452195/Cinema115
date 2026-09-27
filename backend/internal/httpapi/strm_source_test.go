package httpapi

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"strm115/backend/internal/service"
	"testing"
)

func TestSTRMAnyOriginAndRequiredParameters(t *testing.T) {
	a := NewShareAPI(func() string { return "" })
	const query = "/api/?share_code=abc123&id=3516737870024934541&receive_code=pass"
	for _, extra := range []string{"&filename=movie.mkv", "&foo=1&foo=2&token=ignored", "&id2=other&name=%E7%94%B5%E5%BD%B1"} {
		got, err := a.parseSTRMURL("http://nas" + query + extra)
		if err != nil || got.Code != "abc123" || got.FileID != "3516737870024934541" || got.Password != "pass" {
			t.Fatal("额外参数影响必要参数解析", err)
		}
	}
	for _, origin := range []string{"http://192.168.0.3:8115", "https://other.example:9098", "http://192.168.0.24:3002"} {
		if _, err := a.parseSTRMURL(origin + query); err != nil {
			t.Fatal("不同来源未识别", err)
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
		if _, err := a.parseSTRMURL(raw); err == nil {
			t.Errorf("接受了不支持或异常的 STRM: %s", raw)
		}
	}
	if _, err := a.parseSTRMURL("http://nas/api/?share_code=abc&id=1&receive_code="); err != nil {
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
	h := &embyProxyHandler{share: a, config: service.EmbyProxyConfig{EmbyPathPrefix: "/media"}}
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
}

func TestSharePathSTRMOptionalExtensionAndIgnoredHint(t *testing.T) {
	a := NewShareAPI(func() string { return "" })
	for _, suffix := range []string{"", ".mp4", ".MKV", "?/我有一个朋友.2023.S01E01.2160p.mp4", ".mp4?/我有一个朋友.2023.S01E01.2160p.mp4", "?id=999&share_code=other&receive_code=other", ".mp4?bad=%ZZ"} {
		claim, err := a.parseSTRMURL("http://192.168.0.3:9527/s/swfv55x36j9_fwgl_3374316830196467093" + suffix)
		if err != nil || claim.Code != "swfv55x36j9" || claim.Password != "fwgl" || claim.FileID != "3374316830196467093" {
			t.Fatal("路径参数解析失败", suffix, err)
		}
	}
	claim, err := a.parseSTRMURL("https://other-tool/s/abc__123")
	if err != nil || claim.Password != "" {
		t.Fatal("空提取码未支持", err)
	}
	for _, path := range []string{
		"/s/abc_pass_0", "/s/abc_pass_0123", "/s/abc_pass_-1", "/s/abc_pass_123abc",
		"/s/abc_pass_18446744073709551616", "/s/abc_pass_123.", "/s/abc_pass_123.mp4/extra",
		"/s/abc_pass_123.tar.gz", "/s/abc_pass_123_extra", "/s/abc_pass_123/", "/s/abc_123",
		"/s/abc_pass%5f123.mp4", "/s/abc_pass_123%2Emp4", "/prefix/s/abc_pass_123",
	} {
		if _, err := a.parseSTRMURL("http://nas" + path); err == nil {
			t.Fatal("接受异常分享路径", path)
		}
	}
	normal, err := a.parseSTRMURL("http://nas/api/?share_code=abc&receive_code=&id=123")
	if err != nil || *normal != *claim {
		t.Fatal("两种格式没有归一化到相同文件身份", err)
	}
	root := t.TempDir()
	a.OutputRoot = root
	if err := os.WriteFile(filepath.Join(root, "movie.strm"), []byte("http://other-tool/s/abc__123.mp4?/movie.mp4"), 0600); err != nil {
		t.Fatal(err)
	}
	h := &embyProxyHandler{share: a, config: service.EmbyProxyConfig{EmbyPathPrefix: "/media"}}
	got, err := h.sourceClaim(map[string]json.RawMessage{"Path": json.RawMessage(`"/media/movie.strm"`)})
	if err != nil || *got != *normal {
		t.Fatal("本地 STRM 路径格式未识别", err)
	}
}
