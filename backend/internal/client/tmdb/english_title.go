package tmdb

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// EnglishTitle uses declared English translations, never guesses from the
// original title's script. An empty result also represents valid metadata.
func (c *Client) EnglishTitle(ctx context.Context, kind string, id int) (string, error) {
	if !validKind(kind) || id < 1 {
		return "", ErrInput
	}
	var raw struct {
		ID               int    `json:"id"`
		OriginalLanguage string `json:"original_language"`
		OriginalTitle    string `json:"original_title"`
		OriginalName     string `json:"original_name"`
		Translations     struct {
			Translations []struct {
				Language string `json:"iso_639_1"`
				Country  string `json:"iso_3166_1"`
				Data     struct {
					Title string `json:"title"`
					Name  string `json:"name"`
				} `json:"data"`
			} `json:"translations"`
		} `json:"translations"`
	}
	if err := c.get(ctx, "/"+kind+"/"+strconv.Itoa(id), url.Values{"language": {"en-US"}, "append_to_response": {"translations"}}, &raw); err != nil {
		return "", err
	}
	if raw.ID != id || raw.Translations.Translations == nil {
		return "", fmt.Errorf("TMDB 未返回有效的译名信息")
	}
	title, rank := "", 0
	if raw.OriginalLanguage == "en" {
		title = strings.TrimSpace(raw.OriginalTitle)
		if kind == "tv" {
			title = strings.TrimSpace(raw.OriginalName)
		}
		if title != "" {
			return title, nil
		}
	}
	for _, entry := range raw.Translations.Translations {
		if entry.Language != "en" {
			continue
		}
		value := entry.Data.Title
		if kind == "tv" {
			value = entry.Data.Name
		}
		value = strings.TrimSpace(value)
		priority := 1
		if entry.Country == "US" {
			priority = 3
		} else if entry.Country == "GB" {
			priority = 2
		}
		if value != "" && priority > rank {
			title, rank = value, priority
		}
	}
	return title, nil
}
