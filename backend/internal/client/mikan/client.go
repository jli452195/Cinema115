package mikan

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"golang.org/x/net/html"
)

const BaseURL = "https://mikanani.me"

var (
	ErrInput          = errors.New("蜜柑搜索词、番剧或字幕组参数无效")
	ErrBlocked        = errors.New("蜜柑访问受限或需要安全验证，请稍后重试或访问原站")
	ErrParse          = errors.New("蜜柑页面结构异常，无法确认搜索结果，请访问原站核对")
	ErrNetwork        = errors.New("无法连接蜜柑，请检查网络后重试")
	hashPattern       = regexp.MustCompile(`(?i)^[a-f0-9]{40}$`)
	groupPattern      = regexp.MustCompile(`^[0-9]{1,12}$`)
	resolutionPattern = regexp.MustCompile(`(?i)(?:\b(2160|1080|720|480)[pi]\b|(?:3840|1920|1280|640)[x×](2160|1080|720|480)\b|\b(4k)\b)`)
)

type Item struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Size       string `json:"size"`
	UpdatedAt  string `json:"updatedAt"`
	Magnet     string `json:"magnet"`
	URL        string `json:"url"`
	TorrentURL string `json:"torrentUrl"`
	Resolution string `json:"resolution"`
	GroupID    string `json:"groupId,omitempty"`
}

type Group struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type Result struct {
	Items    []Item      `json:"items"`
	Groups   []Group     `json:"groups"`
	Bangumis []Bangumi   `json:"bangumis"`
	Bangumi  *Bangumi    `json:"bangumi,omitempty"`
	More     []Expansion `json:"more"`
	RawCount int         `json:"-"`
}

type Client struct {
	HTTP    *http.Client
	BaseURL string
}

func New() *Client {
	return &Client{BaseURL: BaseURL, HTTP: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}}
}

func NormalizeQuery(raw string) (string, error) {
	q := strings.TrimSpace(raw)
	if q == "" || len([]rune(q)) > 200 || strings.ContainsFunc(q, unicode.IsControl) {
		return "", ErrInput
	}
	return q, nil
}

func ValidGroup(group string) bool { return group == "" || groupPattern.MatchString(group) }

func (c *Client) Search(ctx context.Context, query, group string) (Result, error) {
	query, err := NormalizeQuery(query)
	if err != nil || !ValidGroup(group) {
		return Result{}, ErrInput
	}
	params := url.Values{"searchstr": {query}}
	if group != "" {
		params.Set("subgroupid", group)
	}
	return c.read(ctx, "/Home/Search?"+params.Encode(), "", "", 0)
}

func (c *Client) read(ctx context.Context, path, bangumiID, group string, take int) (Result, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return Result{}, ErrInput
	}
	req.Header.Set("User-Agent", "Cinema115-MikanSearch/1.0")
	req.Header.Set("Accept", "text/html")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return Result{}, ctx.Err()
		}
		return Result{}, ErrNetwork
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Result{}, responseError(resp, time.Now())
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20+1))
	if err != nil {
		return Result{}, ErrNetwork
	}
	if len(body) > 16<<20 {
		return Result{}, errors.New("蜜柑结果页面过大，请缩小搜索范围")
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	return parse(strings.NewReader(string(body)), bangumiID, group, take)
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}
func hasClass(n *html.Node, class string) bool {
	for _, c := range strings.Fields(attr(n, "class")) {
		if c == class {
			return true
		}
	}
	return false
}
func walk(n *html.Node, visit func(*html.Node)) {
	visit(n)
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		walk(child, visit)
	}
}
func content(n *html.Node) string {
	var text strings.Builder
	walk(n, func(child *html.Node) {
		if child.Type == html.TextNode {
			text.WriteString(child.Data)
		}
	})
	return strings.Join(strings.Fields(text.String()), " ")
}
func siteLink(raw, prefix string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return ""
	}
	if u.IsAbs() && (u.Scheme != "https" || u.Host != "mikanani.me") {
		return ""
	}
	if !u.IsAbs() && u.Host != "" {
		return ""
	}
	if !strings.HasPrefix(u.Path, prefix) {
		return ""
	}
	return BaseURL + u.EscapedPath()
}

func resolution(title string) string {
	m := resolutionPattern.FindStringSubmatch(title)
	if len(m) == 0 {
		return "未知"
	}
	if m[3] != "" {
		return "2160P"
	}
	if m[1] != "" {
		return m[1] + "P"
	}
	return m[2] + "P"
}

// 解析搜索结果和可选番剧卡片，不加载原站脚本、广告或逐条资源详情。
func Parse(reader io.Reader) (Result, error) {
	return parse(reader, "", "", 0)
}

func parse(reader io.Reader, bangumiID, group string, take int) (Result, error) {
	return parseWithOptions(reader, bangumiID, group, take, false)
}

