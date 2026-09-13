package bot

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf16"
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

func testLinkEntity(text, label, target string) telegramEntity {
	start := strings.Index(text, label)
	return telegramEntity{Type: "text_link", Offset: len(utf16.Encode([]rune(text[:start]))), Length: len(utf16.Encode([]rune(label))), URL: target}
}

func TestTelegramHiddenLinks(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		links      [][2]string
		want       []messageShare
	}{
		{"截图中的图文", "🎬 综艺：中国新说唱 (2017) {tmdb-103863} S09E37\n📦 体积：1.62G\n⭐ 评分：7\n🔗 获取链接", [][2]string{{"获取链接", "https://115cdn.com/s/swsa6rn3h9l?password=ayss"}}, []messageShare{{URL: "https://115cdn.com/s/swsa6rn3h9l?password=ayss"}}},
		{"相邻提取码", "🎥 电影甲 获取链接\n提取码：aaaa", [][2]string{{"获取链接", "https://115.com/s/a"}}, []messageShare{{URL: "https://115.com/s/a", Password: "aaaa"}}},
		{"多个隐藏链接按位置匹配密码", "甲链接 提取码：aaaa\n乙链接 提取码：bbbb", [][2]string{{"乙链接", "https://115.com/s/b"}, {"甲链接", "https://115.com/s/a"}}, []messageShare{{URL: "https://115.com/s/a", Password: "aaaa"}, {URL: "https://115.com/s/b", Password: "bbbb"}}},
		{"隐藏与明文混合去重", "甲链接\nhttps://115cdn.com/s/a 提取码：aaaa\nhttps://115.com/s/b", [][2]string{{"甲链接", "https://115.com/s/a?password=aaaa"}}, []messageShare{{URL: "https://115.com/s/a?password=aaaa"}, {URL: "https://115.com/s/b"}}},
		{"同分享不同密码不去重", "甲链接\n乙链接", [][2]string{{"甲链接", "https://115.com/s/a?password=aaaa"}, {"乙链接", "https://115.com/s/a?password=bbbb"}}, []messageShare{{URL: "https://115.com/s/a?password=aaaa"}, {URL: "https://115.com/s/a?password=bbbb"}}},
		{"超链接标签本身是其他分享", "https://115.com/s/shown", [][2]string{{"https://115.com/s/shown", "https://115.com/s/actual"}}, []messageShare{{URL: "https://115.com/s/actual"}}},
		{"外站地址包含115不应提取", "获取链接", [][2]string{{"获取链接", "https://example.com/?next=https://115.com/s/a"}}, []messageShare{}},
		{"伪装显示地址不提取", "https://115.com/s/a", [][2]string{{"https://115.com/s/a", "https://example.com/"}}, []messageShare{}},
		{"非HTTP链接不提取", "获取链接", [][2]string{{"获取链接", "ftp://115.com/s/a"}}, []messageShare{}},
		{"有格式实体的明文仍识别", "🔗 https://115.com/s/a", nil, []messageShare{{URL: "https://115.com/s/a"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entities := []telegramEntity{{Type: "bold", Offset: 0, Length: 2}}
			for _, link := range tc.links {
				entities = append(entities, testLinkEntity(tc.text, link[0], link[1]))
			}
			for _, caption := range []bool{false, true} {
				message := &telegramMessage{Text: tc.text, Entities: entities}
				if caption {
					message = &telegramMessage{Caption: tc.text, CaptionEntities: entities}
				}
				if got := parseTelegramMessage(message); !reflect.DeepEqual(got, tc.want) {
					t.Fatalf("caption=%v, 提取结果不正确：%+v", caption, got)
				}
			}
		})
	}
}

func TestInvalidTelegramEntityOffsets(t *testing.T) {
	text := "🔗 获取链接"
	for _, entity := range []telegramEntity{
		{Type: "text_link", Offset: -1, Length: 4},
		{Type: "text_link", Offset: 3, Length: 0},
		{Type: "text_link", Offset: 3, Length: 1000},
		{Type: "text_link", Offset: 1, Length: 1},
		{Type: "text_link", Offset: 0, Length: 1},
		{Type: "text_link", Offset: int(^uint(0) >> 1), Length: int(^uint(0) >> 1)},
	} {
		entity.URL = "https://115.com/s/invalid"
		if got := parseTelegramMessage(&telegramMessage{Caption: text, CaptionEntities: []telegramEntity{entity}}); len(got) != 0 {
			t.Fatal("非法偏移量被当作链接", got)
		}
	}
	valid := testLinkEntity(text, "获取链接", "https://115.com/s/a")
	if got := parseTelegramMessage(&telegramMessage{Caption: text, CaptionEntities: []telegramEntity{valid, valid}}); len(got) != 1 {
		t.Fatal("重叠实体被重复提取", got)
	}
}
