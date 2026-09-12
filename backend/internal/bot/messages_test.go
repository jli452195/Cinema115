package bot

import (
	"reflect"
	"testing"
)

func TestMultipleLinksKeepTheirOwnPasswords(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		want       []messageShare
	}{
		{"分行提取码", "https://115.com/s/a\n提取码：aaaa\nhttps://115.com/s/b\n提取码：bbbb", []messageShare{{"https://115.com/s/a", "aaaa"}, {"https://115.com/s/b", "bbbb"}}},
		{"同一行多个链接", "https://115.com/s/a 提取码: aaaa https://115.com/s/b 提取码: bbbb", []messageShare{{"https://115.com/s/a", "aaaa"}, {"https://115.com/s/b", "bbbb"}}},
		{"行内前缀不串码", "密码: aaaa https://115.com/s/a\n密码: bbbb https://115.com/s/b", []messageShare{{"https://115.com/s/a", "aaaa"}, {"https://115.com/s/b", "bbbb"}}},
		{"链接内密码及标点", "[第一部](https://115cdn.com/s/a?password=aaaa),https://115.com/s/b?password=bbbb；", []messageShare{{"https://115cdn.com/s/a?password=aaaa", ""}, {"https://115.com/s/b?password=bbbb", ""}}},
		{"密码不全局套用", "https://115.com/s/a 提取码: aaaa\nhttps://115.com/s/b", []messageShare{{"https://115.com/s/a", "aaaa"}, {"https://115.com/s/b", ""}}},
		{"同消息重复去重", "https://115.com/s/a?password=aaaa\nhttps://115cdn.com/s/a 提取码: aaaa\nhttps://115.com/s/a?password=bbbb", []messageShare{{"https://115.com/s/a?password=aaaa", ""}, {"https://115.com/s/a?password=bbbb", ""}}},
		{"单链接前置提取码兼容", "提取码：aaaa\nhttps://115.com/s/a", []messageShare{{"https://115.com/s/a", "aaaa"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ParseMessages(tc.text); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("提取结果错误：%+v", got)
			}
		})
	}
}