func parseWithOptions(reader io.Reader, bangumiID, group string, take int, strict bool) (Result, error) {
	doc, err := html.Parse(reader)
	if err != nil {
		return Result{}, ErrParse
	}
	result := Result{Items: make([]Item, 0, 64), Groups: make([]Group, 0, 16), Bangumis: make([]Bangumi, 0, 8), More: make([]Expansion, 0, 8)}
	if bangumiID != "" {
		result.Bangumi = &Bangumi{ID: bangumiID, URL: BaseURL + "/Home/Bangumi/" + bangumiID}
	}
	parseBangumis(doc, &result)
	currentGroup, rowCount := group, 0
	seen := make(map[string]bool, 64)
	seenItems := make(map[string]Item, 64)
	groups := make(map[string]bool, 16)
	tableFound, bodyFound, badRow := false, false, false
	emptyFound, searchInputFound := false, false
	walk(doc, func(n *html.Node) {
		if bangumiID != "" {
			if hasClass(n, "bangumi-title") {
				result.Bangumi.Title = content(n)
			}
			if hasClass(n, "subgroup-text") {
				currentGroup = attr(n, "id")
			}
			if n.Data == "a" && hasClass(n, "subgroup-name") {
				id := strings.TrimPrefix(attr(n, "data-anchor"), "#")
				if ValidID(id) && content(n) != "" && !groups[id] {
					result.Groups = append(result.Groups, Group{id, content(n)})
					groups[id] = true
				}
			}
			if hasClass(n, "js-expand-episode") {
				expansion, ok := parseExpansion(n, bangumiID)
				if !ok {
					badRow = true
				} else {
					result.More = append(result.More, expansion)
				}
			}
		}
		if n.Data == "input" && attr(n, "id") == "header-search" && attr(n, "name") == "searchstr" {
			searchInputFound = true
		}
		if hasClass(n, "no-subscribe-bangumi") && strings.Contains(content(n), "找不到对应结果") {
			emptyFound = true
		}
		if hasClass(n, "episode-table") {
			tableFound = true
		}
		// 结果表仍有内容但行标记改变时应报结构错误，不能缓存成正常空结果。
		if n.Data == "tbody" || (n.Data == "tr" && !hasClass(n, "js-search-results-row")) {
			for parent := n.Parent; parent != nil; parent = parent.Parent {
				if !hasClass(parent, "episode-table") {
					continue
				}
				if n.Data == "tbody" {
					bodyFound = true
				}
				if n.Data == "tr" && bangumiID == "" {
					for child := n.FirstChild; child != nil; child = child.NextSibling {
						if child.Data == "td" {
							badRow = true
						}
					}
				}
				break
			}
		}
		if n.Data == "a" && hasClass(n, "subgroup-longname") {
			id := attr(n, "data-subgroupid")
			name := content(n)
			if id != "" && ValidGroup(id) && name != "" && !groups[id] {
				result.Groups = append(result.Groups, Group{id, name})
				groups[id] = true
			}
		}
		if n.Data != "tr" || (bangumiID == "" && !hasClass(n, "js-search-results-row")) {
			return
		}
		if bangumiID != "" && !insideEpisodeTable(n) {
			return
		}
		cells := make([]*html.Node, 0, 6)
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			if child.Data == "td" {
				cells = append(cells, child)
			}
		}
		if len(cells) == 0 {
			if bangumiID == "" {
				badRow = true
			}
			return
		}
		rowCount++
		if strict && rowCount > AnimeMaxRows {
			return
		}
		item := Item{GroupID: currentGroup}
		if bangumiID != "" && !ValidID(currentGroup) {
			badRow = true
			return
		}
		walk(n, func(child *html.Node) {
			if child.Data == "a" && hasClass(child, "magnet-link-wrap") {
				item.Title = content(child)
				item.URL = siteLink(attr(child, "href"), "/Home/Episode/")
			}
			if hasClass(child, "js-magnet") {
				item.Magnet = attr(child, "data-clipboard-text")
			}
			if item.Magnet == "" && attr(child, "data-magnet") != "" {
				item.Magnet = attr(child, "data-magnet")
			}
			if child.Data == "a" && strings.HasSuffix(attr(child, "href"), ".torrent") {
				item.TorrentURL = siteLink(attr(child, "href"), "/Download/")
			}
		})
		magnet, parseErr := url.Parse(item.Magnet)
		if parseErr != nil || magnet.Scheme != "magnet" {
			badRow = true
			return
		}
		hash := strings.TrimPrefix(magnet.Query().Get("xt"), "urn:btih:")
		if !hashPattern.MatchString(hash) || item.Title == "" || item.URL == "" || len(cells) < 4 {
			badRow = true
			return
		}
		item.ID = strings.ToLower(hash)
		if !strings.EqualFold(strings.TrimPrefix(item.URL, BaseURL+"/Home/Episode/"), hash) {
			badRow = true
			return
		}
		item.Size = content(cells[2])
		item.UpdatedAt = content(cells[3])
		item.Resolution = resolution(item.Title)
		if len(item.Title) > 8192 || len(item.Magnet) > 16384 {
			badRow = true
			return
		}
		if !seen[item.ID] {
			result.Items = append(result.Items, item)
			seen[item.ID] = true
			seenItems[item.ID] = item
		} else if strict && seenItems[item.ID] != item {
			badRow = true
		}
	})
	result.RawCount = rowCount
	if strict && rowCount > AnimeMaxRows {
		return Result{}, &LimitError{Kind: "rows"}
	}
	// 蜜柑零结果使用独立提示页，不包含结果表格；需同时确认搜索框及明确的空结果标记。
	if !tableFound && emptyFound && searchInputFound && !badRow && len(result.Items) == 0 {
		return result, nil
	}
	if bangumiID != "" && take == 0 && !tableFound && result.Bangumi.Title != "" && len(result.Groups) == 0 && !badRow {
		return result, nil
	}
	if !tableFound || !bodyFound || badRow {
		return Result{}, ErrParse
	}
	if bangumiID != "" && take > 0 && rowCount >= take {
		result.More = append(result.More, Expansion{GroupID: group, Take: take + 50})
	}
	// 原站时间不带时区，按原站显示值排序，不擅自转换为用户本地时间。
	sort.SliceStable(result.Items, func(i, j int) bool { return result.Items[i].UpdatedAt > result.Items[j].UpdatedAt })
	return result, nil
}
