package strm

import (
	"strings"
	"testing"
)

func TestED2KPathKeepsStableContentIdentity(t *testing.T) {
	want := ED2K{Hash: strings.Repeat("a", 32), SHA1: strings.Repeat("B", 40), Size: 123456789, Name: "影片 #1+100%.mkv"}
	path, err := ED2KPath(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseED2KPath(path)
	if err != nil || got != want {
		t.Fatalf("路径往返失败：got=%+v err=%v", got, err)
	}
	if _, err := ED2KURL("https://example.test/", want); err != nil {
		t.Fatal(err)
	}
	for _, base := range []string{"file:///tmp", "https://user@example.test", "https://example.test/path", "https://example.test?x=1", "https://example.test#x"} {
		if _, err := ED2KURL(base, want); err == nil {
			t.Errorf("接收了无效服务地址 %q", base)
		}
	}
}

func TestED2KPathRejectsInvalidIdentityAndUnsafeNames(t *testing.T) {
	good := ED2KPrefix + strings.Repeat("a", 32) + "/" + strings.Repeat("b", 40) + "/123/"
	for _, suffix := range []string{"", ".", "..", "%2Ffoo.mkv", "%5Cfoo.mkv", "%00.mkv", "%FF.mkv", "a/b.mkv", "a.mkv?x=1", "a.mkv#x", strings.Repeat("字", 256)} {
		if _, err := ParseED2KPath(good + suffix); err == nil {
			t.Errorf("接收了不安全文件名 %q", suffix)
		}
	}
	for _, path := range []string{
		ED2KPrefix + strings.Repeat("g", 32) + "/" + strings.Repeat("b", 40) + "/123/a.mkv",
		ED2KPrefix + strings.Repeat("a", 32) + "/" + strings.Repeat("b", 32) + "/123/a.mkv",
		strings.Replace(good+"a.mkv", "/123/", "/0/", 1),
		strings.Replace(good+"a.mkv", "/123/", "/9223372036854775808/", 1),
	} {
		if _, err := ParseED2KPath(path); err == nil {
			t.Errorf("接收了无效文件身份 %q", path)
		}
	}
}
