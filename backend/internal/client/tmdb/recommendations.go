package tmdb

import (
	"context"
	"net/url"
	"strconv"
)

// Recommendations 优先采用 TMDB 推荐顺序，不足二十项时用相似内容补充。
func (c *Client) Recommendations(ctx context.Context, kind string, id int) (*Page, error) {
	if !validKind(kind) || id < 1 {
		return nil, ErrInput
	}
	page := &Page{Items: make([]Item, 0, 20), Page: 1, TotalPages: 1}
	seen := map[int]bool{id: true}
	for _, category := range []string{"recommendations", "similar"} {
		result, err := c.readPage(ctx, kind, "/"+kind+"/"+strconv.Itoa(id)+"/"+category, url.Values{"language": {"zh-CN"}, "page": {"1"}})
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			// 补充接口失败时仍展示已经取得的推荐，主推荐失败则让页面提供重试。
			if len(page.Items) > 0 {
				break
			}
			return nil, err
		}
		for _, item := range result.Items {
			if seen[item.ID] {
				continue
			}
			seen[item.ID] = true
			page.Items = append(page.Items, item)
			if len(page.Items) == 20 {
				break
			}
		}
		if len(page.Items) == 20 {
			break
		}
	}
	page.Total = len(page.Items)
	return page, nil
}
