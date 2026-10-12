package woniu

import (
	"encoding/base32"
	"encoding/hex"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

var detailID = regexp.MustCompile(`^/voddetail/([0-9]{1,12})/$`)
var posterName = regexp.MustCompile(`^[a-fA-F0-9]{32}\.(jpg|jpeg|png|webp)$`)
var posterDirectory = regexp.MustCompile(`^[0-9]{8}(?:-[1-9][0-9]{0,3})?$`)
var yearPattern = regexp.MustCompile(`\b(19[0-9]{2}|20[0-9]{2})\b`)
var webURL = regexp.MustCompile(`https?://[^\s<>"'（）()]+`)
var passwordPattern = regexp.MustCompile(`(?i)(?:提取码|访问码|密码|pwd|password)\s*[:：=]?\s*([0-9A-Za-z]{3,12})`)
var sizePattern = regexp.MustCompile(`(?i)([0-9]+(?:\.[0-9]+)?\s*(?:TB|GB|MB|T|G|M))\b`)
var hexHash = regexp.MustCompile(`(?i)^[a-f0-9]{40}$`)

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}
func class(n *html.Node, value string) bool {
	for _, c := range strings.Fields(attr(n, "class")) {
		if c == value {
			return true
		}
	}
	return false
}
func find(root *html.Node, match func(*html.Node) bool) *html.Node {
	if match(root) {
		return root
	}
	for n := root.FirstChild; n != nil; n = n.NextSibling {
		if value := find(n, match); value != nil {
			return value
		}
	}
	return nil
}
func each(root *html.Node, match func(*html.Node) bool, visit func(*html.Node)) {
	if match(root) {
		visit(root)
	}
	for n := root.FirstChild; n != nil; n = n.NextSibling {
		each(n, match, visit)
	}
}
func text(root *html.Node) string {
	if root == nil {
		return ""
	}
	var result strings.Builder
	var collect func(*html.Node)
	collect = func(n *html.Node) {
		if n.Data == "script" || n.Data == "style" {
			return
		}
		if n.Type == html.TextNode {
			result.WriteString(n.Data)
			result.WriteByte(' ')
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			collect(child)
		}
	}
	collect(root)
	return strings.Join(strings.Fields(result.String()), " ")
}
func classText(root *html.Node, value string) string {
	return text(find(root, func(n *html.Node) bool { return class(n, value) }))
}

