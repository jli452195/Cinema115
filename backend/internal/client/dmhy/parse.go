package dmhy

import (
	"bytes"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/html"
	"strm115/backend/internal/btresource"
)

type Item struct {
	ID            string `json:"id"`
	Title         string `json:"title"`
	Size          string `json:"size"`
	UpdatedAt     string `json:"updatedAt"`
	Magnet        string `json:"magnet"`
	MagnetIssue   string `json:"magnetIssue,omitempty"`
	URL           string `json:"url"`
	TorrentURL    string `json:"torrentUrl"`
	GroupID       string `json:"groupId,omitempty"`
	Fansub        string `json:"fansub,omitempty"`
	Publisher     string `json:"publisher,omitempty"`
	CategoryID    int    `json:"categoryId"`
	CategoryName  string `json:"categoryName"`
	CategoryScope string `json:"categoryScope"`
	Seeders       int    `json:"seeders"`
	Leechers      int    `json:"leechers"`
	Completed     int    `json:"completed"`
}

type Group struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Result struct {
	Items    []Item  `json:"items"`
	Groups   []Group `json:"groups"`
	HasMore  bool    `json:"hasMore"`
	RawCount int     `json:"-"`
}

type Detail struct {
	ID         string
	Magnet     string
	TorrentURL string
}

var postPath = regexp.MustCompile(`^/topics/view/([1-9][0-9]{0,11})_[^/]*\.html$`)
var sortPath = regexp.MustCompile(`^/topics/list/sort_id/([1-9][0-9]{0,4})$`)
var groupPath = regexp.MustCompile(`^/topics/list/team_id/([1-9][0-9]{0,11})$`)
var dateText = regexp.MustCompile(`[0-9]{4}/[0-9]{2}/[0-9]{2} [0-9]{2}:[0-9]{2}`)

func CategoryScope(id int) string {
	switch id {
	case 2, 31, 6, 7, 12:
		return "film"
	case 3, 41, 42, 9, 17, 18, 19, 20, 21:
		return "other"
	default:
		// 未知分类与音乐、其他一起作为补充保留，避免上游新增分类时静默丢失。
		return "supplement"
	}
}

func attr(node *html.Node, key string) string {
	for _, value := range node.Attr {
		if value.Key == key {
			return value.Val
		}
	}
	return ""
}

func walk(node *html.Node, visit func(*html.Node)) {
	visit(node)
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		walk(child, visit)
	}
}

func text(node *html.Node) string {
	var output strings.Builder
	var collect func(*html.Node)
	collect = func(current *html.Node) {
		if current.Type == html.ElementNode && (current.Data == "script" || current.Data == "style") {
			return
		}
		if current.Type == html.TextNode {
			output.WriteString(current.Data)
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			collect(child)
		}
	}
	collect(node)
	return strings.Join(strings.Fields(output.String()), " ")
}

func findID(node *html.Node, id string) *html.Node {
	var found *html.Node
	walk(node, func(current *html.Node) {
		if found == nil && current.Type == html.ElementNode && attr(current, "id") == id {
			found = current
		}
	})
	return found
}

func blocked(node *html.Node) bool {
	var found bool
	walk(node, func(current *html.Node) {
		if current.Type != html.ElementNode {
			return
		}
		id := attr(current, "id")
		if id == "challenge-form" || id == "cf-challenge-running" || strings.HasPrefix(id, "cf-chl-") {
			found = true
		}
		if current.Data == "title" {
			title := strings.ToLower(text(current))
			if strings.Contains(title, "just a moment") || strings.Contains(title, "attention required") || strings.Contains(title, "captcha") {
				found = true
			}
		}
	})
	return found
}

func sitePath(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return "", false
	}
	if u.IsAbs() {
		if u.Scheme != "https" || u.Host != "share.dmhy.org" {
			return "", false
		}
	} else if u.Host != "" || !strings.HasPrefix(u.Path, "/") {
		return "", false
	}
	return u.EscapedPath(), true
}

