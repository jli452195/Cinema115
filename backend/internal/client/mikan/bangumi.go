package mikan

import (
	"context"
	"net/url"
	"strconv"
	"strings"

	"strm115/backend/internal/client/imagecache"

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

// External subject links are display hints only. Never request these URLs or
// treat their presence (or agreement) as evidence of a confirmed edition.
func bangumiSubjectHint(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.EscapedPath() != u.Path {
		return ""
	}
	if u.Host != "bgm.tv" && u.Host != "bangumi.tv" && u.Host != "chii.in" {
		return ""
	}
	id := strings.TrimPrefix(u.Path, "/subject/")
	if id == u.Path || !ValidID(id) {
		return ""
	}
	return id
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
		// The current site puts the poster next to the title link in the same
		// list item, while older cards wrap both in one link.
		if card.CoverURL == "" {
			for parent := n.Parent; parent != nil; parent = parent.Parent {
				if parent.Data != "li" {
					continue
				}
				walk(parent, func(child *html.Node) {
					if card.CoverURL == "" {
						card.CoverURL = coverLink(attr(child, "data-src"))
					}
				})
				break
			}
		}
		if card.Title == "" || len(card.Title) > 2048 {
			return
		}
		seen[id] = true
		result.Bangumis = append(result.Bangumis, card)
	})
}

// 封面统一返回本站缓存地址，浏览器不直接连接蜜柑。
func coverLink(raw string) string {
	return imagecache.MikanCoverURL(raw)
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
