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

type telegramResources struct {
	CloudLinks          []string
	Shares              []messageShare
	HasMagnet           bool
	Duplicates          int
	InvalidDocumentLine int
}

// 说明和附件分别匹配相邻提取码，再对整条消息的资源去重。
func parseTelegramResources(message *telegramMessage, document ...string) telegramResources {
	text, entities := message.Text, message.Entities
	if text == "" {
		text, entities = message.Caption, message.CaptionEntities
	}
	texts := append([]string{expandTelegramLinkTargets(text, entities)}, document...)
	resources := telegramResources{Shares: []messageShare{}}
	seenCloud := make(map[string]bool)
	seenShares := make(map[messageShareKey]bool)
	for i, source := range texts {
		resources.appendText(source, i > 0, seenCloud, seenShares)
	}
	return resources
}

func (resources *telegramResources) appendText(text string, document bool, seenCloud map[string]bool, seenShares map[messageShareKey]bool) {
	positions := append(magnetLinkPattern.FindAllStringIndex(text, -1), ed2kLinkPattern.FindAllStringIndex(text, -1)...)
	positions = append(positions, shareLinkPattern.FindAllStringIndex(text, -1)...)
	if document {
		// 完整链接在同一位置先匹配，余下的前缀用于定位截断或无法解析的链接。
		positions = append(positions, documentCloudPrefix.FindAllStringIndex(text, -1)...)
	}
	sort.SliceStable(positions, func(i, j int) bool { return positions[i][0] < positions[j][0] })
	var shareText strings.Builder
	shareText.Grow(len(text))
	lastEnd, lastCloudEnd := 0, 0
	for _, position := range positions {
		if position[0] < lastEnd {
			continue
		}
		link := text[position[0]:position[1]]
		// 最外层资源的参数和文件名中的协议文字不再作为另一条资源。
		if strings.HasPrefix(link, "http://") || strings.HasPrefix(link, "https://") {
			lastEnd = position[1]
			continue
		}
		normalized, err := p115.ValidateCloudURL(link)
		if err != nil {
			if document && resources.InvalidDocumentLine == 0 {
				resources.InvalidDocumentLine = strings.Count(text[:position[0]], "\n") + 1
			}
			continue
		}
		lastEnd = position[1]
		shareText.WriteString(text[lastCloudEnd:position[0]])
		shareText.WriteByte(' ')
		lastCloudEnd = position[1]
		if seenCloud[normalized] {
			resources.Duplicates++
			continue
		}
		seenCloud[normalized] = true
		resources.CloudLinks = append(resources.CloudLinks, normalized)
		if strings.EqualFold(normalized[:7], "magnet:") {
			resources.HasMagnet = true
		}
	}
	// 屏蔽云下载链接后复用分享提取码的相邻匹配与去重规则。
	shareText.WriteString(text[lastCloudEnd:])
	shares, repeated := parseMessageShares(shareText.String(), seenShares)
	resources.Shares = append(resources.Shares, shares...)
	resources.Duplicates += repeated
}

var magnetLinkPattern = regexp.MustCompile(`(?i)magnet:\?xt=urn:btih:[A-Z0-9]+(?:&[^\s<>"，。；、（）\[\]{},;]+)*`)
var ed2kLinkPattern = regexp.MustCompile(`(?i)ed2k://\|file\|[^\r\n<>]+?\|/`)
var documentCloudPrefix = regexp.MustCompile(`(?i)magnet:\?xt=urn:btih:|ed2k://\|file\|`)

func telegramMessageText(message *telegramMessage) string {
	if message.Text != "" {
		return message.Text
	}
	return message.Caption
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

func telegramResourceLink(link string) string {
	if normalized, err := p115.ValidateCloudURL(link); err == nil {
		return normalized
	}
	parsed, err := url.Parse(link)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.User != nil {
		return ""
	}
	if _, err := p115.ParseShareURL(link); err != nil {
		return ""
	}
	if match := shareLinkPattern.FindString(link); strings.TrimSuffix(link, "#") != match {
		return ""
	}
	return link
}

// 隐藏链接的显示文字先按 UTF-16 区间替换，再解析真实目标，避免将伪装文字当资源。
func expandTelegramLinkTargets(text string, entities []telegramEntity) string {
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
		link := telegramResourceLink(strings.TrimSpace(entity.URL))
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
	shares, _ := parseMessageShares(text, make(map[messageShareKey]bool))
	return shares
}

type messageShareKey struct{ code, password string }

func parseMessageShares(text string, seen map[messageShareKey]bool) ([]messageShare, int) {
	positions := shareLinkPattern.FindAllStringIndex(text, -1)
	shares := make([]messageShare, 0, len(positions))
	repeated := 0
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
		key := messageShareKey{code: link, password: password}
		if share, err := p115.ParseShareURL(link); err == nil {
			key.code = share.Code
			if password == "" {
				key.password = share.Password
			}
		}
		if seen[key] {
			repeated++
			continue
		}
		seen[key] = true
		shares = append(shares, messageShare{URL: link, Password: password})
	}
	return shares, repeated
}
