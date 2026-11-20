package httpapi

import (
	"strings"
	"testing"
)

func TestShareSTRMOriginsAndValidation(t *testing.T) {
	a := NewShareAPI(func() string { return "" })
	const path = "/api/share115/abc123/0123456789abcdef0123456789abcdef01234567/pass/3516737870024934541"
	for _, origin := range []string{"http://nas:8115", "https://other.example:9098"} {
		got, err := a.parseSTRMURL(origin + path)
		if err != nil || got.Code != "abc123" || got.Password != "pass" || got.FileID != "3516737870024934541" || got.SHA1 != "0123456789ABCDEF0123456789ABCDEF01234567" {
			t.Fatal(got, err)
		}
	}
	for _, raw := range []string{
		"ftp://nas" + path, "//nas" + path, "http://user:pass@nas" + path, "http://nas:99999" + path,
		"http://nas:" + path, "http://nas" + path + "#fragment", "http://nas" + path + "?", "http://nas" + path + "?id=2",
		"http://nas" + path + "/extra", "http://nas" + path + "/", "http://nas" + strings.Replace(path, "abc123", "abc%2F123", 1),
		"http://nas" + strings.Replace(path, "abc123", "%61bc123", 1),
		"http://nas/api/?share_code=abc&id=1&receive_code=", "http://nas/api/stream/share/obsolete.payload",
	} {
		if _, err := a.parseSTRMURL(raw); err == nil {
			t.Error("accepted", raw)
		}
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
	if _, err := a.parseSTRMURL("https://other-tool/s/abc__123"); err == nil {
		t.Fatal("接受空提取码")
	}
	claim, err := a.parseSTRMURL("https://other-tool/s/abc_pass_123")
	if err != nil {
		t.Fatal(err)
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
	normal, err := a.parseSTRMURL("http://nas/api/share115/abc/0123456789ABCDEF0123456789ABCDEF01234567/pass/123")
	if err != nil || normal.Code != claim.Code || normal.Password != claim.Password || normal.FileID != claim.FileID {
		t.Fatal("两种格式没有归一化到相同文件身份", err)
	}
}
