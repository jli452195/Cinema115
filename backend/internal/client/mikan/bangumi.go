package mikan

import (
	"context"
	"net/url"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

type Bangumi struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	CoverURL string `json:"coverUrl"`
	URL      string `json:"url"`
}

type Expansion struct {
	GroupID string `json:"groupId"`
	Take    int    `json:"take"`
}

func ValidID(id string) bool {
	return groupPattern.MatchString(id) && id[0] != '0'
}

func ValidBangumiInput(id, group string, take int) bool {
	return ValidID(id) && ((group == "" && take == 0) || (ValidID(group) && take >= 1 && take <= 10000))
}

func (c *Client) Bangumi(ctx context.Context, id, group string, take int) (Result, error) {
	if !ValidBangumiInput(id, group, take) {
		return Result{}, ErrInput
	}
	path := "/Home/Bangumi/" + id
	if group != "" {
		params := url.Values{"bangumiId": {id}, "subtitleGroupId": {group}, "take": {strconv.Itoa(take)}}
		path = "/Home/ExpandEpisodeTable?" + params.Encode()
	}
	return c.read(ctx, path, id, group, take)
}

// 推荐卡片是可选信息；异常卡片单独跳过，不影响资源列表解析。
func parseBangumis(doc *html.Node, result *Result) {
	seen := make(map[string]bool, 8)
	walk(doc, func(n *html.Node) {
		if n.Data != "a" {
			return
		}
		link := siteLink(attr(n, "href"), "/Home/Bangumi/")
		id := strings.TrimPrefix(link, BaseURL+"/Home/Bangumi/")
		if !ValidID(id) || seen[id] {
			return
		}
		card := Bangumi{ID: id, URL: link}
		walk(n, func(child *html.Node) {
			if hasClass(child, "an-text") {
				card.Title = strings.TrimSpace(attr(child, "title"))
				if card.Title == "" {
					card.Title = content(child)
				}
			}
			if card.CoverURL == "" {
				card.CoverURL = coverLink(attr(child, "data-src"))
				if child.Data == "img" && card.CoverURL == "" {
					card.CoverURL = coverLink(attr(child, "src"))
				}
			}
		})
		if card.Title == "" || len(card.Title) > 2048 {
			return
		}
		seen[id] = true
		result.Bangumis = append(result.Bangumis, card)
	})
}

// 封面只接受蜜柑站内图片，重新生成缩略图参数，不透传任意抓取地址。
func coverLink(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Fragment != "" {
		return ""
	}
	if u.IsAbs() && (u.Scheme != "https" || u.Host != "mikanani.me") {
		return ""
	}
	if !u.IsAbs() && u.Host != "" {
		return ""
	}
	if !strings.HasPrefix(u.Path, "/images/Bangumi/") || strings.Contains(u.Path, "..") || strings.Contains(u.Path, "\\") {
		return ""
	}
	return BaseURL + u.EscapedPath() + "?width=240&height=336&format=webp"
}

func insideEpisodeTable(n *html.Node) bool {
	for parent := n.Parent; parent != nil; parent = parent.Parent {
		if hasClass(parent, "episode-table") {
			return true
		}
	}
	return false
}

func parseExpansion(n *html.Node, id string) (Expansion, bool) {
	take, err := strconv.Atoi(attr(n, "data-take"))
	group := attr(n, "data-subtitlegroupid")
	return Expansion{GroupID: group, Take: take}, err == nil && attr(n, "data-bangumiid") == id && ValidBangumiInput(id, group, take)
}
