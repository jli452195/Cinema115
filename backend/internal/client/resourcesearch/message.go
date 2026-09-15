package resourcesearch

import (
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
)

// Entity 使用 Telegram 的 UTF-16 偏移，不能按 UTF-8 字节截取中文和表情。
type Entity struct {
	Type   string
	Offset int
	Length int
	URL    string
}
type Message struct {
	ID       int
	Text     string
	Entities []Entity
}
type Resource struct {
	Title  string `json:"title"`
	URL    string `json:"url"`
	TMDBID string `json:"tmdbId,omitempty"`
	Size   string `json:"size,omitempty"`
}
type Result struct {
	MessageID  int        `json:"messageId"`
	Items      []Resource `json:"items"`
	Remaining  *int       `json:"remaining,omitempty"`
	Page       *int       `json:"page,omitempty"`
	Empty      bool       `json:"empty"`
	Recognized bool       `json:"recognized"`
}

var quotaPattern = regexp.MustCompile(`本月剩余次数\s*[:：]\s*(\d+)`)
var pagePattern = regexp.MustCompile(`第\s*(\d+)\s*页`)
var tmdbPattern = regexp.MustCompile(`(?i)\{tmdb\s*[-=:：]\s*(\d+)\}`)
var sizePattern = regexp.MustCompile(`(?i)(\d+(?:\.\d+)?\s*(?:[KMGT]i?B|[KMGT]))(?:\s|$)`)

func number(pattern *regexp.Regexp, text string) *int {
	match := pattern.FindStringSubmatch(text)
	if len(match) < 2 {
		return nil
	}
	n, err := strconv.Atoi(match[1])
	if err != nil {
		return nil
	}
	return &n
}

func label(units []uint16, offset, length int) string {
	if offset < 0 || length <= 0 || offset > len(units) || length > len(units)-offset {
		return ""
	}
	end := offset + length
	if offset > 0 && units[offset] >= 0xDC00 && units[offset] <= 0xDFFF {
		return ""
	}
	if end < len(units) && units[end] >= 0xDC00 && units[end] <= 0xDFFF {
		return ""
	}
	return string(utf16.Decode(units[offset:end]))
}

// 仅保留可点击的网页链接；这里不访问外部 URL，也不执行 Telegram 按钮。
func safeURL(value string) string {
	u, err := url.Parse(strings.TrimSpace(value))
	if err != nil || u.Hostname() == "" || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return ""
	}
	return u.String()
}

// Parse 只解析收到的单条消息。是否属于本次搜索由接入层按聊天和消息 ID 确认。
func Parse(message Message) Result {
	result := Result{MessageID: message.ID, Items: make([]Resource, 0, len(message.Entities)), Remaining: number(quotaPattern, message.Text), Page: number(pagePattern, message.Text)}
	result.Empty = strings.Contains(message.Text, "没有找到资源") || strings.Contains(message.Text, "未找到资源")
	result.Recognized = result.Empty || strings.Contains(message.Text, "资源列表")
	if !result.Recognized || result.Empty {
		return result
	}
	units := utf16.Encode([]rune(message.Text))
	entities := append([]Entity(nil), message.Entities...)
	sort.SliceStable(entities, func(i, j int) bool { return entities[i].Offset < entities[j].Offset })
	seen := make(map[string]bool, len(entities))
	for _, entity := range entities {
		if entity.Type != "text_link" && entity.Type != "url" {
			continue
		}
		title := strings.TrimSpace(label(units, entity.Offset, entity.Length))
		if title == "" {
			continue
		}
		target := entity.URL
		if entity.Type == "url" {
			target = title
		}
		target = safeURL(target)
		if target == "" {
			continue
		}
		// 多个清晰度版本可能共用链接，只有标题和链接均相同才去重。
		key := title + "\x00" + target
		if seen[key] {
			continue
		}
		seen[key] = true
		item := Resource{Title: title, URL: target}
		if match := tmdbPattern.FindStringSubmatch(title); len(match) > 1 {
			item.TMDBID = match[1]
		}
		if matches := sizePattern.FindAllStringSubmatch(title, -1); len(matches) > 0 {
			item.Size = matches[len(matches)-1][1]
		}
		result.Items = append(result.Items, item)
	}
	return result
}