func ParseSearch(body []byte, page int) (Result, error) {
	out := Result{Items: make([]Item, 0, 100), Groups: make([]Group, 0, 16)}
	if page < 1 || page > MaxPage || len(body) > 16<<20 {
		return out, ErrInput
	}
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return out, ErrParse
	}
	if blocked(doc) {
		return out, ErrBlocked
	}
	table := findID(doc, "topic_list")
	if table == nil || table.Data != "table" {
		// 原站零结果不输出表格，需要同时确认搜索页表单和明确的空结果标记。
		searchForm, empty := false, false
		walk(doc, func(current *html.Node) {
			if current.Type == html.ElementNode && current.Data == "input" && attr(current, "name") == "keyword" {
				searchForm = true
			}
			if current.Type == html.TextNode && strings.TrimSpace(current.Data) == "沒有可顯示資源" {
				empty = true
			}
		})
		if searchForm && empty {
			return out, nil
		}
		return out, ErrParse
	}
	seen := make(map[string]bool, 100)
	groups := make(map[string]bool, 16)
	var rowErr error
	walk(table, func(row *html.Node) {
		if rowErr != nil || row.Type != html.ElementNode || row.Data != "tr" {
			return
		}
		cells := make([]*html.Node, 0, 9)
		for child := row.FirstChild; child != nil; child = child.NextSibling {
			if child.Type == html.ElementNode && child.Data == "td" {
				cells = append(cells, child)
			}
		}
		if len(cells) == 0 {
			return
		}
		if len(cells) != 9 || out.RawCount >= 1000 {
			rowErr = ErrParse
			return
		}
		item, err := parseRow(cells)
		if err != nil {
			rowErr = err
			return
		}
		out.RawCount++
		if seen[item.ID] {
			return
		}
		seen[item.ID] = true
		out.Items = append(out.Items, item)
		if item.GroupID != "" && !groups[item.GroupID] {
			groups[item.GroupID] = true
			out.Groups = append(out.Groups, Group{ID: item.GroupID, Name: item.Fansub})
		}
	})
	if rowErr != nil || out.RawCount == 0 {
		// 真正的零结果由原站明确提示；空表格可能是结构改动或拦截页。
		if rowErr == nil {
			rowErr = ErrParse
		}
		return out, rowErr
	}
	walk(doc, func(current *html.Node) {
		if current.Type != html.ElementNode || current.Data != "a" || text(current) != "下一頁" {
			return
		}
		u, err := url.Parse(attr(current, "href"))
		if err != nil || u.IsAbs() || u.Host != "" || u.Fragment != "" {
			return
		}
		if u.Path == "/topics/list/page/"+strconv.Itoa(page+1) && page < MaxPage {
			out.HasMore = true
		}
	})
	return out, nil
}

func parseRow(cells []*html.Node) (Item, error) {
	out := Item{UpdatedAt: dateText.FindString(text(cells[0])), Size: text(cells[4]), Publisher: text(cells[8])}
	var bad bool
	walk(cells[1], func(current *html.Node) {
		if current.Type != html.ElementNode || current.Data != "a" {
			return
		}
		path, valid := sitePath(attr(current, "href"))
		parts := sortPath.FindStringSubmatch(path)
		if valid && len(parts) == 2 {
			out.CategoryID, _ = strconv.Atoi(parts[1])
			out.CategoryName = text(current)
		}
	})
	walk(cells[2], func(current *html.Node) {
		if current.Type != html.ElementNode || current.Data != "a" {
			return
		}
		path, valid := sitePath(attr(current, "href"))
		if !valid {
			return
		}
		if parts := postPath.FindStringSubmatch(path); len(parts) == 2 {
			if out.ID != "" && out.ID != parts[1] {
				bad = true
			}
			out.ID, out.Title = parts[1], text(current)
			out.URL = BaseURL + path
		}
		if parts := groupPath.FindStringSubmatch(path); len(parts) == 2 {
			out.GroupID, out.Fansub = parts[1], text(current)
		}
	})
	out.Magnet, out.MagnetIssue = collectMagnet(cells[3])
	for index, target := range map[int]*int{5: &out.Seeders, 6: &out.Leechers, 7: &out.Completed} {
		value := strings.ReplaceAll(text(cells[index]), ",", "")
		if value == "-" {
			// 旧帖没有跟踪器统计时原站使用横线，保留未知值而不丢弃资源。
			*target = -1
			continue
		}
		number, err := strconv.Atoi(value)
		if err != nil || number < 0 {
			bad = true
		}
		*target = number
	}
	if bad || out.ID == "" || out.Title == "" || len(out.Title) > 4096 || out.UpdatedAt == "" || out.CategoryID == 0 || out.CategoryName == "" || len(out.Publisher) > 1024 || len(out.Fansub) > 1024 {
		return out, ErrParse
	}
	out.CategoryScope = CategoryScope(out.CategoryID)
	return out, nil
}

