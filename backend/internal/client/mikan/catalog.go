package mikan

import (
	"context"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// Catalog identities are Mikan item IDs; they are never Bangumi subject IDs.
type CatalogItem struct {
	Bangumi
	UpdatedDate string `json:"updatedDate"`
}

type CatalogDay struct {
	// 1..7 = Monday..Sunday, 8 = movies, 9 = OVA. Mikan's raw 0 is Sunday.
	Day   int           `json:"day"`
	Items []CatalogItem `json:"items"`
}

type Catalog struct {
	Season string        `json:"season"`
	Days   []CatalogDay  `json:"days"`
	Items  []CatalogItem `json:"items"`
}

func ValidCatalogSeason(season string) bool {
	if len(season) != 7 || season[4] != '-' {
		return false
	}
	year, err := strconv.Atoi(season[:4])
	return err == nil && year >= 2000 && year <= 2100 && (season[5:] == "01" || season[5:] == "04" || season[5:] == "07" || season[5:] == "10")
}

func (c *Client) Catalog(ctx context.Context, season, keyword string) (Catalog, error) {
	if keyword != "" {
		result, err := c.Search(ctx, keyword, "")
		if err != nil {
			return Catalog{}, err
		}
		out := Catalog{Days: []CatalogDay{}, Items: make([]CatalogItem, 0, len(result.Bangumis))}
		for _, item := range result.Bangumis {
			out.Items = append(out.Items, CatalogItem{Bangumi: item})
		}
		return out, nil
	}
	if !ValidCatalogSeason(season) {
		return Catalog{}, ErrInput
	}
	seasons := map[string]string{"01": "冬", "04": "春", "07": "夏", "10": "秋"}
	params := url.Values{"year": {season[:4]}, "seasonStr": {seasons[season[5:]]}}
	body, err := c.readHTML(ctx, "/Home/BangumiCoverFlowByDayOfWeek?"+params.Encode())
	if err != nil {
		return Catalog{}, err
	}
	return ParseCatalog(strings.NewReader(string(body)), season)
}

func ParseCatalog(reader io.Reader, season string) (Catalog, error) {
	out := Catalog{Season: season, Days: make([]CatalogDay, 0, 9), Items: []CatalogItem{}}
	doc, err := html.Parse(reader)
	if err != nil {
		return out, ErrParse
	}
	seenDays := make(map[int]bool, 9)
	invalid := false
	walk(doc, func(block *html.Node) {
		if !hasClass(block, "sk-bangumi") || attr(block, "data-dayofweek") == "" {
			return
		}
		raw, err := strconv.Atoi(attr(block, "data-dayofweek"))
		if err != nil || raw < 0 || raw > 8 {
			invalid = true
			return
		}
		day := raw
		if day == 0 {
			day = 7
		} else if day >= 7 {
			day++
		}
		if seenDays[day] {
			invalid = true
			return
		}
		seenDays[day] = true
		group := CatalogDay{Day: day, Items: make([]CatalogItem, 0, 24)}
		seen := make(map[string]bool, 24)
		walk(block, func(n *html.Node) {
			if n.Data != "li" {
				return
			}
			item := CatalogItem{}
			hasEntry := false
			walk(n, func(child *html.Node) {
				if child.Data == "a" && hasClass(child, "an-text") {
					hasEntry = true
					item.URL = siteLink(attr(child, "href"), "/Home/Bangumi/")
					item.ID = strings.TrimPrefix(item.URL, BaseURL+"/Home/Bangumi/")
					item.Title = strings.TrimSpace(attr(child, "title"))
					if item.Title == "" {
						item.Title = content(child)
					}
				}
				if item.CoverURL == "" {
					item.CoverURL = coverLink(attr(child, "data-src"))
				}
				if hasClass(child, "date-text") {
					date := strings.TrimSuffix(content(child), " 更新")
					if parsed, e := time.Parse("2006/01/02", date); e == nil {
						item.UpdatedDate = parsed.Format("2006-01-02")
					}
				}
			})
			if !hasEntry {
				return
			}
			if !ValidID(item.ID) || item.Title == "" || len(item.Title) > 4096 || len(group.Items) >= 500 {
				invalid = true
				return
			}
			if seen[item.ID] {
				return
			}
			seen[item.ID] = true
			group.Items = append(group.Items, item)
		})
		out.Days = append(out.Days, group)
	})
	if invalid || len(out.Days) == 0 {
		return Catalog{}, ErrParse
	}
	return out, nil
}
