package tmdb

import (
	"context"
	"net/url"
	"strconv"
)

// AlternativeNames is only used when recognition needs alias verification.
func (c *Client) AlternativeNames(ctx context.Context, kind string, id int) ([]string, error) {
	if !validKind(kind) || id < 1 {
		return nil, ErrInput
	}
	var raw struct {
		ID          int `json:"id"`
		Alternative struct {
			Titles []struct {
				Title string `json:"title"`
			} `json:"titles"`
			Results []struct {
				Title string `json:"title"`
			} `json:"results"`
		} `json:"alternative_titles"`
		Translations struct {
			Items []struct {
				Data struct {
					Name  string `json:"name"`
					Title string `json:"title"`
				} `json:"data"`
			} `json:"translations"`
		} `json:"translations"`
	}
	if err := c.get(ctx, "/"+kind+"/"+strconv.Itoa(id), url.Values{"append_to_response": {"alternative_titles,translations"}}, &raw); err != nil {
		return nil, err
	}
	if raw.ID != id {
		return nil, ErrInput
	}
	names := make([]string, 0, len(raw.Alternative.Titles)+len(raw.Alternative.Results)+len(raw.Translations.Items))
	for _, item := range raw.Alternative.Titles {
		names = append(names, item.Title)
	}
	for _, item := range raw.Alternative.Results {
		names = append(names, item.Title)
	}
	for _, item := range raw.Translations.Items {
		names = append(names, item.Data.Name, item.Data.Title)
	}
	return names, nil
}
