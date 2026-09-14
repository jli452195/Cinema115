package tmdb

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strconv"
)

type CollectionDetail struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Overview    string `json:"overview"`
	PosterURL   string `json:"posterUrl"`
	BackdropURL string `json:"backdropUrl"`
	Items       []Item `json:"items"`
}

// CollectionDetails 读取完整系列，复用固定域名访问、图片校验与有界缓存。
func (c *Client) CollectionDetails(ctx context.Context, id int) (*CollectionDetail, error) {
	if id < 1 {
		return nil, ErrInput
	}
	var raw struct {
		ID       int    `json:"id"`
		Name     string `json:"name"`
		Overview string `json:"overview"`
		Poster   string `json:"poster_path"`
		Backdrop string `json:"backdrop_path"`
		Parts    []struct {
			ID            int     `json:"id"`
			Title         string  `json:"title"`
			OriginalTitle string  `json:"original_title"`
			Overview      string  `json:"overview"`
			Poster        string  `json:"poster_path"`
			Date          string  `json:"release_date"`
			Rating        float64 `json:"vote_average"`
			Votes         int     `json:"vote_count"`
			Adult         bool    `json:"adult"`
		} `json:"parts"`
	}
	if err := c.get(ctx, "/collection/"+strconv.Itoa(id), url.Values{"language": {"zh-CN"}}, &raw); err != nil {
		return nil, err
	}
	if raw.ID != id || raw.Parts == nil {
		return nil, fmt.Errorf("TMDB 未返回有效系列数据")
	}
	result := &CollectionDetail{ID: raw.ID, Name: raw.Name, Overview: raw.Overview, PosterURL: imageURL(raw.Poster, "w500"), BackdropURL: imageURL(raw.Backdrop, "w1280"), Items: make([]Item, 0, len(raw.Parts))}
	seen := make(map[int]bool, len(raw.Parts))
	for _, part := range raw.Parts {
		if part.ID < 1 || part.Adult || seen[part.ID] {
			continue
		}
		seen[part.ID] = true
		title := part.Title
		if title == "" {
			title = part.OriginalTitle
		}
		if title == "" {
			title = "未命名"
		}
		result.Items = append(result.Items, Item{ID: part.ID, Kind: "movie", Title: title, OriginalTitle: part.OriginalTitle, Overview: part.Overview, PosterURL: posterURL(part.Poster), Date: part.Date, Rating: part.Rating, Votes: part.Votes})
	}
	// 以发行时间排列，尚未公布日期的影片放在最后，同日保留上游顺序。
	sort.SliceStable(result.Items, func(i, j int) bool {
		a, b := result.Items[i].Date, result.Items[j].Date
		if a == "" {
			return false
		}
		if b == "" {
			return true
		}
		return a < b
	})
	return result, nil
}