func parseSearch(data []byte, base *url.URL, query, method string, page int) (SearchResponse, error) {
	doc, err := html.Parse(strings.NewReader(string(data)))
	if err != nil {
		return SearchResponse{}, ErrFormat
	}
	content := text(doc)
	if strings.Contains(content, "请不要频繁操作") || strings.Contains(content, "搜索时间间隔") {
		return SearchResponse{}, ErrRateLimited
	}
	if strings.Contains(content, "验证码") && strings.Contains(content, "验证") {
		return SearchResponse{}, ErrChallenge
	}
	if !strings.HasPrefix(text(find(doc, func(n *html.Node) bool { return n.Data == "title" })), "搜索") || strings.Contains(content, "跳转提示") {
		return SearchResponse{}, ErrFormat
	}
	result := SearchResponse{Query: query, MatchedBy: method, Page: page, Items: make([]SearchItem, 0, MaxItemsPerPage)}
	seen := make(map[string]bool, MaxItemsPerPage)
	each(doc, func(n *html.Node) bool { return n.Data == "a" && class(n, "video-card") }, func(n *html.Node) {
		u, e := url.Parse(attr(n, "href"))
		if e != nil {
			return
		}
		u = base.ResolveReference(u)
		if u.Host != base.Host || u.Scheme != base.Scheme || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return
		}
		match := detailID.FindStringSubmatch(u.Path)
		if len(match) != 2 || seen[match[1]] {
			return
		}
		title := strings.TrimSpace(attr(n, "title"))
		if title == "" {
			title = classText(n, "video-title")
		}
		if title == "" {
			return
		}
		seen[match[1]] = true
		meta := classText(n, "video-meta")
		item := SearchItem{ID: match[1], Title: title, MediaType: "other", Links: []ResourceLink{}, DetailStatus: "failed"}
		if y := yearPattern.FindString(meta); y != "" {
			item.Year, _ = strconv.Atoi(y)
		}
		for _, typ := range []struct{ label, kind string }{{"连续剧", "tv"}, {"电影", "movie"}, {"动漫", "anime"}} {
			if strings.Contains(meta, typ.label) {
				item.MediaType = typ.kind
				break
			}
		}
		if poster := find(n, func(n *html.Node) bool { return n.Data == "img" }); poster != nil {
			if image, e := url.Parse(attr(poster, "src")); e == nil {
				image = base.ResolveReference(image)
				if image.Host == base.Host && image.Scheme == base.Scheme && image.RawQuery == "" && image.Fragment == "" && image.User == nil && strings.HasPrefix(image.Path, "/upload/vod/") {
					parts := strings.Split(strings.TrimPrefix(image.Path, "/upload/vod/"), "/")
					if len(parts) == 1 || len(parts) == 2 && posterDirectory.MatchString(parts[0]) {
						directory := "vod"
						if len(parts) == 2 {
							directory = parts[0]
						}
						name := parts[len(parts)-1]
						if posterName.MatchString(name) {
							item.CoverURL = "/api/woniu/image/" + directory + "/" + name
						}
					}
				}
			}
		}
		if len(result.Items) < MaxItemsPerPage {
			result.Items = append(result.Items, item)
		} else {
			result.Truncated = true
		}
	})
	// Only accept the actual next-page route for this query. No arbitrary URL
	// from the page is ever followed or accepted from the frontend.
	expected := "/vodsearch/" + query + "----------" + strconv.Itoa(page+1) + "---/"
	if find(doc, func(n *html.Node) bool {
		if n.Data != "a" {
			return false
		}
		u, e := url.Parse(attr(n, "href"))
		if e != nil {
			return false
		}
		u = base.ResolveReference(u)
		return u.Host == base.Host && u.Scheme == base.Scheme && u.User == nil && u.Path == expected && u.RawQuery == "" && u.Fragment == ""
	}) != nil {
		if page < MaxPages {
			result.HasMore = true
			result.NextPage = page + 1
		} else {
			result.Truncated = true
		}
	}
	result.Total = len(result.Items)
	result.Attempts = []SearchAttempt{{Method: method, Query: query, Total: result.Total, Truncated: result.Truncated}}
	return result, nil
}

func parseDetail(data []byte, item SearchItem) (SearchItem, error) {
	doc, err := html.Parse(strings.NewReader(string(data)))
	if err != nil {
		return item, ErrFormat
	}
	if find(doc, func(n *html.Node) bool { return class(n, "pan-lock-box") || class(n, "is-locked") }) != nil {
		return item, ErrSession
	}
	if strings.Contains(text(doc), "https://******") {
		return item, ErrSession
	}
	if classText(doc, "premium-title") == "" {
		return item, ErrFormat
	}
	if find(doc, func(n *html.Node) bool { return class(n, "pan-group") || class(n, "pan-link-item") }) == nil {
		content := text(doc)
		if !strings.Contains(content, "网盘资源") || !strings.Contains(content, "暂无") {
			return item, ErrFormat
		}
	}
	item.Description = classText(doc, "premium-desc")
	updated := ""
	each(doc, func(n *html.Node) bool { return class(n, "meta-item") }, func(n *html.Node) {
		if classText(n, "m-label") == "更新时间" {
			updated = classText(n, "m-val")
		}
	})
	seen := make(map[string]bool, 8)
	item.Links = make([]ResourceLink, 0, 8)
	each(doc, func(n *html.Node) bool { return class(n, "pan-link-item") }, func(n *html.Node) {
		if len(item.Links) >= 200 {
			return
		}
		note := classText(n, "pan-link-title")
		if note == "" {
			note = classText(n, "pan-link-name")
		}
		candidates := []string{attr(n, "data-pan-item")}
		each(n, func(n *html.Node) bool {
			return n.Data == "a" || attr(n, "data-copy") != "" || class(n, "pan-link-meta")
		}, func(n *html.Node) {
			candidates = append(candidates, attr(n, "href"), attr(n, "data-copy"))
			if class(n, "pan-link-meta") {
				candidates = append(candidates, text(n))
			}
		})
		for _, raw := range candidates {
			link, ok := normalizeLink(raw, text(n), note, item.Title)
			if !ok {
				continue
			}
			key := link.URL + "\x00" + link.Password
			if !seen[key] {
				link.UpdatedAt = updated
				item.Links = append(item.Links, link)
				seen[key] = true
			}
			break
		}
	})
	item.DetailStatus = "empty"
	if len(item.Links) > 0 {
		item.DetailStatus = "ready"
	}
	return item, nil
}