func ParseDetail(body []byte, id string) (Detail, error) {
	out := Detail{}
	if !numericID.MatchString(id) || len(body) > 16<<20 {
		return out, ErrInput
	}
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return out, ErrParse
	}
	if blocked(doc) {
		return out, ErrBlocked
	}
	var valid bool
	out.ID, valid = detailIdentity(doc)
	if !valid || out.ID != id {
		return out, ErrParse
	}
	root := findID(doc, "resource-tabs")
	if root == nil {
		return out, ErrParse
	}
	var issue string
	out.Magnet, issue = collectMagnet(root)
	if issue != "" {
		return out, ErrParse
	}
	torrentLinks := make([]string, 0, 2)
	walk(root, func(current *html.Node) {
		if current.Type != html.ElementNode || current.Data != "a" {
			return
		}
		href := attr(current, "href")
		if strings.Contains(href, ".torrent") {
			if strings.HasPrefix(href, "//") {
				href = "https:" + href
			}
			torrentLinks = append(torrentLinks, href)
		}
	})
	hash, _ := btresource.MagnetHash(out.Magnet)
	for _, candidate := range torrentLinks {
		endpoint, err := btresource.TorrentURL("dmhy", hash, candidate)
		if err != nil {
			return out, ErrParse
		}
		if out.TorrentURL != "" && out.TorrentURL != endpoint {
			return out, ErrParse
		}
		out.TorrentURL = endpoint
	}
	return out, nil
}

func hasClass(node *html.Node, class string) bool {
	for _, value := range strings.Fields(attr(node, "class")) {
		if value == class {
			return true
		}
	}
	return false
}

// collectMagnet 只读取调用方限定区域内的原生磁力，统一校验普通链接和迅雷数据属性。
// 单条缺失或损坏不影响搜索页其他资源；多个来源必须指向同一份种子。
func collectMagnet(root *html.Node) (string, string) {
	hash, invalid, conflict := "", false, false
	accept := func(raw string) {
		actual, err := nativeMagnetHash(raw)
		if err != nil {
			invalid = true
			return
		}
		if hash != "" && hash != actual {
			conflict = true
		}
		hash = actual
	}
	walk(root, func(node *html.Node) {
		if node.Type != html.ElementNode || node.Data != "a" {
			return
		}
		if href := strings.TrimSpace(attr(node, "href")); len(href) >= len("magnet:") && strings.EqualFold(href[:len("magnet:")], "magnet:") {
			accept(href)
		}
		if raw := strings.TrimSpace(attr(node, "data-magnet")); raw != "" {
			accept(raw)
		}
	})
	switch {
	case conflict:
		return "", "conflict"
	case invalid:
		return "", "invalid"
	case hash == "":
		return "", "missing"
	default:
		return "magnet:?xt=urn:btih:" + hash, ""
	}
}

func nativeMagnetHash(raw string) (string, error) {
	// 少量旧帖附有数百个 tracker，先提取唯一身份，避免冗长参数遮蔽合法资源。
	if len(raw) > 256<<10 || strings.ContainsAny(raw, "\r\n\x00") {
		return "", ErrParse
	}
	u, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(u.Scheme, "magnet") || u.Host != "" || u.User != nil || u.Path != "" || u.Fragment != "" || u.Opaque != "" {
		return "", ErrParse
	}
	values, err := url.ParseQuery(u.RawQuery)
	if err != nil || len(values["xt"]) != 1 {
		return "", ErrParse
	}
	return btresource.MagnetHash("magnet:?xt=" + url.QueryEscape(values.Get("xt")))
}

// detailIdentity 只从页面规范地址或标题区的原站分享链接提取原帖编号，忽略侧栏推荐。
func detailIdentity(doc *html.Node) (string, bool) {
	id, bad := "", false
	accept := func(raw string) {
		u, err := url.Parse(raw)
		if err != nil || u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Host != "share.dmhy.org" || u.Scheme != "https" && u.Scheme != "http" {
			bad = true
			return
		}
		parts := postPath.FindStringSubmatch(u.EscapedPath())
		if len(parts) != 2 || id != "" && id != parts[1] {
			bad = true
			return
		}
		id = parts[1]
	}
	walk(doc, func(node *html.Node) {
		if node.Type != html.ElementNode {
			return
		}
		if node.Data == "link" && strings.EqualFold(attr(node, "rel"), "canonical") {
			accept(attr(node, "href"))
		}
		if node.Data == "meta" && strings.EqualFold(attr(node, "property"), "og:url") {
			accept(attr(node, "content"))
		}
		if !hasClass(node, "topic-title") {
			return
		}
		walk(node, func(share *html.Node) {
			if share.Type != html.ElementNode || !hasClass(share, "share") {
				return
			}
			walk(share, func(link *html.Node) {
				if link.Type != html.ElementNode || link.Data != "a" {
					return
				}
				u, err := url.Parse(attr(link, "href"))
				if err != nil || u.Path != "/js/share.html" {
					return
				}
				sameSite := u.Scheme == "" && u.Host == "" || u.Scheme == "https" && u.Host == "share.dmhy.org"
				if !sameSite || u.User != nil || u.Opaque != "" || u.Fragment != "" || len(u.Query()["url"]) != 1 {
					bad = true
					return
				}
				accept(u.Query().Get("url"))
			})
		})
	})
	return id, id != "" && !bad
}
