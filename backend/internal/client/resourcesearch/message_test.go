package resourcesearch

import (
	"strings"
	"testing"
	"unicode/utf16"
)

func link(text, title, target string) Entity {
	start := strings.Index(text, title)
	return Entity{Type: "text_link", Offset: len(utf16.Encode([]rune(text[:start]))), Length: len(utf16.Encode([]rune(title))), URL: target}
}
func TestResourceMessage(t *testing.T) {
	a := "[动画电影] 你的名字 (2016) {tmdb-372058} 4K REMUX 50.08G"
	b := "[外语电影] 保镖恋人 (2026) {tmdb-1440098} 2160p WEB-DL 14.85G"
	text := "📁 本月剩余次数：735\n资源列表\n🔗 " + a + "\n🔗 " + b + "\n📄 第 1 页"
	first := link(text, a, "https://t.me/resource_bot?start=one")
	result := Parse(Message{ID: 7, Text: text, Entities: []Entity{link(text, b, "https://115cdn.com/s/example?password=abcd"), first, first}})
	if !result.Recognized || result.Empty || result.Remaining == nil || *result.Remaining != 735 || result.Page == nil || *result.Page != 1 || len(result.Items) != 2 {
		t.Fatal("资源列表、额度、页码或去重错误")
	}
	if result.Items[0].Title != a || result.Items[0].TMDBID != "372058" || result.Items[0].Size != "50.08G" || result.Items[1].TMDBID != "1440098" {
		t.Fatal("UTF-16 链接实体或元数据解析错误")
	}
}
func TestEmptyAndInvalidMessage(t *testing.T) {
	result := Parse(Message{Text: "❌ 没有找到资源\n可以点击下方按钮发起悬赏"})
	if !result.Empty || !result.Recognized || len(result.Items) != 0 {
		t.Fatal("未找到资源不能解析成资源列表")
	}
	if Parse(Message{Text: "请求处理中"}).Recognized {
		t.Fatal("处理中消息不应作为搜索完成")
	}
	text := "资源列表\n😀点击"
	valid := link(text, "点击", "https://example.com/resource")
	for _, entity := range []Entity{{Type: "text_link", Offset: -1, Length: 1, URL: valid.URL}, {Type: "text_link", Offset: 6, Length: 1, URL: valid.URL}, {Type: "text_link", Offset: 0, Length: int(^uint(0) >> 1), URL: valid.URL}, {Type: "text_link", Offset: valid.Offset, Length: valid.Length, URL: "javascript:alert(1)"}} {
		if len(Parse(Message{Text: text, Entities: []Entity{entity}}).Items) > 0 {
			t.Fatal("非法偏移或危险链接未过滤")
		}
	}
}