func normalizeLink(raw, context, note, title string) (ResourceLink, bool) {
	raw = strings.TrimSpace(raw)
	link := ResourceLink{Category: "pan", Type: "other", TypeLabel: "其他网盘", WorkTitle: note}
	if note == "" {
		link.WorkTitle = title
	}
	if strings.HasPrefix(strings.ToLower(raw), "magnet:?") {
		u, err := url.Parse(raw)
		if err != nil {
			return ResourceLink{}, false
		}
		valid := false
		canonicalHash := ""
		for _, xt := range u.Query()["xt"] {
			if !strings.HasPrefix(strings.ToLower(xt), "urn:btih:") {
				continue
			}
			hash := xt[len("urn:btih:"):]
			if hexHash.MatchString(hash) {
				valid = true
				canonicalHash = strings.ToLower(hash)
			} else if len(hash) == 32 {
				decoded, err := base32.StdEncoding.DecodeString(strings.ToUpper(hash))
				valid = err == nil && len(decoded) == 20
				if valid {
					canonicalHash = hex.EncodeToString(decoded)
				}
			}
			if valid {
				break
			}
		}
		if !valid {
			return ResourceLink{}, false
		}
		params := u.Query()
		params.Del("xt")
		canonical := "magnet:?xt=urn:btih:" + canonicalHash
		if rest := params.Encode(); rest != "" {
			canonical += "&" + rest
		}
		link.Category, link.Type, link.TypeLabel, link.URL = "torrent", "magnet", "磁力", canonical
		return link, true
	}
	value := strings.TrimRight(webURL.FindString(raw), ".,;，。；]】")
	u, err := url.Parse(value)
	if err != nil || u.Host == "" || u.User != nil || u.Port() != "" || (u.Scheme != "http" && u.Scheme != "https") || strings.Contains(value, "******") {
		return ResourceLink{}, false
	}
	host := strings.ToLower(u.Hostname())
	if net.ParseIP(host) != nil || host == "localhost" || strings.HasSuffix(host, ".local") {
		return ResourceLink{}, false
	}
	for _, provider := range []struct {
		hosts       []string
		kind, label string
	}{
		{[]string{"115.com", "115cdn.com", "anxia.com"}, "115", "115"},
		{[]string{"pan.baidu.com"}, "baidu", "百度"}, {[]string{"pan.quark.cn"}, "quark", "夸克"},
		{[]string{"drive.uc.cn"}, "uc", "UC"}, {[]string{"alipan.com", "aliyundrive.com"}, "aliyun", "阿里云盘"},
		{[]string{"pan.xunlei.com"}, "xunlei", "迅雷"}, {[]string{"cloud.189.cn"}, "tianyi", "天翼"},
		{[]string{"123pan.com", "123pan.cn", "123684.com", "123685.com", "123912.com", "123865.com", "123592.com"}, "123", "123"},
		{[]string{"guangyapan.com"}, "guangya", "光鸭网盘"}, {[]string{"caiyun.139.com", "yun.139.com"}, "mobile", "移动云盘"},
	} {
		for _, root := range provider.hosts {
			if host == root || strings.HasSuffix(host, "."+root) {
				link.Type, link.TypeLabel = provider.kind, provider.label
				break
			}
		}
		if link.Type != "other" {
			break
		}
	}
	link.URL = u.String()
	for _, key := range []string{"password", "pwd", "passcode", "code"} {
		if value := u.Query().Get(key); value != "" {
			link.Password = value
			break
		}
	}
	if link.Password == "" {
		if match := passwordPattern.FindStringSubmatch(context); len(match) == 2 {
			link.Password = match[1]
		}
	}
	if match := sizePattern.FindStringSubmatch(note); len(match) == 2 {
		link.Size = match[1]
	}
	return link, true
}
