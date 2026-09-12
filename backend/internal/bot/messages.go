package bot

import (
	"regexp"
	"strings"

	"strm115/backend/internal/client/p115"
)

type messageShare struct {
	URL      string `json:"url"`
	Password string `json:"password,omitempty"`
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
