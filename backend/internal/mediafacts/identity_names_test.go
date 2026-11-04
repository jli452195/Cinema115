package mediafacts

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
)

func TestNameKeyUnicodeAndTraditionalCharacters(t *testing.T) {
	for _, pair := range [][2]string{
		{"ＷＯＲＫ　１２３", "work123"},
		{"ｶﾞﾝﾀﾞﾑ", "ガンダム"},
		{"Cafe\u0301", "Café"},
		{"堀與宮村", "堀与宫村"},
		{"我們這一家", "我们这一家"},
		{"進擊的巨人", "进击的巨人"},
		{"劇場版：繁體電影", "剧场版繁体电影"},
	} {
		if got, want := NameKey(pair[0]), NameKey(pair[1]); got != want {
			t.Errorf("%q => %q; want %q => %q", pair[0], got, pair[1], want)
		}
	}
	if NameKey("Work One") == NameKey("Work Two") {
		t.Fatal("名称规范化不能合并不同标题")
	}
}

func TestNameSimplificationDataIntegrity(t *testing.T) {
	normalized := strings.ReplaceAll(nameTraditionalCharacters, "\r\n", "\n")
	if got := fmt.Sprintf("%x", sha256.Sum256([]byte(normalized))); got != "6b5a0a799bea2bb22c001f635eaa3fc2904310f0c08addbff275477a80ecf09a" {
		t.Fatalf("OpenCC 单字表与固定上游版本不一致：%s", got)
	}
	if got := len(nameSimplifications); got != 4113 {
		t.Fatalf("OpenCC 单字表读取不完整：%d", got)
	}
}
