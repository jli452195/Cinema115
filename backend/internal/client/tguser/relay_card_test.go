package tguser

import (
	"strings"
	"testing"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/gotd/td/tg"
)

const relayTestED2K = "ed2k://|file|Episode.mkv|12345|0123456789ABCDEF0123456789ABCDEF|/"

func TestRelayResourceExpandsSupportedLinksWithoutNetwork(t *testing.T) {
	message := &tg.Message{ID: 9, Message: "测试影片 1080p\n提取码：abcd\n下载资源", Entities: []tg.MessageEntityClass{
		&tg.MessageEntityTextURL{Offset: 0, Length: 4, URL: "https://115.com/s/sw123456"},
	}, ReplyMarkup: &tg.ReplyInlineMarkup{Rows: []tg.KeyboardButtonRow{{Buttons: []tg.KeyboardButtonClass{
		&tg.KeyboardButtonURL{Text: "电驴", URL: relayTestED2K},
		&tg.KeyboardButtonURL{Text: "重复", URL: relayTestED2K},
		&tg.KeyboardButtonURL{Text: "其他", URL: "https://115.com.attacker.invalid/s/evil"},
		&tg.KeyboardButtonCallback{Text: "回调", Data: []byte("do-not-execute")},
	}}}}}
	resource, ok := RelayResource(message, Channel{ID: "123", Title: "来源", Username: "source_channel"})
	if !ok || len(resource.Links) != 2 || resource.Links[0].URL != "https://115cdn.com/s/sw123456?password=abcd" {
		t.Fatalf("unexpected extracted links: %#v, %v", resource.Links, ok)
	}
	cards, err := RelayCardText(resource, resource.Links)
	if err != nil || len(cards) != 1 || !strings.Contains(cards[0], relayTestED2K) || strings.Contains(cards[0], resource.SourceURL) || strings.Contains(cards[0], "来源：") || strings.Contains(cards[0], "attacker") || strings.Contains(cards[0], "do-not-execute") {
		t.Fatal("card lost links or leaked unsupported markup", cards, err)
	}
	for _, blocked := range []*tg.Message{nil, {Message: message.Message, Entities: message.Entities, Out: true}, {Message: message.Message, Entities: message.Entities, Noforwards: true}} {
		if _, ok := RelayResource(blocked, Channel{ID: "123"}); ok {
			t.Fatal("relayed protected or outgoing message")
		}
	}
}

func TestRelayCardsKeepAllLinksAndUTF16Limits(t *testing.T) {
	resource := ChannelResource{Title: "影片", Text: strings.Repeat("中文𠀀", 2500), SourceURL: "https://t.me/c/123/1"}
	for i := 0; i < 36; i++ {
		resource.Links = append(resource.Links, ChannelLink{Type: "ed2k", URL: "ed2k://|file|" + strings.Repeat(string(rune('A'+i)), 180) + "|123|0123456789ABCDEF0123456789ABCDEF|/"})
	}
	cards, err := RelayCardText(resource, resource.Links)
	if err != nil || len(cards) < 2 {
		t.Fatal("expected split cards", len(cards), err)
	}
	for _, card := range cards {
		if !utf8.ValidString(card) || len(utf16.Encode([]rune(card))) > 4096 || strings.Contains(card, resource.SourceURL) {
			t.Fatal("invalid or oversized Telegram card")
		}
	}
	for _, link := range resource.Links {
		if strings.Count(strings.Join(cards, "\n"), link.URL) != 1 {
			t.Fatal("link lost or repeated", link.URL)
		}
	}
	if strings.Count(strings.Join(cards, "\n"), "𠀀") != 2500 {
		t.Fatal("long description silently truncated")
	}
	_, err = RelayCardText(resource, []ChannelLink{{URL: strings.Repeat("x", 4000)}})
	if err == nil {
		t.Fatal("silently truncated oversized link")
	}
}

func TestRelayCardOnlyContainsChosenNewResources(t *testing.T) {
	old := ChannelLink{Type: "115", URL: "https://115.com/s/old123"}
	newLink := ChannelLink{Type: "ed2k", URL: relayTestED2K}
	resource := ChannelResource{Text: "影片\n" + old.URL + "\n" + newLink.URL, Links: []ChannelLink{old, newLink}, SourceURL: "https://t.me/c/1/2"}
	cards, err := RelayCardText(resource, []ChannelLink{newLink})
	if err != nil || len(cards) != 1 || strings.Contains(cards[0], old.URL) || strings.Count(cards[0], newLink.URL) != 1 {
		t.Fatal("replayed unselected or old link", cards, err)
	}
}
