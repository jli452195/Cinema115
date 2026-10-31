package tguser

import (
	"errors"
	"strings"
	"unicode/utf16"

	"github.com/gotd/td/tg"
)

var ErrRelayCardTooLong = errors.New("资源链接过长，无法完整投递到 Telegram")

// RelayResource shares the search extractor but does not query Telegram or 115.
func RelayResource(message *tg.Message, channel Channel) (ChannelResource, bool) {
	if message == nil || message.Noforwards || message.Out {
		return ChannelResource{}, false
	}
	return channelMessage(message, channel, "")
}

// RelayCardText expands the selected resources into the post itself. It never
// carries reply markup, callback buttons or hidden resource links to a target.
// Resource links are not truncated, even when the original caption is long.
func RelayCardText(resource ChannelResource, links []ChannelLink) ([]string, error) {
	if len(links) == 0 {
		return nil, nil
	}
	body := channelURLPattern.ReplaceAllStringFunc(resource.Text, func(raw string) string {
		if shareLink(raw) != "" || strings.HasPrefix(strings.ToLower(raw), "ed2k://") {
			return ""
		}
		return raw
	})
	body = strings.TrimSpace(body)
	if body == "" {
		body = resource.Title
	}
	cards := make([]string, 0, 1)
	var cardError error
	var group strings.Builder
	flush := func() {
		if group.Len() == 0 {
			return
		}
		tail := "\n\n资源链接：\n" + strings.TrimSpace(group.String())
		budget := 4096 - len(utf16.Encode([]rune(tail)))
		// Preserve the original description. Long posts become ordered cards,
		// while resource URLs always remain intact in a single card.
		for len(utf16.Encode([]rune(body))) > budget {
			prefix := relayTextPrefix(body, 4096)
			cards = append(cards, prefix)
			body = body[len(prefix):]
		}
		cards = append(cards, strings.TrimSpace(body+tail))
		body = ""
		group.Reset()
	}
	for _, link := range links {
		if len(utf16.Encode([]rune(link.URL))) > 3700 {
			cardError = ErrRelayCardTooLong
			continue
		}
		if len(utf16.Encode([]rune(group.String()+link.URL))) > 2800 {
			flush()
		}
		group.WriteString(link.URL)
		group.WriteByte('\n')
	}
	flush()
	return cards, cardError
}

func relayTextPrefix(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	units := 0
	for index, r := range value {
		n := 1
		if r > 0xffff {
			n = 2
		}
		if units+n > limit {
			return value[:index]
		}
		units += n
	}
	return value
}
