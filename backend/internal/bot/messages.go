package bot

import (
	"net/url"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"strm115/backend/internal/client/p115"
)

type messageShare struct {
	URL      string `json:"url"`
	Password string `json:"password,omitempty"`
}

type telegramEntity struct {
	Type   string `json:"type"`
	Offset int    `json:"offset"`
	Length int    `json:"length"`
	URL    string `json:"url"`
}

func parseTelegramMessage(message *telegramMessage) []messageShare {
	if message.Text != "" {
		return ParseMessages(expandTelegramLinks(message.Text, message.Entities))
	}
	return ParseMessages(expandTelegramLinks(message.Caption, message.CaptionEntities))
}

var magnetLinkPattern = regexp.MustCompile(`(?i)magnet:\?xt=urn:btih:[A-Z0-9]{32,40}(?:&[^\s<>"，。；、（）\[\]{},;]+)*`)
var ed2kLinkPattern = regexp.MustCompile(`(?i)ed2k://\|file\|[^\r\n<>]+?\|/`)

func telegramMessageText(message *telegramMessage) string {
	if message.Text != "" {
		return message.Text
	}
	return message.Caption
}

func parseCloudLinks(text string) []string {
	positions := append(magnetLinkPattern.FindAllStringIndex(text, -1), ed2kLinkPattern.FindAllStringIndex(text, -1)...)
	sort.SliceStable(positions, func(i, j int) bool { return positions[i][0] < positions[j][0] })
	links := make([]string, 0, len(positions))
	seen := make(map[string]bool, len(positions))
	for _, position := range positions {
		link := text[position[0]:position[1]]
		if normalized, err := p115.ValidateCloudURL(link); err == nil && !seen[normalized] {
			seen[normalized] = true
			links = append(links, normalized)
		}
	}
	return links
}

func isCloudCommand(text string) bool {
	fields := strings.Fields(strings.TrimSpace(text))
	if len(fields) == 0 {
		return false
	}
	name := strings.ToLower(strings.SplitN(fields[0], "@", 2)[0])
	switch name {
	case "/cloud", "/磁力转存", "/重新转存":
		return true
	default:
		return false
	}
}

// 将文字超链接按原位置展开，复用多链接排序、相邻提取码和去重逻辑。
// Telegram 的偏移量按 UTF-16 计数，不能直接当作 Go 字节下标，特别是含 emoji 的图文说明。
func expandTelegramLinks(text string, entities []telegramEntity) string {
	if len(entities) == 0 {
		return text
	}
	boundaries := make(map[int]int, utf8.RuneCountInString(text)+1)
	units := 0
	for index, r := range text {
		boundaries[units] = index
		units++
		if r > 0xffff {
			units++
		}
	}
	boundaries[units] = len(text)
	type linkSpan struct {
		start, end int
		url        string
	}
	spans := make([]linkSpan, 0, len(entities))
	for _, entity := range entities {
		if entity.Type != "text_link" || entity.Offset < 0 || entity.Length <= 0 || entity.Offset > units || entity.Length > units-entity.Offset {
			continue
		}
		start, startOK := boundaries[entity.Offset]
		end, endOK := boundaries[entity.Offset+entity.Length]
		if !startOK || !endOK {
			continue
		}
		link := strings.TrimSpace(entity.URL)
		parsed, err := url.Parse(link)
		// 只解析直接指向 115 的 HTTP 链接，不跟随外站跳转，也不把伪装的显示文字当作目标。
		if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.User != nil {
			link = ""
		} else if _, err := p115.ParseShareURL(link); err != nil {
			link = ""
		} else if match := shareLinkPattern.FindString(link); strings.TrimSuffix(link, "#") != match {
			link = ""
		}
		spans = append(spans, linkSpan{start: start, end: end, url: link})
	}
	sort.SliceStable(spans, func(i, j int) bool { return spans[i].start < spans[j].start })
	var expanded strings.Builder
	expanded.Grow(len(text))
	last := 0
	for _, span := range spans {
		if span.start < last {
			continue
		}
		expanded.WriteString(text[last:span.start])
		expanded.WriteByte(' ')
		expanded.WriteString(span.url)
		expanded.WriteByte(' ')
		last = span.end
	}
	expanded.WriteString(text[last:])
	return expanded.String()
}

var shareLinkPattern = regexp.MustCompile(`https?://(?:www\.)?(?:115\.com|115cdn\.com)/s/[A-Za-z0-9]+(?:\?[^\s<>"，。；、（）()\[\]{},;]*)?`)
var sharePasswordPattern = regexp.MustCompile(`(?:提取码|访问码|密码)\s*[:：]?\s*([A-Za-z0-9]{4})\b`)

// ParseMessages 按出现顺序提取全部链接；独立提取码只作用于相邻链接，重复分享仅保留一次。
func ParseMessages(text string) []messageShare {
	positions := shareLinkPattern.FindAllStringIndex(text, -1)
	shares := make([]messageShare, 0, len(positions))
	type shareKey struct{ code, password string }
	seen := make(map[shareKey]bool, len(positions))
	for i, position := range positions {
		link := strings.TrimRight(text[position[0]:position[1]], ").,;]}")
		end := len(text)
		if i+1 < len(positions) {
			end = positions[i+1][0]
			// 下一链接所在行的前缀不能被误当成本链接的提取码。
			if lastLine := strings.LastIndex(text[position[1]:end], "\n"); lastLine >= 0 {
				end = position[1] + lastLine + 1
			}
		}
		password := ""
		if match := sharePasswordPattern.FindStringSubmatch(text[position[1]:end]); len(match) > 1 {
			password = match[1]
		}
		// 支持“提取码：abcd 链接...”的行内前缀，但不跨过上一条链接取密码。
		lineStart := strings.LastIndex(text[:position[0]], "\n") + 1
		if password == "" && (i == 0 || lineStart >= positions[i-1][1]) {
			if match := sharePasswordPattern.FindStringSubmatch(text[lineStart:position[0]]); len(match) > 1 {
				password = match[1]
			}
		}
		if len(positions) == 1 && password == "" {
			if match := sharePasswordPattern.FindStringSubmatch(text[:position[0]]); len(match) > 1 {
				password = match[1]
			}
		}
		key := shareKey{code: link, password: password}
		if share, err := p115.ParseShareURL(link); err == nil {
			key.code = share.Code
			if password == "" {
				key.password = share.Password
			}
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		shares = append(shares, messageShare{URL: link, Password: password})
	}
	return shares
}
