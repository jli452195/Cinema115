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

type mediaRecognition struct {
	Detail *tmdb.Detail
	Season int
}

func recognizeMedia(ctx context.Context, source MediaRecognitionSource, identity mediafacts.Identity) (*mediaRecognition, error) {
	if source == nil {
		return nil, fmt.Errorf("TMDB 服务未配置")
	}
	if identity.TMDBID > 0 && (identity.Kind == "movie" || identity.Kind == "tv") {
		detail, err := source.Details(ctx, identity.Kind, identity.TMDBID)
		if err != nil || detail == nil {
			return nil, err
		}
		return &mediaRecognition{Detail: detail, Season: identity.Season}, nil
	}
	if identity.Name == "" {
		return nil, nil
	}
	kind := identity.Kind
	if kind == "" {
		kind = "multi"
	}
	queries := []string{identity.Name}
	if identity.ParentName != "" && mediafacts.NameKey(identity.ParentName) != mediafacts.NameKey(identity.Name) {
		queries = append(queries, identity.ParentName)
		// A parent can contain a series followed by a seasonal subtitle. The
		// shorter query is accepted only with filename and season verification.
		if kind == "tv" && !identity.SeasonExplicit {
			if split := strings.LastIndexAny(identity.ParentName, " ：:"); split > 0 {
				queries = append(queries, strings.TrimSpace(identity.ParentName[:split]))
			}
		}
	}
	queried := make(map[string]bool, len(queries))
	for _, query := range queries {
		queryKey := mediafacts.NameKey(query)
		if queried[queryKey] {
			continue
		}
		queried[queryKey] = true
		page, err := source.Browse(ctx, tmdb.Options{Kind: kind, Query: query, Page: 1})
		if err != nil {
			return nil, err
		}
		if page == nil {
			return nil, fmt.Errorf("TMDB 未返回有效搜索结果")
		}
		// Exact titles win before alias I/O, so an unrelated candidate cannot
		// delay or invalidate an already verified direct match.
		for _, aliasPass := range []bool{false, true} {
			matches := make([]mediaRecognition, 0, 2)
			seen := make(map[string]bool)
			for index, item := range page.Items {
				key := item.Kind + ":" + strconv.Itoa(item.ID)
				if seen[key] || (item.Kind != "movie" && item.Kind != "tv") || item.ID < 1 || (identity.Kind != "" && item.Kind != identity.Kind) || (identity.TMDBID > 0 && item.ID != identity.TMDBID) {
					continue
				}
				seen[key] = true
				names := []string{item.Title, item.OriginalTitle}
				if aliasPass {
					if mediaExactName(query, names) && mediaExactName(identity.Name, names) {
						continue
					}
					aliases, ok := source.(interface {
						AlternativeNames(context.Context, string, int) ([]string, error)
					})
					if !ok || index >= 3 {
						continue
					}
					other, err := aliases.AlternativeNames(ctx, item.Kind, item.ID)
					if err != nil {
						return nil, err
					}
					names = append(names, other...)
				}
				if !mediaExactName(query, names) || !mediaExactName(identity.Name, names) && (item.Kind != "tv" || !mediaRelatedName(identity.Name, names)) {
					continue
				}
				year := identity.Year
				if identity.YearFromParent && identity.ParentName != "" && !mediaRelatedName(identity.ParentName, names) {
					year = ""
				}
				// Preserve confirmation for short, undated work names.
				if year == "" && len([]rune(mediafacts.NameKey(query))) < 4 {
					continue
				}
				if item.Kind == "movie" && year != "" && !strings.HasPrefix(item.Date, year) {
					continue
				}
				detail, err := source.Details(ctx, item.Kind, item.ID)
				if err != nil {
					return nil, err
				}
				if detail == nil {
					continue
				}
				season, ok := mediaRecognitionSeason(identity, detail, names, year)
				if ok {
					matches = append(matches, mediaRecognition{Detail: detail, Season: season})
				}
			}
			if len(matches) > 1 {
				return nil, nil
			}
			if len(matches) == 1 {
				return &matches[0], nil
			}
		}
	}
	return nil, nil
}
