package service

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"strm115/backend/internal/client/tmdb"
	"strm115/backend/internal/mediafacts"
)

func (s *TMDBService) AlternativeNames(ctx context.Context, kind string, id int) ([]string, error) {
	source, ok := s.source.(interface {
		AlternativeNames(context.Context, string, int) ([]string, error)
	})
	if !ok {
		return nil, nil
	}
	names, _, err := cachedTMDB[[]string](ctx, s, "recognition:names:"+kind+":"+strconv.Itoa(id), 30*24*time.Hour, func(ctx context.Context) (any, error) { return source.AlternativeNames(ctx, kind, id) })
	if err != nil {
		return nil, err
	}
	return *names, nil
}

type MediaRecognitionSource interface {
	OrganizeMetadata
	Browse(context.Context, tmdb.Options) (*tmdb.Page, error)
}

func recognizeMedia(ctx context.Context, source MediaRecognitionSource, identity mediafacts.Identity) (*tmdb.Detail, error) {
	if source == nil {
		return nil, fmt.Errorf("TMDB 服务未配置")
	}
	if identity.TMDBID > 0 {
		if identity.Kind == "movie" || identity.Kind == "tv" {
			return source.Details(ctx, identity.Kind, identity.TMDBID)
		}
	}
	if identity.Name == "" {
		return nil, nil
	}
	kind := identity.Kind
	if kind == "" {
		kind = "multi"
	}
	page, err := source.Browse(ctx, tmdb.Options{Kind: kind, Query: identity.Name, Page: 1})
	if err != nil {
		return nil, err
	}
	if page == nil {
		return nil, fmt.Errorf("TMDB 未返回有效搜索结果")
	}
	key := mediafacts.NameKey(identity.Name)
	matches := make([]tmdb.Item, 0, 2)
	for _, item := range page.Items {
		if (item.Kind != "movie" && item.Kind != "tv") || item.ID < 1 || (identity.Kind != "" && item.Kind != identity.Kind) || (identity.TMDBID > 0 && item.ID != identity.TMDBID) {
			continue
		}
		if identity.Year != "" && (item.Kind == "movie" || identity.Season <= 1) && !strings.HasPrefix(item.Date, identity.Year) {
			continue
		}
		if mediafacts.NameKey(item.Title) == key || mediafacts.NameKey(item.OriginalTitle) == key {
			matches = append(matches, item)
		}
	}
	// Alias queries are bounded and only needed after the main title pass fails.
	if len(matches) == 0 {
		if aliases, ok := source.(interface {
			AlternativeNames(context.Context, string, int) ([]string, error)
		}); ok {
			for _, item := range page.Items[:min(len(page.Items), 3)] {
				if (item.Kind != "movie" && item.Kind != "tv") || item.ID < 1 || (identity.Kind != "" && item.Kind != identity.Kind) || (identity.TMDBID > 0 && item.ID != identity.TMDBID) {
					continue
				}
				if identity.Year != "" && (item.Kind == "movie" || identity.Season <= 1) && !strings.HasPrefix(item.Date, identity.Year) {
					continue
				}
				names, err := aliases.AlternativeNames(ctx, item.Kind, item.ID)
				if err != nil {
					return nil, err
				}
				for _, name := range names {
					if mediafacts.NameKey(name) == key {
						matches = append(matches, item)
						break
					}
				}
			}
		}
	}
	if len(matches) != 1 {
		return nil, nil
	}
	// Short, undated names need explicit confirmation even with one search hit.
	if identity.Year == "" && len([]rune(key)) < 4 {
		return nil, nil
	}
	return source.Details(ctx, matches[0].Kind, matches[0].ID)
}

func mediaCategory(detail *tmdb.Detail) string {
	genre := func(id int) bool {
		for _, g := range detail.Genres {
			if g.ID == id {
				return true
			}
		}
		return false
	}
	if detail.Kind == "movie" {
		if genre(16) {
			return "电影/动画电影"
		}
		if detail.OriginalLanguage == "zh" || detail.OriginalLanguage == "cn" {
			return "电影/华语电影"
		}
		return "电影/其他电影"
	}
	if genre(16) {
		return "剧集/动漫"
	}
	if genre(99) {
		return "剧集/纪录片"
	}
	if genre(10764) || genre(10767) {
		return "剧集/综艺"
	}
	for _, country := range detail.OriginCountryCodes {
		switch country {
		case "CN", "HK", "TW":
			return "剧集/国产剧"
		case "JP", "KR":
			return "剧集/日韩剧"
		case "US", "GB", "CA", "FR", "DE", "AU":
			return "剧集/欧美剧"
		}
	}
	return "剧集/其他剧集"
}
